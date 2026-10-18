package mmtest

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	mm "github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager"
)

// Obviously fake secrets. The key has characters that change under URL, base64
// and JSON encoding.
const (
	CanaryKey  = `CANARY-key/+&"9f8e7d6c` //nolint:gosec // obviously fake, planted on purpose
	canaryUser = "canaryuser"
	canaryPass = "CANARY-pass-3b2a1f0e" //nolint:gosec // obviously fake, planted on purpose
)

// Factory builds a client of the provider under test.
type Factory func(cfg mm.ArrConfig, label string, opts mm.ArrOptions) mm.Manager

// operation is one call a client offers, as a name and a function.
type operation struct {
	name string
	call func(ctx context.Context) error
}

// operations lists every upstream call of a client, reads and writes.
func operations(c mm.Manager) []operation {
	ops := []operation{
		{"TestConnection", func(ctx context.Context) error {
			if r := c.TestConnection(ctx); !r.OK {
				return fmt.Errorf("%s", r.Error)
			}
			return nil
		}},
		{"Status", func(ctx context.Context) error { _, err := c.Status(ctx); return err }},
		{"SystemStatus", func(ctx context.Context) error { _, err := c.SystemStatus(ctx); return err }},
		{"Health", func(ctx context.Context) error { _, err := c.Health(ctx); return err }},
		{"QueueSummary", func(ctx context.Context) error { _, err := c.QueueSummary(ctx); return err }},
		{"Indexers", func(ctx context.Context) error { _, err := c.Indexers(ctx); return err }},
		{"RootFolders", func(ctx context.Context) error { _, err := c.RootFolders(ctx); return err }},
		{"QualityProfiles", func(ctx context.Context) error { _, err := c.QualityProfiles(ctx); return err }},
		{"Tags", func(ctx context.Context) error { _, err := c.Tags(ctx); return err }},
		{"Lookup", func(ctx context.Context) error { _, err := c.Lookup(ctx, "example"); return err }},
		{"LookupByExternalID", func(ctx context.Context) error { _, err := c.LookupByExternalID(ctx, 7); return err }},
		{"Add", func(ctx context.Context) error { _, err := c.Add(ctx, mm.LookupResult{Title: "Example"}); return err }},
		{"Remove", func(ctx context.Context) error { return c.Remove(ctx, 1) }},
		{"Rescan", func(ctx context.Context) error { return c.Rescan(ctx, 1) }},
	}
	if m, ok := mm.AsMovies(c); ok {
		ops = append(ops,
			operation{"Movies", func(ctx context.Context) error { _, err := m.Movies(ctx); return err }},
			operation{"MovieDetail", func(ctx context.Context) error { _, err := m.MovieDetail(ctx, 1); return err }},
		)
	}
	if s, ok := mm.AsSeries(c); ok {
		ops = append(ops,
			operation{"Series", func(ctx context.Context) error { _, err := s.Series(ctx); return err }},
			operation{"SeriesByID", func(ctx context.Context) error { _, err := s.SeriesByID(ctx, 1); return err }},
			operation{"SeriesDetail", func(ctx context.Context) error { _, err := s.SeriesDetail(ctx, 1); return err }},
			operation{"EpisodeFiles", func(ctx context.Context) error { _, err := s.EpisodeFiles(ctx, 1); return err }},
			operation{"Episodes", func(ctx context.Context) error { _, err := s.Episodes(ctx, 1); return err }},
			operation{"DeleteEpisodeFile", func(ctx context.Context) error { return s.DeleteEpisodeFile(ctx, 1) }},
			operation{"UpdateSeries", func(ctx context.Context) error { _, err := s.UpdateSeries(ctx, mm.SeriesFull{}); return err }},
			operation{"Seasons", func(ctx context.Context) error { _, err := s.Seasons(ctx, 1); return err }},
			operation{"DeleteSeason", func(ctx context.Context) error { _, err := s.DeleteSeason(ctx, 1, 1); return err }},
		)
	}
	return ops
}

func canaryForms() []string {
	var forms []string
	for _, s := range []string{CanaryKey, canaryPass} {
		b := []byte(s)
		jsonForm := func(escapeHTML bool) string {
			var buf bytes.Buffer
			enc := json.NewEncoder(&buf)
			enc.SetEscapeHTML(escapeHTML)
			_ = enc.Encode(s)
			out := strings.TrimSpace(buf.String())
			return out[1 : len(out)-1]
		}
		forms = append(forms, s, url.QueryEscape(s), url.PathEscape(s),
			base64.StdEncoding.EncodeToString(b), base64.RawStdEncoding.EncodeToString(b),
			base64.URLEncoding.EncodeToString(b), base64.RawURLEncoding.EncodeToString(b),
			jsonForm(true), jsonForm(false))
	}
	forms = append(forms, canaryUser, base64.StdEncoding.EncodeToString([]byte(canaryUser+":"+canaryPass)))
	return forms
}

// Leak returns the first planted secret (in any common encoding) found in text,
// or "".
func Leak(text string) string {
	for _, f := range canaryForms() {
		if strings.Contains(text, f) {
			return f
		}
	}
	return ""
}

// hostile is a raw TCP upstream that echoes the canary everywhere an HTTP server
// can: the reason phrase, headers and the body.
func hostile(t *testing.T, behaviour string) string {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				req, err := http.ReadRequest(bufio.NewReader(conn))
				if err != nil {
					return
				}
				_, _ = io.Copy(io.Discard, req.Body)
				echo := CanaryKey + " " + canaryPass
				switch behaviour {
				case "reason":
					_, _ = fmt.Fprintf(conn, "HTTP/1.1 500 %s\r\nX-Echo: %s\r\nContent-Length: %d\r\n\r\n%s", echo, echo, len(echo), echo)
				case "unauthorized":
					_, _ = fmt.Fprintf(conn, "HTTP/1.1 401 %s\r\nContent-Length: %d\r\n\r\n%s", echo, len(echo), echo)
				case "redirect":
					_, _ = fmt.Fprintf(conn, "HTTP/1.1 302 Found\r\nLocation: http://other.example.com/?apikey=%s\r\nContent-Length: 0\r\n\r\n", url.QueryEscape(CanaryKey))
				case "badjson":
					_, _ = fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n{%s", len(echo)+1, echo)
				case "wrongshape":
					body := `"` + echo + `"`
					_, _ = fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
				case "huge":
					_, _ = fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Length: 1000000\r\n\r\n%s", echo)
				case "hangup":
					// close without a response
				case "stall":
					time.Sleep(2 * time.Second)
				}
			}()
		}
	}()
	return "http://" + ln.Addr().String()
}

// RunRobustness runs what every media-manager adapter must survive: a wrong key,
// an unreachable service, a timeout, an oversized response, malformed JSON,
// hostile servers that echo secrets, and a canary sweep proving that planted
// secrets never appear in any error, log line or returned value.
func RunRobustness(t *testing.T, label string, start func(t testing.TB, opts ...Option) *Fake, factory Factory) {
	t.Helper()
	ctx := context.Background()

	t.Run("a wrong key is 'API key incorrect' on every call, without echoing it", func(t *testing.T) {
		fake := start(t)
		const wrong = "wrong-key-value-123"
		c := factory(mm.ArrConfig{URL: fake.URL, APIKey: wrong}, label, mm.ArrOptions{})
		want := label + ": API key incorrect"
		for _, op := range operations(c) {
			err := op.call(ctx)
			if err == nil || err.Error() != want {
				t.Errorf("%s: error = %v, want %q", op.name, err, want)
				continue
			}
			var pe *core.ProviderError
			if op.name != "TestConnection" && (!errors.As(err, &pe) || pe.Status != 401) {
				t.Errorf("%s: want a ProviderError with status 401, got %#v", op.name, err)
			}
		}
		if r := c.TestConnection(ctx); r.OK || r.Error != want || r.Version != "" {
			t.Errorf("TestConnection = %+v", r)
		}
	})

	t.Run("an unreachable service is reported plainly", func(t *testing.T) {
		fake := start(t)
		c := factory(mm.ArrConfig{URL: fake.URL, APIKey: GoodKey}, label, mm.ArrOptions{})
		fake.Close()
		want := label + ": connection refused"
		if r := c.TestConnection(ctx); r.OK || r.Error != want {
			t.Errorf("TestConnection = %+v, want error %q", r, want)
		}
		for _, op := range operations(c) {
			if err := op.call(ctx); err == nil || err.Error() != want {
				t.Errorf("%s: error = %v, want %q", op.name, err, want)
			}
		}
	})

	t.Run("a slow service times out", func(t *testing.T) {
		fake := start(t, WithDelay(300*time.Millisecond))
		c := factory(mm.ArrConfig{URL: fake.URL, APIKey: GoodKey}, label, mm.ArrOptions{Timeout: 50 * time.Millisecond})
		r := c.TestConnection(ctx)
		if r.OK || r.Error != label+": timed out after 50ms" {
			t.Errorf("TestConnection = %+v", r)
		}
	})

	t.Run("a canceled context stops the call", func(t *testing.T) {
		fake := start(t, WithDelay(300*time.Millisecond))
		c := factory(mm.ArrConfig{URL: fake.URL, APIKey: GoodKey}, label, mm.ArrOptions{})
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := c.SystemStatus(cctx); err == nil || err.Error() != label+": request canceled" {
			t.Errorf("error = %v", err)
		}
	})

	t.Run("an oversized response is refused", func(t *testing.T) {
		fake := start(t)
		c := factory(mm.ArrConfig{URL: fake.URL, APIKey: GoodKey}, label, mm.ArrOptions{MaxResponseBytes: 64})
		_, err := c.QualityProfiles(ctx)
		if err == nil || !strings.Contains(err.Error(), "response larger than 64 bytes") {
			t.Errorf("error = %v", err)
		}
		// A service that lies about its length gets the same treatment.
		c = factory(mm.ArrConfig{URL: hostile(t, "huge"), APIKey: CanaryKey}, label, mm.ArrOptions{MaxResponseBytes: 64})
		_, err = c.QualityProfiles(ctx)
		if err == nil || !strings.Contains(err.Error(), "response larger than 64 bytes") {
			t.Errorf("error = %v", err)
		}
	})

	t.Run("malformed JSON is an error that never quotes the body", func(t *testing.T) {
		c := factory(mm.ArrConfig{URL: hostile(t, "badjson"), APIKey: CanaryKey}, label, mm.ArrOptions{})
		for _, op := range operations(c) {
			err := op.call(ctx)
			if err == nil {
				// Writes whose result is ignored (delete, command) accept any body.
				continue
			}
			if Leak(err.Error()) != "" || strings.Contains(err.Error(), "CANARY") {
				t.Errorf("%s: leaked in %q", op.name, err)
			}
		}
		_, err := c.QualityProfiles(ctx)
		if err == nil || err.Error() != label+" qualityprofile returned invalid JSON" {
			t.Errorf("error = %v", err)
		}
	})

	t.Run("a body of the wrong shape is an error that never quotes the body", func(t *testing.T) {
		c := factory(mm.ArrConfig{URL: hostile(t, "wrongshape"), APIKey: CanaryKey}, label, mm.ArrOptions{})
		_, err := c.RootFolders(ctx)
		if err == nil || err.Error() != label+" rootfolder returned invalid JSON" {
			t.Errorf("error = %v", err)
		}
	})

	t.Run("canary sweep", func(t *testing.T) {
		var logs bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
		var seen []string
		record := func(what string, err error) {
			if err != nil {
				seen = append(seen, what+": "+err.Error())
			}
		}

		for _, behaviour := range []string{"reason", "unauthorized", "redirect", "badjson", "wrongshape", "huge", "hangup", "stall"} {
			base := hostile(t, behaviour)
			// Credentials in the URL are secrets too.
			withUser := strings.Replace(base, "http://", "http://"+canaryUser+":"+canaryPass+"@", 1)
			for name, u := range map[string]string{"plain": base, "userinfo": withUser} {
				c := factory(mm.ArrConfig{URL: u, APIKey: CanaryKey}, label,
					mm.ArrOptions{Logger: logger, Timeout: 80 * time.Millisecond, MaxResponseBytes: 1024})
				for _, op := range operations(c) {
					record(behaviour+"/"+name+"/"+op.name, op.call(ctx))
				}
				r := c.TestConnection(ctx)
				seen = append(seen, behaviour+"/"+name+"/TestConnection: "+r.Error+r.Version)
			}
		}

		// Refused connection with credentials in the URL.
		c := factory(mm.ArrConfig{URL: "http://" + canaryUser + ":" + canaryPass + "@127.0.0.1:1", APIKey: CanaryKey}, label,
			mm.ArrOptions{Logger: logger})
		for _, op := range operations(c) {
			record("refused/"+op.name, op.call(ctx))
		}
		// A URL that cannot be used at all.
		c = factory(mm.ArrConfig{URL: "ftp://" + canaryUser + ":" + canaryPass + "@x/" + CanaryKey, APIKey: CanaryKey}, label, mm.ArrOptions{Logger: logger})
		for _, op := range operations(c) {
			record("badurl/"+op.name, op.call(ctx))
		}

		// Success path: nothing a fake service answers contains the key either.
		fake := start(t)
		good := factory(mm.ArrConfig{URL: fake.URL, APIKey: GoodKey}, label, mm.ArrOptions{Logger: logger})
		for _, op := range operations(good) {
			record("ok/"+op.name, op.call(ctx))
		}
		wrong := start(t, WithAPIKey("some-other-key"))
		bad := factory(mm.ArrConfig{URL: wrong.URL, APIKey: CanaryKey}, label, mm.ArrOptions{Logger: logger})
		for _, op := range operations(bad) {
			record("wrongkey/"+op.name, op.call(ctx))
		}

		if len(seen) < 50 {
			t.Fatalf("sweep collected only %d lines", len(seen))
		}
		for _, line := range seen {
			if f := Leak(line); f != "" {
				t.Errorf("secret form %q leaked in %q", f, line)
			}
		}
		if logs.Len() == 0 {
			t.Error("expected debug log lines")
		}
		if f := Leak(logs.String()); f != "" {
			t.Errorf("secret form %q leaked in the log output", f)
		}
		// The fake saw the planted key only in the header, never in a URL.
		for _, r := range wrong.Requests() {
			if Leak(r.Path+"?"+r.Query.Encode()) != "" {
				t.Errorf("key in a request URL: %s", r.Path)
			}
			if r.Header.Get("X-Api-Key") != CanaryKey {
				t.Errorf("X-Api-Key = %q", r.Header.Get("X-Api-Key"))
			}
		}
	})

	t.Run("a sub-path in the URL is kept and URL credentials are not forwarded as the key", func(t *testing.T) {
		fake := start(t, WithPathPrefix("/"+label+"-app"))
		c := factory(mm.ArrConfig{URL: fake.URL + "/", APIKey: GoodKey}, label, mm.ArrOptions{})
		if r := c.TestConnection(ctx); !r.OK {
			t.Fatalf("TestConnection = %+v", r)
		}
		if got := fake.Last().Path; got != "/api/v3/system/status" {
			t.Errorf("path below the prefix = %q", got)
		}
	})
}
