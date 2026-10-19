package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

var goodCfg = map[string]any{"url": "http://sab:8080", "apiKey": "k-valid-1"}

func TestRuntimeResolvesStatuses(t *testing.T) {
	sab := fakeProvider("sab", RoleDownloadClient)
	rt := NewRuntime(mustRegistry(t, sab.def), staticSource{[]Instance{
		inst("sab", "sab", goodCfg, true),
		inst("off", "sab", goodCfg, false),
		inst("ghost", "nonexistent", map[string]any{}, true),
		inst("broken", "sab", map[string]any{"url": "not a url"}, true),
	}})
	list, err := rt.ListInstances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range list {
		got = append(got, fmt.Sprintf("%s=%s", r.Instance.ID, r.Status))
	}
	want := "sab=ready off=disabled ghost=unknown-provider broken=invalid-config"
	if strings.Join(got, " ") != want {
		t.Fatalf("statuses = %v", got)
	}
	broken := list[3]
	if broken.Error != "URL must be an http or https URL; API key is required" {
		t.Fatalf("error = %q", broken.Error)
	}
	if broken.Definition == nil || list[2].Definition != nil {
		t.Fatal("definition should be set for known providers only")
	}
	if list[2].Error != `Unknown provider "nonexistent"` {
		t.Fatalf("error = %q", list[2].Error)
	}
}

func TestRuntimeClientsOnlyForReadyInstancesOfTheRole(t *testing.T) {
	sab := fakeProvider("sab", RoleDownloadClient)
	plex := fakeProvider("plex", RoleMediaServer)
	rt := NewRuntime(mustRegistry(t, sab.def, plex.def), staticSource{[]Instance{
		inst("sab", "sab", goodCfg, true),
		inst("sab-2", "sab", map[string]any{"url": "http://sab2:8080", "apiKey": "k-valid-1"}, true),
		inst("plex", "plex", map[string]any{"url": "http://plex:32400", "apiKey": "t-valid-1"}, true),
		inst("bad", "sab", map[string]any{}, true),
	}})
	got, err := rt.Clients(context.Background(), RoleDownloadClient)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Instance.ID != "sab" || got[1].Instance.ID != "sab-2" {
		t.Fatalf("got %+v", got)
	}
	if got[1].Client.(*fakeClient).URL != "http://sab2:8080" {
		t.Fatalf("client = %+v", got[1].Client)
	}
}

func TestRuntimeReusesAClientUntilItsConfigChanges(t *testing.T) {
	sab := fakeProvider("sab", RoleDownloadClient)
	src := &mutableSource{instances: []Instance{inst("sab", "sab", goodCfg, true)}}
	rt := NewRuntime(mustRegistry(t, sab.def), src)
	ctx := context.Background()

	first, err := rt.Client(ctx, "sab")
	if err != nil {
		t.Fatal(err)
	}
	again, _ := rt.Client(ctx, "sab")
	if first != again || sab.creates.Load() != 1 {
		t.Fatalf("not reused: creates=%d", sab.creates.Load())
	}

	src.set([]Instance{inst("sab", "sab", map[string]any{"url": "http://other:8080", "apiKey": "k-valid-1"}, true)})
	second, _ := rt.Client(ctx, "sab")
	if second == first || sab.creates.Load() != 2 {
		t.Fatalf("not rebuilt: creates=%d", sab.creates.Load())
	}

	// Unknown extra keys do not count as a config change (zod strips them too).
	src.set([]Instance{inst("sab", "sab", map[string]any{"url": "http://other:8080", "apiKey": "k-valid-1", "x": 1}, true)})
	third, _ := rt.Client(ctx, "sab")
	if third != second || sab.creates.Load() != 2 {
		t.Fatalf("rebuilt for an ignored key: creates=%d", sab.creates.Load())
	}

	// Parity difference: a rename rebuilds, so the client never holds a stale name.
	renamed := inst("sab", "sab", map[string]any{"url": "http://other:8080", "apiKey": "k-valid-1"}, true)
	renamed.Name = "Renamed"
	src.set([]Instance{renamed})
	fourth, _ := rt.Client(ctx, "sab")
	if fourth.(*fakeClient).Name != "Renamed" || sab.creates.Load() != 3 {
		t.Fatalf("rename: %+v creates=%d", fourth, sab.creates.Load())
	}
}

func TestRuntimeForgetsRemovedInstances(t *testing.T) {
	sab := fakeProvider("sab", RoleDownloadClient)
	src := &mutableSource{instances: []Instance{inst("sab", "sab", goodCfg, true)}}
	rt := NewRuntime(mustRegistry(t, sab.def), src)
	ctx := context.Background()
	_, _ = rt.Client(ctx, "sab")
	src.set(nil)
	_, _ = rt.ListInstances(ctx)
	rt.mu.Lock()
	n := len(rt.clients)
	rt.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d clients kept", n)
	}
}

func TestRuntimePassesInstanceIdentityToTheProvider(t *testing.T) {
	var got ProviderContext
	def := fakeProvider("sab", RoleDownloadClient).def
	def.Create = func(cfg any, pc ProviderContext) any { got = pc; return cfg }
	rt := NewRuntime(mustRegistry(t, def), staticSource{[]Instance{inst("sab-4k", "sab", goodCfg, true)}})
	client, err := rt.Client(context.Background(), "sab-4k")
	if err != nil {
		t.Fatal(err)
	}
	if got != (ProviderContext{InstanceID: "sab-4k", InstanceName: "sab-4k"}) {
		t.Fatalf("ctx = %+v", got)
	}
	if client.(fakeConfig).URL != "http://sab:8080" {
		t.Fatalf("client = %+v", client)
	}
}

func TestRuntimeExplainsWhyClientFailed(t *testing.T) {
	sab := fakeProvider("sab", RoleDownloadClient)
	rt := NewRuntime(mustRegistry(t, sab.def), staticSource{[]Instance{
		inst("broken", "sab", map[string]any{}, true),
		inst("off", "sab", goodCfg, false),
	}})
	ctx := context.Background()
	for id, want := range map[string]string{
		"missing": `No instance "missing" is configured`,
		"off":     `Instance "off" is disabled`,
		"broken":  `Instance "broken" is invalid-config`,
	} {
		_, err := rt.Client(ctx, id)
		var pe *ProviderError
		if !errors.As(err, &pe) || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("%s: err = %v", id, err)
		}
	}
}

type failingSource struct{}

func (failingSource) Instances(context.Context) ([]Instance, error) {
	return nil, errors.New("database exploded: password=" + canary)
}

func TestRuntimeHidesSourceErrors(t *testing.T) {
	rt := NewRuntime(mustRegistry(t, fakeProvider("sab", RoleDownloadClient).def), failingSource{})
	_, err := rt.ListInstances(context.Background())
	if err == nil || strings.Contains(err.Error(), canary) {
		t.Fatalf("err = %v", err)
	}
}

type mutableSource struct {
	mu        sync.Mutex
	instances []Instance
}

func (s *mutableSource) Instances(context.Context) ([]Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Instance(nil), s.instances...), nil
}

func (s *mutableSource) set(in []Instance) {
	s.mu.Lock()
	s.instances = in
	s.mu.Unlock()
}

// Run with -race. Readers hammer the runtime while the config flips between two
// versions; every caller must get a client that matches one of them.
func TestRuntimeIsSafeForConcurrentUse(t *testing.T) {
	sab := fakeProvider("sab", RoleDownloadClient)
	cfgs := []map[string]any{
		{"url": "http://a.example.com", "apiKey": "key-aaaa"},
		{"url": "http://b.example.com", "apiKey": "key-bbbb"},
	}
	src := &mutableSource{instances: []Instance{
		inst("sab", "sab", cfgs[0], true), inst("sab-2", "sab", cfgs[1], true),
	}}
	rt := NewRuntime(mustRegistry(t, sab.def), src)
	ctx := context.Background()

	var wg sync.WaitGroup
	for g := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 200 {
				if g == 0 {
					src.set([]Instance{inst("sab", "sab", cfgs[i%2], true), inst("sab-2", "sab", cfgs[1], true)})
				}
				switch i % 3 {
				case 0:
					clients, err := rt.Clients(ctx, RoleDownloadClient)
					if err != nil || len(clients) != 2 {
						t.Errorf("Clients: %v %v", clients, err)
						return
					}
				case 1:
					c, err := rt.Client(ctx, "sab-2")
					if err != nil || c.(*fakeClient).URL != "http://b.example.com" {
						t.Errorf("Client: %v %v", c, err)
						return
					}
				default:
					if list, err := rt.ListInstances(ctx); err != nil || len(list) != 2 {
						t.Errorf("List: %v %v", list, err)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	// sab-2 never changed, so it was built once no matter how many goroutines asked.
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if len(rt.clients) != 2 {
		t.Fatalf("clients = %d", len(rt.clients))
	}
}
