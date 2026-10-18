package downloadclient

import (
	"bufio"
	"bytes"
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

// Test helpers for the adapters' canary sweeps. They live in a non-test file so
// both adapter packages share them; nothing in the server binary imports them.

// Hostile starts a raw TCP upstream that echoes echo everywhere an HTTP server
// can: the reason phrase, a header (Location, Set-Cookie) and the body. It
// returns host:port. Behaviours: "reason", "redirect", "badjson", "huge",
// "hangup", "stall" (answers after stallFor).
func Hostile(t *testing.T, behaviour, echo string, stallFor time.Duration) string {
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
				_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
				req, err := http.ReadRequest(bufio.NewReader(conn))
				if err != nil {
					return
				}
				_, _ = io.Copy(io.Discard, req.Body)
				switch behaviour {
				case "reason":
					_, _ = fmt.Fprintf(conn, "HTTP/1.1 401 %s\r\nX-Echo: %s\r\nSet-Cookie: SID=%s\r\nContent-Length: %d\r\n\r\n%s",
						oneLine(echo), oneLine(echo), url.QueryEscape(echo), len(echo), echo)
				case "redirect":
					_, _ = fmt.Fprintf(conn, "HTTP/1.1 302 Found\r\nLocation: http://other.example.com/?apikey=%s\r\nContent-Length: 0\r\n\r\n", url.QueryEscape(echo))
				case "badjson":
					_, _ = fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n{%s", len(echo)+1, echo)
				case "huge":
					_, _ = fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Length: 10000000\r\n\r\n%s", echo)
				case "hangup":
					// close without a response
				case "stall":
					time.Sleep(stallFor)
				}
			}()
		}
	}()
	return ln.Addr().String()
}

func oneLine(s string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(s)
}

// SecretForms lists the ways a secret can appear in text: as is, URL-encoded,
// base64 in all four alphabets and JSON-escaped. A canary sweep fails if any
// form appears in any output.
func SecretForms(secret string) []string {
	b := []byte(secret)
	forms := []string{
		secret,
		url.QueryEscape(secret),
		url.PathEscape(secret),
		base64.StdEncoding.EncodeToString(b),
		base64.RawStdEncoding.EncodeToString(b),
		base64.URLEncoding.EncodeToString(b),
		base64.RawURLEncoding.EncodeToString(b),
	}
	for _, escapeHTML := range []bool{true, false} {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(escapeHTML)
		if enc.Encode(secret) == nil {
			if j := strings.TrimSpace(buf.String()); len(j) > 2 {
				forms = append(forms, j[1:len(j)-1])
			}
		}
	}
	return forms
}

// Leaked returns the first form of any secret found in text, or "".
func Leaked(text string, secrets ...string) string {
	for _, s := range secrets {
		for _, form := range SecretForms(s) {
			if form != "" && strings.Contains(text, form) {
				return form
			}
		}
	}
	return ""
}
