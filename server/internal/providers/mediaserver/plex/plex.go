// Package plex is the Plex media-server provider, the Go port of
// lib/providers/media-server/plex.ts.
//
// Plex answers a missing or wrong token with HTTP 401 and an HTML page. The
// token goes in the X-Plex-Token header and is never sent to /identity, which
// needs none and works before the server is claimed.
package plex

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	"github.com/tristanwl/wheelhouse/server/internal/providers/mediaserver"
)

// Config is the Plex provider's settings.
type Config struct {
	URL string `json:"url" label:"URL" config:"required,url"`
	// Optional: a server that has not been claimed yet has no token to give.
	Token string `json:"token" label:"Token" config:"secret"`
}

// Definition is the Plex provider.
func Definition() core.Definition {
	return core.Definition{
		ID:           "plex",
		Role:         core.RoleMediaServer,
		DisplayName:  "Plex",
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
	http  *core.HTTPClient
	label string
	token string
}

// newClient builds the client; tune, if set, adjusts the HTTP options (tests
// use it for short timeouts and size limits).
func newClient(cfg Config, label string, tune func(*core.HTTPOptions)) (*client, error) {
	opts := core.HTTPOptions{
		BaseURL: cfg.URL,
		Label:   label,
		Headers: http.Header{"Accept": {"application/json"}},
	}
	if cfg.Token != "" {
		opts.Secrets = []string{cfg.Token}
	}
	if tune != nil {
		tune(&opts)
	}
	h, err := core.NewHTTPClient(opts)
	if err != nil {
		return nil, err
	}
	return &client{http: h, label: label, token: cfg.Token}, nil
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

// Plex wraps every JSON response in { "MediaContainer": ... }. The pointer
// tells a missing container apart from an empty one.
type container[T any] struct {
	MediaContainer *T `json:"MediaContainer"`
}

type identity struct {
	Claimed bool   `json:"claimed"`
	Version string `json:"version"`
}

type serverInfo struct {
	FriendlyName string `json:"friendlyName"`
	Version      string `json:"version"`
}

type sessions struct {
	Size *int `json:"size"`
}

func isAuthError(err error) bool {
	var pe *core.ProviderError
	return errors.As(err, &pe) && (pe.Status == http.StatusUnauthorized || pe.Status == http.StatusForbidden)
}

// get decodes a Plex response, unwrapping MediaContainer. A valid JSON answer
// without one (not Plex, or a proxy's page) is an error, where the TypeScript
// version would crash on the missing property.
func get[T any](ctx context.Context, c *client, path string, authed bool) (T, error) {
	var zero T
	req := core.Request{}
	if authed {
		req.Header = http.Header{"X-Plex-Token": {c.token}}
	}
	var wrapped container[T]
	if err := c.http.JSON(ctx, path, req, &wrapped); err != nil {
		return zero, err
	}
	if wrapped.MediaContainer == nil {
		return zero, &core.ProviderError{Message: c.label + " " + path + ": unexpected response"}
	}
	return *wrapped.MediaContainer, nil
}

// identity needs no token and works before the server is claimed.
func (c *client) identity(ctx context.Context) (identity, error) {
	return get[identity](ctx, c, "/identity", false)
}

func (c *client) TestConnection(ctx context.Context) core.ConnectionResult {
	id, err := c.identity(ctx)
	if err != nil {
		return core.ConnectionResult{Error: err.Error()}
	}
	if c.token != "" {
		var discard json.RawMessage // like the original, only needs a JSON answer
		if err := c.http.JSON(ctx, "/", core.Request{Header: http.Header{"X-Plex-Token": {c.token}}}, &discard); err != nil {
			if isAuthError(err) {
				return core.ConnectionResult{Version: id.Version, Error: c.label + ": token rejected"}
			}
			return core.ConnectionResult{Error: err.Error()}
		}
	}
	return core.ConnectionResult{OK: true, Version: id.Version}
}

func (c *client) Status(ctx context.Context) (mediaserver.Status, error) {
	id, err := c.identity(ctx)
	if err != nil {
		return mediaserver.Status{}, err
	}

	if !id.Claimed {
		return mediaserver.Status{
			State:   mediaserver.StateNeedsSetup,
			Notice:  "Not claimed yet. Finish setting up Plex in its web interface.",
			Version: id.Version,
		}, nil
	}
	if c.token == "" {
		return mediaserver.Status{
			State:   mediaserver.StateOK,
			Notice:  "No Plex token is configured, so only basic status is shown.",
			Version: id.Version,
		}, nil
	}

	var (
		info    serverInfo
		sess    sessions
		infoErr error
		sessErr error
		wg      sync.WaitGroup
	)
	wg.Go(func() { info, infoErr = get[serverInfo](ctx, c, "/", true) })
	wg.Go(func() { sess, sessErr = get[sessions](ctx, c, "/status/sessions", true) })
	wg.Wait()
	if err := errors.Join(infoErr, sessErr); err != nil {
		// The server is up (identity answered), so do not report it as down.
		first := infoErr
		if first == nil {
			first = sessErr
		}
		notice := "The Plex token was rejected, so only basic status is shown."
		if !isAuthError(first) {
			notice = "Couldn't read Plex details (" + core.Redact(first.Error(), c.token) + ")."
		}
		return mediaserver.Status{State: mediaserver.StateOK, Notice: notice, Version: id.Version}, nil
	}

	version := info.Version
	if version == "" {
		version = id.Version
	}
	return mediaserver.Status{
		State:          mediaserver.StateOK,
		Name:           info.FriendlyName,
		Version:        version,
		ActiveSessions: sess.Size,
		Authenticated:  true,
	}, nil
}
