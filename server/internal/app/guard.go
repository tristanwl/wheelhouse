package app

import (
	"context"
	"crypto/subtle"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"github.com/tristanwl/wheelhouse/server/internal/auth"
)

// Guard is the single authorization hook every route passes through. For each
// request it checks, in this order:
//
//  1. a state-changing request (anything but GET, HEAD and OPTIONS) must be
//     same-origin: a foreign Origin or a cross-site Sec-Fetch-Site is 403, for
//     every route including login and setup;
//  2. a route that is not public needs the service to be set up (503 "setup
//     required" while no account exists) and a valid session (401);
//  3. a state-changing authenticated request must carry the session's CSRF token
//     in X-CSRF-Token (403);
//  4. an account that must change its password may do nothing else (403);
//  5. an admin route needs the admin role (403).
//
// Nothing here redirects: the API answers JSON, and the UI decides what to show.
type Guard struct {
	// Auth is the account service. Nil refuses every non-public route.
	Auth *auth.Service
	// TrustedProxies are the peers whose X-Forwarded-Proto is believed.
	TrustedProxies []netip.Prefix
}

type identityKey struct{}

// IdentityOf returns the signed-in identity of the request, or nil on a public
// route (or one the guard did not authenticate).
func IdentityOf(ctx context.Context) *auth.Identity {
	i, _ := ctx.Value(identityKey{}).(*auth.Identity)
	return i
}

// Routes a session that must change its password may still use.
var allowedWhileMustChange = map[string]bool{
	"GET /api/auth/session":   true,
	"POST /api/auth/password": true,
	"POST /api/auth/logout":   true,
}

func isSafeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

func (g *Guard) authorize(pattern string, level Level, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), levelKey{}, level)
		r = r.WithContext(ctx)

		if !isSafeMethod(r.Method) && !sameOrigin(r) {
			errorJSON(w, http.StatusForbidden, "cross-origin request refused")
			return
		}
		if level == Public {
			next.ServeHTTP(w, r)
			return
		}
		if g == nil || g.Auth == nil {
			errorJSON(w, http.StatusServiceUnavailable, "authentication is not available")
			return
		}
		need, err := g.Auth.NeedsSetup(ctx)
		if err != nil {
			errorJSON(w, http.StatusServiceUnavailable, "service unavailable")
			return
		}
		if need {
			errorJSON(w, http.StatusServiceUnavailable, "setup required")
			return
		}

		secure := g.secure(r)
		token := sessionToken(r, secure)
		id, err := g.Auth.Authenticate(ctx, token)
		if err != nil {
			if token != "" {
				clearSessionCookie(w, secure) // a dead cookie is dropped by the browser
			}
			if err == auth.ErrUnauthenticated { //nolint:errorlint // the sentinel is returned as is
				errorJSON(w, http.StatusUnauthorized, "authentication required")
			} else {
				errorJSON(w, http.StatusServiceUnavailable, "service unavailable")
			}
			return
		}
		if !isSafeMethod(r.Method) && !csrfOK(r, id) {
			errorJSON(w, http.StatusForbidden, "missing or invalid CSRF token")
			return
		}
		if id.MustChangePassword && !allowedWhileMustChange[pattern] {
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error": "you must change your password before doing anything else", "code": "password-change-required",
			})
			return
		}
		if level == Admin && id.Role != auth.RoleAdmin {
			errorJSON(w, http.StatusForbidden, "forbidden")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, identityKey{}, id)))
	})
}

func csrfOK(r *http.Request, id *auth.Identity) bool {
	got := r.Header.Get("X-CSRF-Token")
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(id.CSRF)) == 1
}

// sameOrigin is the cross-site check for state-changing requests. A browser
// always sends Sec-Fetch-Site and Origin on those; only "same-origin" (and
// "none", a navigation the user typed) pass, and an Origin must name this
// request's own host. A request with neither header is not from a browser
// (curl, the acceptance suite, a script), which cannot be tricked into acting
// for someone else's cookie; on authenticated routes it still needs the CSRF
// token. The proxy must pass the Host header through unchanged.
func sameOrigin(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
	default:
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return false // "null" and anything odd
	}
	return strings.EqualFold(u.Host, r.Host)
}

// secure reports whether the request reached the server over HTTPS: directly,
// or through a proxy in the trusted list that says so.
func (g *Guard) secure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if g == nil || len(g.TrustedProxies) == 0 || !strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https") {
		return false
	}
	peer, ok := peerAddr(r)
	if !ok {
		return false
	}
	for _, p := range g.TrustedProxies {
		if p.Contains(peer) {
			return true
		}
	}
	return false
}

func peerAddr(r *http.Request) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return a.Unmap(), true
}

// clientAddr is the address recorded for a request: the TCP peer. Forwarded
// headers are not believed (item 5 of the plan adds the trusted-proxy rules).
func clientAddr(r *http.Request) string {
	if a, ok := peerAddr(r); ok {
		return a.String()
	}
	return r.RemoteAddr
}

// Session cookie. Over HTTPS the name carries the __Host- prefix, which makes
// browsers refuse it unless it is Secure, has Path=/ and no Domain, so a
// sibling subdomain cannot plant or overwrite it.
const (
	cookieName       = "wheelhouse"
	secureCookieName = "__Host-wheelhouse"
)

func cookieNameFor(secure bool) string {
	if secure {
		return secureCookieName
	}
	return cookieName
}

// sessionToken returns the session cookie for this kind of request. Only the
// name that fits the transport is read: a plain-HTTP cookie is not accepted on
// an HTTPS request, so one planted from an insecure origin is ignored.
func sessionToken(r *http.Request, secure bool) string {
	c, err := r.Cookie(cookieNameFor(secure))
	if err != nil {
		return ""
	}
	return c.Value
}

func setSessionCookie(w http.ResponseWriter, secure bool, token string, res *auth.LoginResult) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure follows the request's transport (true over HTTPS), by design
		Name: cookieNameFor(secure), Value: token, Path: "/",
		Expires: res.Session.ExpiresAt, MaxAge: int(res.Session.ExpiresAt.Sub(res.Session.CreatedAt).Seconds()),
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode,
	})
}

func clearSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure follows the request's transport, as for the cookie it clears
		Name: cookieNameFor(secure), Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode,
	})
}
