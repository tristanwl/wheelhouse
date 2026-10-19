package core

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
)

// Definition describes a provider: how to validate its config and how to build
// a client for it. Capabilities are static, per provider (not per instance), so
// the UI can decide what to render without calling the service.
type Definition struct {
	// ID is unique across all providers: "sonarr", "sabnzbd", "qbittorrent", ...
	ID          string
	Role        Role
	DisplayName string
	// Config is a zero value of the provider's config struct (see config.go).
	// It drives validation, secret masking and the JSON Schema.
	Config any
	// Capabilities is whatever static description the role defines.
	Capabilities any
	// Create builds a client from a config that already passed validation: the
	// value is the same struct type as Config.
	Create func(config any, ctx ProviderContext) any

	spec *configSpec
}

// Fields lists the provider's settings in declaration order.
func (d *Definition) Fields() []Field { return slices.Clone(d.spec.fields) }

// Label is the name people see for the setting with this key ("API key" for
// "apiKey"), or "a setting" for a key the provider does not have. Text shown to
// people (notices, form errors, audit entries) uses it, never the key.
func (d *Definition) Label(key string) string {
	for _, f := range d.spec.fields {
		if f.Name == key {
			return f.Label
		}
	}
	return "a setting"
}

// Labels maps keys to Label, keeping the order.
func (d *Definition) Labels(keys []string) []string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = d.Label(k)
	}
	return out
}

// SecretKeys lists the settings that hold credentials.
func (d *Definition) SecretKeys() []string {
	var keys []string
	for _, f := range d.spec.fields {
		if f.Secret {
			keys = append(keys, f.Name)
		}
	}
	return keys
}

// Decode validates raw settings and returns the typed config. The error, if
// any, is a *ValidationError and never contains a setting's value.
func (d *Definition) Decode(raw map[string]any) (any, error) { return d.spec.decode(raw) }

// Schema describes the config as JSON Schema for the admin UI.
func (d *Definition) Schema() Schema { return buildSchema(d.DisplayName, d.spec.fields) }

// MarshalJSON gives the provider's public description: no config values, no
// factory. The UI renders its forms from "configSchema".
func (d *Definition) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ID           string   `json:"id"`
		Role         Role     `json:"role"`
		DisplayName  string   `json:"displayName"`
		ConfigSchema Schema   `json:"configSchema"`
		SecretKeys   []string `json:"secretKeys"`
		Capabilities any      `json:"capabilities"`
	}{d.ID, d.Role, d.DisplayName, d.Schema(), d.SecretKeys(), d.Capabilities})
}

var providerIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Registry holds the known providers. It is immutable once built, so it is
// safe for concurrent use.
type Registry struct {
	ordered []*Definition
	byID    map[string]*Definition
}

// NewRegistry rejects duplicate ids and malformed definitions.
func NewRegistry(definitions ...Definition) (*Registry, error) {
	r := &Registry{byID: map[string]*Definition{}}
	for _, def := range definitions {
		if !providerIDPattern.MatchString(def.ID) {
			return nil, fmt.Errorf("invalid provider id %q", def.ID)
		}
		if _, dup := r.byID[def.ID]; dup {
			return nil, fmt.Errorf("duplicate provider id: %s", def.ID)
		}
		if !def.Role.Valid() {
			return nil, fmt.Errorf("provider %s: invalid role %q", def.ID, def.Role)
		}
		if def.Create == nil {
			return nil, fmt.Errorf("provider %s: Create is required", def.ID)
		}
		spec, err := newConfigSpec(def.Config)
		if err != nil {
			return nil, fmt.Errorf("provider %s: %w", def.ID, err)
		}
		def.spec = spec
		d := def
		r.ordered = append(r.ordered, &d)
		r.byID[def.ID] = &d
	}
	return r, nil
}

// All returns every provider in registration order.
func (r *Registry) All() []*Definition { return slices.Clone(r.ordered) }

// Get looks a provider up by id.
func (r *Registry) Get(id string) (*Definition, bool) {
	d, ok := r.byID[id]
	return d, ok
}

// ByRole returns the providers of one role in registration order.
func (r *Registry) ByRole(role Role) []*Definition {
	var out []*Definition
	for _, d := range r.ordered {
		if d.Role == role {
			out = append(out, d)
		}
	}
	return out
}
