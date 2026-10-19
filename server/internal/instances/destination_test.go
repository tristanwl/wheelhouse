package instances

import (
	"errors"
	"strings"
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
)

func TestStoredSecretIsNotSentToAChangedDestination(t *testing.T) {
	f := newFixture(t)
	if _, err := f.src.Create(ctx(), radarr("http://192.0.2.10:7878/radarr", keyA)); err != nil {
		t.Fatal(err)
	}
	def, _ := f.src.Registry().Get("radarr")
	stored := map[string]any{"url": "http://192.0.2.10:7878/radarr", "apiKey": keyA}

	changed := []string{
		"http://attacker.example.com",
		"http://192.0.2.10:7879/radarr",
		"https://192.0.2.10:7878/radarr",
		"http://192.0.2.11:7878/radarr",
		"http://192.0.2.10:7878/other",
		"http://192.0.2.10:7878",
	}
	for _, u := range changed {
		// Merge is what the connection test uses.
		_, err := Merge(def, map[string]any{"url": u}, stored)
		var ve *core.ValidationError
		if !errors.As(err, &ve) || ve.Errors[0].Field != "apiKey" || !strings.Contains(err.Error(), "entered again") {
			t.Errorf("Merge with %s: %v", u, err)
		}
		if err != nil && strings.Contains(err.Error(), keyA) {
			t.Errorf("the error shows the secret: %v", err)
		}
		// So is a stored update.
		if _, _, err := f.src.Update(ctx(), "radarr", Patch{Config: map[string]any{"url": u}}); err == nil {
			t.Errorf("Update with %s kept the stored secret", u)
		}
		// Re-entering the secret is fine.
		if _, err := Merge(def, map[string]any{"url": u, "apiKey": keyB}, stored); err != nil {
			t.Errorf("Merge with %s and a new key: %v", u, err)
		}
	}
	// The stored secret is intact after the refused updates.
	if e, err := f.src.Get(ctx(), "radarr"); err != nil || e.Config["apiKey"] != keyA || e.Config["url"] != "http://192.0.2.10:7878/radarr" {
		t.Errorf("stored instance changed: %+v %v", e.Config, err)
	}

	same := []string{
		"http://192.0.2.10:7878/radarr",
		"http://192.0.2.10:7878/radarr/",
		"HTTP://192.0.2.10:7878/radarr",
	}
	for _, u := range same {
		got, err := Merge(def, map[string]any{"url": u}, stored)
		if err != nil || got["apiKey"] != keyA {
			t.Errorf("Merge with the same destination %s: %v %v", u, got, err)
		}
	}
	// No URL in the request at all is not a change of destination (it is
	// refused as missing, never sent anywhere).
	if _, err := Merge(def, map[string]any{}, stored); err == nil || strings.Contains(err.Error(), "entered again") {
		t.Errorf("missing url: %v", err)
	}
}

func TestDefaultPortsAreTheSameDestination(t *testing.T) {
	for _, c := range []struct{ a, b string }{
		{"http://h:80", "http://h"},
		{"https://h:443/x/", "https://H/x"},
		{"http://[2001:db8::1]:80", "http://[2001:DB8::1]"},
	} {
		if normalizeDestination(c.a) != normalizeDestination(c.b) {
			t.Errorf("%s and %s should match: %q %q", c.a, c.b, normalizeDestination(c.a), normalizeDestination(c.b))
		}
	}
	if normalizeDestination("http://h:81") == normalizeDestination("http://h") || normalizeDestination("not a url") != "" {
		t.Error("distinct destinations compared equal")
	}
}

func TestInstanceURLWithAFragmentOrQueryIsRefused(t *testing.T) {
	f := newFixture(t)
	for _, u := range []string{"http://192.0.2.10:7878#/api", "http://192.0.2.10:7878/#", "http://192.0.2.10:7878/radarr?x=1", "http://192.0.2.10:7878?"} {
		_, err := f.src.Create(ctx(), radarr(u, keyA))
		var ve *core.ValidationError
		if !errors.As(err, &ve) || !strings.Contains(err.Error(), "query string") {
			t.Errorf("%s: %v", u, err)
		}
	}
}
