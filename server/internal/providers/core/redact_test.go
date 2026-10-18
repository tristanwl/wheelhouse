package core

import (
	"encoding/base64"
	"net/url"
	"strings"
	"testing"
)

func TestRedactMasksURLCredentials(t *testing.T) {
	got := Redact("failed: http://user:pa55word@host.example.com:8080/x")
	if want := "failed: http://***@host.example.com:8080/x"; got != want {
		t.Fatalf("got %q", got)
	}
}

func TestRedactStripsKnownSecretsAndTheirEncodings(t *testing.T) {
	if got, want := Redact("key abc123XYZ and abc%2B123", "abc123XYZ", "abc+123"), "key *** and ***"; got != want {
		t.Fatalf("got %q", got)
	}
}

func TestRedactStripsCommonSecretQueryParameters(t *testing.T) {
	cases := map[string]string{
		"GET /api?mode=queue&apikey=s3cr3tvalue&output=json": "GET /api?mode=queue&apikey=***&output=json",
		"https://x/y?X-Plex-Token=abcdefgh&a=1":              "https://x/y?X-Plex-Token=***&a=1",
		"?api_key=abcd&password=hunter22&token=tok1":         "?api_key=***&password=***&token=***",
	}
	for in, want := range cases {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRedactIgnoresVeryShortSecrets(t *testing.T) {
	if got := Redact("a b c", "a"); got != "a b c" {
		t.Fatalf("got %q", got)
	}
	if got := Redact("abc abc", "abc"); got != "abc abc" {
		t.Fatalf("3-char secret should be ignored: %q", got)
	}
}

// Every form a secret can take on its way into an upstream error.
func TestRedactCoversEveryForm(t *testing.T) {
	secret := `p@ss w/rd+"x"&y=z` + "é??>?"
	forms := map[string]string{
		"plain":        secret,
		"encodeURIC":   encodeURIComponent(secret),
		"query":        url.QueryEscape(secret),
		"path":         url.PathEscape(secret),
		"lower hex":    lowerPercentHex(url.QueryEscape(secret)),
		"base64 std":   base64.StdEncoding.EncodeToString([]byte(secret)),
		"base64 raw":   base64.RawStdEncoding.EncodeToString([]byte(secret)),
		"base64 url":   base64.URLEncoding.EncodeToString([]byte(secret)),
		"base64 rawu":  base64.RawURLEncoding.EncodeToString([]byte(secret)),
		"json escaped": `p@ss w/rd+\"x\"&y=z` + "é??>?",
	}
	// Every form must be distinct, or removing one from the implementation would
	// go unnoticed.
	seen := map[string]string{}
	for name, form := range forms {
		if other, dup := seen[form]; dup {
			t.Fatalf("test secret gives the same text for %s and %s", name, other)
		}
		seen[form] = name
	}
	for name, form := range forms {
		got := Redact("upstream said: ["+form+"] done", secret)
		if strings.Contains(got, form) {
			t.Errorf("%s form survived: %q", name, got)
		}
		if got != "upstream said: [***] done" {
			t.Errorf("%s: got %q", name, got)
		}
	}
}

func TestRedactHandlesOverlappingSecrets(t *testing.T) {
	got := Redact("x abcdefgh y", "abcd", "abcdefgh")
	if got != "x *** y" {
		t.Fatalf("longer secret should go first: %q", got)
	}
}

func TestEncodeURIComponentMatchesJavaScript(t *testing.T) {
	// JavaScript: encodeURIComponent("a b+c/d?e=f&g!'()*~é")
	got := encodeURIComponent("a b+c/d?e=f&g!'()*~é")
	if want := "a%20b%2Bc%2Fd%3Fe%3Df%26g!'()*~%C3%A9"; got != want {
		t.Fatalf("got %q", got)
	}
}
