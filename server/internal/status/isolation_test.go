package status

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	dc "github.com/tristanwl/wheelhouse/server/internal/providers/downloadclient"
)

type boomConfig struct {
	URL string `json:"url" label:"URL" config:"required,url"`
}

// boomClient misbehaves in the way set by URL: it panics, or fails with an
// error that carries a secret.
type boomClient struct{ mode string }

func (boomClient) TestConnection(context.Context) core.ConnectionResult {
	return core.ConnectionResult{}
}

func (c boomClient) Status(context.Context) (dc.Status, error) {
	if c.mode == "leak" {
		return dc.Status{}, errString("GET http://user:CANARY-PASS@host/?apikey=CANARY-KEY failed")
	}
	panic("provider bug with CANARY-KEY")
}

func (boomClient) Items(context.Context) ([]dc.Item, error) { return nil, nil }

type errString string

func (e errString) Error() string { return string(e) }

type fixedSource []core.Instance

func (s fixedSource) Instances(context.Context) ([]core.Instance, error) { return s, nil }

func TestOneInstanceFailingNeverAffectsTheOthers(t *testing.T) {
	def := func(id, mode string) core.Definition {
		return core.Definition{
			ID: id, Role: core.RoleDownloadClient, DisplayName: id, Config: boomConfig{},
			Create: func(any, core.ProviderContext) any { return boomClient{mode: mode} },
		}
	}
	reg, err := core.NewRegistry(def("panics", "panic"), def("leaks", "leak"))
	if err != nil {
		t.Fatal(err)
	}
	rt := core.NewRuntime(reg, fixedSource{
		{ID: "panics", ProviderID: "panics", Name: "Panics", Enabled: true, Config: map[string]any{"url": "http://192.0.2.1:1"}},
		{ID: "leaks", ProviderID: "leaks", Name: "Leaks", Enabled: true, Config: map[string]any{"url": "http://192.0.2.2:1"}},
	})
	resp, err := Compute(context.Background(), rt, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Instances) != 2 {
		t.Fatalf("%+v", resp)
	}
	for _, i := range resp.Instances {
		if i.State != StateDown || i.Error == "" || i.Detail != nil {
			t.Errorf("%s should be down with a reason: %+v", i.ID, i)
		}
		if strings.Contains(i.Error, "CANARY") {
			t.Errorf("%s: error leaks: %q", i.ID, i.Error)
		}
	}
}
