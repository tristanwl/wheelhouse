package app

import (
	"crypto/tls"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/ui"
)

func TestHSTSOnlyOnHTTPS(t *testing.T) {
	acc := newAccounts(t)
	h := Handler(Options{UI: ui.Placeholder(), Auth: acc.Svc, TrustedProxies: []netip.Prefix{netip.MustParsePrefix("192.0.2.50/32")}})
	send := func(peer string, tlsOn bool, xfp string) string {
		req := httptest.NewRequest("GET", "/healthz", nil)
		req.RemoteAddr = peer
		if tlsOn {
			req.TLS = &tls.ConnectionState{}
		}
		if xfp != "" {
			req.Header.Set("X-Forwarded-Proto", xfp)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Header().Get("Strict-Transport-Security")
	}
	const want = "max-age=31536000"
	if got := send("198.51.100.1:1", true, ""); got != want {
		t.Errorf("direct TLS: %q", got)
	}
	if got := send("192.0.2.50:1", false, "https"); got != want {
		t.Errorf("trusted proxy saying https: %q", got)
	}
	for name, c := range map[string]struct {
		peer string
		tls  bool
		xfp  string
	}{
		"plain http":                   {"198.51.100.1:1", false, ""},
		"untrusted peer claims https":  {"198.51.100.1:1", false, "https"},
		"trusted proxy says http":      {"192.0.2.50:1", false, "http"},
		"trusted proxy says nothing":   {"192.0.2.50:1", false, ""},
		"untrusted with garbage value": {"198.51.100.1:1", false, "https, http"},
	} {
		if got := send(c.peer, c.tls, c.xfp); got != "" {
			t.Errorf("%s: HSTS %q on a request that is not HTTPS", name, got)
		}
	}
}
