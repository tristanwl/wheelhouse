package app

import (
	"errors"
	"net/http"

	"github.com/tristanwl/wheelhouse/server/internal/netaccess"
)

// registerNetwork registers the administrator routes for the local-network
// bypass and the trusted proxies. The bypass is configuration, so only a real
// administrator session reaches them (the bypass principal is never admin).
func registerNetwork(r *Router, d deps) {
	body := func(req *http.Request) map[string]any {
		st := d.net.Settings()
		c := ClientOf(req.Context())
		return map[string]any{
			"enabled":           st.Enabled,
			"cidrs":             netaccess.Strings(st.Bypass),
			"trustedProxies":    netaccess.Strings(st.Proxies),
			"envTrustedProxies": netaccess.Strings(d.net.EnvProxies()),
			"warnings":          nonNil(netaccess.Warnings(st)),
			"client": map[string]any{
				"address": c.String(), "forwarded": c.Forwarded, "inRange": d.net.Bypasses(c),
			},
		}
	}

	r.HandleFunc("GET /api/admin/network", Admin, func(w http.ResponseWriter, req *http.Request) {
		writeJSON(w, http.StatusOK, body(req))
	})

	r.HandleFunc("PUT /api/admin/network", Admin, func(w http.ResponseWriter, req *http.Request) {
		var in struct {
			Enabled        *bool    `json:"enabled"`
			CIDRs          []string `json:"cidrs"`
			TrustedProxies []string `json:"trustedProxies"`
		}
		if !decodeJSON(w, req, &in) {
			return
		}
		if in.Enabled == nil {
			errorJSON(w, http.StatusBadRequest, "enabled is required")
			return
		}
		before := d.net.Settings()
		st, _, err := d.net.Update(req.Context(), netaccess.Input{Enabled: *in.Enabled, CIDRs: in.CIDRs, TrustedProxies: in.TrustedProxies})
		var ve *netaccess.ValidationError
		switch {
		case errors.As(err, &ve):
			errorJSON(w, http.StatusBadRequest, ve.Msg)
			return
		case err != nil:
			internalError(w)
			return
		}
		detail := map[string]any{
			"wasEnabled": before.Enabled, "enabled": st.Enabled,
			"cidrCount": len(st.Bypass), "trustedProxyCount": len(st.Proxies),
			"cidrs": netaccess.Strings(st.Bypass), "trustedProxies": netaccess.Strings(st.Proxies),
		}
		if len(st.Bypass)+len(st.Proxies) > 40 {
			delete(detail, "cidrs") // the audit detail is bounded; the counts remain
			delete(detail, "trustedProxies")
		}
		d.audit(req, "network.update", "network", detail)
		writeJSON(w, http.StatusOK, body(req))
	})
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
