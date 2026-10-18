// Package hostiletest is shared test support for the provider packages: the
// planted canary secrets, a detector that knows every encoded form of them, and
// a raw TCP upstream that echoes the canary everywhere an HTTP server can (the
// reason phrase, headers and body). It is only imported from tests.
package hostiletest

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Obviously fake secrets. Several have characters that change under URL,
// base64 and JSON encoding.
const (
	Key  = `CANARY-key/+&"9f8e7d6c`
	Pass = "CANARY-pass-3b2a1f0e" //nolint:gosec // planted fake secret
)

// Behaviours of Hostile.
const (
	Reason   = "reason"   // 401 whose reason phrase, header and body echo the canary
	Redirect = "redirect" // 302 to another host with the canary in the query
	BadJSON  = "badjson"  // 200 with a truncated JSON body containing the canary
	Huge     = "huge"     // 200 claiming a huge body
	Hangup   = "hangup"   // closes without answering
	Stall    = "stall"    // never answers within a test's timeout
)

// Behaviours lists every Hostile behaviour.
var Behaviours = []string{Reason, Redirect, BadJSON, Huge, Hangup, Stall}

// Forms returns the plain and encoded variants of the canaries.
func Forms() []string {
	var forms []string
	for _, s := range []string{Key, Pass} {
		b := []byte(s)
		forms = append(forms, s,
			url.QueryEscape(s), url.PathEscape(s),
			strings.ReplaceAll(url.QueryEscape(s), "+", "%20"),
			base64.StdEncoding.EncodeToString(b), base64.RawStdEncoding.EncodeToString(b),
			base64.URLEncoding.EncodeToString(b), base64.RawURLEncoding.EncodeToString(b),
			jsonEscaped(s, true), jsonEscaped(s, false))
	}
	return forms
}

func jsonEscaped(s string, escapeHTML bool) string {
	var sb strings.Builder
	enc := json.NewEncoder(&sb)
	enc.SetEscapeHTML(escapeHTML)
	if enc.Encode(s) != nil {
		return ""
	}
	out := strings.TrimSpace(sb.String())
	return out[1 : len(out)-1]
}

// Leaked returns the first canary form found in text, or "".
func Leaked(text string) string {
	for _, form := range Forms() {
		if strings.Contains(text, form) {
			return form
		}
	}
	return ""
}

// Sweep collects every string a package produced, to prove no canary is in it.
type Sweep struct {
	t   *testing.T
	out []string
}

// NewSweep starts a sweep.
func NewSweep(t *testing.T) *Sweep { return &Sweep{t: t} }

// Add records text under a label.
func (s *Sweep) Add(label, text string) { s.out = append(s.out, label+": "+text) }

// AddErr records an error (nil is ignored).
func (s *Sweep) AddErr(label string, err error) {
	if err != nil {
		s.Add(label, err.Error())
	}
}

// AddJSON records the JSON encoding of v.
func (s *Sweep) AddJSON(label string, v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		s.t.Fatal(err)
	}
	s.Add(label, string(raw))
}

// Check fails if anything recorded contains a canary.
func (s *Sweep) Check() {
	s.t.Helper()
	if len(s.out) == 0 {
		s.t.Fatal("sweep collected nothing")
	}
	for _, line := range s.out {
		if form := Leaked(line); form != "" {
			s.t.Errorf("secret form %q leaked in %q", form, line)
		}
	}
}

// Hostile starts a raw TCP upstream and returns its host:port.
func Hostile(t *testing.T, behaviour string) string {
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
			go serve(conn, behaviour)
		}
	}()
	return ln.Addr().String()
}

func serve(conn net.Conn, behaviour string) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	req, err := http.ReadRequest(bufio.NewReader(conn))
	if err != nil {
		return
	}
	_, _ = io.Copy(io.Discard, req.Body)
	echo := Key + " " + Pass
	switch behaviour {
	case Reason:
		_, _ = fmt.Fprintf(conn, "HTTP/1.1 401 %s\r\nX-Echo: %s\r\nContent-Length: %d\r\n\r\n%s", echo, echo, len(echo), echo)
	case Redirect:
		_, _ = fmt.Fprintf(conn, "HTTP/1.1 302 Found\r\nLocation: http://other.example.com/?api_key=%s\r\nContent-Length: 0\r\n\r\n", url.QueryEscape(Key))
	case BadJSON:
		_, _ = fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n{%s", len(echo)+1, echo)
	case Huge:
		_, _ = fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Length: 1000000\r\n\r\n%s", echo)
	case Stall:
		time.Sleep(2 * time.Second)
	}
}
