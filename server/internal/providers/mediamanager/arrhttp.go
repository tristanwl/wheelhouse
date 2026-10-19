package mediamanager

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
)

// ArrConfig is the config of Sonarr and Radarr alike.
type ArrConfig struct {
	URL    string `json:"url" label:"URL" config:"required,url"`
	APIKey string `json:"apiKey" label:"API key" config:"required,secret"`
}

// LibraryCacheTTL is how long a library list stays cached. Short on purpose:
// the point is to collapse the burst of requests one page load makes (and repeat
// visits), not to hide changes. Writes through the client clear it immediately.
const LibraryCacheTTL = 5 * time.Second

// ArrOptions tune the HTTP client; the zero value is what production uses.
type ArrOptions struct {
	Timeout          time.Duration
	MaxResponseBytes int64
	Logger           *slog.Logger
}

// Arr is the HTTP dialect Sonarr and Radarr share: an X-Api-Key header and JSON
// under /api/v3 (below any path prefix in the configured URL).
type Arr struct {
	http  *core.HTTPClient
	label string
	err   error // set when the client could not be built; every call returns it
}

// NewArr builds the client for one instance. The label (the instance id)
// prefixes error messages. A bad URL does not panic or leak: every call fails
// with the (redacted) reason, and Definition validation normally rejects it
// earlier.
func NewArr(cfg ArrConfig, label string, opts ArrOptions) *Arr {
	h := http.Header{}
	h.Set("X-Api-Key", cfg.APIKey)
	client, err := core.NewHTTPClient(core.HTTPOptions{
		BaseURL:          cfg.URL,
		Label:            label,
		Headers:          h,
		Secrets:          []string{cfg.APIKey},
		Timeout:          opts.Timeout,
		MaxResponseBytes: opts.MaxResponseBytes,
		Logger:           opts.Logger,
	})
	return &Arr{http: client, label: label, err: err}
}

// call performs one request under /api/v3 and returns the body. An empty body is
// not an error: some endpoints (Radarr's DELETE, for one) answer 200 with
// nothing rather than 204, so the body itself is checked, not just the status.
func (a *Arr) call(ctx context.Context, path string, req core.Request) ([]byte, error) {
	if a.err != nil {
		return nil, a.err
	}
	if req.MaxBytes <= 0 {
		req.MaxBytes = core.MediumListBytes
	}
	res, err := a.http.Do(ctx, "/api/v3/"+path, req)
	if err != nil {
		// A wrong key is a plain 401 in the *arr apps.
		var pe *core.ProviderError
		if errors.As(err, &pe) && pe.Status == http.StatusUnauthorized {
			return nil, &core.ProviderError{Message: a.label + ": API key incorrect", Status: http.StatusUnauthorized}
		}
		return nil, err
	}
	return res.Body, nil
}

// smallReq is the request for an endpoint with a short answer (status, health,
// configuration lists): a low byte cap and element limit.
func smallReq() core.Request {
	return core.Request{MaxBytes: core.SmallResponseBytes, Limits: core.JSONLimits{MaxElements: 10_000}}
}

// LookupLimits bounds the answer of a lookup (a handful of search results).
var LookupLimits = core.JSONLimits{MaxElements: 5_000}

func (a *Arr) invalidJSON(path string, cause error) error {
	// Not the decoder's message: it can quote parts of the body.
	return &core.ProviderError{Message: a.label + " " + strings.SplitN(path, "?", 2)[0] + " " + core.InvalidJSONText(cause)}
}

// Get performs a GET under /api/v3 and decodes the JSON body into T. An empty
// body yields the zero value.
func Get[T any](ctx context.Context, a *Arr, path string, req core.Request) (T, error) {
	var out T
	body, err := a.call(ctx, path, req)
	if err != nil {
		return out, err
	}
	if len(body) == 0 {
		return out, nil
	}
	if err := core.DecodeJSON(ctx, body, &out, req.Limits); err != nil {
		var zero T
		return zero, a.invalidJSON(path, err)
	}
	return out, nil
}

// Send performs a write under /api/v3 (POST, PUT or DELETE) with an optional
// JSON body, decodes the response into T, and clears the response cache even if
// the call fails part-way.
func Send[T any](ctx context.Context, a *Arr, method, path string, query url.Values, body any) (T, error) {
	var out T
	if a.err != nil {
		return out, a.err
	}
	defer a.http.Invalidate()
	req := core.Request{Method: method, Query: query}
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return out, &core.ProviderError{Message: a.label + ": could not encode the request"}
		}
		req.Body = encoded
		req.Header = http.Header{"Content-Type": {"application/json"}}
	}
	raw, err := a.call(ctx, path, req)
	if err != nil {
		return out, err
	}
	if len(raw) == 0 {
		return out, nil
	}
	if err := core.DecodeJSON(ctx, raw, &out, req.Limits); err != nil {
		var zero T
		return zero, a.invalidJSON(path, err)
	}
	return out, nil
}

// Base is the part of a client that is the same for Sonarr and Radarr. Embed it
// and add the kind-specific calls.
type Base struct {
	arr  *Arr
	kind Kind
}

// NewBase builds the shared calls on top of arr.
func NewBase(arr *Arr, kind Kind) Base { return Base{arr: arr, kind: kind} }

// Arr gives the embedding client the HTTP helpers.
func (b Base) Arr() *Arr { return b.arr }

// Kind implements Manager.
func (b Base) Kind() Kind { return b.kind }

// SystemStatus implements Manager.
func (b Base) SystemStatus(ctx context.Context) (SystemStatus, error) {
	return Get[SystemStatus](ctx, b.arr, "system/status", core.Request{})
}

// Health implements Manager.
func (b Base) Health(ctx context.Context) ([]HealthIssue, error) {
	h, err := Get[[]HealthIssue](ctx, b.arr, "health", core.Request{})
	return nonNil(h), err
}

// Indexers implements Manager.
func (b Base) Indexers(ctx context.Context) ([]Indexer, error) {
	i, err := Get[[]Indexer](ctx, b.arr, "indexer", core.Request{})
	return nonNil(i), err
}

// QueueSummary implements Manager: the first page (25 records) and the total.
func (b Base) QueueSummary(ctx context.Context) (QueueSummary, error) {
	q, err := Get[QueueSummary](ctx, b.arr, "queue", core.Request{MaxBytes: core.MediumListBytes,
		Query: url.Values{"page": {"1"}, "pageSize": {"25"}},
	})
	q.Records = nonNil(q.Records)
	return q, err
}

// RootFolders implements Manager.
func (b Base) RootFolders(ctx context.Context) ([]RootFolder, error) {
	r, err := Get[[]RootFolder](ctx, b.arr, "rootfolder", core.Request{})
	return nonNil(r), err
}

// QualityProfiles implements Manager.
func (b Base) QualityProfiles(ctx context.Context) ([]QualityProfile, error) {
	p, err := Get[[]QualityProfile](ctx, b.arr, "qualityprofile", core.Request{})
	return nonNil(p), err
}

// Tags implements Manager.
func (b Base) Tags(ctx context.Context) ([]Tag, error) {
	t, err := Get[[]Tag](ctx, b.arr, "tag", core.Request{})
	return nonNil(t), err
}

// TestConnection implements Manager. The error text is already redacted.
func (b Base) TestConnection(ctx context.Context) core.ConnectionResult {
	status, err := b.SystemStatus(ctx)
	if err != nil {
		return core.ConnectionResult{Error: err.Error()}
	}
	return core.ConnectionResult{OK: true, Version: status.Version}
}

// Status implements Manager: four calls in parallel.
func (b Base) Status(ctx context.Context) (Status, error) {
	var (
		wg       sync.WaitGroup
		status   SystemStatus
		health   []HealthIssue
		queue    QueueSummary
		indexers []Indexer
		errs     [4]error
	)
	run := func(i int, f func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = f()
		}()
	}
	run(0, func() (err error) { status, err = b.SystemStatus(ctx); return })
	run(1, func() (err error) { health, err = b.Health(ctx); return })
	run(2, func() (err error) { queue, err = b.QueueSummary(ctx); return })
	run(3, func() (err error) { indexers, err = b.Indexers(ctx); return })
	wg.Wait()
	if err := firstError(errs[:]); err != nil {
		return Status{}, err
	}

	out := Status{
		Version:    status.Version,
		Health:     health,
		QueueCount: queue.TotalRecords,
		Indexers:   IndexerSummary{Total: len(indexers), Issues: []string{}},
	}
	for _, i := range indexers {
		if i.EnableRss {
			out.Indexers.Enabled++
		}
	}
	for _, h := range health {
		if strings.HasPrefix(h.Source, "Indexer") {
			out.Indexers.Issues = append(out.Indexers.Issues, h.Message)
		}
	}
	return out, nil
}

func firstError(errs []error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// nonNil makes an absent or null list an empty one, so it encodes as [].
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
