package app

import (
	"net/http"

	"github.com/tristanwl/wheelhouse/server/internal/auth"
)

// registerUsers registers the administrator routes for accounts, their
// sessions and the audit log. All are Admin: the role is checked by the Guard
// before any of these handlers run, and the acting account is never taken from
// the request.
func registerUsers(r *Router, d deps) {
	svc := d.auth
	handle := accountRoutes(r, d)

	// userID answers 404 for anything that cannot be an id, before any lookup.
	userID := func(w http.ResponseWriter, req *http.Request) (string, bool) {
		id := req.PathValue("id")
		if !isID(id) {
			errorJSON(w, http.StatusNotFound, "not found")
			return "", false
		}
		return id, true
	}
	actor := func(req *http.Request) auth.Actor { return IdentityOf(req.Context()).Actor(clientAddr(req)) }

	handle("GET /api/users", Admin, func(w http.ResponseWriter, req *http.Request) {
		users, err := svc.ListUsers(req.Context())
		if err != nil {
			internalError(w)
			return
		}
		out := make([]map[string]any, 0, len(users))
		for _, u := range users {
			out = append(out, userJSON(u))
		}
		writeJSON(w, http.StatusOK, map[string]any{"users": out})
	})

	handle("POST /api/users", Admin, func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Username           string    `json:"username"`
			Password           string    `json:"password"`
			Role               auth.Role `json:"role"`
			MustChangePassword *bool     `json:"mustChangePassword"`
		}
		if !decodeJSON(w, req, &body) {
			return
		}
		u, err := svc.CreateUser(req.Context(), actor(req), auth.NewUser{
			Username: body.Username, Password: body.Password, Role: body.Role,
			MustChangePassword: body.MustChangePassword == nil || *body.MustChangePassword, // a password an admin chose is temporary
		})
		if err != nil {
			writeAuthError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"user": userJSON(u)})
	})

	handle("PATCH /api/users/{id}", Admin, func(w http.ResponseWriter, req *http.Request) {
		id, ok := userID(w, req)
		if !ok {
			return
		}
		var body struct {
			Username *string    `json:"username"`
			Role     *auth.Role `json:"role"`
			Disabled *bool      `json:"disabled"`
		}
		if !decodeJSON(w, req, &body) {
			return
		}
		u, err := svc.UpdateUser(req.Context(), actor(req), id, auth.UserPatch{Username: body.Username, Role: body.Role, Disabled: body.Disabled})
		if err != nil {
			writeAuthError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"user": userJSON(u)})
	})

	handle("POST /api/users/{id}/password", Admin, func(w http.ResponseWriter, req *http.Request) {
		id, ok := userID(w, req)
		if !ok {
			return
		}
		var body struct {
			Password string `json:"password"`
		}
		if !decodeJSON(w, req, &body) {
			return
		}
		if err := svc.SetPassword(req.Context(), actor(req), id, body.Password); err != nil {
			writeAuthError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})

	handle("DELETE /api/users/{id}", Admin, func(w http.ResponseWriter, req *http.Request) {
		id, ok := userID(w, req)
		if !ok {
			return
		}
		if err := svc.DeleteUser(req.Context(), actor(req), id); err != nil {
			writeAuthError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})

	handle("GET /api/users/{id}/sessions", Admin, func(w http.ResponseWriter, req *http.Request) {
		id, ok := userID(w, req)
		if !ok {
			return
		}
		if _, err := svc.GetUser(req.Context(), id); err != nil {
			writeAuthError(w, err)
			return
		}
		list, err := svc.ListSessions(req.Context(), id)
		if err != nil {
			internalError(w)
			return
		}
		out := make([]map[string]any, 0, len(list))
		for _, s := range list {
			out = append(out, sessionJSON(s, IdentityOf(req.Context()).SessionID))
		}
		writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
	})

	handle("DELETE /api/users/{id}/sessions", Admin, func(w http.ResponseWriter, req *http.Request) {
		id, ok := userID(w, req)
		if !ok {
			return
		}
		n, err := svc.RevokeAllSessions(req.Context(), actor(req), id)
		if err != nil {
			writeAuthError(w, err)
			return
		}
		if id == IdentityOf(req.Context()).ID {
			clearSessionCookie(w, d.guard.secure(req)) // an admin who ended their own sessions ended this one too
		}
		writeJSON(w, http.StatusOK, map[string]int{"revoked": n})
	})

	handle("GET /api/audit", Admin, func(w http.ResponseWriter, req *http.Request) {
		limit, ok1 := queryInt(req, "limit", 50, 1, 200)
		before, ok2 := queryInt(req, "before", 0, 0, 1<<62-1)
		if !ok1 || !ok2 {
			errorJSON(w, http.StatusBadRequest, "invalid limit or before")
			return
		}
		entries, err := svc.ListAudit(req.Context(), limit, int64(before))
		if err != nil {
			internalError(w)
			return
		}
		out := make([]map[string]any, 0, len(entries))
		for _, e := range entries {
			var actorID any
			if e.ActorID != "" {
				actorID = e.ActorID
			}
			out = append(out, map[string]any{
				"id": e.ID, "at": rfc3339(e.At), "actorId": actorID, "actor": e.ActorName,
				"action": e.Action, "target": e.Target, "address": e.Address, "detail": e.Detail,
			})
		}
		var next any
		if len(entries) == limit {
			next = entries[len(entries)-1].ID
		}
		writeJSON(w, http.StatusOK, map[string]any{"entries": out, "nextBefore": next, "retention": retentionView(svc.AuditRetention())})
	})
}

// retentionView is how long audit rows are kept and what the last prune did.
// Read-only: the retention is set in the environment, never through the API.
func retentionView(r auth.AuditRetention) map[string]any {
	out := map[string]any{"days": r.Days, "floorDays": r.FloorDays}
	if !r.Last.At.IsZero() {
		out["lastPrune"] = map[string]any{
			"at": rfc3339(r.Last.At), "deleted": r.Last.Deleted, "cutoff": rfc3339(r.Last.Cutoff), "failed": r.Last.Failed,
		}
	}
	return out
}
