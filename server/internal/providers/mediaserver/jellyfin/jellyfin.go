// Package jellyfin is the Jellyfin media-server provider, the Go port of
// lib/providers/media-server/jellyfin.ts.
//
// Jellyfin 12 only accepts the key in the Authorization header
// (`MediaBrowser Token="..."`; the older X-Emby-Token header gets a 401), and
// that form also works on 10.x. A missing or wrong key gets HTTP 401 with an
// empty body. /System/Info/Public needs no key and works before the setup
// wizard has run.
package jellyfin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	"github.com/tristanwl/wheelhouse/server/internal/providers/mediaserver"
)

// Config is the Jellyfin provider's settings.
type Config struct {
	URL string `json:"url" label:"URL" config:"required,url"`
	// Optional: without a key only the public status is available.
	APIKey string `json:"apiKey" label:"API key" config:"secret"`
}

// Definition is the Jellyfin provider.
func Definition() core.Definition {
	return core.Definition{
		ID:           "jellyfin",
		Role:         core.RoleMediaServer,
		DisplayName:  "Jellyfin",
		Config:       Config{},
		Capabilities: mediaserver.Capabilities{Sessions: true},
		Create: func(config any, pc core.ProviderContext) any {
			c, err := newClient(config.(Config), pc.InstanceID, nil)
			if err != nil {
				// Unreachable: the config passed validation, so the URL is good.
				return &brokenClient{err: err}
			}
			return c
		},
	}
}

type client struct {
	http   *core.HTTPClient
	label  string
	apiKey string
}

// newClient builds the client; tune, if set, adjusts the HTTP options (tests
// use it for short timeouts and size limits).
func newClient(cfg Config, label string, tune func(*core.HTTPOptions)) (*client, error) {
	opts := core.HTTPOptions{
		BaseURL: cfg.URL,
		Label:   label,
		Headers: http.Header{"Accept": {"application/json"}},
	}
	if cfg.APIKey != "" {
		opts.Secrets = []string{cfg.APIKey}
	}
	if tune != nil {
		tune(&opts)
	}
	h, err := core.NewHTTPClient(opts)
	if err != nil {
		return nil, err
	}
	return &client{http: h, label: label, apiKey: cfg.APIKey}, nil
}

// brokenClient stands in when the HTTP client cannot be built; every call
// reports the (already redacted) reason.
type brokenClient struct{ err error }

func (b *brokenClient) TestConnection(context.Context) core.ConnectionResult {
	return core.ConnectionResult{Error: b.err.Error()}
}

func (b *brokenClient) Status(context.Context) (mediaserver.Status, error) {
	return mediaserver.Status{}, b.err
}

type publicInfo struct {
	ServerName string `json:"ServerName"`
	Version    string `json:"Version"`
	// A pointer: only an explicit false means the wizard has not run.
	StartupWizardCompleted *bool `json:"StartupWizardCompleted"`
}

type info struct {
	ServerName string `json:"ServerName"`
	Version    string `json:"Version"`
}

type session struct {
	// Any non-null value means the session is playing something.
	NowPlayingItem any `json:"NowPlayingItem"`
}

func isAuthError(err error) bool {
	var pe *core.ProviderError
	return errors.As(err, &pe) && (pe.Status == http.StatusUnauthorized || pe.Status == http.StatusForbidden)
}

// authed is the per-request header carrying the key.
func (c *client) authed() core.Request {
	return core.Request{Header: http.Header{"Authorization": {`MediaBrowser Token="` + c.apiKey + `"`}}}
}

func (c *client) publicInfo(ctx context.Context) (publicInfo, error) {
	var p publicInfo
	err := c.http.JSON(ctx, "/System/Info/Public", core.Request{}, &p)
	return p, err
}

func (c *client) TestConnection(ctx context.Context) core.ConnectionResult {
	pub, err := c.publicInfo(ctx)
	if err != nil {
		return core.ConnectionResult{Error: err.Error()}
	}
	if c.apiKey != "" {
		var discard json.RawMessage // like the original, only needs a JSON answer
		if err := c.http.JSON(ctx, "/System/Info", c.authed(), &discard); err != nil {
			if isAuthError(err) {
				return core.ConnectionResult{Version: pub.Version, Error: c.label + ": API key rejected"}
			}
			return core.ConnectionResult{Error: err.Error()}
		}
	}
	return core.ConnectionResult{OK: true, Version: pub.Version}
}

func (c *client) Status(ctx context.Context) (mediaserver.Status, error) {
	pub, err := c.publicInfo(ctx)
	if err != nil {
		return mediaserver.Status{}, err
	}

	if pub.StartupWizardCompleted != nil && !*pub.StartupWizardCompleted {
		return mediaserver.Status{
			State:   mediaserver.StateNeedsSetup,
			Notice:  "Setup isn't finished yet. Finish setting up Jellyfin in its web interface.",
			Name:    pub.ServerName,
			Version: pub.Version,
		}, nil
	}
	if c.apiKey == "" {
		return mediaserver.Status{
			State:   mediaserver.StateOK,
			Notice:  "No Jellyfin API key is configured, so only basic status is shown.",
			Name:    pub.ServerName,
			Version: pub.Version,
		}, nil
	}

	var (
		inf     info
		list    []session
		infoErr error
		listErr error
		wg      sync.WaitGroup
	)
	wg.Go(func() { infoErr = c.http.JSON(ctx, "/System/Info", c.authed(), &inf) })
	wg.Go(func() { listErr = c.http.JSON(ctx, "/Sessions", c.authed(), &list) })
	wg.Wait()
	if err := errors.Join(infoErr, listErr); err != nil {
		// The server is up (the public endpoint answered), so do not report it as down.
		first := infoErr
		if first == nil {
			first = listErr
		}
		notice := "The Jellyfin API key was rejected, so only basic status is shown."
		if !isAuthError(first) {
			notice = "Couldn't read Jellyfin details (" + core.Redact(first.Error(), c.apiKey) + ")."
		}
		return mediaserver.Status{State: mediaserver.StateOK, Notice: notice, Name: pub.ServerName, Version: pub.Version}, nil
	}

	// Only sessions playing something count; Jellyfin also lists every
	// connected client (an open web page, say) as a session.
	playing := 0
	for _, s := range list {
		if s.NowPlayingItem != nil {
			playing++
		}
	}
	st := mediaserver.Status{
		State:          mediaserver.StateOK,
		Name:           pub.ServerName,
		Version:        pub.Version,
		ActiveSessions: &playing,
		Authenticated:  true,
	}
	if inf.ServerName != "" {
		st.Name = inf.ServerName
	}
	if inf.Version != "" {
		st.Version = inf.Version
	}
	return st, nil
}
