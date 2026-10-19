package app

import (
	"errors"
	"net/http"

	"github.com/tristanwl/wheelhouse/server/internal/converter"
)

// registerConverterSettings registers the administrator routes for the
// converter settings (issues #31 and #32): the media roots conversion is
// limited to, and whether HDR and Dolby Vision files may be converted. The
// encoder and quality preferences stay at /api/codec-analysis/settings.
func registerConverterSettings(r *Router, d deps) {
	body := func() map[string]any {
		st := d.convSet.State()
		return map[string]any{
			"mediaRoots":     nonNil(st.MediaRoots),
			"includeHdr":     st.IncludeHDR,
			"effectiveRoots": nonNil(st.EffectiveRoots),
			"source":         string(st.Source),
		}
	}

	r.HandleFunc("GET /api/admin/converter", Admin, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, body())
	})

	r.HandleFunc("PUT /api/admin/converter", Admin, func(w http.ResponseWriter, req *http.Request) {
		var in struct {
			MediaRoots *[]string `json:"mediaRoots"`
			IncludeHDR *bool     `json:"includeHdr"`
		}
		if !decodeJSON(w, req, &in) {
			return
		}
		if in.MediaRoots == nil && in.IncludeHDR == nil {
			errorJSON(w, http.StatusBadRequest, "send mediaRoots, includeHdr or both")
			return
		}
		before := d.convSet.State()
		var roots []string
		if in.MediaRoots != nil {
			roots = *in.MediaRoots
			if roots == nil {
				roots = []string{}
			}
		}
		st, err := d.convSet.Update(req.Context(), roots, in.IncludeHDR)
		var ve *converter.ValidationError
		switch {
		case errors.As(err, &ve):
			errorJSON(w, http.StatusBadRequest, ve.Msg)
			return
		case err != nil:
			internalError(w)
			return
		}
		detail := map[string]any{
			"wasSource": string(before.Source), "source": string(st.Source),
			"includeHdr": st.IncludeHDR, "wasIncludeHdr": before.IncludeHDR,
			"rootCount": len(st.EffectiveRoots),
		}
		if len(st.EffectiveRoots) <= 8 {
			detail["roots"] = st.EffectiveRoots // the detail is bounded; the count remains
		}
		d.audit(req, "converter.update", "converter", detail)
		writeJSON(w, http.StatusOK, body())
	})
}
