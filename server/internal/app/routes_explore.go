package app

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/tristanwl/wheelhouse/server/internal/explore"
	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
)

// number parses like JavaScript's Number(): surrounding space is ignored, empty
// is 0, anything unparsable or non-finite is not ok.
func number(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, true
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return 0, false
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

// wholeNumber is Number.isInteger (and safe to hold in an int).
func wholeNumber(f float64) bool { return f == math.Trunc(f) && math.Abs(f) <= 1<<53 }

// param is searchParams.get(name) ?? def: only an absent parameter is
// defaulted, an empty one is not.
func param(req *http.Request, name, def string) string {
	q := req.URL.Query()
	if !q.Has(name) {
		return def
	}
	return q.Get(name)
}

// registerExplore registers the Explore routes (TMDB lists and details).
func registerExplore(r *Router, d deps) {
	r.HandleFunc("GET /api/explore", Authenticated, func(w http.ResponseWriter, req *http.Request) {
		cat, ok := explore.ParseCategory(req.URL.Query().Get("category"))
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "category must be one of: theaters, bluray, popular"})
			return
		}
		limit := 20
		if l, ok := number(param(req, "limit", "20")); ok {
			limit = int(math.Min(math.Max(l, 1), 60))
		}
		page := 1
		if p, ok := number(param(req, "page", "1")); ok && wholeNumber(p) && p >= 1 && p <= math.MaxInt32 {
			page = int(p)
		}
		list, err := explore.Browse(req.Context(), d.rt, cat, limit, page)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": core.ErrorText(err)})
			return
		}
		writeJSON(w, http.StatusOK, list)
	})

	r.HandleFunc("GET /api/explore/{id}", Authenticated, func(w http.ResponseWriter, req *http.Request) {
		f, ok := number(req.PathValue("id"))
		if !ok || !wholeNumber(f) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}
		detail, err := explore.Movie(req.Context(), d.rt, int(f))
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": core.ErrorText(err)})
			return
		}
		writeJSON(w, http.StatusOK, detail)
	})
}
