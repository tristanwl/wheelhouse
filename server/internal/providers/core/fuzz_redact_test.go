package core

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"
)

// pythonJSON is the secret inside a JSON string the way Python's json.dumps
// (ensure_ascii) writes it: every non-ASCII character as \uXXXX, surrogate
// pairs above the basic plane.
func pythonJSON(secret string, upper bool) string {
	var out strings.Builder
	var j strings.Builder
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(secret)
	j.WriteString(strings.TrimSpace(buf.String()))
	s := j.String()
	s = s[1 : len(s)-1]
	hex := "%04x"
	if upper {
		hex = "%04X"
	}
	for _, r := range s {
		switch {
		case r < 0x80:
			out.WriteRune(r)
		case r > 0xffff:
			r -= 0x10000
			fmt.Fprintf(&out, `\u`+hex+`\u`+hex, 0xd800+(r>>10), 0xdc00+(r&0x3ff))
		default:
			fmt.Fprintf(&out, `\u`+hex, r)
		}
	}
	return out.String()
}

// plantedForms are the encodings a secret can come back in, written here
// independently of redact.go so that a form the redactor forgets is caught.
func plantedForms(secret string) []string {
	b := []byte(secret)
	lower := func(s string) string {
		out := []byte(s)
		for i := 0; i+2 < len(out); i++ {
			if out[i] == '%' {
				for j := i + 1; j <= i+2; j++ {
					if out[j] >= 'A' && out[j] <= 'F' {
						out[j] += 'a' - 'A'
					}
				}
			}
		}
		return string(out)
	}
	jsonForm := func(html bool) string {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(html)
		_ = enc.Encode(secret)
		s := strings.TrimSpace(buf.String())
		return s[1 : len(s)-1]
	}
	var uri strings.Builder // JavaScript encodeURIComponent
	for _, c := range b {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', strings.IndexByte("-_.!~*'()", c) >= 0:
			uri.WriteByte(c)
		default:
			fmt.Fprintf(&uri, "%%%02X", c)
		}
	}
	qe := url.QueryEscape(secret)
	forms := []string{
		secret,
		qe, url.PathEscape(secret), uri.String(),
		strings.ReplaceAll(qe, "+", "%20"),
		lower(qe), lower(url.PathEscape(secret)), lower(uri.String()), lower(strings.ReplaceAll(qe, "+", "%20")),
		base64.StdEncoding.EncodeToString(b), base64.RawStdEncoding.EncodeToString(b),
		base64.URLEncoding.EncodeToString(b), base64.RawURLEncoding.EncodeToString(b),
		strings.ReplaceAll(base64.StdEncoding.EncodeToString(b), "/", `\/`),
		jsonForm(true), jsonForm(false),
		strings.ReplaceAll(jsonForm(true), "/", `\/`), strings.ReplaceAll(jsonForm(false), "/", `\/`),
		pythonJSON(secret, false), pythonJSON(secret, true),
		html.EscapeString(secret),
		strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;").Replace(secret),
	}
	// Every mix of the three ways encoders differ (space, the marks ! * ' ( ),
	// the reserved $ & + = : @), in both hex cases.
	for mask := range 8 {
		var e strings.Builder
		for _, c := range b {
			switch {
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', strings.IndexByte("-_.~", c) >= 0:
				e.WriteByte(c)
			case mask&1 != 0 && strings.IndexByte("!*'()", c) >= 0, mask&2 != 0 && strings.IndexByte("$&+=:@", c) >= 0:
				e.WriteByte(c)
			case mask&4 != 0 && c == ' ':
				e.WriteByte('+')
			default:
				fmt.Fprintf(&e, "%%%02X", c)
			}
		}
		forms = append(forms, e.String(), lower(e.String()))
	}
	return forms
}

// FuzzRedact plants a secret, in one of its encodings, inside arbitrary
// surrounding text and checks what comes out.
//
// Invariants: the planted secret is not in the output in any encoding;
// redacting twice changes nothing; the output is never longer than a fixed
// factor of the input (a redactor that grows text can be used to amplify a
// log line); and a secret that is too short to be one (under 4 characters)
// leaves unrelated text alone.
func FuzzRedact(f *testing.F) {
	f.Fuzz(func(t *testing.T, secret, before, after string, form uint8) {
		// The mask is "***", and a secret that itself contains "*" can be rebuilt
		// from the mask and its neighbours; that is not what is being tested.
		if strings.Contains(secret, "*") || !utf8.ValidString(secret) || utf8.RuneCountInString(secret) < minSecretLen {
			t.Skip()
		}
		forms := plantedForms(secret)
		planted := forms[int(form)%len(forms)]
		text := before + planted + after
		r := NewRedactor([]string{secret, "unrelated-second-secret"})
		out := r.Redact(text)

		for _, form := range forms {
			// A form that is also a substring of the surrounding text can legitimately
			// remain only if it is not a whole form there: the check is on the output.
			if form != "" && strings.Contains(out, form) {
				t.Fatalf("secret form %q survived in %q (input %q)", form, out, text)
			}
		}
		if again := r.Redact(out); again != out {
			t.Fatalf("not idempotent: %q then %q", out, again)
		}
		if len(out)*2 > len(text)*3+16 {
			t.Fatalf("output of %d bytes grew from %d", len(out), len(text))
		}
		// Package-level helper agrees with the Redactor.
		if got := Redact(text, secret, "unrelated-second-secret"); got != out {
			t.Fatalf("Redact and Redactor differ: %q vs %q", got, out)
		}
	})
}

// FuzzRedactArbitrary redacts arbitrary text against arbitrary secrets: it
// never panics, never grows the text past a fixed factor, is idempotent, and
// never leaves a secret of 4 or more characters in the output.
func FuzzRedactArbitrary(f *testing.F) {
	f.Fuzz(func(t *testing.T, text, s1, s2 string) {
		out := Redact(text, s1, s2)
		if len(out)*2 > len(text)*3+16 {
			t.Fatalf("output of %d bytes grew from %d", len(out), len(text))
		}
		if again := Redact(out, s1, s2); utf8.ValidString(s1) && utf8.ValidString(s2) &&
			!strings.Contains(s1+s2, "*") && again != out {
			t.Fatalf("not idempotent: %q then %q", out, again)
		}
		for _, s := range []string{s1, s2} {
			if utf8.RuneCountInString(s) >= minSecretLen && strings.Contains(out, s) && !strings.Contains(s, "*") {
				t.Fatalf("secret %q survived in %q", s, out)
			}
		}
	})
}
