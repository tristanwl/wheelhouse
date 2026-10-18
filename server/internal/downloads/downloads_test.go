package downloads

import (
	"context"
	"strings"
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	dc "github.com/tristanwl/wheelhouse/server/internal/providers/downloadclient"
)

type cfg struct {
	URL string `json:"url" label:"URL" config:"required,url"`
}

// client is a download client that works, panics or fails with a secret in its error.
type client struct{ mode string }

func (client) TestConnection(context.Context) core.ConnectionResult { return core.ConnectionResult{} }

func (c client) Status(context.Context) (dc.Status, error) {
	switch c.mode {
	case "panic":
		panic("provider bug with CANARY-KEY")
	case "leak":
		return dc.Status{}, leakyError("GET http://user:CANARY-PASS@host/?apikey=CANARY-KEY failed")
	}
	return dc.Status{Paused: true, SpeedBytesPerSec: 5}, nil
}

func (c client) Items(context.Context) ([]dc.Item, error) {
	return []dc.Item{{ID: c.mode, Name: "Example Download 1", State: dc.StateQueued}}, nil
}

type leakyError string

func (e leakyError) Error() string { return string(e) }

type source []core.Instance

func (s source) Instances(context.Context) ([]core.Instance, error) { return s, nil }

func TestPartialFailuresAndConfiguredOrder(t *testing.T) {
	def := func(id string) core.Definition {
		return core.Definition{
			ID: id, Role: core.RoleDownloadClient, DisplayName: id, Config: cfg{},
			Capabilities: dc.Capabilities{Protocol: dc.Torrent},
			Create:       func(any, core.ProviderContext) any { return client{mode: id} },
		}
	}
	reg, err := core.NewRegistry(def("panic"), def("ok"), def("leak"), def("ok2"))
	if err != nil {
		t.Fatal(err)
	}
	inst := func(id string, enabled bool, url string) core.Instance {
		return core.Instance{ID: id, ProviderID: strings.TrimSuffix(id, "-x"), Name: id, Enabled: enabled, Config: map[string]any{"url": url}}
	}
	rt := core.NewRuntime(reg, source{
		inst("panic", true, "http://192.0.2.1:1"),
		inst("ok", true, "http://192.0.2.2:1"),
		inst("leak", true, "http://192.0.2.3:1"),
		inst("ok2", false, "http://192.0.2.4:1"), // disabled: left out
	})
	resp, err := Compute(context.Background(), rt)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, i := range resp.Instances {
		ids = append(ids, i.InstanceID)
		if i.Protocol != dc.Torrent {
			t.Errorf("%s: protocol %q", i.InstanceID, i.Protocol)
		}
		switch i.InstanceID {
		case "ok":
			if !i.Up || i.Paused == nil || !*i.Paused || i.SpeedBytesPerSec == nil || *i.SpeedBytesPerSec != 5 {
				t.Errorf("ok: %+v", i)
			}
		default:
			if i.Up || i.Error == "" || i.Paused != nil || strings.Contains(i.Error, "CANARY") {
				t.Errorf("%s should be down with a clean reason: %+v", i.InstanceID, i)
			}
		}
	}
	if strings.Join(ids, ",") != "panic,ok,leak" {
		t.Errorf("instances: %v", ids)
	}
	if len(resp.Items) != 1 || resp.Items[0].InstanceID != "ok" || resp.Items[0].ID != "ok" {
		t.Errorf("items: %+v", resp.Items)
	}
}
