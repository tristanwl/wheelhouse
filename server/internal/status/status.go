// Package status builds the body of GET /api/status: one entry per configured
// media manager, download client and media server, with a state the dashboard
// can show. It is the server half of lib/status.ts and app/api/status/route.ts.
package status

import (
	"context"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	dc "github.com/tristanwl/wheelhouse/server/internal/providers/downloadclient"
	"github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager"
	"github.com/tristanwl/wheelhouse/server/internal/providers/mediaserver"
)

// State is how an instance is doing.
type State string

// The instance states.
const (
	StateOK         State = "ok"
	StateDegraded   State = "degraded"
	StateNeedsSetup State = "needs-setup"
	StateDown       State = "down"
)

// WarningMaxAge: warnings older than this are history, not news.
const WarningMaxAge = 24 * time.Hour

// instanceTimeout is a backstop on one instance's whole status call. Every
// upstream request already has its own 8 s timeout, so this only matters for an
// adapter that makes several requests one after another.
const instanceTimeout = 10 * time.Second

// Link is what the browser needs to build an "open this service" link; it takes
// the host from its own address bar. Never the host, credentials or query of
// the configured URL.
type Link struct {
	// Protocol is the configured URL's scheme: "http" or "https".
	Protocol string `json:"protocol"`
	Port     int    `json:"port,omitempty"`
	Path     string `json:"path"`
}

// Instance is one configured instance and its normalized status. Detail is
// absent while the instance is down; its type depends on Role
// (mediamanager.Status, downloadclient.Status or mediaserver.Status).
type Instance struct {
	ID         string    `json:"id"`
	ProviderID string    `json:"providerId"`
	Name       string    `json:"name"`
	Role       core.Role `json:"role"`
	State      State     `json:"state"`
	Version    string    `json:"version,omitempty"`
	// Error says why the instance is down. Redacted.
	Error string `json:"error,omitempty"`
	// Link is absent when the configured URL is missing or unusable.
	Link   *Link `json:"link,omitempty"`
	Detail any   `json:"detail,omitempty"`
}

// Response is the body of GET /api/status.
type Response struct {
	Instances []Instance `json:"instances"`
}

// webUIPath is where a provider's own web UI lives, under the configured URL.
var webUIPath = map[string]string{"plex": "/web"}

var defaultPort = map[string]int{"http": 80, "https": 443}

// roleOrder is the dashboard's group order.
var roleOrder = []core.Role{core.RoleMediaManager, core.RoleDownloadClient, core.RoleMediaServer}

// WarningVisible reports whether a warning is recent enough to show. One
// without a usable timestamp always is.
func WarningVisible(w dc.Warning, now time.Time) bool {
	if w.At == "" {
		return true
	}
	at, err := time.Parse(time.RFC3339Nano, w.At)
	if err != nil {
		return true
	}
	return now.Sub(at) <= WarningMaxAge
}

// HasRecentError reports whether the warnings include a visible error.
func HasRecentError(warnings []dc.Warning, now time.Time) bool {
	return slices.ContainsFunc(warnings, func(w dc.Warning) bool {
		return w.Severity == dc.SeverityError && WarningVisible(w, now)
	})
}

// ServiceLink is the port and path prefix of a configured service URL, plus an
// optional path to append (Plex's web UI lives under /web). nil when there is
// no usable http(s) URL.
func ServiceLink(rawURL any, appendPath string) *Link {
	s, ok := rawURL.(string)
	if !ok {
		return nil
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return nil
	}
	prefix := strings.TrimRight(u.EscapedPath(), "/")
	link := &Link{Protocol: u.Scheme, Path: prefix + appendPath}
	if link.Path == "" {
		link.Path = "/"
	}
	// Like a browser's URL, a scheme's default port counts as no port.
	if p, err := strconv.Atoi(u.Port()); err == nil && p != defaultPort[u.Scheme] {
		link.Port = p
	}
	return link
}

// Compute reports on every enabled media manager, download client and media
// server, in parallel. An instance that fails (or panics) becomes a "down"
// entry and never affects the others. Entries are grouped by role, and keep the
// configured order within a role.
func Compute(ctx context.Context, rt *core.Runtime, now time.Time) (Response, error) {
	resolved, err := rt.ListInstances(ctx)
	if err != nil {
		return Response{}, err
	}
	var todo []core.ResolvedInstance
	for _, r := range resolved {
		if r.Status == core.StatusDisabled || r.Definition == nil || !slices.Contains(roleOrder, r.Definition.Role) {
			continue
		}
		todo = append(todo, r)
	}
	out := make([]Instance, len(todo))
	var wg sync.WaitGroup
	for i, r := range todo {
		wg.Go(func() { out[i] = statusOf(ctx, rt, r, now) })
	}
	wg.Wait()
	slices.SortStableFunc(out, func(a, b Instance) int {
		return slices.Index(roleOrder, a.Role) - slices.Index(roleOrder, b.Role)
	})
	if out == nil {
		out = []Instance{}
	}
	return Response{Instances: out}, nil
}

func statusOf(ctx context.Context, rt *core.Runtime, r core.ResolvedInstance, now time.Time) (inst Instance) {
	base := Instance{
		ID:         r.Instance.ID,
		ProviderID: r.Instance.ProviderID,
		Name:       r.Instance.Name,
		Role:       r.Definition.Role,
		Link:       ServiceLink(r.Instance.Config["url"], webUIPath[r.Instance.ProviderID]),
	}
	down := func(msg string) Instance {
		base.State, base.Error = StateDown, msg
		return base
	}
	defer func() {
		if recover() != nil {
			inst = down("unexpected error")
		}
	}()

	if r.Status != core.StatusReady {
		if r.Error != "" {
			return down(r.Error)
		}
		return down(string(r.Status))
	}
	ctx, cancel := context.WithTimeout(ctx, instanceTimeout)
	defer cancel()
	client, err := rt.Client(ctx, r.Instance.ID)
	if err != nil {
		return down(core.ErrorText(err))
	}
	switch c := client.(type) {
	case mediamanager.Manager:
		s, err := c.Status(ctx)
		if err != nil {
			return down(core.ErrorText(err))
		}
		base.State = StateOK
		if slices.ContainsFunc(s.Health, func(h mediamanager.HealthIssue) bool {
			return h.Type == mediamanager.HealthWarning || h.Type == mediamanager.HealthError
		}) {
			base.State = StateDegraded
		}
		base.Version, base.Detail = s.Version, s
	case dc.Client:
		s, err := c.Status(ctx)
		if err != nil {
			return down(core.ErrorText(err))
		}
		base.State = StateOK
		if HasRecentError(s.Warnings, now) {
			base.State = StateDegraded
		}
		base.Version, base.Detail = s.Version, s
	case mediaserver.Client:
		s, err := c.Status(ctx)
		if err != nil {
			return down(core.ErrorText(err))
		}
		base.State = StateOK
		if s.State == mediaserver.StateNeedsSetup {
			base.State = StateNeedsSetup
		}
		base.Version, base.Detail = s.Version, s
	default:
		return down("unexpected error")
	}
	return base
}
