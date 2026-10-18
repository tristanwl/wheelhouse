package sabnzbd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	dc "github.com/tristanwl/wheelhouse/server/internal/providers/downloadclient"
	"github.com/tristanwl/wheelhouse/server/internal/providers/downloadclient/sabnzbd/fakesabnzbd"
)

var bg = context.Background()

func clientFor(t *testing.T, scenario fakesabnzbd.Scenario, apiKey string) (dc.Client, *fakesabnzbd.Server) {
	t.Helper()
	srv := fakesabnzbd.Start(t, scenario)
	c, err := New(Config{URL: srv.URL, APIKey: apiKey}, "sabnzbd")
	if err != nil {
		t.Fatal(err)
	}
	return c, srv
}

func good(t *testing.T, scenario fakesabnzbd.Scenario) dc.Client {
	c, _ := clientFor(t, scenario, fakesabnzbd.GoodKey)
	return c
}

func TestContract(t *testing.T) {
	dc.RunContract(t, "sabnzbd", func(t *testing.T) (dc.Client, dc.Protocol) {
		return good(t, fakesabnzbd.Active), dc.Usenet
	})
}

func TestParseDuration(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int64
	}{
		{"0:07:12", 432},
		{"1:02:03", 3723},
		{"1:02:03:04", 86400 + 2*3600 + 3*60 + 4},
	} {
		if got, ok := ParseDuration(tc.in); !ok || got != tc.want {
			t.Errorf("ParseDuration(%q) = %d, %v; want %d", tc.in, got, ok, tc.want)
		}
	}
	// Zero means unknown, as does anything malformed.
	for _, in := range []string{"0:00:00", "", "soon", "1:2", "1:2:3:4:5", "-1:00:00", "1:xx:00", "1::3", "0x1:00:00", "99999999999:00:00"} {
		if got, ok := ParseDuration(in); ok {
			t.Errorf("ParseDuration(%q) = %d, want unknown", in, got)
		}
	}
}

func TestItems(t *testing.T) {
	c := good(t, fakesabnzbd.Active)
	items, err := c.Items(bg)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("got %d items", len(items))
	}

	t.Run("converts megabytes to bytes and percentages to 0..1", func(t *testing.T) {
		first := items[0]
		if first.Name != "Example.Movie.2024.1080p.WEB-DL.x264-GROUP" ||
			first.SizeBytes != 4096*1024*1024 || first.RemainingBytes != 3072*1024*1024 ||
			first.Progress != 0.25 || first.State != dc.StateDownloading ||
			first.ETASeconds == nil || *first.ETASeconds != 60 || first.Protocol != dc.Usenet {
			t.Errorf("%+v", first)
		}
	})
	t.Run("maps queued jobs and unknown ETAs", func(t *testing.T) {
		if items[1].State != dc.StateQueued || items[1].ETASeconds != nil {
			t.Errorf("%+v", items[1])
		}
	})
	t.Run("history reveals post-processing that the queue calls Downloading", func(t *testing.T) {
		it := items[2]
		if it.State != dc.StatePostprocessing || it.StateDetail != "Extracting" || it.Progress != 1 {
			t.Errorf("%+v", it)
		}
		if it.ETASeconds == nil || *it.ETASeconds != 86400+2*3600+3*60+4 {
			t.Errorf("eta %v", it.ETASeconds)
		}
	})
	t.Run("an idle queue is an empty, non-nil list", func(t *testing.T) {
		got, err := good(t, fakesabnzbd.Idle).Items(bg)
		if err != nil || got == nil || len(got) != 0 {
			t.Errorf("%v %v", got, err)
		}
		b, _ := json.Marshal(got)
		if string(b) != "[]" {
			t.Errorf("marshals as %s", b)
		}
	})
	t.Run("a paused item is paused", func(t *testing.T) {
		got, err := good(t, fakesabnzbd.Paused).Items(bg)
		if err != nil || got[0].State != dc.StatePaused {
			t.Errorf("%+v %v", got, err)
		}
	})
}

func TestSlotState(t *testing.T) {
	for _, tc := range []struct {
		status, phase string
		state         dc.State
		detail        string
	}{
		{"Downloading", "", dc.StateDownloading, ""},
		{"Fetching", "", dc.StateDownloading, ""},
		{"Paused", "", dc.StatePaused, ""},
		{"Queued", "", dc.StateQueued, ""},
		{"Grabbing", "", dc.StateQueued, "Grabbing"},
		{"Propagating", "", dc.StateQueued, "Propagating"},
		{"SomethingNew", "", dc.StateDownloading, "SomethingNew"},
		{"Downloading", "Verifying", dc.StatePostprocessing, "Verifying"},
		{"Paused", "Repairing", dc.StatePostprocessing, "Repairing"},
	} {
		state, detail := slotState(tc.status, tc.phase)
		if state != tc.state || detail != tc.detail {
			t.Errorf("slotState(%q, %q) = %s, %q; want %s, %q", tc.status, tc.phase, state, detail, tc.state, tc.detail)
		}
	}
}

func TestStatus(t *testing.T) {
	t.Run("normalizes speed, queue, ETA, version and servers", func(t *testing.T) {
		s, err := good(t, fakesabnzbd.Active).Status(bg)
		if err != nil {
			t.Fatal(err)
		}
		if s.Paused || s.StatusText != "Downloading" || s.SpeedBytesPerSec != 51200*1024 ||
			s.QueueCount != 3 || s.ETASeconds == nil || *s.ETASeconds != 432 || s.Version != "4.5.2" {
			t.Errorf("%+v", s)
		}
		want := []dc.UsenetServer{
			{Name: "news.example.com", Connections: 20, SSL: true, Enabled: true},
			{Name: "backup.example.net", Connections: 8, SSL: false, Enabled: false},
		}
		if s.Usenet == nil || !reflect.DeepEqual(s.Usenet.Servers, want) || s.Torrent != nil {
			t.Errorf("%+v", s.Usenet)
		}
	})
	t.Run("an idle queue", func(t *testing.T) {
		s, err := good(t, fakesabnzbd.Idle).Status(bg)
		if err != nil {
			t.Fatal(err)
		}
		if s.Paused || s.StatusText != "Idle" || s.SpeedBytesPerSec != 0 || s.QueueCount != 0 || s.ETASeconds != nil {
			t.Errorf("%+v", s)
		}
	})
	t.Run("a paused queue", func(t *testing.T) {
		s, err := good(t, fakesabnzbd.Paused).Status(bg)
		if err != nil || !s.Paused || s.StatusText != "Paused" {
			t.Errorf("%+v %v", s, err)
		}
	})
	t.Run("keeps the last three warnings with time and severity, minus refused connections", func(t *testing.T) {
		s, err := good(t, fakesabnzbd.Active).Status(bg)
		if err != nil {
			t.Fatal(err)
		}
		// Fixture: ERROR, "Refused connection" (dropped), WARNING, INFO -> last three kept.
		var sev []string
		for _, w := range s.Warnings {
			sev = append(sev, w.Severity)
			if strings.HasPrefix(w.Message, "Refused connection") {
				t.Errorf("kept a refused-connection warning")
			}
		}
		if !reflect.DeepEqual(sev, []string{"error", "warning", "warning"}) {
			t.Errorf("severities %v", sev)
		}
		if !strings.Contains(s.Warnings[0].Message, "__ADMIN__") {
			t.Errorf("message %q", s.Warnings[0].Message)
		}
		if want := time.Unix(1792297332, 0).UTC().Format("2006-01-02T15:04:05.000Z"); s.Warnings[0].At != want {
			t.Errorf("at %q, want %q", s.Warnings[0].At, want)
		}
	})
}

func TestConnectionAndErrors(t *testing.T) {
	t.Run("TestConnection returns the version", func(t *testing.T) {
		got := good(t, fakesabnzbd.Idle).TestConnection(bg)
		if got != (core.ConnectionResult{OK: true, Version: "4.5.2"}) {
			t.Errorf("%+v", got)
		}
	})

	t.Run("a wrong API key is a clear message that does not contain the key", func(t *testing.T) {
		const wrong = "wrong-key-value-123"
		c, _ := clientFor(t, fakesabnzbd.Idle, wrong)
		const msg = "sabnzbd: API key incorrect"
		if got := c.TestConnection(bg); got != (core.ConnectionResult{Error: msg}) {
			t.Errorf("%+v", got)
		}
		if _, err := c.Status(bg); err == nil || err.Error() != msg {
			t.Errorf("Status: %v", err)
		}
		if _, err := c.Items(bg); err == nil || err.Error() != msg {
			t.Errorf("Items: %v", err)
		}
		var pe *core.ProviderError
		_, err := c.Status(bg)
		if !asProvider(err, &pe) || pe.Status != 403 {
			t.Errorf("want a ProviderError with status 403, got %#v", err)
		}
	})

	t.Run("an unreachable server is reported, not thrown, by TestConnection", func(t *testing.T) {
		c, srv := clientFor(t, fakesabnzbd.Idle, fakesabnzbd.GoodKey)
		srv.Close()
		got := c.TestConnection(bg)
		if got.OK || got.Error != "sabnzbd: connection refused" {
			t.Errorf("%+v", got)
		}
		if _, err := c.Items(bg); err == nil || err.Error() != "sabnzbd: connection refused" {
			t.Errorf("Items: %v", err)
		}
	})

	t.Run("sends the key as a query parameter (so errors must never echo URLs)", func(t *testing.T) {
		c, srv := clientFor(t, fakesabnzbd.Idle, fakesabnzbd.GoodKey)
		c.TestConnection(bg)
		reqs := srv.Requests()
		if len(reqs) == 0 || reqs[0].Query.Get("apikey") != fakesabnzbd.GoodKey ||
			reqs[0].Query.Get("mode") != "queue" || reqs[0].Query.Get("limit") != "1" ||
			reqs[0].Query.Get("output") != "json" {
			t.Errorf("%+v", reqs)
		}
	})
}

// upstream starts a server running h and a client with tightened limits.
func upstream(t *testing.T, h http.HandlerFunc, tweak func(*core.HTTPOptions)) *client {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	c, err := newClient(Config{URL: ts.URL, APIKey: "upstream-key-123456"}, "sabnzbd", tweak)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestUpstreamFailures(t *testing.T) {
	t.Run("an error field on a 200 is reported, with the key redacted", func(t *testing.T) {
		c := upstream(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"status":false,"error":"bad call apikey=` + r.URL.Query().Get("apikey") + ` upstream-key-123456"}`)) //nolint:gosec // test server echoing on purpose
		}, nil)
		res := c.TestConnection(bg)
		if res.OK || !strings.HasPrefix(res.Error, "sabnzbd: bad call") || strings.Contains(res.Error, "upstream-key-123456") {
			t.Errorf("%+v", res)
		}
	})

	t.Run("malformed JSON is an error that does not quote the body", func(t *testing.T) {
		c := upstream(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"queue": SECRET-BODY`)) }, nil)
		res := c.TestConnection(bg)
		if res.Error != "sabnzbd /api returned invalid JSON" {
			t.Errorf("%+v", res)
		}
	})

	t.Run("a response of the wrong shape is an error", func(t *testing.T) {
		for _, body := range []string{`{}`, `[]`, `{"queue":null}`, `{"queue":"x"}`} {
			c := upstream(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }, nil)
			if res := c.TestConnection(bg); res.OK || res.Error == "" {
				t.Errorf("%s: %+v", body, res)
			}
			if _, err := c.Items(bg); err == nil {
				t.Errorf("%s: Items succeeded", body)
			}
			if _, err := c.Status(bg); err == nil {
				t.Errorf("%s: Status succeeded", body)
			}
		}
	})

	t.Run("an oversized response is refused", func(t *testing.T) {
		c := upstream(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"queue":{"slots":[]},"pad":"` + strings.Repeat("x", 4096) + `"}`))
		}, func(o *core.HTTPOptions) { o.MaxResponseBytes = 1024 })
		res := c.TestConnection(bg)
		if res.OK || !strings.Contains(res.Error, "response larger than 1024 bytes") {
			t.Errorf("%+v", res)
		}
	})

	t.Run("a slow service times out", func(t *testing.T) {
		release := make(chan struct{})
		c := upstream(t, func(http.ResponseWriter, *http.Request) { <-release },
			func(o *core.HTTPOptions) { o.Timeout = 100 * time.Millisecond })
		t.Cleanup(func() { close(release) }) // runs before the server's Close
		res := c.TestConnection(bg)
		if res.OK || res.Error != "sabnzbd: timed out after 100ms" {
			t.Errorf("%+v", res)
		}
	})

	t.Run("a server error is reported with the status only", func(t *testing.T) {
		c := upstream(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("stack trace with upstream-key-123456"))
		}, nil)
		res := c.TestConnection(bg)
		if res.Error != "sabnzbd /api failed: 500 Internal Server Error" {
			t.Errorf("%+v", res)
		}
	})

	t.Run("a canceled context is reported as canceled", func(t *testing.T) {
		c := good(t, fakesabnzbd.Idle)
		ctx, cancel := context.WithCancel(bg)
		cancel()
		if res := c.TestConnection(ctx); res.OK || !strings.Contains(res.Error, "canceled") {
			t.Errorf("%+v", res)
		}
	})

	t.Run("odd values degrade instead of failing", func(t *testing.T) {
		c := upstream(t, func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Query().Get("mode") {
			case "queue":
				_, _ = w.Write([]byte(`{"queue":{"status":"Downloading","kbpersec":"abc","noofslots_total":"4","timeleft":"1:00","slots":[
				  {"nzo_id":"a","filename":"A","status":"Downloading","percentage":"150","mb":"-5","mbleft":"9"},
				  {"nzo_id":"b","filename":"B","status":"Queued","percentage":"-3","mb":"10","mbleft":"20"}]}}`))
			default:
				_, _ = w.Write([]byte(`{"history":{"slots":[]}}`))
			}
		}, nil)
		items, err := c.Items(bg)
		if err != nil {
			t.Fatal(err)
		}
		if items[0].Progress != 1 || items[0].SizeBytes != 0 || items[0].RemainingBytes != 0 {
			t.Errorf("%+v", items[0])
		}
		if items[1].Progress != 0 || items[1].RemainingBytes != items[1].SizeBytes {
			t.Errorf("%+v", items[1])
		}
	})
}

func asProvider(err error, target **core.ProviderError) bool { return errors.As(err, target) }
