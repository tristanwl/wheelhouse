package plex

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
	"github.com/tristanwl/wheelhouse/server/internal/providers/mediaserver/plex/plextest"
)

// The canary sweep: plant a secret as the token (and as URL credentials), make
// Plex fail in every way it can, and prove the secret appears in no status,
// connection result, error or log line the package produced.
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
	record := func(label string, c *client) {
		for i := range 2 { // a second pass proves nothing is remembered or cached
			st, err := c.Status(ctx)
			s.AddErr(fmt.Sprintf("%s status %d", label, i), err)
			s.AddJSON(fmt.Sprintf("%s status %d", label, i), st)
			s.Add(fmt.Sprintf("%s fmt %d", label, i), fmt.Sprintf("%v|%+v", st, st))
			res := c.TestConnection(ctx)
			s.AddJSON(fmt.Sprintf("%s test %d", label, i), res)
		}
	}

	// Upstreams that echo the canary in every place a server can.
	for _, behaviour := range hostiletest.Behaviours {
		addr := hostiletest.Hostile(t, behaviour)
		base := "http://user:" + url.QueryEscape(hostiletest.Pass) + "@" + addr + "/plex-" + url.PathEscape(hostiletest.Key)
		c, err := newClient(Config{URL: base, Token: hostiletest.Key}, "plex-"+behaviour, tuneSmall)
		if err != nil {
			t.Fatal(err)
		}
		record(behaviour, c)
	}

	// The real fake with a canary token: the 401 HTML page path, and a 500.
	for _, tc := range []struct {
		sc   plextest.Scenario
		want string // the path the scenario must exercise
	}{
		{plextest.Scenario{Claimed: true, Active: true}, "token was rejected"},
		{plextest.Scenario{Claimed: true, AuthedFailure: 500}, "Couldn't read Plex details"},
		{plextest.Scenario{Claimed: false}, "Not claimed"},
	} {
		srv := plextest.Start(t, tc.sc)
		c, _ := newClient(Config{URL: srv.URL, Token: hostiletest.Key}, "plex", tune)
		record("fake", c)
		if st, _ := c.Status(ctx); !strings.Contains(st.Notice, tc.want) {
			t.Fatalf("scenario %+v: notice %q, want %q (the sweep would prove nothing)", tc.sc, st.Notice, tc.want)
		}
		// The token reaches the server only as the X-Plex-Token header.
		for _, r := range srv.Requests() {
			if r.Path == "/identity" && r.Token != "" {
				t.Error("token sent to /identity")
			}
		}
	}

	// Unreachable, and a name that does not resolve, both carrying the canary.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := ln.Addr().String()
	_ = ln.Close()
	for _, base := range []string{
		"http://user:" + hostiletest.Pass + "@" + dead,
		"http://user:" + hostiletest.Pass + "@canary-key.invalid",
	} {
		c, _ := newClient(Config{URL: base, Token: hostiletest.Key}, "plex", tune)
		record("dead", c)
	}

	// A base URL that cannot be used: the error must not echo it.
	_, err := newClient(Config{URL: "ftp://user:" + hostiletest.Pass + "@h/" + hostiletest.Key, Token: hostiletest.Key}, "plex", tune)
	s.AddErr("bad base", err)
	if err == nil {
		t.Fatal("expected an error for a bad base URL")
	}
	b := &brokenClient{err: err}
	st, err := b.Status(ctx)
	s.AddErr("broken", err)
	s.AddJSON("broken", st)
	s.AddJSON("broken test", b.TestConnection(ctx))

	s.Add("log", logs.String())
	if !strings.Contains(logs.String(), "upstream request failed") {
		t.Fatal("expected the failures to be logged")
	}
	s.Check()
}

// Config (with the token) must not leak through formatting of the instance.
func TestConfigStaysOutOfFormattedInstances(t *testing.T) {
	inst := core.Instance{ID: "plex", ProviderID: "plex", Name: "Plex", Enabled: true,
		Config: map[string]any{"url": "http://h", "token": hostiletest.Key}}
	s := hostiletest.NewSweep(t)
	s.Add("fmt", fmt.Sprintf("%v|%+v|%#v", inst, inst, inst))
	s.AddJSON("json", inst)
	s.Check()
}

func TestTheDetectorCanFail(t *testing.T) {
	if hostiletest.Leaked("x "+hostiletest.Key) == "" {
		t.Fatal("detector missed the plain canary")
	}
}
