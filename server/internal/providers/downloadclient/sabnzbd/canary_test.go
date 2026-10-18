package sabnzbd

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

// The canary sweep: plant an API key (with characters that change under URL,
// base64 and JSON encoding), make SABnzbd misbehave in every way it can, and
// prove the key appears in no error, returned value or log line.
const (
	canaryKey  = `CANARY-key/+&"9f8e7d6c`
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
		c, err := newClient(Config{URL: base, APIKey: canaryKey}, "sabnzbd", func(o *core.HTTPOptions) {
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
		"403 body": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("API Key Incorrect: " + r.URL.RawQuery + canaryKey)) //nolint:gosec // test server echoing on purpose
		},
		"200 error field": func(w http.ResponseWriter, r *http.Request) {
			b, _ := json.Marshal(map[string]any{"status": false, "error": "bad request " + r.URL.String() + " key " + canaryKey})
			_, _ = w.Write(b)
		},
		"500 body": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(r.URL.String() + canaryKey)) //nolint:gosec // test server echoing on purpose
		},
		"warnings quoting the request": func(w http.ResponseWriter, r *http.Request) {
			quote := "GET " + r.URL.String() + " failed for key " + canaryKey
			switch r.URL.Query().Get("mode") {
			case "queue":
				_, _ = w.Write([]byte(`{"queue":{"status":"Idle","version":"4.5.2","slots":[]}}`))
			case "get_config":
				_, _ = w.Write([]byte(`{"config":{"servers":[]}}`))
			case "warnings":
				b, _ := json.Marshal(map[string]any{"warnings": []map[string]any{
					{"type": "ERROR", "text": quote, "time": 1792297332},
					{"type": "WARNING", "text": "apikey=" + canaryKey, "time": 1792297333},
				}})
				_, _ = w.Write(b)
			default:
				_, _ = w.Write([]byte(`{"history":{"slots":[]}}`))
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
