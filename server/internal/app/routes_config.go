package app

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/features"
	"github.com/tristanwl/wheelhouse/server/internal/instances"
	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
)

// The stored-configuration API: provider instances, the provider registry that
// drives the admin forms, connection tests and feature toggles. Every route is
// Admin. Instance URLs are typed by an administrator and the server requests
// them (a connection test, then every poll), so they are trusted input; the one
// thing refused is link-local and cloud-metadata addresses (core/netpolicy.go).
// A secret setting is accepted on create and update and never returned: the
// responses only say whether it is set.

// disabledFeatures is the set of features an administrator switched off; empty
// when there is no stored configuration.
func (d deps) disabledFeatures(ctx context.Context) (map[string]bool, error) {
	if d.cfg == nil {
		return nil, nil
	}
	return d.cfg.DisabledFeatures(ctx)
}

// featureOfPattern is the feature whose data a route serves, or "". Routes the
// features share (add, search, add-meta, storage) are not gated: another
// feature uses them.
func featureOfPattern(pattern string) string {
	_, path, ok := strings.Cut(pattern, " ")
	if !ok {
		path = pattern
	}
	under := func(prefix string) bool { return path == prefix || strings.HasPrefix(path, prefix+"/") }
	switch {
	case under("/api/library"):
		return "library"
	case under("/api/explore"):
		return "explore"
	case under("/api/codec-analysis"):
		return "codecs"
	}
	return ""
}

// featureGate refuses the API of a feature an administrator switched off. It
// runs inside the guard, so it only ever answers signed-in callers. The toggle
// is a convenience for the people the admin manages, enforced here so a switched
// off Codecs page cannot be driven by hand; it is not a security boundary
// between roles.
func (d deps) featureGate(pattern string, next http.Handler) http.Handler {
	feature := featureOfPattern(pattern)
	if feature == "" || d.cfg == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		disabled, err := d.cfg.DisabledFeatures(r.Context())
		if err != nil {
			internalError(w)
			return
		}
		if disabled[feature] {
			errorJSON(w, http.StatusForbidden, "this feature is "+features.DisabledReason)
			return
		}
		next.ServeHTTP(w, r)
	})
}

var instanceIDPattern = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

type lastTest struct {
	OK    bool
	Error string
	At    time.Time
}

type configAPI struct {
	d    deps
	reg  *core.Registry
	mu   sync.Mutex
	last map[string]lastTest // by instance id: the last connection test of a saved instance
}

func registerConfig(r *Router, d deps) {
	a := &configAPI{d: d, last: map[string]lastTest{}}
	handle := func(pattern string, f http.HandlerFunc) {
		if d.cfg == nil {
			f = func(w http.ResponseWriter, _ *http.Request) {
				errorJSON(w, http.StatusServiceUnavailable, "stored configuration is not available")
			}
		} else {
			a.reg = d.cfg.Registry()
		}
		r.HandleFunc(pattern, Admin, f)
	}

	handle("GET /api/admin/providers", a.providers)
	handle("GET /api/admin/instances", a.list)
	handle("POST /api/admin/instances", a.create)
	handle("POST /api/admin/instances/test", a.test)
	handle("PUT /api/admin/instances/{id}", a.update)
	handle("DELETE /api/admin/instances/{id}", a.delete)
	handle("POST /api/admin/instances/{id}/import", a.importEnv)
	handle("GET /api/admin/features", a.getFeatures)
	handle("PUT /api/admin/features", a.putFeatures)
}

func (a *configAPI) audit(req *http.Request, action, target string, detail map[string]any) {
	if a.d.auth == nil {
		return
	}
	a.d.auth.Record(req.Context(), IdentityOf(req.Context()).Actor(clientAddr(req)), action, target, detail)
}

// writeConfigError answers an error from the stored configuration. Validation
// problems name the setting and what is wrong, never a value.
func (a *configAPI) writeConfigError(w http.ResponseWriter, err error) {
	var ve *core.ValidationError
	var ie *instances.InvalidError
	switch {
	case errors.As(err, &ve):
		fields := make([]map[string]string, 0, len(ve.Errors))
		for _, fe := range ve.Errors {
			// "field" is the key the form maps the problem to; the text is human.
			fields = append(fields, map[string]string{"field": fe.Field, "message": fe.Text()})
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid configuration", "fields": fields})
	case errors.As(err, &ie):
		errorJSON(w, http.StatusBadRequest, ie.Msg)
	case errors.Is(err, instances.ErrNotFound):
		errorJSON(w, http.StatusNotFound, "not found")
	case errors.Is(err, instances.ErrKeyChanged):
		a.d.log.Error("a configuration change was refused: the secret key in the database changed while Wheelhouse was running; restart Wheelhouse")
		errorJSON(w, http.StatusServiceUnavailable, "The secret key was changed while Wheelhouse was running, so credentials cannot be saved safely. Restart Wheelhouse, then try again.")
	case errors.Is(err, instances.ErrExists), errors.Is(err, instances.ErrEnvironmentID), errors.Is(err, instances.ErrReadOnly):
		errorJSON(w, http.StatusConflict, err.Error())
	default:
		a.d.log.Error("stored configuration", "error", core.Redact(err.Error()))
		internalError(w)
	}
}

func instanceID(w http.ResponseWriter, req *http.Request) (string, bool) {
	id := req.PathValue("id")
	if !instanceIDPattern.MatchString(id) {
		errorJSON(w, http.StatusNotFound, "not found")
		return "", false
	}
	return id, true
}

// ---- providers ----

func (a *configAPI) providers(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"providers": a.reg.All()})
}

// ---- instances ----

// displayValue is a non-secret setting as the admin sees it. A URL never shows
// user information: the server refuses it on input, but an environment value is
// not ours to vet and must not leak through this response.
func displayValue(f core.Field, v any) any {
	if f.Format == "url" {
		if s, ok := v.(string); ok {
			if u, err := url.Parse(s); err == nil {
				u.User = nil
				return u.String()
			}
			return ""
		}
	}
	return v
}

func (a *configAPI) view(e instances.Entry, resolved map[string]core.ResolvedInstance) map[string]any {
	res := resolved[e.ID]
	cfg := map[string]any{}
	secrets := map[string]any{}
	role := any(nil)
	if def, ok := a.reg.Get(e.ProviderID); ok {
		role = def.Role
		for _, f := range def.Fields() {
			v, present := e.Config[f.Name]
			if f.Secret {
				secrets[f.Name] = map[string]bool{"set": present && v != nil && v != ""}
			} else if present {
				cfg[f.Name] = displayValue(f, v)
			}
		}
	}
	out := map[string]any{
		"id": e.ID, "providerId": e.ProviderID, "role": role, "name": e.Name, "enabled": e.Enabled,
		"source": string(e.Origin), "readOnly": e.Origin == instances.OriginEnvironment,
		"overridesEnvironment": e.OverridesEnvironment,
		"config":               cfg, "secrets": secrets,
		"status": string(res.Status),
	}
	if res.Status == "" {
		out["status"] = string(core.StatusInvalidConfig)
	}
	if res.Error != "" {
		out["error"] = res.Error
	}
	a.mu.Lock()
	lt, ok := a.last[e.ID]
	a.mu.Unlock()
	if ok {
		t := map[string]any{"ok": lt.OK, "at": rfc3339(lt.At)}
		if lt.Error != "" {
			t["error"] = lt.Error
		}
		out["lastTest"] = t
	}
	return out
}

func (a *configAPI) resolved(ctx context.Context) (map[string]core.ResolvedInstance, error) {
	list, err := a.d.rt.ListInstances(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[string]core.ResolvedInstance, len(list))
	for _, r := range list {
		m[r.Instance.ID] = r
	}
	return m, nil
}

func (a *configAPI) list(w http.ResponseWriter, req *http.Request) {
	entries, err := a.d.cfg.Entries(req.Context())
	if err != nil {
		a.writeConfigError(w, err)
		return
	}
	res, err := a.resolved(req.Context())
	if err != nil {
		a.writeConfigError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		out = append(out, a.view(e, res))
	}
	writeJSON(w, http.StatusOK, map[string]any{"instances": out})
}

func (a *configAPI) respond(w http.ResponseWriter, req *http.Request, status int, e instances.Entry) {
	res, err := a.resolved(req.Context())
	if err != nil {
		a.writeConfigError(w, err)
		return
	}
	writeJSON(w, status, map[string]any{"instance": a.view(e, res)})
}

func (a *configAPI) forgetTest(id string) {
	a.mu.Lock()
	delete(a.last, id)
	a.mu.Unlock()
}

func (a *configAPI) create(w http.ResponseWriter, req *http.Request) {
	var body struct {
		ProviderID string         `json:"providerId"`
		ID         string         `json:"id"`
		Name       string         `json:"name"`
		Enabled    *bool          `json:"enabled"`
		Config     map[string]any `json:"config"`
	}
	if !decodeJSON(w, req, &body) {
		return
	}
	e, err := a.d.cfg.Create(req.Context(), instances.NewInstance{
		ProviderID: body.ProviderID, ID: body.ID, Name: body.Name, Enabled: body.Enabled, Config: body.Config,
	})
	if err != nil {
		a.writeConfigError(w, err)
		return
	}
	a.forgetTest(e.ID)
	a.audit(req, "instance.create", e.ID, map[string]any{"provider": e.ProviderID, "name": e.Name, "enabled": e.Enabled})
	a.respond(w, req, http.StatusCreated, e)
}

func (a *configAPI) update(w http.ResponseWriter, req *http.Request) {
	id, ok := instanceID(w, req)
	if !ok {
		return
	}
	var body struct {
		Name    *string        `json:"name"`
		Enabled *bool          `json:"enabled"`
		Config  map[string]any `json:"config"`
	}
	if !decodeJSON(w, req, &body) {
		return
	}
	e, change, err := a.d.cfg.Update(req.Context(), id, instances.Patch{Name: body.Name, Enabled: body.Enabled, Config: body.Config})
	if err != nil {
		a.writeConfigError(w, err)
		return
	}
	if !change.Empty() {
		a.forgetTest(id)
		detail := map[string]any{"provider": e.ProviderID}
		if change.Name {
			detail["name"] = e.Name
		}
		if change.Enabled {
			detail["enabled"] = e.Enabled
		}
		// Settings are recorded by their label ("API key"), never their key, and
		// secrets by name only, so the log reads the same as the form.
		def, known := a.reg.Get(e.ProviderID)
		if len(change.Fields) > 0 {
			detail["fields"] = labelsOf(def, known, change.Fields)
		}
		if len(change.Secrets) > 0 {
			detail["secrets"] = labelsOf(def, known, change.Secrets)
		}
		a.audit(req, "instance.update", id, detail)
	}
	a.respond(w, req, http.StatusOK, e)
}

func (a *configAPI) delete(w http.ResponseWriter, req *http.Request) {
	id, ok := instanceID(w, req)
	if !ok {
		return
	}
	prev, _ := a.d.cfg.Get(req.Context(), id)
	reverts, err := a.d.cfg.Delete(req.Context(), id)
	if err != nil {
		a.writeConfigError(w, err)
		return
	}
	a.forgetTest(id)
	a.audit(req, "instance.delete", id, map[string]any{"provider": prev.ProviderID, "environmentAgain": reverts})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "environmentAgain": reverts})
}

func (a *configAPI) importEnv(w http.ResponseWriter, req *http.Request) {
	id, ok := instanceID(w, req)
	if !ok {
		return
	}
	e, err := a.d.cfg.Import(req.Context(), id)
	if err != nil {
		a.writeConfigError(w, err)
		return
	}
	a.forgetTest(id)
	a.audit(req, "instance.import", id, map[string]any{"provider": e.ProviderID})
	a.respond(w, req, http.StatusCreated, e)
}

// test runs the provider's connection test on unsaved settings, or on a saved
// instance (optionally with edited settings, a secret left out being filled from
// the stored one). The answer is the redacted result and nothing else.
func (a *configAPI) test(w http.ResponseWriter, req *http.Request) {
	var body struct {
		ID         string         `json:"id"`
		ProviderID string         `json:"providerId"`
		Config     map[string]any `json:"config"`
	}
	if !decodeJSON(w, req, &body) {
		return
	}
	var (
		providerID = body.ProviderID
		stored     map[string]any
		name       = "Connection test"
		saved      bool
	)
	if body.ID != "" {
		if !instanceIDPattern.MatchString(body.ID) {
			errorJSON(w, http.StatusNotFound, "not found")
			return
		}
		e, err := a.d.cfg.Get(req.Context(), body.ID)
		if err != nil {
			a.writeConfigError(w, err)
			return
		}
		if providerID != "" && providerID != e.ProviderID {
			errorJSON(w, http.StatusBadRequest, "providerId does not match the instance")
			return
		}
		providerID, stored, name, saved = e.ProviderID, e.Config, e.Name, true
	}
	def, ok := a.reg.Get(providerID)
	if !ok {
		errorJSON(w, http.StatusBadRequest, "unknown provider")
		return
	}
	raw := stored
	if body.Config != nil || !saved {
		var err error
		if raw, err = instances.Merge(def, body.Config, stored); err != nil {
			a.writeConfigError(w, err)
			return
		}
	}
	instID := body.ID
	if instID == "" {
		instID = "test"
	}
	start := time.Now()
	res, err := def.TestConnection(req.Context(), instID, name, raw)
	if err != nil {
		a.writeConfigError(w, err)
		return
	}
	// Redact against every secret in play, plus any URL credential, whatever the
	// provider put in its text.
	res.Error = core.Redact(res.Error, secretValues(def, raw)...)
	if saved && body.Config == nil {
		a.mu.Lock()
		a.last[body.ID] = lastTest{OK: res.OK, Error: res.Error, At: time.Now()}
		a.mu.Unlock()
	}
	a.audit(req, "instance.test", func() string {
		if saved {
			return body.ID
		}
		return providerID
	}(), map[string]any{"ok": res.OK, "saved": saved})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": res.OK, "version": res.Version, "error": res.Error,
		"durationMs": time.Since(start).Milliseconds(),
	})
}

func secretValues(def *core.Definition, raw map[string]any) []string {
	var out []string
	for _, k := range def.SecretKeys() {
		if s, ok := raw[k].(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// ---- features ----

func (a *configAPI) featureList(ctx context.Context) ([]map[string]any, map[string]bool, error) {
	disabled, err := a.d.cfg.DisabledFeatures(ctx)
	if err != nil {
		return nil, nil, err
	}
	out := make([]map[string]any, 0, len(features.Definitions))
	for _, f := range features.Definitions {
		out = append(out, map[string]any{
			"id": f.ID, "label": f.Label, "route": f.Route,
			"enabled": !disabled[f.ID] || !features.CanDisable(f.ID), "canDisable": features.CanDisable(f.ID),
		})
	}
	return out, disabled, nil
}

func (a *configAPI) getFeatures(w http.ResponseWriter, req *http.Request) {
	list, _, err := a.featureList(req.Context())
	if err != nil {
		a.writeConfigError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"features": list})
}

func (a *configAPI) putFeatures(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Features map[string]bool `json:"features"`
	}
	if !decodeJSON(w, req, &body) {
		return
	}
	if len(body.Features) == 0 {
		errorJSON(w, http.StatusBadRequest, "features must list at least one feature")
		return
	}
	known := map[string]bool{}
	for _, f := range features.Definitions {
		known[f.ID] = true
	}
	for id, on := range body.Features {
		switch {
		case !known[id]:
			errorJSON(w, http.StatusBadRequest, "unknown feature")
			return
		case !on && !features.CanDisable(id):
			errorJSON(w, http.StatusBadRequest, "the Dashboard cannot be disabled")
			return
		}
	}
	_, before, err := a.featureList(req.Context())
	if err != nil {
		a.writeConfigError(w, err)
		return
	}
	if err := a.d.cfg.SetFeatures(req.Context(), body.Features); err != nil {
		a.writeConfigError(w, err)
		return
	}
	ids := make([]string, 0, len(body.Features))
	for id := range body.Features {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		if on := body.Features[id]; on == before[id] { // before holds the disabled ones
			a.audit(req, "feature.toggle", id, map[string]any{"enabled": on})
		}
	}
	list, _, err := a.featureList(req.Context())
	if err != nil {
		a.writeConfigError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"features": list})
}

// labelsOf is the labels of setting keys, for text people read.
func labelsOf(def *core.Definition, known bool, keys []string) []string {
	if !known {
		return []string{"settings"}
	}
	return def.Labels(keys)
}
