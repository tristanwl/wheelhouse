package core

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// minSecretLen: very short values would mangle unrelated text (and are not
// real secrets).
const minSecretLen = 4

// Secrets that appear in URLs and error text even when we were not told about
// them (SABnzbd, for one, takes its API key as a query parameter).
//
// Parity note: like the TypeScript version this only matches `name=value`, so a
// JSON-style `"apikey":"value"` is only caught by the exact-secret pass.
var (
	secretParam    = regexp.MustCompile(`(?i)\b(api[_-]?key|token|password|passwd|secret|x-plex-token)=([^&\s"'<>]+)`)
	urlCredentials = regexp.MustCompile(`(//)[^/\s@]+@`)
)

// Redactor replaces known secret values (in plain, URL-encoded, base64 and
// JSON-escaped form) and common secret query parameters with ***.
type Redactor struct {
	variants []string // longest first, so a secret that contains another goes first
}

// NewRedactor prepares the variants of each secret once.
func NewRedactor(secrets []string) *Redactor {
	seen := map[string]bool{}
	var variants []string
	add := func(v string) {
		if v != "" && !seen[v] {
			seen[v] = true
			variants = append(variants, v)
		}
	}
	for _, secret := range secrets {
		if utf8.RuneCountInString(secret) < minSecretLen {
			continue
		}
		add(secret)
		for _, enc := range []string{
			encodeURIComponent(secret),
			url.QueryEscape(secret),
			url.PathEscape(secret),
		} {
			add(enc)
			add(lowerPercentHex(enc))
		}
		b := []byte(secret)
		add(base64.StdEncoding.EncodeToString(b))
		add(base64.RawStdEncoding.EncodeToString(b))
		add(base64.URLEncoding.EncodeToString(b))
		add(base64.RawURLEncoding.EncodeToString(b))
		add(jsonEscaped(secret, true))
		add(jsonEscaped(secret, false))
	}
	slices.SortStableFunc(variants, func(a, b string) int { return len(b) - len(a) })
	return &Redactor{variants: variants}
}

// Redact masks the text.
func (r *Redactor) Redact(text string) string {
	out := text
	if r != nil {
		for _, v := range r.variants {
			out = strings.ReplaceAll(out, v, "***")
		}
	}
	out = urlCredentials.ReplaceAllString(out, "${1}***@")
	return secretParam.ReplaceAllString(out, "${1}=***")
}

// Redact masks text using the given secrets. Prefer a Redactor when redacting
// repeatedly with the same secrets.
func Redact(text string, secrets ...string) string {
	return NewRedactor(secrets).Redact(text)
}

// jsonEscaped is the secret as it would appear inside a JSON string, with or
// without Go's HTML escaping (&, <, > as \u00XX).
func jsonEscaped(s string, escapeHTML bool) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(escapeHTML)
	if enc.Encode(s) != nil {
		return ""
	}
	out := strings.TrimSpace(buf.String())
	if len(out) < 2 {
		return ""
	}
	return out[1 : len(out)-1]
}

// encodeURIComponent mirrors the JavaScript function: everything except
// A-Z a-z 0-9 - _ . ! ~ * ' ( ) is percent-encoded.
func encodeURIComponent(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			strings.IndexByte("-_.!~*'()", c) >= 0:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// lowerPercentHex turns %2B into %2b: servers do echo the lowercase form.
func lowerPercentHex(s string) string {
	b := []byte(s)
	for i := 0; i+2 < len(b); i++ {
		if b[i] == '%' {
			b[i+1] = lowerHex(b[i+1])
			b[i+2] = lowerHex(b[i+2])
			i += 2
		}
	}
	return string(b)
}

func lowerHex(c byte) byte {
	if c >= 'A' && c <= 'F' {
		return c + 'a' - 'A'
	}
	return c
}
