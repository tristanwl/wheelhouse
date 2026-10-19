// Package sabnzbd is the SABnzbd download-client provider, ported from
// lib/providers/download-client/sabnzbd.ts.
//
// SABnzbd takes its API key as a query parameter, so every upstream call goes
// through the core HTTP client with the key registered as a secret: no error,
// log line or returned value can contain it.
package sabnzbd

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	dc "github.com/tristanwl/wheelhouse/server/internal/providers/downloadclient"
)

// ID is the provider id.
const ID = "sabnzbd"

// Config is the provider's settings.
type Config struct {
	URL    string `json:"url" label:"URL" config:"required,url"`
	APIKey string `json:"apiKey" label:"API key" config:"required,secret"`
}

// Definition describes the provider for the registry.
func Definition() core.Definition {
	return core.Definition{
		ID:           ID,
		Role:         core.RoleDownloadClient,
		DisplayName:  "SABnzbd",
		Config:       Config{},
		Capabilities: dc.Capabilities{Protocol: dc.Usenet, CanPause: true, UsenetServers: true, Seeding: false},
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
	opts := core.HTTPOptions{BaseURL: cfg.URL, Label: label, Secrets: []string{cfg.APIKey}}
	if tweak != nil {
		tweak(&opts)
	}
	http, err := core.NewHTTPClient(opts)
	if err != nil {
		return nil, err
	}
	return &client{http: http, label: label, apiKey: cfg.APIKey, redactor: core.NewRedactor([]string{cfg.APIKey})}, nil
}

// SABnzbd reports sizes in MB where 1 MB = 1024 * 1024 bytes.
const mb = 1024 * 1024

// SABnzbd's queue view collapses every post-download phase (Extracting,
// Verifying, Repairing, Moving, Running scripts) into "Downloading"; that is
// baked into its API. The accurate per-job phase only appears in mode=history,
// which documents itself as "the jobs still in post-processing and the
// database history". So anything in history that is not Completed or Failed is
// still being processed, and tells us the real phase.
func postprocDone(status string) bool { return status == "Completed" || status == "Failed" }

// "Refused connection from ..." is SABnzbd's host whitelist rejecting some other
// client's web UI access. It says nothing about download health and would read
// as a false alarm on a dashboard.
var ignoredWarning = regexp.MustCompile(`^Refused connection`)

type slot struct {
	ID       string    `json:"nzo_id"`
	Filename string    `json:"filename"`
	Status   string    `json:"status"`
	Percent  dc.Number `json:"percentage"`
	MB       dc.Number `json:"mb"`
	MBLeft   dc.Number `json:"mbleft"`
	TimeLeft string    `json:"timeleft"`
}

type queue struct {
	Status     string    `json:"status"`
	Paused     bool      `json:"paused"`
	KBPerSec   dc.Number `json:"kbpersec"`
	SlotsTotal dc.Number `json:"noofslots_total"`
	TimeLeft   string    `json:"timeleft"`
	Version    string    `json:"version"`
	Slots      []slot    `json:"slots"`
}

type queueResponse struct {
	Queue *queue `json:"queue"`
}

type server struct {
	Name        string    `json:"name"`
	Enable      dc.Number `json:"enable"`
	Connections dc.Number `json:"connections"`
	SSL         dc.Number `json:"ssl"`
}

type configResponse struct {
	Config *struct {
		Servers []server `json:"servers"`
	} `json:"config"`
}

type warning struct {
	Type string    `json:"type"`
	Text string    `json:"text"`
	Time dc.Number `json:"time"`
}

type warningsResponse struct {
	Warnings []warning `json:"warnings"`
}

type historyResponse struct {
	History *struct {
		Slots []struct {
			ID     string `json:"nzo_id"`
			Status string `json:"status"`
		} `json:"slots"`
	} `json:"history"`
}

// ParseDuration reads SABnzbd's durations: H:MM:SS, or D:HH:MM:SS once past a
// day. Zero means "unknown", as does anything malformed.
//
// Difference from the TypeScript parseSabDuration: parts must be plain digits,
// where JavaScript's Number() also accepts "", "0x10", "1e2" and "1.5".
func ParseDuration(value string) (seconds int64, ok bool) {
	if value == "" {
		return 0, false
	}
	parts := strings.Split(value, ":")
	if len(parts) < 3 || len(parts) > 4 {
		return 0, false
	}
	nums := make([]int64, len(parts))
	for i, p := range parts {
		if p == "" || len(p) > 9 {
			return 0, false
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return 0, false
			}
		}
		nums[i], _ = strconv.ParseInt(p, 10, 64)
	}
	// Three parts are H:MM:SS; a fourth, leading one is days.
	var days int64
	if len(nums) == 4 {
		days, nums = nums[0], nums[1:]
	}
	total := days*86400 + nums[0]*3600 + nums[1]*60 + nums[2]
	if total <= 0 {
		return 0, false
	}
	return total, true
}

func etaPtr(value string) *int64 {
	if s, ok := ParseDuration(value); ok {
		return &s
	}
	return nil
}

func slotState(status, phase string) (dc.State, string) {
	if phase != "" {
		return dc.StatePostprocessing, phase
	}
	switch status {
	case "Downloading", "Fetching":
		return dc.StateDownloading, ""
	case "Paused":
		return dc.StatePaused, ""
	case "Queued":
		return dc.StateQueued, ""
	case "Grabbing", "Propagating":
		return dc.StateQueued, status
	}
	// A phase we have not seen: keep the client's wording instead of guessing.
	return dc.StateDownloading, status
}

type client struct {
	http     *core.HTTPClient
	label    string
	apiKey   string
	redactor *core.Redactor
}

// api calls one mode and decodes the JSON answer into out.
func (c *client) api(ctx context.Context, mode string, extra url.Values, maxBytes int64, out any) error {
	q := url.Values{"mode": {mode}, "output": {"json"}, "apikey": {c.apiKey}}
	for k, v := range extra {
		q[k] = v
	}
	res, err := c.http.Do(ctx, "/api", core.Request{Query: q, MaxBytes: maxBytes})
	if err != nil {
		// SABnzbd 4.x answers a wrong key with 403 and a plain-text body.
		var pe *core.ProviderError
		if errors.As(err, &pe) && pe.Status == 403 {
			return &core.ProviderError{Message: c.label + ": API key incorrect", Status: 403}
		}
		return err
	}
	if err := core.CheckJSON(res.Body, core.JSONLimits{}); err != nil {
		return &core.ProviderError{Message: c.label + " /api " + core.InvalidJSONText(err)}
	}
	// Some SABnzbd versions answer errors with 200 and an error field.
	var probe struct {
		Error any `json:"error"`
	}
	if json.Unmarshal(res.Body, &probe) == nil {
		if msg, ok := probe.Error.(string); ok && msg != "" {
			return &core.ProviderError{Message: c.redactor.Redact(c.label + ": " + msg)}
		}
	}
	if err := core.DecodeJSON(ctx, res.Body, out, core.JSONLimits{}); err != nil {
		// Not err.Error(): decoder errors can quote parts of the body.
		return &core.ProviderError{Message: c.label + " /api " + core.InvalidJSONText(err)}
	}
	return nil
}

func (c *client) unexpected() error {
	return &core.ProviderError{Message: c.label + " /api returned an unexpected response"}
}

func (c *client) queue(ctx context.Context, extra url.Values) (*queue, error) {
	var r queueResponse
	if err := c.api(ctx, "queue", extra, core.MediumListBytes, &r); err != nil {
		return nil, err
	}
	if r.Queue == nil {
		return nil, c.unexpected()
	}
	return r.Queue, nil
}

// TestConnection implements dc.Client.
func (c *client) TestConnection(ctx context.Context) core.ConnectionResult {
	q, err := c.queue(ctx, url.Values{"limit": {"1"}})
	if err != nil {
		return core.ConnectionResult{Error: dc.ErrorText(err)}
	}
	return core.ConnectionResult{OK: true, Version: q.Version}
}

// Status implements dc.Client.
func (c *client) Status(ctx context.Context) (dc.Status, error) {
	var (
		q        *queue
		cfg      configResponse
		warnings warningsResponse
		errs     [3]error
		wg       sync.WaitGroup
	)
	wg.Go(func() { q, errs[0] = c.queue(ctx, nil) })
	wg.Go(func() {
		errs[1] = c.api(ctx, "get_config", url.Values{"section": {"servers"}}, core.SmallResponseBytes, &cfg)
	})
	wg.Go(func() { errs[2] = c.api(ctx, "warnings", nil, core.SmallResponseBytes, &warnings) })
	wg.Wait()
	if err := firstNonNil(errs[:]); err != nil {
		return dc.Status{}, err
	}
	if cfg.Config == nil {
		return dc.Status{}, c.unexpected()
	}

	var kept []warning
	for _, w := range warnings.Warnings {
		if !ignoredWarning.MatchString(w.Text) {
			kept = append(kept, w)
		}
	}
	if len(kept) > 3 {
		kept = kept[len(kept)-3:]
	}
	recent := make([]dc.Warning, 0, len(kept))
	for _, w := range kept {
		out := dc.Warning{
			// Defence in depth: SABnzbd's own warnings can quote request URLs.
			Message:  c.redactor.Redact(w.Text),
			Severity: dc.SeverityWarning,
		}
		if w.Type == "ERROR" {
			out.Severity = dc.SeverityError
		}
		if secs, ok := warningTime(w.Time); ok {
			out.At = time.Unix(secs, 0).UTC().Format("2006-01-02T15:04:05.000Z")
		}
		if out.Message != "" {
			recent = append(recent, out)
		}
	}

	servers := make([]dc.UsenetServer, 0, len(cfg.Config.Servers))
	for _, s := range cfg.Config.Servers {
		servers = append(servers, dc.UsenetServer{
			Name:        s.Name,
			Connections: s.Connections.Whole(),
			SSL:         s.SSL.Float() != 0,
			Enabled:     s.Enable.Float() != 0,
		})
	}

	return dc.Status{
		Paused:     q.Paused,
		StatusText: q.Status,
		// Difference: clamped at zero (the TypeScript passes a negative through).
		SpeedBytesPerSec: q.KBPerSec.Times(1024),
		QueueCount:       q.SlotsTotal.Whole(),
		ETASeconds:       etaPtr(q.TimeLeft),
		Version:          q.Version,
		Warnings:         recent,
		Usenet:           &dc.UsenetStatus{Servers: servers},
	}, nil
}

// warningTime accepts timestamps a time.Time can format as a four-digit year.
func warningTime(n dc.Number) (int64, bool) {
	if !n.Valid() || n.Float() < 0 || n.Float() > 253402300799 {
		return 0, false
	}
	return int64(n.Float()), true
}

func firstNonNil(errs []error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// Items implements dc.Client.
func (c *client) Items(ctx context.Context) ([]dc.Item, error) {
	var (
		q    *queue
		hist historyResponse
		errs [2]error
		wg   sync.WaitGroup
	)
	wg.Go(func() { q, errs[0] = c.queue(ctx, nil) })
	wg.Go(func() { errs[1] = c.api(ctx, "history", url.Values{"limit": {"25"}}, core.SmallResponseBytes, &hist) })
	wg.Wait()
	if err := firstNonNil(errs[:]); err != nil {
		return nil, err
	}
	if hist.History == nil {
		return nil, c.unexpected()
	}

	phases := map[string]string{}
	for _, s := range hist.History.Slots {
		if !postprocDone(s.Status) {
			phases[s.ID] = s.Status
		}
	}

	items := make([]dc.Item, 0, len(q.Slots))
	for _, s := range q.Slots {
		size := s.MB.Times(mb)
		left := min(s.MBLeft.Times(mb), size)
		state, detail := slotState(s.Status, phases[s.ID])
		items = append(items, dc.Item{
			ID:             s.ID,
			Name:           s.Filename,
			Protocol:       dc.Usenet,
			State:          state,
			StateDetail:    detail,
			Progress:       dc.Clamp01(s.Percent.Float() / 100),
			SizeBytes:      size,
			RemainingBytes: left,
			ETASeconds:     etaPtr(s.TimeLeft),
		})
	}
	return items, nil
}
