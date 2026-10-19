package instances

import (
	"errors"
	"net/url"
	"path"
	"strings"
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/providers/all"
	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
)

// FuzzStoredSecretDestination: an administrator edits an instance and leaves the
// secret blank; the stored one is filled in only while requests still go where
// they went when it was stored (otherwise a stolen session could point the
// instance at a server it controls and read the key from that server's logs).
//
// Invariants: when Merge fills in the stored secret, the request URL the client
// would build from the new setting and the one it builds from the stored setting
// (core.JoinURL, then net/url, which is what the transport dials) have the same
// scheme, the same host name (any letter case), the same effective port and the
// same path once dot segments are resolved; whenever they do not, the call is a
// validation error that names the field and holds neither secret; a secret that
// is typed again is used as typed and the stored one is never mixed in.
func FuzzStoredSecretDestination(f *testing.F) {
	reg, err := all.Registry()
	if err != nil {
		f.Fatal(err)
	}
	const storedSecret, newSecret = "STORED-secret-value-0123", "NEW-secret-value-4567"
	type target struct {
		id     string
		stored map[string]any
		extra  map[string]any // other required settings the request carries
	}
	targets := []target{
		{"radarr", map[string]any{"url": "http://192.0.2.10:7878/radarr", "apiKey": storedSecret}, nil},
		{"sabnzbd", map[string]any{"url": "https://sab.example.com:8443", "apiKey": storedSecret}, nil},
		{"qbittorrent", map[string]any{"url": "http://qbit.example.com", "username": "admin", "password": storedSecret}, map[string]any{"username": "admin"}},
		{"plex", map[string]any{"url": "http://[2001:db8::10]:32400", "token": storedSecret}, nil},
		{"jellyfin", map[string]any{"url": "http://jelly.example.com:8096/jf/", "apiKey": storedSecret}, nil},
	}
	f.Fuzz(func(t *testing.T, sel uint8, provided string, typeSecret bool) {
		tg := targets[int(sel)%len(targets)]
		def, ok := reg.Get(tg.id)
		if !ok {
			t.Fatalf("no provider %s", tg.id)
		}
		req := map[string]any{"url": provided}
		for k, v := range tg.extra {
			req[k] = v
		}
		secretKey := def.SecretKeys()[0]
		if typeSecret {
			req[secretKey] = newSecret
		}
		got, err := Merge(def, req, tg.stored)
		if err != nil {
			if strings.Contains(err.Error(), storedSecret) || strings.Contains(err.Error(), newSecret) || (len(provided) >= 12 && strings.Contains(err.Error(), provided)) {
				t.Fatalf("the error shows a value: %v", err)
			}
			var ve *core.ValidationError
			var inv *InvalidError
			if !errors.As(err, &ve) && !errors.As(err, &inv) {
				t.Fatalf("error is %T", err)
			}
			return
		}
		if typeSecret {
			if got[secretKey] != newSecret {
				t.Fatalf("a typed secret was replaced by %v", got[secretKey])
			}
			return
		}
		if got[secretKey] != storedSecret {
			t.Fatalf("no secret typed and none filled in: %v", got[secretKey])
		}
		// The stored secret was filled in: the request goes to the stored destination.
		a, aerr := url.Parse(core.JoinURL(provided, "/api/x"))
		b, berr := url.Parse(core.JoinURL(tg.stored["url"].(string), "/api/x"))
		if aerr != nil || berr != nil {
			t.Fatalf("unparsable request URL: %v %v", aerr, berr)
		}
		port := func(u *url.URL) string {
			if p := u.Port(); p != "" {
				return p
			}
			if u.Scheme == "https" {
				return "443"
			}
			return "80"
		}
		if a.Scheme != b.Scheme || !strings.EqualFold(a.Hostname(), b.Hostname()) || port(a) != port(b) || path.Clean(a.Path) != path.Clean(b.Path) {
			t.Fatalf("the stored secret goes to %s://%s:%s%s instead of %s://%s:%s%s (setting %q)",
				a.Scheme, a.Hostname(), port(a), a.Path, b.Scheme, b.Hostname(), port(b), b.Path, provided)
		}
	})
}
