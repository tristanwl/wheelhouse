package core

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// The canary sweep: plant secrets, make the core fail in every way it knows,
// and prove no secret appears in any error string, JSON schema or log line it
// produced. Secrets are obviously fake. Several have characters that change
// under URL, base64 and JSON encoding.
const (
	canaryKey  = `CANARY-key/+&"9f8e7d6c`
	canaryPass = "CANARY-pass-3b2a1f0e"
	canaryUser = "canaryuser"
)

func canaryForms() []string {
	forms := []string{}
	for _, s := range []string{canaryKey, canaryPass} {
		b := []byte(s)
		forms = append(forms, s,
			encodeURIComponent(s), url.QueryEscape(s), url.PathEscape(s),
			lowerPercentHex(encodeURIComponent(s)),
			base64.StdEncoding.EncodeToString(b), base64.RawStdEncoding.EncodeToString(b),
			base64.URLEncoding.EncodeToString(b), base64.RawURLEncoding.EncodeToString(b),
			jsonEscaped(s, true), jsonEscaped(s, false))
	}
	forms = append(forms, base64.StdEncoding.EncodeToString([]byte(canaryUser+":"+canaryPass)))
	return forms
}

// sweep collects every string the core produced.
type sweep struct {
	t   *testing.T
	out []string
}

func (s *sweep) add(label, text string) { s.out = append(s.out, label+": "+text) }

func (s *sweep) addErr(label string, err error) {
	if err != nil {
		s.add(label, err.Error())
	}
}

func (s *sweep) check() {
	s.t.Helper()
	if len(s.out) == 0 {
		s.t.Fatal("sweep collected nothing")
	}
	for _, line := range s.out {
		if form := leakedForm(line); form != "" {
			s.t.Errorf("secret form %q leaked in %q", form, line)
		}
	}
}

// hostile is a raw TCP upstream that echoes the canary everywhere an HTTP
// server can: the reason phrase, headers (Location, Set-Cookie) and the body.
func hostile(t *testing.T, behaviour string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
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
				echo := canaryKey + " " + canaryPass
				switch behaviour {
				case "reason":
					_, _ = fmt.Fprintf(conn, "HTTP/1.1 401 %s\r\nX-Echo: %s\r\nContent-Length: %d\r\n\r\n%s", echo, echo, len(echo), echo)
				case "redirect":
					_, _ = fmt.Fprintf(conn, "HTTP/1.1 302 Found\r\nLocation: http://other.example.com/?apikey=%s\r\nContent-Length: 0\r\n\r\n", url.QueryEscape(canaryKey))
				case "badjson":
					_, _ = fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n{%s", len(echo)+1, echo)
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
	return ln.Addr().String()
}

func TestCanarySweepHTTPClient(t *testing.T) {
	s := &sweep{t: t}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	for _, behaviour := range []string{"reason", "redirect", "badjson", "huge", "hangup", "stall"} {
		addr := hostile(t, behaviour)
		c, err := NewHTTPClient(HTTPOptions{
			BaseURL:          "http://" + canaryUser + ":" + url.QueryEscape(canaryPass) + "@" + addr + "/prefix-" + url.PathEscape(canaryKey),
			Label:            "svc-" + behaviour,
			Headers:          http.Header{"X-Api-Key": {canaryKey}},
			Secrets:          []string{canaryKey},
			Timeout:          300 * time.Millisecond,
			MaxResponseBytes: 64,
			Logger:           logger,
		})
		if err != nil {
			t.Fatal(err)
		}
		q := url.Values{"apikey": {canaryKey}, "token": {canaryKey}}
		_, err = c.Do(context.Background(), "/api?apikey="+url.QueryEscape(canaryKey), Request{Query: q})
		if err == nil && behaviour != "badjson" { // a 200 only fails when decoded as JSON
			t.Fatalf("%s: expected an error", behaviour)
		}
		s.addErr(behaviour, err)
		var out any
		s.addErr(behaviour+" json", c.JSON(context.Background(), "/api", Request{Query: q}, &out))
		_, err = c.Do(context.Background(), "/api", Request{Query: q, CacheTTL: time.Minute})
		s.addErr(behaviour+" cached", err)
	}

	// A refused connection to an address that carries the credentials, and a
	// name that does not resolve.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := ln.Addr().String()
	_ = ln.Close()
	for _, base := range []string{
		"http://" + canaryUser + ":" + canaryPass + "@" + dead,
		"http://" + canaryUser + ":" + canaryPass + "@" + canaryKey[:6] + ".invalid",
	} {
		c, _ := NewHTTPClient(HTTPOptions{BaseURL: base, Label: "dead", Secrets: []string{canaryKey}, Timeout: 3 * time.Second, Logger: logger})
		_, err := c.Do(context.Background(), "/x", Request{Query: url.Values{"apikey": {canaryKey}}})
		s.addErr("dead", err)
	}

	// TLS failure.
	tlsSrv := httptest.NewUnstartedServer(http.NotFoundHandler())
	tlsSrv.Config.ErrorLog = log.New(io.Discard, "", 0)
	tlsSrv.StartTLS()
	defer tlsSrv.Close()
	c, _ := NewHTTPClient(HTTPOptions{BaseURL: tlsSrv.URL, Label: "tls", Secrets: []string{canaryKey}, Logger: logger})
	_, err := c.Do(context.Background(), "/x", Request{Query: url.Values{"apikey": {canaryKey}}})
	s.addErr("tls", err)

	// Bad base URLs carrying the secret.
	for _, base := range []string{"ftp://u:" + canaryPass + "@h", "://" + canaryKey, "http://" + canaryUser + ":%zz" + canaryPass + "@h"} {
		_, err := NewHTTPClient(HTTPOptions{BaseURL: base, Label: "bad", Secrets: []string{canaryKey}})
		s.addErr("base", err)
	}

	s.add("log", logs.String())
	if !strings.Contains(logs.String(), "upstream request failed") {
		t.Fatal("expected the failures to be logged")
	}
	s.check()
}

func canaryRuntime(t *testing.T) (*Runtime, []Instance) {
	t.Helper()
	def := fakeProvider("sab", RoleDownloadClient).def
	vdef := Definition{
		ID: "v", Role: RoleMetadata, DisplayName: "V", Config: validatedConfig{},
		Create: func(any, ProviderContext) any { return nil },
	}
	instances := []Instance{
		inst("a", "sab", map[string]any{"url": "http://h", "apiKey": 12345}, true),
		inst("b", "sab", map[string]any{"url": canaryKey, "apiKey": canaryKey}, true),
		inst("c", "sab", map[string]any{"url": "ftp://" + canaryKey, "apiKey": []any{canaryKey}}, true),
		inst("d", "sab", map[string]any{"url": "http://u:" + canaryPass + "@", "apiKey": canaryKey, canaryKey: canaryKey}, true),
		inst("e", "v", map[string]any{"url": "http://h", "token": canaryKey, "port": 99}, true),
		inst("f", "v", map[string]any{"url": 5, "token": map[string]any{"k": canaryKey}, "port": canaryKey, "insecure": canaryKey}, true),
		inst("g", "unknown", map[string]any{"apiKey": canaryKey}, true),
		inst("h", "sab", map[string]any{"apiKey": canaryKey}, false),
	}
	return NewRuntime(mustRegistry(t, def, vdef), staticSource{instances}), instances
}

// A Validate hook that carelessly echoes a secret must still be redacted.
type leakyConfig struct {
	Token string `json:"token" config:"required,secret"`
}

func (c leakyConfig) Validate() error { return fmt.Errorf("token %s is not allowed", c.Token) }

func TestCanarySweepConfigRuntimeAndSchema(t *testing.T) {
	s := &sweep{t: t}
	rt, instances := canaryRuntime(t)
	ctx := context.Background()

	list, err := rt.ListInstances(ctx)
	if err != nil {
		t.Fatal(err)
	}
	invalid := 0
	for _, r := range list {
		s.add("list "+string(r.Status), r.Error)
		if r.Status == StatusInvalidConfig {
			invalid++
		}
	}
	if invalid < 4 {
		t.Fatalf("expected the planted configs to be invalid, got %d", invalid)
	}
	for _, in := range instances {
		_, err := rt.Client(ctx, in.ID)
		s.addErr("client", err)
		s.add("fmt", sprint("%v|%+v|%#v|%s", in, in, in, in))
		raw, _ := json.Marshal(in)
		s.add("json", string(raw))
		s.add("slog", slogText(in))
	}
	_, err = rt.Client(ctx, "missing")
	s.addErr("client missing", err)

	// The careless Validate hook: the runtime redacts known secret values.
	leaky := mustRegistry(t, Definition{
		ID: "leaky", Role: RoleMetadata, DisplayName: "Leaky", Config: leakyConfig{},
		Create: func(any, ProviderContext) any { return nil },
	})
	lrt := NewRuntime(leaky, staticSource{[]Instance{inst("l", "leaky", map[string]any{"token": canaryKey}, true)}})
	llist, _ := lrt.ListInstances(ctx)
	if llist[0].Status != StatusInvalidConfig {
		t.Fatalf("status = %s", llist[0].Status)
	}
	s.add("leaky", llist[0].Error)
	_, err = lrt.Client(ctx, "l")
	s.addErr("leaky client", err)

	// Schemas and provider descriptions are built from metadata only; marshal
	// them all, labels and secrets-keys included.
	for _, d := range rt.registry.All() {
		raw, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		s.add("definition", string(raw))
		raw, _ = json.Marshal(d.Schema())
		s.add("schema", string(raw))
	}

	// The environment source, with the canary as the value of every variable.
	env := map[string]string{}
	for _, m := range envMappings {
		for _, v := range m.vars {
			env[v] = canaryKey
		}
	}
	env["RADARR_4K_URL"], env["RADARR_4K_API_KEY"] = canaryKey, canaryKey
	envInst, _ := NewEnvConfigSource(env).Instances(ctx)
	if len(envInst) == 0 {
		t.Fatal("no env instances")
	}
	for _, in := range envInst {
		s.add("env fmt", sprint("%v|%+v|%#v", in, in, in))
		raw, _ := json.Marshal(in)
		s.add("env json", string(raw))
		s.add("env slog", slogText(in))
	}
	s.check()
}

func slogText(in Instance) string {
	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Info("instance", "instance", in)
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("instance", "instance", in)
	return buf.String()
}

// The sweep must be able to fail: it notices every encoded form of a planted secret.
func TestCanaryDetectorSeesEveryForm(t *testing.T) {
	for _, form := range canaryForms() {
		if leakedForm("upstream said "+form) == "" {
			t.Errorf("form %q not detected", form)
		}
	}
	if leakedForm("sab /api failed: 401 Unauthorized") != "" {
		t.Error("false positive")
	}
}

func leakedForm(line string) string {
	for _, form := range canaryForms() {
		if strings.Contains(line, form) {
			return form
		}
	}
	return ""
}
