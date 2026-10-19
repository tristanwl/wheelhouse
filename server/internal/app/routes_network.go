package app

import (
	"errors"
	"net/http"
	"slices"

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
			"allowedHosts":      nonNil(st.AllowedHosts),
			"envAllowedHosts":   nonNil(d.net.EnvHosts()),
			"warnings":          nonNil(netaccess.Warnings(st)),
			"client": map[string]any{
				"address": c.String(), "forwarded": c.Forwarded, "inRange": d.net.Bypasses(c),
				"host": req.Host, "hostAllowed": d.net.HostAllowed(req.Host),
			},
		}
	}

	r.HandleFunc("GET /api/admin/network", Admin, func(w http.ResponseWriter, req *http.Request) {
		writeJSON(w, http.StatusOK, body(req))
	})

	r.HandleFunc("PUT /api/admin/network", Admin, func(w http.ResponseWriter, req *http.Request) {
		var in struct {
			Enabled        *bool     `json:"enabled"`
			CIDRs          []string  `json:"cidrs"`
			TrustedProxies []string  `json:"trustedProxies"`
			AllowedHosts   *[]string `json:"allowedHosts"`
		}
		if !decodeJSON(w, req, &in) {
			return
		}
		if in.Enabled == nil {
			errorJSON(w, http.StatusBadRequest, "enabled is required")
			return
		}
		before := d.net.Settings()
		// Turning the bypass on from a host name that is not otherwise allowed
		// adds that very Host to the stored list, so the page the administrator
		// is looking at keeps working; the response says so (addedHost).
		addedHost := ""
		hosts := in.AllowedHosts
		if *in.Enabled && !before.Enabled && !d.net.HostAllowed(req.Host) {
			if h, ok := netaccess.NormalizeHost(req.Host); ok {
				if entry, err := netaccess.ParseHostEntry(h); err == nil {
					list := append([]string(nil), before.AllowedHosts...)
					if hosts != nil {
						list = append([]string(nil), *hosts...)
					}
					if !slices.Contains(list, entry) && len(list) < netaccess.MaxHosts {
						list = append(list, entry)
						hosts, addedHost = &list, entry
					}
				}
			}
		}
		st, _, err := d.net.Update(req.Context(), netaccess.Input{Enabled: *in.Enabled, CIDRs: in.CIDRs, TrustedProxies: in.TrustedProxies, AllowedHosts: hosts})
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
			"allowedHosts": st.AllowedHosts, "addedHost": addedHost,
		}
		if len(st.Bypass)+len(st.Proxies) > 40 {
			delete(detail, "cidrs") // the audit detail is bounded; the counts remain
			delete(detail, "trustedProxies")
			delete(detail, "allowedHosts")
		}
		d.audit(req, "network.update", "network", detail)
		out := body(req)
		out["addedHost"] = addedHost
		writeJSON(w, http.StatusOK, out)
	})
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
