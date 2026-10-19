package app

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"github.com/tristanwl/wheelhouse/server/internal/auth"
	"github.com/tristanwl/wheelhouse/server/internal/netaccess"
)

// Guard is the single authorization hook every route passes through. For each
// request it checks, in this order:
//
//  1. a state-changing request (anything but GET, HEAD and OPTIONS) must be
//     same-origin: a foreign Origin or a cross-site Sec-Fetch-Site is 403, for
//     every route including login and setup;
//  2. a route that is not public needs the service to be set up (503 "setup
//     required" while no account exists) and a valid session (401). With the
//     local-network bypass on, a request without a valid session from inside the
//     configured ranges is a signed-in user instead (never an administrator, and
//     never on the routes that need a real login: password and sessions). A real
//     session always wins;
//  3. a state-changing authenticated request must carry the CSRF token in
//     X-CSRF-Token (403): the session's, or for the bypass the stateless token
//     GET /api/auth/session hands out;
//  4. an account that must change its password may do nothing else (403);
//  5. an admin route needs the admin role (403).
//
// Nothing here redirects: the API answers JSON, and the UI decides what to show.
type Guard struct {
	// Auth is the account service. Nil refuses every non-public route.
	Auth *auth.Service
	// Net decides the client address and the bypass. Nil means no bypass and no
	// trusted proxy.
	Net *netaccess.Service

	bypassKey [32]byte // per process; the bypass CSRF token is an HMAC under it
}

// NewGuard returns a Guard over the account service and the network settings.
func NewGuard(a *auth.Service, n *netaccess.Service) *Guard {
	g := &Guard{Auth: a, Net: n}
	_, _ = rand.Read(g.bypassKey[:]) // crypto/rand.Read never fails (it ends the program)
	return g
}

var noNet = netaccess.NewMemory(nil)

func (g *Guard) network() *netaccess.Service {
	if g == nil || g.Net == nil {
		return noNet
	}
	return g.Net
}

type clientKey struct{}

// ClientOf returns where the request came from, as decided at the start of the
// request (the TCP peer, or the forwarded address of a trusted proxy).
func ClientOf(ctx context.Context) netaccess.Client {
	c, _ := ctx.Value(clientKey{}).(netaccess.Client)
	return c
}

// bypassCSRF is the CSRF token of a bypass client. A bypass request has no
// session to derive one from, so it is an HMAC of the client address under a key
// made at start: stateless (nothing stored, no cookie), unguessable by another
// site, and it dies with the process (the UI fetches a new one from
// GET /api/auth/session).
func (g *Guard) bypassCSRF(c netaccess.Client) string {
	m := hmac.New(sha256.New, g.bypassKey[:])
	m.Write([]byte("wheelhouse-bypass-csrf-v1\x00" + c.Addr.String()))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

type identityKey struct{}

// IdentityOf returns the signed-in identity of the request, or nil on a public
// route (or one the guard did not authenticate).
func IdentityOf(ctx context.Context) *auth.Identity {
	i, _ := ctx.Value(identityKey{}).(*auth.Identity)
	return i
}

// Routes that need a real login: they act on the account or its sessions, which
// the bypass principal does not have.
var needsRealLogin = map[string]bool{
	"POST /api/auth/logout":          true,
	"POST /api/auth/password":        true,
	"GET /api/auth/sessions":         true,
	"DELETE /api/auth/sessions/{id}": true,
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
		client := g.network().Resolve(r)
		ctx := context.WithValue(context.WithValue(r.Context(), levelKey{}, level), clientKey{}, client)
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
			switch {
			case !errors.Is(err, auth.ErrUnauthenticated):
				errorJSON(w, http.StatusServiceUnavailable, "service unavailable")
				return
			case g.network().Bypasses(client):
				id = auth.BypassIdentity(g.bypassCSRF(client))
			default:
				errorJSON(w, http.StatusUnauthorized, "authentication required")
				return
			}
		}
		if id.Bypass && needsRealLogin[pattern] {
			errorJSON(w, http.StatusUnauthorized, "authentication required")
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
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https") {
		return false
	}
	peer, ok := peerAddr(r)
	return ok && g.network().IsTrustedProxy(peer)
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

// clientAddr is the address recorded for a request: the TCP peer, or, when the
// peer is a trusted proxy, the client its forwarded headers name. A forwarded
// address that cannot be trusted falls back to the peer.
func clientAddr(r *http.Request) string {
	if c, ok := r.Context().Value(clientKey{}).(netaccess.Client); ok {
		if s := c.String(); s != "" {
			return s
		}
	}
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
