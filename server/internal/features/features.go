// Package features decides which parts of the app are available, from the
// configured provider instances. It mirrors lib/features/registry.ts and the
// response of GET /api/features in the Next app.
package features

import (
	"context"
	"os/exec"
	"slices"
	"sync"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	"github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager"
)

// Role is what a provider does.
type Role string

// The provider roles.
const (
	RoleDownloadClient Role = "downloadClient"
	RoleMediaManager   Role = "mediaManager"
	RoleMediaServer    Role = "mediaServer"
	RoleMetadata       Role = "metadata"
)

// Kind narrows a media manager to movies (Radarr) or series (Sonarr).
type Kind string

// The media manager kinds.
const (
	KindMovies Kind = "movies"
	KindSeries Kind = "series"
)

// Requirement is something a feature needs: a ready instance with a role
// (and optionally a kind), or a tool on the host.
type Requirement struct {
	Role Role
	Kind Kind
	Tool string
}

// Describe says what is missing, in words: "a Radarr instance", "ffmpeg".
func (r Requirement) Describe() string {
	if r.Tool != "" {
		return r.Tool
	}
	if r.Role == RoleMediaManager && r.Kind != "" {
		if r.Kind == KindMovies {
			return "a Radarr instance"
		}
		return "a Sonarr instance"
	}
	switch r.Role {
	case RoleDownloadClient:
		return "a download client"
	case RoleMediaManager:
		return "a Sonarr or Radarr instance"
	case RoleMediaServer:
		return "a media server"
	case RoleMetadata:
		return "a TMDB API key"
	}
	return string(r.Role)
}

// Definition is one feature of the app.
type Definition struct {
	ID       string
	Label    string
	Route    string
	Requires []Requirement
}

// Definitions lists the features in navigation order.
var Definitions = []Definition{
	{ID: "dashboard", Label: "Dashboard", Route: "/"},
	{ID: "library", Label: "Library", Route: "/library", Requires: []Requirement{
		{Role: RoleMediaManager},
	}},
	{ID: "explore", Label: "Explore", Route: "/explore", Requires: []Requirement{
		{Role: RoleMetadata}, {Role: RoleMediaManager, Kind: KindMovies},
	}},
	{ID: "codecs", Label: "Codecs", Route: "/codecs", Requires: []Requirement{
		{Role: RoleMediaManager}, {Tool: "ffmpeg"},
	}},
}

// Instance is a configured provider instance reduced to what gating needs.
type Instance struct {
	Role Role
	Kind Kind
	// Ready means valid config and enabled, not merely present.
	Ready bool
}

// Environment is what the host offers.
type Environment struct {
	Ffmpeg bool
}

// Feature is one entry of the GET /api/features response.
type Feature struct {
	ID        string   `json:"id"`
	Label     string   `json:"label"`
	Route     string   `json:"route"`
	Available bool     `json:"available"`
	Missing   []string `json:"missing"`
}

// Response is the body of GET /api/features: nothing about hosts, keys or
// instance names.
type Response struct {
	Features []Feature `json:"features"`
}

func met(r Requirement, instances []Instance, env Environment) bool {
	if r.Tool != "" {
		return r.Tool == "ffmpeg" && env.Ffmpeg
	}
	return slices.ContainsFunc(instances, func(i Instance) bool {
		return i.Ready && i.Role == r.Role && (r.Kind == "" || i.Kind == r.Kind)
	})
}

// Compute decides which features are available. Missing is never null in JSON.
func Compute(instances []Instance, env Environment) Response {
	out := Response{Features: make([]Feature, 0, len(Definitions))}
	for _, d := range Definitions {
		f := Feature{ID: d.ID, Label: d.Label, Route: d.Route, Missing: []string{}}
		for _, r := range d.Requires {
			if !met(r, instances, env) {
				f.Missing = append(f.Missing, r.Describe())
			}
		}
		f.Available = len(f.Missing) == 0
		out.Features = append(out.Features, f)
	}
	return out
}

// HasFfmpeg reports whether `ffmpeg -version` works. It is checked once per
// process, then cached, like the Next app does.
var HasFfmpeg = sync.OnceValue(func() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "ffmpeg", "-version").Run() == nil
})

// InstancesOf reduces the runtime's instances to what gating needs. Instances
// of unknown providers are skipped: they have no role, so they satisfy nothing.
func InstancesOf(ctx context.Context, rt *core.Runtime) ([]Instance, error) {
	resolved, err := rt.ListInstances(ctx)
	if err != nil {
		return nil, err
	}
	var out []Instance
	for _, r := range resolved {
		if r.Definition == nil {
			continue
		}
		inst := Instance{Role: Role(r.Definition.Role), Ready: r.Status == core.StatusReady}
		if r.Definition.Role == core.RoleMediaManager {
			if caps, ok := r.Definition.Capabilities.(mediamanager.Capabilities); ok {
				inst.Kind = Kind(caps.Kind)
			}
		}
		out = append(out, inst)
	}
	return out, nil
}

// Of computes the features for what the runtime has configured and the host.
func Of(ctx context.Context, rt *core.Runtime, env Environment) (Response, error) {
	instances, err := InstancesOf(ctx, rt)
	if err != nil {
		return Response{}, err
	}
	return Compute(instances, env), nil
}
