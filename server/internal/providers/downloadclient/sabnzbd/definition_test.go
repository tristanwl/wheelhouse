package sabnzbd

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	dc "github.com/tristanwl/wheelhouse/server/internal/providers/downloadclient"
	"github.com/tristanwl/wheelhouse/server/internal/providers/downloadclient/sabnzbd/fakesabnzbd"
)

type source []core.Instance

func (s source) Instances(context.Context) ([]core.Instance, error) { return s, nil }

func runtimeFor(t *testing.T, config map[string]any) *core.Runtime {
	t.Helper()
	reg, err := core.NewRegistry(Definition())
	if err != nil {
		t.Fatal(err)
	}
	return core.NewRuntime(reg, source{{ID: "sabnzbd", ProviderID: ID, Name: "SABnzbd", Enabled: true, Config: config}})
}

func TestDefinition(t *testing.T) {
	def := Definition()
	if def.ID != "sabnzbd" || def.Role != core.RoleDownloadClient || def.DisplayName != "SABnzbd" {
		t.Errorf("%+v", def)
	}
	want := dc.Capabilities{Protocol: dc.Usenet, CanPause: true, UsenetServers: true, Seeding: false}
	if !reflect.DeepEqual(def.Capabilities, want) {
		t.Errorf("%+v", def.Capabilities)
	}
	reg, err := core.NewRegistry(def)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := reg.Get(ID)
	if !reflect.DeepEqual(d.SecretKeys(), []string{"apiKey"}) {
		t.Errorf("secret keys %v", d.SecretKeys())
	}
	b, err := json.Marshal(d)
	if err != nil || !strings.Contains(string(b), `"usenetServers":true`) || !strings.Contains(string(b), `"x-secret":true`) {
		t.Errorf("%s %v", b, err)
	}
}

func TestThroughTheRuntime(t *testing.T) {
	t.Run("builds a client from valid config", func(t *testing.T) {
		srv := fakesabnzbd.Start(t, fakesabnzbd.Idle)
		rt := runtimeFor(t, map[string]any{"url": srv.URL, "apiKey": fakesabnzbd.GoodKey})
		clients, err := rt.Clients(context.Background(), core.RoleDownloadClient)
		if err != nil || len(clients) != 1 {
			t.Fatalf("%v %v", clients, err)
		}
		client, ok := clients[0].Client.(dc.Client)
		if !ok {
			t.Fatalf("%T is not a download client", clients[0].Client)
		}
		s, err := client.Status(context.Background())
		if err != nil || s.StatusText != "Idle" {
			t.Errorf("%+v %v", s, err)
		}
	})

	t.Run("reports missing config as invalid, naming the fields", func(t *testing.T) {
		rt := runtimeFor(t, map[string]any{"url": "http://sab:8080"})
		res, err := rt.ListInstances(context.Background())
		if err != nil || len(res) != 1 {
			t.Fatal(err)
		}
		if res[0].Status != core.StatusInvalidConfig || !strings.Contains(res[0].Error, "apiKey") {
			t.Errorf("%+v", res[0])
		}
	})

	t.Run("an invalid URL reaching Create yields a client that reports the error", func(t *testing.T) {
		c := Definition().Create(Config{URL: "not a url", APIKey: "k-123456"}, core.ProviderContext{InstanceID: "sabnzbd"})
		client := c.(dc.Client)
		if res := client.TestConnection(context.Background()); res.OK || res.Error != "sabnzbd: invalid base URL" {
			t.Errorf("%+v", res)
		}
		if _, err := client.Items(context.Background()); err == nil {
			t.Error("expected an error")
		}
	})
}
