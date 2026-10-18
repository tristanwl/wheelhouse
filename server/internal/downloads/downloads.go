// Package downloads builds the body of GET /api/downloads: the normalized rows
// of every download client, with the clients that could not answer listed as
// down instead of failing the whole response. It mirrors
// app/api/downloads/route.ts.
package downloads

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	dc "github.com/tristanwl/wheelhouse/server/internal/providers/downloadclient"
)

// instanceTimeout is a backstop on one client's calls; every upstream request
// has its own 8 s timeout already.
const instanceTimeout = 10 * time.Second

// Instance is one configured download client, working or not.
type Instance struct {
	InstanceID       string      `json:"instanceId"`
	Name             string      `json:"name"`
	Protocol         dc.Protocol `json:"protocol,omitempty"`
	Up               bool        `json:"up"`
	Paused           *bool       `json:"paused,omitempty"`
	SpeedBytesPerSec *int64      `json:"speedBytesPerSec,omitempty"`
	// Error says why the client is down. Redacted.
	Error string `json:"error,omitempty"`
}

// Row is one download plus the client it came from.
type Row struct {
	dc.Item
	InstanceID string `json:"instanceId"`
}

// Response is the body of GET /api/downloads.
type Response struct {
	Instances []Instance `json:"instances"`
	Items     []Row      `json:"items"`
}

// result is what one client contributed.
type result struct {
	inst  Instance
	items []Row
	skip  bool
}

// Compute asks every enabled download client for its status and items, in
// parallel. A client that fails (or panics) is listed with up=false and the
// others are unaffected. Instances and items keep the configured order.
func Compute(ctx context.Context, rt *core.Runtime) (Response, error) {
	resolved, err := rt.ListInstances(ctx)
	if err != nil {
		return Response{}, err
	}
	var clients []core.ResolvedInstance
	for _, r := range resolved {
		if r.Definition != nil && r.Definition.Role == core.RoleDownloadClient {
			clients = append(clients, r)
		}
	}
	results := make([]result, len(clients))
	var wg sync.WaitGroup
	for i, r := range clients {
		wg.Go(func() { results[i] = one(ctx, rt, r) })
	}
	wg.Wait()

	out := Response{Instances: []Instance{}, Items: []Row{}}
	for _, res := range results {
		if res.skip {
			continue
		}
		out.Instances = append(out.Instances, res.inst)
		out.Items = append(out.Items, res.items...)
	}
	return out, nil
}

func one(ctx context.Context, rt *core.Runtime, r core.ResolvedInstance) (res result) {
	protocol := dc.Protocol("")
	if caps, ok := r.Definition.Capabilities.(dc.Capabilities); ok {
		protocol = caps.Protocol
	}
	base := Instance{InstanceID: r.Instance.ID, Name: r.Instance.Name, Protocol: protocol}
	down := func(msg string) {
		base.Up, base.Error = false, msg
		res.inst = base
	}
	defer func() {
		if recover() != nil {
			res.items = nil
			down("unexpected error")
		}
	}()

	if r.Status != core.StatusReady {
		// Misconfigured: say so rather than pretending it isn't there. A
		// disabled client is left out on purpose.
		if r.Status == core.StatusDisabled {
			res.skip = true
			return res
		}
		msg := r.Error
		if msg == "" {
			msg = string(r.Status)
		}
		down(msg)
		return res
	}
	ctx, cancel := context.WithTimeout(ctx, instanceTimeout)
	defer cancel()
	raw, err := rt.Client(ctx, r.Instance.ID)
	if err != nil {
		down(core.ErrorText(err))
		return res
	}
	client, ok := raw.(dc.Client)
	if !ok {
		down("unexpected error")
		return res
	}
	var (
		status dc.Status
		items  []dc.Item
		errs   [2]error
		wg     sync.WaitGroup
	)
	wg.Go(func() { errs[0] = safely(func() (err error) { status, err = client.Status(ctx); return }) })
	wg.Go(func() { errs[1] = safely(func() (err error) { items, err = client.Items(ctx); return }) })
	wg.Wait()
	if i := slices.IndexFunc(errs[:], func(e error) bool { return e != nil }); i >= 0 {
		down(core.ErrorText(errs[i]))
		return res
	}
	base.Up, base.Paused, base.SpeedBytesPerSec = true, &status.Paused, &status.SpeedBytesPerSec
	res.inst = base
	for _, it := range items {
		res.items = append(res.items, Row{Item: it, InstanceID: r.Instance.ID})
	}
	return res
}

// safely turns a panic in a provider (in a goroutine of its own, where the
// caller's recover cannot see it) into an error, which ErrorText then replaces
// with a fixed phrase.
func safely(f func() error) (err error) {
	defer func() {
		if recover() != nil {
			err = errPanic
		}
	}()
	return f()
}

var errPanic = errors.New("provider panicked")
