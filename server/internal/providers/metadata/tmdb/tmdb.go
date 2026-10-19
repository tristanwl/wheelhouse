// Package tmdb is the TMDB metadata provider: the Go port of
// lib/providers/metadata/tmdb.ts plus the Explore-specific list and detail
// functions of lib/tmdb-client.ts.
//
// TMDB takes its API key as an api_key query parameter. The core HTTP client
// never echoes request URLs in errors and redacts the key from them as well.
package tmdb

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	"github.com/tristanwl/wheelhouse/server/internal/providers/metadata"
)

// DefaultBaseURL is the TMDB v3 API root.
const DefaultBaseURL = "https://api.themoviedb.org/3"

// Config is the TMDB provider's settings.
type Config struct {
	APIKey string `json:"apiKey" label:"API key" config:"required,secret"`
	// URL exists so tests (and anyone behind a TMDB-compatible proxy) can point
	// at another API root; it is the full base including "/3". The environment
	// variable is TMDB_BASE_URL.
	URL string `json:"url" label:"Base URL" config:"url"`
}

// Definition is the TMDB provider.
func Definition() core.Definition {
	return core.Definition{
		ID:           "tmdb",
		Role:         core.RoleMetadata,
		DisplayName:  "TMDB",
		Config:       Config{},
		Capabilities: metadata.Capabilities{},
		Create: func(config any, pc core.ProviderContext) any {
			c, err := newClient(config.(Config), pc.InstanceID, nil)
			if err != nil {
				// Unreachable: the config passed validation, so the URL is good.
				return &Client{buildErr: err, now: time.Now}
			}
			return c
		},
	}
}

// Client talks to TMDB. It is safe for concurrent use.
type Client struct {
	http     *core.HTTPClient
	label    string
	apiKey   string
	buildErr error
	now      func() time.Time
}

var _ metadata.Client = (*Client)(nil)

// newClient builds the client; tune, if set, adjusts the HTTP options (tests
// use it for short timeouts and size limits).
func newClient(cfg Config, label string, tune func(*core.HTTPOptions)) (*Client, error) {
	base := cfg.URL
	if base == "" {
		base = DefaultBaseURL
	}
	opts := core.HTTPOptions{
		BaseURL: base,
		Label:   label,
		Headers: http.Header{"Accept": {"application/json"}},
		Secrets: []string{cfg.APIKey},
	}
	if tune != nil {
		tune(&opts)
	}
	h, err := core.NewHTTPClient(opts)
	if err != nil {
		return nil, err
	}
	return &Client{http: h, label: label, apiKey: cfg.APIKey, now: time.Now}, nil
}

// Get fetches a TMDB v3 path and decodes the JSON answer into out. The API key
// is added here and never leaves this client. A key in params is overridden.
func (c *Client) Get(ctx context.Context, path string, params map[string]string, out any) error {
	if c.buildErr != nil {
		return c.buildErr
	}
	q := url.Values{}
	for k, v := range params {
		q.Set(k, v)
	}
	q.Set("api_key", c.apiKey)
	return c.http.JSON(ctx, path, core.Request{Query: q, MaxBytes: core.SmallResponseBytes * 2}, out)
}

// TestConnection makes the cheapest authenticated call: a few hundred bytes of
// static configuration.
func (c *Client) TestConnection(ctx context.Context) core.ConnectionResult {
	var discard struct{}
	if err := c.Get(ctx, "/configuration", nil, &discard); err != nil {
		var pe *core.ProviderError
		if errors.As(err, &pe) && (pe.Status == http.StatusUnauthorized || pe.Status == http.StatusForbidden) {
			return core.ConnectionResult{Error: c.label + ": API key rejected"}
		}
		return core.ConnectionResult{Error: err.Error()}
	}
	return core.ConnectionResult{OK: true}
}
