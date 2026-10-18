package tmdb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	"github.com/tristanwl/wheelhouse/server/internal/providers/metadata"
	"github.com/tristanwl/wheelhouse/server/internal/providers/metadata/tmdb/tmdbtest"
)

var ctx = context.Background()

func registry(t *testing.T) *core.Registry {
	t.Helper()
	reg, err := core.NewRegistry(Definition())
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// create goes through validation like the runtime does. url "" means the default.
func create(t *testing.T, apiKey, url string) *Client {
	t.Helper()
	d, _ := registry(t).Get("tmdb")
	raw := map[string]any{"apiKey": apiKey}
	if url != "" {
		raw["url"] = url
	}
	cfg, err := d.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	return d.Create(cfg, core.ProviderContext{InstanceID: "tmdb", InstanceName: "TMDB"}).(*Client)
}

func TestIsAMetadataProviderWhoseKeyIsASecret(t *testing.T) {
	d, _ := registry(t).Get("tmdb")
	if d.Role != core.RoleMetadata || d.DisplayName != "TMDB" {
		t.Errorf("def = %+v", d)
	}
	if keys := d.SecretKeys(); len(keys) != 1 || keys[0] != "apiKey" {
		t.Errorf("secret keys = %v", keys)
	}
	for name, raw := range map[string]map[string]any{
		"empty":        {},
		"empty key":    {"apiKey": ""},
		"bad base url": {"apiKey": tmdbtest.GoodKey, "url": "nope"},
		"empty url":    {"apiKey": tmdbtest.GoodKey, "url": ""},
	} {
		if _, err := d.Decode(raw); err == nil {
			t.Errorf("%s: should be rejected", name)
		}
	}
	if _, err := d.Decode(map[string]any{"apiKey": tmdbtest.GoodKey}); err != nil {
		t.Error(err)
	}
	if _, err := d.Decode(map[string]any{"apiKey": tmdbtest.GoodKey, "url": "http://192.0.2.1:8080/3"}); err != nil {
		t.Error(err)
	}
	raw, _ := json.Marshal(d)
	if !strings.Contains(string(raw), `"capabilities":{}`) {
		t.Errorf("description = %s", raw)
	}
}

func TestTestsTheConnectionWithACheapAuthenticatedCall(t *testing.T) {
	srv := tmdbtest.Start(t, tmdbtest.Options{})
	c := create(t, tmdbtest.GoodKey, srv.APIRoot())
	if got := c.TestConnection(ctx); got != (core.ConnectionResult{OK: true}) {
		t.Errorf("got %+v", got)
	}
	if paths := srv.Paths(); len(paths) != 1 || paths[0] != "/3/configuration" {
		t.Errorf("paths = %v", paths)
	}
	if k := srv.Requests()[0].Query().Get("api_key"); k != tmdbtest.GoodKey {
		t.Errorf("api_key = %q", k)
	}
}

func TestReportsARejectedKeyWithoutEchoingIt(t *testing.T) {
	srv := tmdbtest.Start(t, tmdbtest.Options{})
	got := create(t, "wrong-key-value-123", srv.APIRoot()).TestConnection(ctx)
	if got.OK || got.Error != "tmdb: API key rejected" {
		t.Errorf("got %+v", got)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "wrong-key-value-123") {
		t.Errorf("leak in %s", raw)
	}
}

func TestReportsAnUnreachableTMDBWithoutEchoingTheKey(t *testing.T) {
	srv := tmdbtest.Start(t, tmdbtest.Options{})
	root := srv.APIRoot()
	srv.Close()
	got := create(t, tmdbtest.GoodKey, root).TestConnection(ctx)
	if got != (core.ConnectionResult{Error: "tmdb: connection refused"}) {
		t.Errorf("got %+v", got)
	}
}

func TestKeepsTheKeyOutOfErrorsFromOtherCallsToo(t *testing.T) {
	srv := tmdbtest.Start(t, tmdbtest.Options{})
	var out map[string]any
	err := create(t, "wrong-key-value-123", srv.APIRoot()).Get(ctx, "/movie/popular", nil, &out)
	if err == nil || err.Error() != "tmdb /movie/popular failed: 401 Unauthorized" {
		t.Fatalf("err = %v", err)
	}
	var pe *core.ProviderError
	if !errors.As(err, &pe) || pe.Status != 401 {
		t.Errorf("err = %#v", err)
	}
}

func TestAPIKeyParamCannotBeOverriddenByCallers(t *testing.T) {
	srv := tmdbtest.Start(t, tmdbtest.Options{})
	var out map[string]any
	err := create(t, tmdbtest.GoodKey, srv.APIRoot()).Get(ctx, "/movie/popular", map[string]string{"api_key": "other", "region": "US"}, &out)
	if err != nil {
		t.Fatal(err)
	}
	q := srv.Requests()[0].Query()
	if q.Get("api_key") != tmdbtest.GoodKey || q.Get("region") != "US" {
		t.Errorf("query = %v", q)
	}
}

func TestResolvesThroughTheRuntimeFromEnvironmentConfig(t *testing.T) {
	srv := tmdbtest.Start(t, tmdbtest.Options{})
	src := core.NewEnvConfigSource(map[string]string{"TMDB_API_KEY": tmdbtest.GoodKey, "TMDB_BASE_URL": srv.APIRoot()})
	clients, err := core.NewRuntime(registry(t), src).Clients(ctx, core.RoleMetadata)
	if err != nil || len(clients) != 1 || clients[0].Instance.ID != "tmdb" {
		t.Fatalf("clients = %v, %v", clients, err)
	}
	c := clients[0].Client.(metadata.Client)
	if !c.TestConnection(ctx).OK {
		t.Error("not ok")
	}
}

func TestRuntimeReportsAMissingKey(t *testing.T) {
	src := core.NewEnvConfigSource(map[string]string{"TMDB_BASE_URL": "http://192.0.2.1/3"})
	list, _ := core.NewRuntime(registry(t), src).ListInstances(ctx)
	if len(list) != 1 || list[0].Status != core.StatusInvalidConfig || !strings.Contains(list[0].Error, "apiKey") {
		t.Errorf("list = %+v", list)
	}
}

func TestTalksToTheConfiguredBaseURLInsteadOfTMDB(t *testing.T) {
	srv := tmdbtest.Start(t, tmdbtest.Options{})
	c := create(t, tmdbtest.GoodKey, srv.URL+"/3")
	if got := c.TestConnection(ctx); !got.OK {
		t.Fatalf("got %+v", got)
	}
	var popular struct {
		Results []Movie `json:"results"`
	}
	if err := c.Get(ctx, "/movie/popular", nil, &popular); err != nil {
		t.Fatal(err)
	}
	var ids []int
	for _, m := range popular.Results {
		ids = append(ids, m.ID)
	}
	if len(ids) != 3 || ids[0] != 101 || ids[1] != 102 || ids[2] != 103 {
		t.Errorf("ids = %v", ids)
	}
	if paths := srv.Paths(); len(paths) != 2 || paths[0] != "/3/configuration" || paths[1] != "/3/movie/popular" {
		t.Errorf("paths = %v", paths)
	}
}

func TestDefaultBaseURLIsTheTMDBAPI(t *testing.T) {
	if DefaultBaseURL != "https://api.themoviedb.org/3" {
		t.Errorf("DefaultBaseURL = %q", DefaultBaseURL)
	}
	c := create(t, tmdbtest.GoodKey, "")
	if c.http == nil {
		t.Fatal("no client")
	}
}

// --- broken upstreams ---

func serveRaw(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv.URL
}

func tuned(t *testing.T, url string, tune func(*core.HTTPOptions)) *Client {
	t.Helper()
	c, err := newClient(Config{APIKey: tmdbtest.GoodKey, URL: url}, "tmdb", tune)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestMalformedJSONIsAnErrorWithoutTheBody(t *testing.T) {
	url := serveRaw(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{<html>SECRET-BODY")) })
	c := tuned(t, url, nil)
	var out map[string]any
	err := c.Get(ctx, "/movie/popular", nil, &out)
	if err == nil || !strings.Contains(err.Error(), "invalid JSON") || strings.Contains(err.Error(), "SECRET-BODY") {
		t.Errorf("err = %v", err)
	}
	if res := c.TestConnection(ctx); res.OK || strings.Contains(res.Error, "SECRET-BODY") {
		t.Errorf("res = %+v", res)
	}
}

func TestOversizedResponseIsRejected(t *testing.T) {
	url := serveRaw(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(bytes.Repeat([]byte("x"), 5000)) })
	c := tuned(t, url, func(o *core.HTTPOptions) { o.MaxResponseBytes = 1024 })
	res := c.TestConnection(ctx)
	if res.OK || !strings.Contains(res.Error, "larger than 1024 bytes") {
		t.Errorf("res = %+v", res)
	}
}

func TestTimeoutIsReportedNotHung(t *testing.T) {
	srv := tmdbtest.Start(t, tmdbtest.Options{Delay: 5 * time.Second})
	c := tuned(t, srv.APIRoot(), func(o *core.HTTPOptions) { o.Timeout = 100 * time.Millisecond })
	start := time.Now()
	res := c.TestConnection(ctx)
	if res.OK || !strings.Contains(res.Error, "timed out after 100ms") {
		t.Errorf("res = %+v", res)
	}
	if time.Since(start) > 3*time.Second {
		t.Error("did not time out promptly")
	}
}

func TestServerErrorsAreReportedByStatusOnly(t *testing.T) {
	url := serveRaw(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("SECRET-BODY"))
	})
	res := tuned(t, url, nil).TestConnection(ctx)
	if res.OK || res.Error != "tmdb /configuration failed: 502 Bad Gateway" {
		t.Errorf("res = %+v", res)
	}
}
