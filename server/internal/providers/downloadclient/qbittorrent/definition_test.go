package qbittorrent

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	dc "github.com/tristanwl/wheelhouse/server/internal/providers/downloadclient"
	fake "github.com/tristanwl/wheelhouse/server/internal/providers/downloadclient/qbittorrent/fakeqbittorrent"
)

type source []core.Instance

func (s source) Instances(context.Context) ([]core.Instance, error) { return s, nil }

func runtimeFor(t *testing.T, config map[string]any) *core.Runtime {
	t.Helper()
	reg, err := core.NewRegistry(Definition())
	if err != nil {
		t.Fatal(err)
	}
	return core.NewRuntime(reg, source{{ID: "qbittorrent", ProviderID: ID, Name: "qBittorrent", Enabled: true, Config: config}})
}

func TestDefinition(t *testing.T) {
	def := Definition()
	if def.ID != "qbittorrent" || def.Role != core.RoleDownloadClient || def.DisplayName != "qBittorrent" {
		t.Errorf("%+v", def)
	}
	want := dc.Capabilities{Protocol: dc.Torrent, CanPause: true, UsenetServers: false, Seeding: true}
	if !reflect.DeepEqual(def.Capabilities, want) {
		t.Errorf("%+v", def.Capabilities)
	}
	reg, err := core.NewRegistry(def)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := reg.Get(ID)
	// Only the password is a secret; the username is not.
	if !reflect.DeepEqual(d.SecretKeys(), []string{"password"}) {
		t.Errorf("secret keys %v", d.SecretKeys())
	}
	b, err := json.Marshal(d)
	if err != nil || !strings.Contains(string(b), `"seeding":true`) {
		t.Errorf("%s %v", b, err)
	}
}

func TestThroughTheRuntime(t *testing.T) {
	t.Run("builds a client from valid config", func(t *testing.T) {
		srv := fake.Start(t, fake.V5, fake.Idle)
		rt := runtimeFor(t, map[string]any{"url": srv.URL, "username": fake.GoodUser, "password": fake.GoodPassword})
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

	t.Run("reports an incomplete config as invalid, naming the missing fields", func(t *testing.T) {
		rt := runtimeFor(t, map[string]any{"url": "http://qbittorrent:8080"})
		res, err := rt.ListInstances(context.Background())
		if err != nil || len(res) != 1 {
			t.Fatal(err)
		}
		if res[0].Status != core.StatusInvalidConfig ||
			res[0].Error != "Username is required; Password is required" {
			t.Errorf("%+v", res[0])
		}
	})

	t.Run("an invalid URL reaching Create yields a client that reports the error", func(t *testing.T) {
		c := Definition().Create(Config{URL: "not a url", Username: "u", Password: "p-123456"}, core.ProviderContext{InstanceID: "qbittorrent"})
		client := c.(dc.Client)
		if res := client.TestConnection(context.Background()); res.OK || res.Error != "qbittorrent: invalid base URL" {
			t.Errorf("%+v", res)
		}
	})
}
