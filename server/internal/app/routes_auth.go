package app

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/auth"
)

// maxAuthBody bounds the JSON bodies of the account routes. A password can be
// 1024 bytes; nothing here needs more than a few KiB.
const maxAuthBody = 8 << 10

// decodeJSON reads one strict JSON object from the request: it must be sent as
// application/json (a plain HTML form cannot), have no unknown fields and no
// trailing data, and be small. On failure it answers and returns false.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		errorJSON(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAuthBody))
	dec.DisallowUnknownFields()
	err := dec.Decode(dst)
	if err == nil {
		if _, terr := dec.Token(); !errors.Is(terr, io.EOF) {
			err = errors.New("trailing data")
		}
	}
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			errorJSON(w, http.StatusRequestEntityTooLarge, "request body too large")
		} else {
			errorJSON(w, http.StatusBadRequest, "invalid request body")
		}
		return false
	}
	return true
}

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func userJSON(u auth.User) map[string]any {
	return map[string]any{
		"id": u.ID, "username": u.Username, "role": string(u.Role), "disabled": u.Disabled,
		"mustChangePassword": u.MustChangePassword,
		"createdAt":          rfc3339(u.CreatedAt), "passwordChangedAt": rfc3339(u.PasswordChangedAt),
	}
}

func sessionJSON(s auth.Session, current string) map[string]any {
	return map[string]any{
		"id": s.ID, "createdAt": rfc3339(s.CreatedAt), "lastSeenAt": rfc3339(s.LastSeen), "expiresAt": rfc3339(s.ExpiresAt),
		"ip": s.IP, "userAgent": s.UserAgent, "current": s.ID == current,
	}
}

// idleExpiry is when a session that does nothing more ends.
func idleExpiry(s auth.Session) time.Time {
	return time.Unix(min(s.LastSeen.Add(auth.IdleTimeout).Unix(), s.ExpiresAt.Unix()), 0).UTC()
}

// writeAuthError answers an error from the auth service. Policy failures and
// conflicts say what to fix; anything unexpected is a generic 500 with no text.
func writeAuthError(w http.ResponseWriter, err error) {
	var pe *auth.PolicyError
	switch {
	case errors.As(err, &pe):
		errorJSON(w, http.StatusBadRequest, pe.Msg)
	case errors.Is(err, auth.ErrNotFound):
		errorJSON(w, http.StatusNotFound, "not found")
	case errors.Is(err, auth.ErrUsernameTaken):
		errorJSON(w, http.StatusConflict, err.Error())
	case errors.Is(err, auth.ErrLastAdmin):
		errorJSON(w, http.StatusConflict, err.Error())
	case errors.Is(err, auth.ErrWrongPassword):
		errorJSON(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, auth.ErrTooManyAttempts):
		errorJSON(w, http.StatusTooManyRequests, err.Error())
	default:
		internalError(w)
	}
}

func clientMeta(r *http.Request) auth.ClientMeta {
	return auth.ClientMeta{Addr: clientAddr(r), UserAgent: r.UserAgent()}
}

// sessionBody is what login, session and password change return.
func sessionBody(u auth.User, csrf string, expires, absolute time.Time, sessionID string) map[string]any {
	return map[string]any{
		"user": userJSON(u), "csrfToken": csrf, "sessionId": sessionID,
		"expiresAt": rfc3339(expires), "absoluteExpiresAt": rfc3339(absolute),
	}
}

func registerAuth(r *Router, d deps) {
	svc := d.auth
	handle := accountRoutes(r, d)

	handle("GET /api/auth/setup", Public, func(w http.ResponseWriter, req *http.Request) {
		need, err := svc.NeedsSetup(req.Context())
		if err != nil {
			errorJSON(w, http.StatusServiceUnavailable, "service unavailable")
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"setupRequired": need})
	})

	handle("POST /api/auth/setup", Public, func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Token    string `json:"token"`
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if !decodeJSON(w, req, &body) {
			return
		}
		u, err := svc.Setup(req.Context(), body.Token, body.Username, body.Password, clientMeta(req))
		switch {
		case err == nil:
			writeJSON(w, http.StatusCreated, map[string]any{"user": userJSON(u)})
		case errors.Is(err, auth.ErrBadSetupToken):
			errorJSON(w, http.StatusForbidden, "invalid setup token")
		case errors.Is(err, auth.ErrSetupDone):
			errorJSON(w, http.StatusConflict, "setup has already been completed")
		default:
			writeAuthError(w, err)
		}
	})

	handle("POST /api/auth/login", Public, func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if !decodeJSON(w, req, &body) {
			return
		}
		need, err := svc.NeedsSetup(req.Context())
		if err != nil {
			errorJSON(w, http.StatusServiceUnavailable, "service unavailable")
			return
		}
		if need {
			errorJSON(w, http.StatusServiceUnavailable, "setup required")
			return
		}
		res, err := svc.Login(req.Context(), body.Username, body.Password, clientMeta(req))
		if errors.Is(err, auth.ErrInvalidCredentials) {
			errorJSON(w, http.StatusUnauthorized, "invalid username or password")
			return
		}
		if err != nil {
			internalError(w)
			return
		}
		setSessionCookie(w, d.guard.secure(req), res.Token, res)
		writeJSON(w, http.StatusOK, sessionBody(res.User, res.CSRF, idleExpiry(res.Session), res.Session.ExpiresAt, res.Session.ID))
	})

	handle("POST /api/auth/logout", Authenticated, func(w http.ResponseWriter, req *http.Request) {
		id := IdentityOf(req.Context())
		if err := svc.Logout(req.Context(), id.Actor(clientAddr(req)), id.SessionID); err != nil {
			internalError(w)
			return
		}
		clearSessionCookie(w, d.guard.secure(req))
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})

	handle("GET /api/auth/session", Authenticated, func(w http.ResponseWriter, req *http.Request) {
		id := IdentityOf(req.Context())
		if id.Bypass {
			// No account, no session, no expiry: the client is inside the
			// local-network bypass. The UI skips the login screen on "bypass": true
			// and sends csrfToken on state changes.
			writeJSON(w, http.StatusOK, map[string]any{
				"bypass": true, "csrfToken": id.CSRF,
				"user": map[string]any{"id": "", "username": id.Username, "role": string(id.Role), "disabled": false, "mustChangePassword": false},
			})
			return
		}
		body := sessionBody(id.User, id.CSRF, id.ExpiresAt, id.AbsoluteExpiresAt, id.SessionID)
		body["bypass"] = false
		writeJSON(w, http.StatusOK, body)
	})

	handle("POST /api/auth/password", Authenticated, func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			CurrentPassword string `json:"currentPassword"`
			NewPassword     string `json:"newPassword"`
		}
		if !decodeJSON(w, req, &body) {
			return
		}
		id := IdentityOf(req.Context())
		res, err := svc.ChangePassword(req.Context(), id, body.CurrentPassword, body.NewPassword, clientMeta(req))
		if err != nil {
			writeAuthError(w, err)
			return
		}
		setSessionCookie(w, d.guard.secure(req), res.Token, res)
		writeJSON(w, http.StatusOK, sessionBody(res.User, res.CSRF, idleExpiry(res.Session), res.Session.ExpiresAt, res.Session.ID))
	})

	handle("GET /api/auth/sessions", Authenticated, func(w http.ResponseWriter, req *http.Request) {
		id := IdentityOf(req.Context())
		list, err := svc.ListSessions(req.Context(), id.ID)
		if err != nil {
			internalError(w)
			return
		}
		out := make([]map[string]any, 0, len(list))
		for _, s := range list {
			out = append(out, sessionJSON(s, id.SessionID))
		}
		writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
	})

	handle("DELETE /api/auth/sessions/{id}", Authenticated, func(w http.ResponseWriter, req *http.Request) {
		id := IdentityOf(req.Context())
		sid := req.PathValue("id")
		if !isID(sid) {
			errorJSON(w, http.StatusNotFound, "not found")
			return
		}
		if err := svc.RevokeSession(req.Context(), id.Actor(clientAddr(req)), id.ID, sid); err != nil {
			writeAuthError(w, err)
			return
		}
		if sid == id.SessionID {
			clearSessionCookie(w, d.guard.secure(req))
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
}

// accountRoutes returns a registrar for routes that need the account service.
// Without one (only tests build such an app) the routes still exist, with their
// levels, and answer 503.
func accountRoutes(r *Router, d deps) func(string, Level, http.HandlerFunc) {
	return func(pattern string, level Level, f http.HandlerFunc) {
		if d.auth == nil {
			f = func(w http.ResponseWriter, _ *http.Request) {
				errorJSON(w, http.StatusServiceUnavailable, "authentication is not available")
			}
		}
		r.HandleFunc(pattern, level, f)
	}
}

// isID reports whether s looks like one of our 32-character hex ids.
func isID(s string) bool {
	if len(s) != 32 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// queryInt reads an optional integer query parameter in [lo, hi].
func queryInt(r *http.Request, name string, def, lo, hi int) (int, bool) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def, true
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < lo || n > hi {
		return 0, false
	}
	return n, true
}
