// Package all is the one place that knows every provider. It builds the
// registry from each provider's Definition and wires it to a config source, so
// the app depends on this package and not on the providers one by one.
package all

import (
	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	"github.com/tristanwl/wheelhouse/server/internal/providers/downloadclient/qbittorrent"
	"github.com/tristanwl/wheelhouse/server/internal/providers/downloadclient/sabnzbd"
	"github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager/radarr"
	"github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager/sonarr"
	"github.com/tristanwl/wheelhouse/server/internal/providers/mediaserver/jellyfin"
	"github.com/tristanwl/wheelhouse/server/internal/providers/mediaserver/plex"
	"github.com/tristanwl/wheelhouse/server/internal/providers/metadata/tmdb"
)

// Definitions lists every provider, in the order of the TypeScript registry.
func Definitions() []core.Definition {
	return []core.Definition{
		sonarr.Definition(),
		radarr.Definition(),
		sabnzbd.Definition(),
		qbittorrent.Definition(),
		plex.Definition(),
		jellyfin.Definition(),
		tmdb.Definition(),
	}
}

// Registry builds the registry of every provider.
func Registry() (*core.Registry, error) { return core.NewRegistry(Definitions()...) }

// NewRuntime builds the runtime over every provider and the given config
// source. Production passes core.NewProcessEnvConfigSource(); tests pass a
// source of their own.
func NewRuntime(source core.ConfigSource) (*core.Runtime, error) {
	reg, err := Registry()
	if err != nil {
		return nil, err
	}
	return core.NewRuntime(reg, source), nil
}
