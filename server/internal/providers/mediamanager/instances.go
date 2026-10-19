package mediamanager

import (
	"context"
	"sync"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
)

// Entry is one configured media manager, working or not.
type Entry struct {
	InstanceID string
	Name       string
	Kind       Kind
	// Client is nil when the instance is not ready (see Error).
	Client Manager
	Error  string
}

// Summary is what routes tell the UI about each instance, so a broken one is
// visible and not just missing.
type Summary struct {
	InstanceID string `json:"instanceId"`
	Name       string `json:"name"`
	Kind       Kind   `json:"kind"`
	Up         bool   `json:"up"`
	Error      string `json:"error,omitempty"`
}

// Ref names an instance in a response (the picker's choices, the instances a
// movie is already in).
type Ref struct {
	InstanceID string `json:"instanceId"`
	Name       string `json:"name"`
}

// Problem is an instance that could not answer.
type Problem struct {
	InstanceID string `json:"instanceId"`
	Name       string `json:"name"`
	Error      string `json:"error"`
}

// KindLabel is the heading for a kind in the UI.
var KindLabel = map[Kind]string{KindMovies: "Movies", KindSeries: "TV"}

// NoManagerMessage is "No Radarr instance is configured": for a route that
// needs a kind and has none.
func NoManagerMessage(kind Kind) string {
	if kind == KindMovies {
		return "No Radarr instance is configured"
	}
	return "No Sonarr instance is configured"
}

// ListManagers returns every enabled media-manager instance (of one kind, if
// kind is not empty), in configured order. Misconfigured ones are included
// without a client so callers can report them.
func ListManagers(ctx context.Context, runtime *core.Runtime, kind Kind) ([]Entry, error) {
	clients, err := runtime.Clients(ctx, core.RoleMediaManager)
	if err != nil {
		return nil, err
	}
	ready := make(map[string]Manager, len(clients))
	for _, c := range clients {
		if m, ok := c.Client.(Manager); ok {
			ready[c.Instance.ID] = m
		}
	}
	resolved, err := runtime.ListInstances(ctx)
	if err != nil {
		return nil, err
	}
	var entries []Entry
	for _, r := range resolved {
		if r.Definition == nil || r.Definition.Role != core.RoleMediaManager || r.Status == core.StatusDisabled {
			continue
		}
		caps, _ := r.Definition.Capabilities.(Capabilities)
		if kind != "" && caps.Kind != kind {
			continue
		}
		e := Entry{
			InstanceID: r.Instance.ID,
			Name:       r.Instance.Name,
			Kind:       caps.Kind,
			Client:     ready[r.Instance.ID],
		}
		if e.Client == nil {
			e.Error = r.Error
			if e.Error == "" {
				e.Error = string(r.Status)
			}
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// Result is the value one instance produced.
type Result[T any] struct {
	Entry Entry
	Value T
}

// CollectFromManagers runs fn against every ready entry in parallel. An
// instance that fails (or is not ready) is reported in the problems and does not
// affect the others. Results and problems keep the order of entries.
func CollectFromManagers[T any](
	ctx context.Context,
	entries []Entry,
	fn func(ctx context.Context, client Manager, entry Entry) (T, error),
) ([]Result[T], []Problem) {
	type outcome struct {
		value T
		err   string
	}
	outcomes := make([]outcome, len(entries))
	var wg sync.WaitGroup
	for i, e := range entries {
		wg.Go(func() {
			// A provider bug must not take the server down or hide the others.
			defer func() {
				if recover() != nil {
					outcomes[i].err = "unexpected error"
				}
			}()
			if e.Client == nil {
				outcomes[i].err = e.Error
				if outcomes[i].err == "" {
					outcomes[i].err = "not ready"
				}
				return
			}
			v, err := fn(ctx, e.Client, e)
			if err != nil {
				// Errors from the HTTP client are redacted already; other errors
				// might not be, so redact again.
				outcomes[i].err = core.Redact(err.Error())
				if outcomes[i].err == "" {
					outcomes[i].err = "unknown error"
				}
				return
			}
			outcomes[i].value = v
		})
	}
	wg.Wait()

	var results []Result[T]
	var problems []Problem
	for i, e := range entries {
		if outcomes[i].err != "" {
			problems = append(problems, Problem{InstanceID: e.InstanceID, Name: e.Name, Error: outcomes[i].err})
		} else {
			results = append(results, Result[T]{Entry: e, Value: outcomes[i].value})
		}
	}
	return results, problems
}

// Summarize is the per-instance summary for a response: every entry, marked
// down if it had a problem.
func Summarize(entries []Entry, problems []Problem) []Summary {
	failed := make(map[string]string, len(problems))
	for _, p := range problems {
		failed[p.InstanceID] = p.Error
	}
	out := make([]Summary, len(entries))
	for i, e := range entries {
		msg, down := failed[e.InstanceID]
		out[i] = Summary{InstanceID: e.InstanceID, Name: e.Name, Kind: e.Kind, Up: !down}
		if down {
			out[i].Error = msg
		}
	}
	return out
}
