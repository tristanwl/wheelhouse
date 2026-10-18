package core

import (
	"context"
	"log/slog"
)

// Role is what a provider does for Wheelhouse. Features declare which roles they need.
type Role string

// The four roles.
const (
	RoleDownloadClient Role = "downloadClient"
	RoleMediaManager   Role = "mediaManager"
	RoleMediaServer    Role = "mediaServer"
	RoleMetadata       Role = "metadata"
)

// Valid reports whether r is one of the four roles.
func (r Role) Valid() bool {
	switch r {
	case RoleDownloadClient, RoleMediaManager, RoleMediaServer, RoleMetadata:
		return true
	}
	return false
}

// Instance is one configured copy of a provider (for example two Radarr
// instances: HD and 4K).
type Instance struct {
	// ID is stable and URL-safe: "sonarr", "radarr-4k". It appears in API routes.
	ID         string `json:"id"`
	ProviderID string `json:"providerId"`
	// Name is the display name.
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	// Config is the raw, unvalidated settings, checked against the provider's
	// config struct when the instance is resolved. It holds secrets, so it is
	// excluded from JSON, from fmt verbs and from slog output.
	Config map[string]any `json:"-"`
}

// String omits Config so `%v` and `%+v` cannot print secrets.
func (i Instance) String() string {
	return "Instance(" + i.ID + ", provider " + i.ProviderID + ")"
}

// GoString omits Config so `%#v` cannot print secrets.
func (i Instance) GoString() string { return i.String() }

// LogValue omits Config so slog cannot record secrets.
func (i Instance) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("id", i.ID),
		slog.String("provider", i.ProviderID),
		slog.Bool("enabled", i.Enabled),
	)
}

// ConnectionResult is what a client's connection test reports.
type ConnectionResult struct {
	OK      bool   `json:"ok"`
	Version string `json:"version,omitempty"`
	// Error is already redacted: safe to show in the UI and logs.
	Error string `json:"error,omitempty"`
}

// ProviderContext is handed to a provider's Create function.
type ProviderContext struct {
	InstanceID   string
	InstanceName string
}

// ConfigSource is where instances come from: the environment today, a
// database later.
type ConfigSource interface {
	Instances(ctx context.Context) ([]Instance, error)
}
