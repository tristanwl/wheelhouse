// Package qbittorrent is the qBittorrent download-client provider, ported from
// lib/providers/download-client/qbittorrent.ts. It speaks WebUI API v2 and
// handles the login differences between qBittorrent v4 (200 "Ok." / "Fails.",
// cookie SID) and v5 (204 / 401, cookie QBT_SID_<port>).
package qbittorrent

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	dc "github.com/tristanwl/wheelhouse/server/internal/providers/downloadclient"
)

// ID is the provider id.
const ID = "qbittorrent"

// Config is the provider's settings.
type Config struct {
	URL      string `json:"url" label:"URL" config:"required,url"`
	Username string `json:"username" label:"Username" config:"required"`
	Password string `json:"password" label:"Password" config:"required,secret"`
}

// Definition describes the provider for the registry.
func Definition() core.Definition {
	return core.Definition{
		ID:           ID,
		Role:         core.RoleDownloadClient,
		DisplayName:  "qBittorrent",
		Config:       Config{},
		Capabilities: dc.Capabilities{Protocol: dc.Torrent, CanPause: true, UsenetServers: false, Seeding: true},
		Create: func(config any, ctx core.ProviderContext) any {
			client, err := New(config.(Config), ctx.InstanceID)
			if err != nil {
				return dc.Failed(err)
			}
			return client
		},
	}
}

// New builds a client. label prefixes its error messages (the instance id).
func New(cfg Config, label string) (dc.Client, error) {
	return newClient(cfg, label, nil)
}

// newClient lets tests tighten the HTTP options (timeout, size limit, logger).
func newClient(cfg Config, label string, tweak func(*core.HTTPOptions)) (*client, error) {
	opts := core.HTTPOptions{BaseURL: cfg.URL, Label: label, Secrets: []string{cfg.Password}}
	if tweak != nil {
		tweak(&opts)
	}
	http, err := core.NewHTTPClient(opts)
	if err != nil {
		return nil, err
	}
	return &client{http: http, label: label, cfg: cfg}, nil
}

type torrent struct {
	Hash       string    `json:"hash"`
	Name       string    `json:"name"`
	State      string    `json:"state"`
	Progress   dc.Number `json:"progress"`
	Size       dc.Number `json:"size"`
	TotalSize  dc.Number `json:"total_size"`
	AmountLeft dc.Number `json:"amount_left"`
	ETA        dc.Number `json:"eta"`
	Ratio      dc.Number `json:"ratio"`
	NumSeeds   dc.Number `json:"num_seeds"`
	UpSpeed    dc.Number `json:"upspeed"`
}

type transferInfo struct {
	DLSpeed dc.Number `json:"dl_info_speed"`
	UPSpeed dc.Number `json:"up_info_speed"`
}

// etaInfinity is how qBittorrent reports an unknown ETA: 100 days, in seconds.
const etaInfinity = 8640000

// Mapped is a torrent state mapped onto the shared enum.
type Mapped struct {
	State  dc.State
	Detail string
}

// Both v4 (paused*) and v5 (stopped*) names; v5 renamed them.
var stateMap = map[string]Mapped{
	"downloading":        {dc.StateDownloading, ""},
	"forcedDL":           {dc.StateDownloading, ""},
	"forcedMetaDL":       {dc.StateDownloading, ""},
	"metaDL":             {dc.StateDownloading, "Fetching metadata"},
	"allocating":         {dc.StateDownloading, "Allocating"},
	"stalledDL":          {dc.StateStalled, ""},
	"queuedDL":           {dc.StateQueued, ""},
	"queuedUP":           {dc.StateQueued, ""},
	"checkingDL":         {dc.StatePostprocessing, "Checking"},
	"checkingUP":         {dc.StatePostprocessing, "Checking"},
	"checkingResumeData": {dc.StatePostprocessing, "Checking"},
	"moving":             {dc.StatePostprocessing, "Moving"},
	"uploading":          {dc.StateSeeding, ""},
	"forcedUP":           {dc.StateSeeding, ""},
	"stalledUP":          {dc.StateSeeding, ""},
	"pausedDL":           {dc.StatePaused, ""},
	"stoppedDL":          {dc.StatePaused, ""},
	"pausedUP":           {dc.StateCompleted, ""},
	"stoppedUP":          {dc.StateCompleted, ""},
	"error":              {dc.StateFailed, ""},
	"missingFiles":       {dc.StateFailed, "Files missing"},
}

// MapState maps a qBittorrent torrent state (v4 or v5 name) to the normalized
// state. A state it does not know keeps the client's wording instead of a guess.
func MapState(state string) Mapped {
	if m, ok := stateMap[state]; ok {
		return m
	}
	return Mapped{dc.StateDownloading, state}
}

func (t torrent) sizeBytes() int64 {
	// size is what is selected for download; a magnet without metadata has 0 and total_size -1.
	if n := t.Size.Whole(); n > 0 {
		return n
	}
	return t.TotalSize.Whole()
}

func etaOf(n dc.Number) *int64 {
	if f := n.Float(); f > 0 && f < etaInfinity {
		return dc.Int64Ptr(int64(dc.RoundHalfUp(f)))
	}
	return nil
}

func (t torrent) item() dc.Item {
	size := t.sizeBytes()
	m := MapState(t.State)
	return dc.Item{
		ID:             t.Hash,
		Name:           t.Name,
		Protocol:       dc.Torrent,
		State:          m.State,
		StateDetail:    m.Detail,
		Progress:       dc.Clamp01(t.Progress.Float()),
		SizeBytes:      size,
		RemainingBytes: min(t.AmountLeft.Whole(), size),
		ETASeconds:     etaOf(t.ETA),
		Torrent: &dc.TorrentInfo{
			Ratio:             max(t.Ratio.Float(), 0),
			Seeders:           t.NumSeeds.Whole(),
			UploadBytesPerSec: t.UpSpeed.Whole(),
		},
	}
}

// loginCall is one sign-in that concurrent callers share.
type loginCall struct {
	done   chan struct{}
	err    error
	cookie string
	gen    int // the session generation this sign-in created
}

type client struct {
	http  *core.HTTPClient
	label string
	cfg   Config

	mu sync.Mutex
	// The session cookie (SID in v4, QBT_SID_<port> in v5), kept for the life of
	// the client. gen changes whenever the session is dropped or replaced, so a
	// caller can tell whether the cookie it sent is still the current one.
	cookie   string
	valid    bool
	gen      int
	inflight *loginCall
}

func (c *client) canceled() error {
	return &core.ProviderError{Message: c.label + ": request canceled"}
}

// startLoginLocked returns the sign-in in progress, or starts one. The login
// is not tied to the first caller's context (the others wait on it), but the
// HTTP client's own timeout still bounds it.
func (c *client) startLoginLocked(ctx context.Context) *loginCall {
	if c.inflight != nil {
		return c.inflight
	}
	call := &loginCall{done: make(chan struct{})}
	c.inflight = call
	bgCtx := context.WithoutCancel(ctx)
	go func() {
		cookie, err := c.doLogin(bgCtx)
		c.mu.Lock()
		if err == nil {
			c.cookie, c.valid = cookie, true
			c.gen++
		}
		call.gen = c.gen
		call.cookie, call.err = cookie, err
		c.inflight = nil
		c.mu.Unlock()
		close(call.done)
	}()
	return call
}

func (c *client) wait(ctx context.Context, call *loginCall) (*loginCall, error) {
	select {
	case <-call.done:
		return call, call.err
	case <-ctx.Done():
		return nil, c.canceled()
	}
}

func (c *client) doLogin(ctx context.Context) (string, error) {
	form := url.Values{"username": {c.cfg.Username}, "password": {c.cfg.Password}}
	res, err := c.http.Do(ctx, "/api/v2/auth/login", core.Request{
		Method: http.MethodPost,
		Header: http.Header{"Content-Type": {"application/x-www-form-urlencoded"}},
		Body:   []byte(form.Encode()),
	})
	if err != nil {
		// v4 answers a wrong login with 200 "Fails."; v5 with 401. A banned IP is 403 in both.
		var pe *core.ProviderError
		if errors.As(err, &pe) {
			switch pe.Status {
			case http.StatusUnauthorized:
				return "", &core.ProviderError{Message: c.label + ": login failed (check the username and password)", Status: 401}
			case http.StatusForbidden:
				return "", &core.ProviderError{Message: c.label + ": login refused, this IP may be banned after too many failed attempts", Status: 403}
			}
		}
		return "", err
	}
	if res.Status == http.StatusOK && strings.TrimSpace(string(res.Body)) != "Ok." {
		return "", &core.ProviderError{Message: c.label + ": login failed (check the username and password)", Status: 401}
	}
	var pairs []string
	for _, sc := range res.Header.Values("Set-Cookie") {
		pair, _, _ := strings.Cut(sc, ";")
		pairs = append(pairs, pair)
	}
	return strings.Join(pairs, "; "), nil
}

// session returns the cookie to send and its generation, signing in first when
// there is none.
func (c *client) session(ctx context.Context) (cookie string, gen int, err error) {
	c.mu.Lock()
	if c.valid {
		cookie, gen = c.cookie, c.gen
		c.mu.Unlock()
		return cookie, gen, nil
	}
	call := c.startLoginLocked(ctx)
	c.mu.Unlock()
	done, err := c.wait(ctx, call)
	if err != nil {
		return "", 0, err
	}
	return done.cookie, done.gen, nil
}

// renew handles a refused session. Parallel calls fail together: the first one
// drops the session and signs in, the rest wait for that one sign-in.
func (c *client) renew(ctx context.Context, staleGen int) (string, error) {
	done, err := c.renewCall(ctx, staleGen)
	if err != nil || done == nil {
		return "", err
	}
	return done.cookie, nil
}

func (c *client) renewCall(ctx context.Context, staleGen int) (*loginCall, error) {
	c.mu.Lock()
	var call *loginCall
	switch {
	case c.gen == staleGen:
		c.valid = false
		c.gen++
		call = c.startLoginLocked(ctx)
	case c.inflight != nil:
		call = c.inflight
	default:
		current := &loginCall{cookie: c.cookie, gen: c.gen}
		c.mu.Unlock()
		return current, nil
	}
	c.mu.Unlock()
	return c.wait(ctx, call)
}

func (c *client) get(ctx context.Context, path, cookie string, maxBytes int64) (*core.Response, error) {
	req := core.Request{MaxBytes: maxBytes}
	if cookie != "" {
		req.Header = http.Header{"Cookie": {cookie}}
	}
	return c.http.Do(ctx, path, req)
}

func isAuthStatus(err error) bool {
	var pe *core.ProviderError
	return errors.As(err, &pe) && (pe.Status == http.StatusForbidden || pe.Status == http.StatusUnauthorized)
}

// authed GETs path with the session cookie, signing in first or again as needed.
func (c *client) authed(ctx context.Context, path string, maxBytes int64) (*core.Response, error) {
	cookie, gen, err := c.session(ctx)
	if err != nil {
		return nil, err
	}
	res, err := c.get(ctx, path, cookie, maxBytes)
	if err == nil || !isAuthStatus(err) {
		return res, err
	}
	// An expired session (qBittorrent restarted, or the one-hour timeout) is a 403; sign in once more.
	if cookie, err = c.renew(ctx, gen); err != nil {
		return nil, err
	}
	res, err = c.get(ctx, path, cookie, maxBytes)
	var pe *core.ProviderError
	if errors.As(err, &pe) && pe.Status == http.StatusForbidden {
		return nil, &core.ProviderError{Message: c.label + ": not authorized after signing in", Status: 403}
	}
	return res, err
}

func (c *client) json(ctx context.Context, path string, maxBytes int64, out any) error {
	res, err := c.authed(ctx, path, maxBytes)
	if err != nil {
		return err
	}
	if err := core.DecodeJSON(ctx, res.Body, out, core.JSONLimits{}); err != nil {
		// Not err.Error(): decoder errors can quote parts of the body.
		return &core.ProviderError{Message: c.label + " " + path + " " + core.InvalidJSONText(err)}
	}
	return nil
}

func (c *client) version(ctx context.Context) (string, error) {
	// Plain text: "v5.2.4".
	res, err := c.authed(ctx, "/api/v2/app/version", core.SmallResponseBytes)
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(strings.TrimSpace(string(res.Body)), "v"), nil
}

// TestConnection implements dc.Client.
func (c *client) TestConnection(ctx context.Context) core.ConnectionResult {
	v, err := c.version(ctx)
	if err != nil {
		return core.ConnectionResult{Error: dc.ErrorText(err)}
	}
	return core.ConnectionResult{OK: true, Version: v}
}

// Status implements dc.Client.
func (c *client) Status(ctx context.Context) (dc.Status, error) {
	var (
		transfer transferInfo
		torrents []torrent
		ver      string
		errs     [3]error
		wg       sync.WaitGroup
	)
	wg.Go(func() { errs[0] = c.json(ctx, "/api/v2/transfer/info", core.SmallResponseBytes, &transfer) })
	wg.Go(func() { errs[1] = c.json(ctx, "/api/v2/torrents/info", core.LargeListBytes, &torrents) })
	wg.Go(func() { ver, errs[2] = c.version(ctx) })
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return dc.Status{}, err
		}
	}

	// "Queue" here means torrents that still have something to download.
	var (
		incomplete, pausedIncomplete int64
		seeding, left                int64
	)
	for _, t := range torrents {
		state := MapState(t.State).State
		if p := t.Progress; p.Valid() && p.Float() < 1 {
			incomplete++
			left += t.AmountLeft.Whole()
			if state == dc.StatePaused {
				pausedIncomplete++
			}
		}
		if state == dc.StateSeeding {
			seeding++
		}
	}
	speed := transfer.DLSpeed.Whole()
	upload := transfer.UPSpeed.Whole()
	// qBittorrent has no global pause, so "paused" means everything still downloading is stopped.
	paused := incomplete > 0 && pausedIncomplete == incomplete

	text := "Idle"
	switch {
	case paused:
		text = "Paused"
	case speed > 0:
		text = "Downloading"
	case seeding > 0 || upload > 0:
		text = "Seeding"
	}
	var eta *int64
	if speed > 0 && left > 0 {
		eta = dc.Int64Ptr((left + speed - 1) / speed)
	}
	return dc.Status{
		Paused:           paused,
		StatusText:       text,
		SpeedBytesPerSec: speed,
		QueueCount:       incomplete,
		ETASeconds:       eta,
		Version:          ver,
		Warnings:         []dc.Warning{},
		Torrent:          &dc.TorrentStatus{Seeding: seeding, UploadBytesPerSec: upload},
	}, nil
}

// Items implements dc.Client.
func (c *client) Items(ctx context.Context) ([]dc.Item, error) {
	var torrents []torrent
	if err := c.json(ctx, "/api/v2/torrents/info", core.LargeListBytes, &torrents); err != nil {
		return nil, err
	}
	// A finished, stopped torrent is just a file on disk; everything else is worth a row.
	items := make([]dc.Item, 0, len(torrents))
	for _, t := range torrents {
		if item := t.item(); item.State != dc.StateCompleted {
			items = append(items, item)
		}
	}
	return items, nil
}
