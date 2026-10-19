package app

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/tristanwl/wheelhouse/server/internal/auth"
	"github.com/tristanwl/wheelhouse/server/internal/library"
	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	mm "github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager"
)

// maxBodyBytes bounds the JSON a mutating route reads.
const maxBodyBytes = 1 << 20

func errorJSON(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// resolveManager finds the manager a route was addressed to, or answers the
// error (404 unknown, 502 not ready) and reports false.
func resolveManager(w http.ResponseWriter, r *http.Request, d deps, instanceID string) (mm.Entry, bool) {
	entries, err := mm.ListManagers(r.Context(), d.rt, "")
	if err != nil {
		internalError(w)
		return mm.Entry{}, false
	}
	i := slices.IndexFunc(entries, func(e mm.Entry) bool { return e.InstanceID == instanceID })
	if i < 0 {
		errorJSON(w, http.StatusNotFound, fmt.Sprintf("unknown instance %q", instanceID))
		return mm.Entry{}, false
	}
	return notReady(w, entries[i])
}

func notReady(w http.ResponseWriter, e mm.Entry) (mm.Entry, bool) {
	if e.Client == nil {
		reason := e.Error
		if reason == "" {
			reason = "not ready"
		}
		errorJSON(w, http.StatusBadGateway, core.Redact(e.Name+": "+reason))
		return mm.Entry{}, false
	}
	return e, true
}

// integer parses a path number the way the Next routes did: whole numbers only.
func integer(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	return n, err == nil
}

// confirmed reports whether a destructive request carries the explicit
// confirmation (?confirm=true), and answers 400 when it does not. The Next UI
// has its own confirm dialog; this is the server-side half (issue #41), so a
// stray or scripted DELETE cannot remove files by itself.
func confirmed(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Query().Get("confirm") == "true" {
		return true
	}
	errorJSON(w, http.StatusBadRequest, "confirmation required: send confirm=true to delete the files")
	return false
}

func kindParam(s string) (mm.Kind, bool) {
	switch mm.Kind(s) {
	case mm.KindMovies, mm.KindSeries:
		return mm.Kind(s), true
	}
	return "", false
}

// audit records an action in the audit log with the acting account and the
// client address: never a body, a token or a secret, only ids and names. An app
// built without accounts (tests) falls back to a log line.
func (d deps) audit(r *http.Request, action, target string, detail map[string]any) {
	a := auth.Actor{Name: "system", Addr: clientAddr(r)}
	if id := IdentityOf(r.Context()); id != nil {
		a = id.Actor(clientAddr(r))
	}
	if d.auth != nil {
		d.auth.Record(r.Context(), a, action, target, detail)
		return
	}
	d.log.LogAttrs(r.Context(), slog.LevelInfo, "audit", slog.String("action", action), slog.String("target", target), slog.Any("detail", detail))
}

// registerLibrary registers the library group: library, storage, search, add
// and the per-item routes.
func registerLibrary(r *Router, d deps) {
	r.HandleFunc("GET /api/library", Authenticated, func(w http.ResponseWriter, req *http.Request) {
		entries, err := mm.ListManagers(req.Context(), d.rt, "")
		if err != nil {
			internalError(w)
			return
		}
		writeJSON(w, http.StatusOK, library.Library(req.Context(), entries))
	})

	r.HandleFunc("GET /api/storage", Authenticated, func(w http.ResponseWriter, req *http.Request) {
		entries, err := mm.ListManagers(req.Context(), d.rt, "")
		if err != nil {
			internalError(w)
			return
		}
		writeJSON(w, http.StatusOK, library.Storage(req.Context(), entries))
	})

	r.HandleFunc("GET /api/library/{instanceId}/{id}/details", Authenticated, func(w http.ResponseWriter, req *http.Request) {
		id, ok := integer(req.PathValue("id"))
		if !ok {
			errorJSON(w, http.StatusBadRequest, "invalid id")
			return
		}
		e, ok := resolveManager(w, req, d, req.PathValue("instanceId"))
		if !ok {
			return
		}
		details, err := library.ItemDetails(req.Context(), e.Client, id)
		if err != nil {
			errorJSON(w, http.StatusBadGateway, core.ErrorText(err))
			return
		}
		writeJSON(w, http.StatusOK, details)
	})

	r.HandleFunc("GET /api/library/{instanceId}/{id}/seasons", Authenticated, func(w http.ResponseWriter, req *http.Request) {
		e, ok := resolveManager(w, req, d, req.PathValue("instanceId"))
		if !ok {
			return
		}
		series, isSeries := mm.AsSeries(e.Client)
		if !isSeries {
			errorJSON(w, http.StatusBadRequest, "seasons are only available for series")
			return
		}
		id, ok := integer(req.PathValue("id"))
		if !ok {
			errorJSON(w, http.StatusBadRequest, "invalid id")
			return
		}
		seasons, err := library.Seasons(req.Context(), series, id)
		if err != nil {
			errorJSON(w, http.StatusBadGateway, core.ErrorText(err))
			return
		}
		writeJSON(w, http.StatusOK, seasons)
	})

	// Deletes need ?confirm=true (see confirmed). The id and the instance are
	// checked first so a malformed request keeps its specific answer.
	r.HandleFunc("DELETE /api/library/{instanceId}/{id}", Authenticated, func(w http.ResponseWriter, req *http.Request) {
		id, ok := integer(req.PathValue("id"))
		if !ok {
			errorJSON(w, http.StatusBadRequest, "invalid id")
			return
		}
		e, ok := resolveManager(w, req, d, req.PathValue("instanceId"))
		if !ok || !confirmed(w, req) {
			return
		}
		d.audit(req, "library.delete", e.InstanceID+"/"+strconv.Itoa(id), map[string]any{"instanceId": e.InstanceID, "id": id})
		if err := e.Client.Remove(req.Context(), id); err != nil {
			errorJSON(w, http.StatusBadGateway, core.ErrorText(err))
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})

	r.HandleFunc("DELETE /api/library/{instanceId}/{id}/seasons/{season}", Authenticated, func(w http.ResponseWriter, req *http.Request) {
		e, ok := resolveManager(w, req, d, req.PathValue("instanceId"))
		if !ok {
			return
		}
		series, isSeries := mm.AsSeries(e.Client)
		if !isSeries {
			errorJSON(w, http.StatusBadRequest, "seasons are only available for series")
			return
		}
		id, idOK := integer(req.PathValue("id"))
		season, seasonOK := integer(req.PathValue("season"))
		if !idOK || !seasonOK {
			errorJSON(w, http.StatusBadRequest, "invalid id")
			return
		}
		if !confirmed(w, req) {
			return
		}
		d.audit(req, "library.deleteSeason", e.InstanceID+"/"+strconv.Itoa(id)+"/"+strconv.Itoa(season), map[string]any{"instanceId": e.InstanceID, "id": id, "season": season})
		n, err := series.DeleteSeason(req.Context(), id, season)
		if err != nil {
			errorJSON(w, http.StatusBadGateway, core.ErrorText(err))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "filesDeleted": n})
	})

	r.HandleFunc("GET /api/search", Authenticated, func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		kind, ok := kindParam(q.Get("kind"))
		if !ok {
			errorJSON(w, http.StatusBadRequest, "kind must be movies or series")
			return
		}
		term := strings.TrimSpace(q.Get("term"))
		if term == "" {
			writeJSON(w, http.StatusOK, library.SearchResponse{Results: []library.Hit{}})
			return
		}
		entries, err := mm.ListManagers(req.Context(), d.rt, kind)
		if err != nil {
			internalError(w)
			return
		}
		if len(entries) == 0 {
			errorJSON(w, http.StatusNotFound, mm.NoManagerMessage(kind))
			return
		}
		resp := library.Search(req.Context(), entries, kind, term)
		if len(resp.Results) == 0 && len(resp.Problems) > 0 {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": resp.Problems[0].Error, "problems": resp.Problems})
			return
		}
		writeJSON(w, http.StatusOK, resp)
	})

	// Either ?instanceId=radarr-4k, or ?kind=movies for the first ready instance
	// of that kind.
	r.HandleFunc("GET /api/add-meta", Authenticated, func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		instanceID := q.Get("instanceId")
		kind, _ := kindParam(q.Get("kind"))
		if instanceID == "" && kind == "" {
			errorJSON(w, http.StatusBadRequest, "instanceId or kind is required")
			return
		}
		listKind := kind
		if instanceID != "" {
			listKind = ""
		}
		entries, err := mm.ListManagers(req.Context(), d.rt, listKind)
		if err != nil {
			internalError(w)
			return
		}
		var entry mm.Entry
		found := false
		if instanceID != "" {
			if i := slices.IndexFunc(entries, func(e mm.Entry) bool { return e.InstanceID == instanceID }); i >= 0 {
				entry, found = entries[i], true
			}
		} else if i := slices.IndexFunc(entries, func(e mm.Entry) bool { return e.Client != nil }); i >= 0 {
			entry, found = entries[i], true
		} else if len(entries) > 0 {
			entry, found = entries[0], true
		}
		if !found {
			if instanceID != "" {
				errorJSON(w, http.StatusNotFound, fmt.Sprintf("unknown instance %q", instanceID))
			} else {
				errorJSON(w, http.StatusNotFound, mm.NoManagerMessage(kind))
			}
			return
		}
		entry, ok := notReady(w, entry)
		if !ok {
			return
		}
		meta, err := library.AddMeta(req.Context(), entry)
		if err != nil {
			errorJSON(w, http.StatusBadGateway, core.ErrorText(err))
			return
		}
		writeJSON(w, http.StatusOK, meta)
	})

	r.HandleFunc("POST /api/add", Authenticated, func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			InstanceID          string  `json:"instanceId"`
			ID                  int     `json:"id"` // tvdbId (series) or tmdbId (movies)
			QualityProfileID    int     `json:"qualityProfileId"`
			RootFolderPath      string  `json:"rootFolderPath"`
			Monitored           bool    `json:"monitored"`
			SearchNow           bool    `json:"searchNow"`
			MinimumAvailability *string `json:"minimumAvailability"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, maxBodyBytes)).Decode(&body); err != nil {
			errorJSON(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if body.InstanceID == "" {
			errorJSON(w, http.StatusBadRequest, "instanceId is required")
			return
		}
		if body.ID == 0 || body.QualityProfileID == 0 || body.RootFolderPath == "" {
			errorJSON(w, http.StatusBadRequest, "id, qualityProfileId, and rootFolderPath are required")
			return
		}
		e, ok := resolveManager(w, req, d, body.InstanceID)
		if !ok {
			return
		}
		opts := mm.AddOptions{
			QualityProfileID: body.QualityProfileID, RootFolderPath: body.RootFolderPath,
			Monitored: body.Monitored, SearchNow: body.SearchNow,
		}
		if body.MinimumAvailability != nil {
			opts.MinimumAvailability = *body.MinimumAvailability
		}

		base, err := e.Client.LookupByExternalID(req.Context(), body.ID)
		if err != nil {
			errorJSON(w, http.StatusBadGateway, core.ErrorText(err))
			return
		}
		if base == nil {
			msg := "movie not found"
			if e.Kind == mm.KindSeries {
				msg = "series not found"
			}
			errorJSON(w, http.StatusNotFound, msg)
			return
		}
		payload, err := base.MovieAddPayload(opts)
		if e.Kind == mm.KindSeries {
			payload, err = base.SeriesAddPayload(opts)
		}
		if err != nil {
			internalError(w)
			return
		}
		created, err := e.Client.Add(req.Context(), payload)
		if err != nil {
			errorJSON(w, http.StatusBadGateway, core.ErrorText(err))
			return
		}
		newID := 0
		if created.ID != nil {
			newID = *created.ID
		}
		d.audit(req, "library.add", body.InstanceID+"/"+strconv.Itoa(newID), map[string]any{"instanceId": body.InstanceID, "id": newID, "externalId": body.ID})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": created.ID, "title": created.Title})
	})
}
