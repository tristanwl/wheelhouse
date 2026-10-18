// Package version holds the release version, stamped in at build time.
package version

// Version is the release version ("0.2.0"), or "dev" for a build that did not
// set it. The Dockerfile and release workflow stamp it with
//
//	-ldflags "-X github.com/tristanwl/wheelhouse/server/internal/version.Version=0.2.0"
//
// from the root package.json, the single source of the release version.
var Version = "dev"
