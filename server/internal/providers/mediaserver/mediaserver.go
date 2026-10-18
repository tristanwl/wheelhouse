// Package mediaserver is the media-server role: the normalized status every
// media server (Plex, Jellyfin) reports and the client interface behind it. It
// is the Go port of lib/providers/media-server/types.ts and contract.ts. The
// providers live in the plex and jellyfin subpackages.
package mediaserver

import (
	"context"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
)

// State is whether the server itself is ready to use.
type State string

// The states a media server can be in.
const (
	// StateOK: the server is fine. It may still not be fully readable
	// (Authenticated false) when credentials are missing or were rejected.
	StateOK State = "ok"
	// StateNeedsSetup: the server itself is not ready (an unclaimed Plex, a
	// Jellyfin whose setup wizard has not run).
	StateNeedsSetup State = "needs-setup"
)

// Status is the normalized state of a media server. The JSON names match the
// TypeScript MediaServerStatus the UI consumes.
type Status struct {
	State State `json:"state"`
	// Notice is something the user should know or do, in plain words. It never
	// mentions configuration key names or environment variables.
	Notice  string `json:"notice,omitempty"`
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
	// ActiveSessions counts sessions actually playing something (not merely
	// connected clients). Nil when the server cannot say (no credentials, or
	// they were rejected).
	ActiveSessions *int `json:"activeSessions,omitempty"`
	// Authenticated says whether Wheelhouse is authenticated to the server, so
	// session data is available.
	Authenticated bool `json:"authenticated"`
}

// Capabilities are static, per provider.
type Capabilities struct {
	Sessions bool `json:"sessions"`
}

// Client is what the Plex and Jellyfin providers create.
type Client interface {
	// TestConnection never returns an error: failures are in the result.
	TestConnection(ctx context.Context) core.ConnectionResult
	// Status returns a *core.ProviderError when the server cannot be reached
	// at all. A server that answers but refuses our credentials is a normal
	// Status with Authenticated false and a Notice.
	Status(ctx context.Context) (Status, error)
}

// Ptr returns a pointer to n, for Status.ActiveSessions.
func Ptr(n int) *int { return &n }
