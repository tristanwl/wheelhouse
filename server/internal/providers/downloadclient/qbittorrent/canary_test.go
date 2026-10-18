package qbittorrent

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	dc "github.com/tristanwl/wheelhouse/server/internal/providers/downloadclient"
)

// The canary sweep: plant a password (with characters that change under URL,
// base64 and JSON encoding), make qBittorrent misbehave in every way it can, and
// prove the password appears in no error, returned value or log line.
const (
	canaryKey  = `CANARY-pass/+&"9f8e7d6c`
	canaryUser = "canaryuser"
	canaryPass = "CANARY-urlpass-77aa"
)

func TestCanarySweep(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	var out []string
	collect := func(label string, v any) {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		out = append(out, label+": "+string(b))
	}

	// run exercises every entry point against a base URL and records everything returned.
	run := func(label, base string, tweak func(*core.HTTPOptions)) {
		c, err := newClient(Config{URL: base, Username: "admin", Password: canaryKey}, "qbittorrent", func(o *core.HTTPOptions) {
			o.Logger = logger
			o.Timeout = 300 * time.Millisecond
			o.MaxResponseBytes = 1 << 20
			if tweak != nil {
				tweak(o)
			}
		})
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		ctx := context.Background()
		collect(label+" TestConnection", c.TestConnection(ctx))
		st, err := c.Status(ctx)
		collect(label+" Status", st)
		if err != nil {
			out = append(out, label+" Status error: "+err.Error())
		}
		items, err := c.Items(ctx)
		collect(label+" Items", items)
		if err != nil {
			out = append(out, label+" Items error: "+err.Error())
		}
	}

	echo := canaryKey + " " + url.QueryEscape(canaryKey)
	for _, behaviour := range []string{"reason", "redirect", "badjson", "huge", "hangup", "stall"} {
		addr := dc.Hostile(t, behaviour, echo, 2*time.Second)
		run("hostile "+behaviour, "http://"+addr, nil)
		// Credentials in the URL are secrets too.
		run("hostile "+behaviour+" with URL credentials", "http://"+canaryUser+":"+canaryPass+"@"+addr, nil)
	}

	// A service that echoes the key in every way an HTTP service can, with a
	// 403 (wrong key), with 200 and an error field, and with a 200 whose
	// content quotes the request.
	servers := map[string]http.HandlerFunc{
		"401 body": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_ = r.ParseForm()
			_, _ = w.Write([]byte("Unauthorized " + r.PostForm.Encode() + canaryKey)) //nolint:gosec // test server echoing on purpose
		},
		"403 ban text": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_ = r.ParseForm()
			_, _ = w.Write([]byte("banned " + r.PostForm.Get("password"))) //nolint:gosec // test server echoing on purpose
		},
		"200 Fails with echo": func(w http.ResponseWriter, r *http.Request) {
			_ = r.ParseForm()
			_, _ = w.Write([]byte("Fails. " + r.PostForm.Get("password"))) //nolint:gosec // test server echoing on purpose
		},
		"cookie echoing the password": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v2/auth/login" {
				_ = r.ParseForm()
				w.Header().Set("Set-Cookie", "SID="+url.QueryEscape(r.PostForm.Get("password"))+"; path=/")
				_, _ = w.Write([]byte("Ok."))
				return
			}
			// Quote the cookie back in the error, as a sloppy proxy might.
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(r.Header.Get("Cookie"))) //nolint:gosec // test server echoing on purpose
		},
		"500 body": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(r.URL.String() + canaryKey)) //nolint:gosec // test server echoing on purpose
		},
		"torrents quoting the password": func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v2/auth/login":
				w.Header().Set("Set-Cookie", "SID=abc; path=/")
				_, _ = w.Write([]byte("Ok."))
			case "/api/v2/app/version":
				_, _ = w.Write([]byte("v5.2.4"))
			case "/api/v2/transfer/info":
				_, _ = w.Write([]byte(`{"dl_info_speed":0,"up_info_speed":0}`))
			default:
				_, _ = w.Write([]byte(`[{"hash":"h1","name":"Example","state":"error","progress":0.5,"size":10,"amount_left":5}]`))
			}
		},
	}
	for name, h := range servers {
		ts := httptest.NewServer(h)
		t.Cleanup(ts.Close)
		run(name, ts.URL, nil)
	}

	if len(out) == 0 {
		t.Fatal("sweep collected nothing")
	}
	out = append(out, "logs: "+logs.String())
	if logs.Len() == 0 {
		t.Error("expected the failed requests to be logged at debug level")
	}
	for _, line := range out {
		if leaked := dc.Leaked(line, canaryKey, canaryPass, canaryUser, canaryUser+":"+canaryPass); leaked != "" {
			t.Errorf("secret form %q leaked in %.300q", leaked, line)
		}
	}
}
