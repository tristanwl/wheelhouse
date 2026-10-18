package status

import (
	"testing"
	"time"

	dc "github.com/tristanwl/wheelhouse/server/internal/providers/downloadclient"
)

func TestServiceLink(t *testing.T) {
	cases := []struct {
		name   string
		url    any
		append string
		want   *Link
	}{
		{"plain", "http://192.0.2.10:8989", "", &Link{"http", 8989, "/"}},
		{"https default port", "https://sonarr.example.com:443/", "", &Link{"https", 0, "/"}},
		{"http default port", "http://sonarr.example.com:80", "", &Link{"http", 0, "/"}},
		{"https odd port", "https://sonarr.example.com:8443", "", &Link{"https", 8443, "/"}},
		{"prefix, trailing slashes trimmed", "http://example.com:7878/radarr//", "", &Link{"http", 7878, "/radarr"}},
		{"plex web path", "http://example.com:32400/", "/web", &Link{"http", 32400, "/web"}},
		{"prefix and append", "http://example.com/p/", "/web", &Link{"http", 0, "/p/web"}},
		{"credentials and query never appear", "http://u:secret-pass@example.com:9000/x?apikey=secret-key#frag", "", &Link{"http", 9000, "/x"}},
		{"not a string", 42, "", nil},
		{"nil", nil, "", nil},
		{"empty", "", "", nil},
		{"garbage", "not a url", "", nil},
		{"wrong scheme", "ftp://example.com", "", nil},
		{"no host", "http://", "", nil},
		{"javascript", "javascript:alert(1)", "", nil},
	}
	for _, c := range cases {
		got := ServiceLink(c.url, c.append)
		switch {
		case got == nil && c.want == nil:
		case got == nil || c.want == nil || *got != *c.want:
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestWarningVisibility(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) string { return now.Add(-d).Format(time.RFC3339) }
	cases := []struct {
		name string
		at   string
		want bool
	}{
		{"recent", at(time.Hour), true},
		{"exactly a day", at(24 * time.Hour), true},
		{"a second past a day", at(24*time.Hour + time.Second), false},
		{"future", at(-time.Hour), true},
		{"no timestamp", "", true},
		{"unparseable timestamp", "yesterday-ish", true},
	}
	for _, c := range cases {
		if got := WarningVisible(dc.Warning{At: c.at}, now); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestHasRecentErrorIgnoresOldAndNonErrors(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	old := now.Add(-48 * time.Hour).Format(time.RFC3339)
	recent := now.Add(-time.Hour).Format(time.RFC3339)
	for name, tc := range map[string]struct {
		warnings []dc.Warning
		want     bool
	}{
		"none":             {nil, false},
		"recent warning":   {[]dc.Warning{{Severity: dc.SeverityWarning, At: recent}}, false},
		"old error":        {[]dc.Warning{{Severity: dc.SeverityError, At: old}}, false},
		"recent error":     {[]dc.Warning{{Severity: dc.SeverityError, At: recent}}, true},
		"untimed error":    {[]dc.Warning{{Severity: dc.SeverityError}}, true},
		"old and recent":   {[]dc.Warning{{Severity: dc.SeverityError, At: old}, {Severity: dc.SeverityError, At: recent}}, true},
		"recent non-error": {[]dc.Warning{{Severity: dc.SeverityError, At: old}, {Severity: dc.SeverityWarning, At: recent}}, false},
	} {
		if got := HasRecentError(tc.warnings, now); got != tc.want {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
}
