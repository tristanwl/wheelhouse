// Package metadata is the metadata role. TMDB is the only metadata provider, so
// the role has no rich interface: Explore's categories (now playing, physical
// releases) are TMDB-specific, and an interface now would be invented. It is
// still a provider so it shows up in config, connection tests and feature
// gating. Revisit when TVDB is on the table. The provider is in the tmdb
// subpackage.
package metadata

import (
	"context"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
)

// Capabilities are static, per provider. TMDB has none (the TypeScript type is
// Record<string, never>), so it marshals as {}.
type Capabilities struct{}

// Client is the part of a metadata provider every one has.
type Client interface {
	// TestConnection never returns an error: failures are in the result.
	TestConnection(ctx context.Context) core.ConnectionResult
}
