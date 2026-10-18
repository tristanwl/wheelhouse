package tmdb

import (
	"bytes"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	"github.com/tristanwl/wheelhouse/server/internal/providers/hostiletest"
	"github.com/tristanwl/wheelhouse/server/internal/providers/metadata/tmdb/tmdbtest"
)

// The canary sweep: plant a secret as the API key (TMDB takes it in the query
// string, the hardest place to keep it out of errors), make TMDB fail in every
// way it can, and prove the secret appears in no result, error or log line.
func TestCanarySweep(t *testing.T) {
	s := hostiletest.NewSweep(t)
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	tune := func(o *core.HTTPOptions) {
		o.Logger = logger
		o.Timeout = 300 * time.Millisecond
	}
	// The hostile upstreams also get a tiny size limit; the real fakes answer
	// with bodies larger than that.
	tuneSmall := func(o *core.HTTPOptions) {
		tune(o)
		o.MaxResponseBytes = 64
	}
	record := func(label string, c *Client) {
		for i := range 2 {
			l := fmt.Sprintf("%s %d", label, i)
			s.AddJSON(l+" test", c.TestConnection(ctx))
			var out map[string]any
			s.AddErr(l+" get", c.Get(ctx, "/movie/popular", map[string]string{"region": "US"}, &out))
			_, err := c.Popular(ctx, 40, 1)
			s.AddErr(l+" popular", err)
			_, err = c.NowPlaying(ctx, 20, 1)
			s.AddErr(l+" now playing", err)
			_, err = c.RecentPhysicalReleases(ctx, 20, 1)
			s.AddErr(l+" physical", err)
			d, err := c.MovieDetail(ctx, 1)
			s.AddErr(l+" detail", err)
			s.AddJSON(l+" detail", d)
		}
	}

	for _, behaviour := range hostiletest.Behaviours {
		addr := hostiletest.Hostile(t, behaviour)
		base := "http://user:" + url.QueryEscape(hostiletest.Pass) + "@" + addr + "/3-" + url.PathEscape(hostiletest.Key)
		c, err := newClient(Config{APIKey: hostiletest.Key, URL: base}, "tmdb-"+behaviour, tuneSmall)
		if err != nil {
			t.Fatal(err)
		}
		record(behaviour, c)
	}

	// The fake rejects the canary key with its JSON 401.
	srv := tmdbtest.Start(t, tmdbtest.Options{})
	c, _ := newClient(Config{APIKey: hostiletest.Key, URL: srv.APIRoot()}, "tmdb", tune)
	record("fake", c)
	if got := c.TestConnection(ctx); got.Error != "tmdb: API key rejected" {
		t.Fatalf("result %+v (the sweep would prove nothing)", got)
	}

	// Unreachable, and a name that does not resolve.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := ln.Addr().String()
	_ = ln.Close()
	for _, base := range []string{
		"http://user:" + hostiletest.Pass + "@" + dead + "/3",
		"http://user:" + hostiletest.Pass + "@canary-key.invalid/3",
	} {
		c, _ := newClient(Config{APIKey: hostiletest.Key, URL: base}, "tmdb", tune)
		record("dead", c)
	}

	// A base URL that cannot be used.
	_, err := newClient(Config{APIKey: hostiletest.Key, URL: "ftp://user:" + hostiletest.Pass + "@h/" + hostiletest.Key}, "tmdb", tune)
	if err == nil {
		t.Fatal("expected an error for a bad base URL")
	}
	s.AddErr("bad base", err)
	broken := &Client{buildErr: err, now: time.Now}
	s.AddJSON("broken test", broken.TestConnection(ctx))
	var out map[string]any
	s.AddErr("broken get", broken.Get(ctx, "/x", nil, &out))

	s.Add("log", logs.String())
	if !strings.Contains(logs.String(), "upstream request failed") {
		t.Fatal("expected the failures to be logged")
	}
	if strings.Contains(logs.String(), "api_key") {
		t.Error("a query string reached the log")
	}
	s.Check()
}

// The key must reach TMDB only as the api_key query parameter, nowhere else.
func TestTheKeyOnlyTravelsAsTheAPIKeyParameter(t *testing.T) {
	srv := tmdbtest.Start(t, tmdbtest.Options{})
	c := create(t, tmdbtest.GoodKey, srv.APIRoot())
	c.TestConnection(ctx)
	for _, r := range srv.Requests() {
		if strings.Contains(r.Path, tmdbtest.GoodKey) {
			t.Errorf("key in path %q", r.Path)
		}
	}
}

func TestTheDetectorCanFail(t *testing.T) {
	if hostiletest.Leaked("x "+url.QueryEscape(hostiletest.Key)) == "" {
		t.Fatal("detector missed the URL-encoded canary")
	}
}
