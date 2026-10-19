package app

import "net/http"

// registerUserSessions registers the one route the administration pages need
// beyond the users API: ending a single session of another account (the users
// API can list a user's sessions and end all of them). Administrator only; the
// session must belong to the user in the path, so a mismatched pair is a 404.
func registerUserSessions(r *Router, d deps) {
	handle := accountRoutes(r, d)
	handle("DELETE /api/users/{id}/sessions/{sessionId}", Admin, func(w http.ResponseWriter, req *http.Request) {
		id, sid := req.PathValue("id"), req.PathValue("sessionId")
		if !isID(id) || !isID(sid) {
			errorJSON(w, http.StatusNotFound, "not found")
			return
		}
		ident := IdentityOf(req.Context())
		if err := d.auth.RevokeSession(req.Context(), ident.Actor(clientAddr(req)), id, sid); err != nil {
			writeAuthError(w, err)
			return
		}
		if sid == ident.SessionID {
			clearSessionCookie(w, d.guard.secure(req)) // an administrator who ended this very session signed out
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
}
