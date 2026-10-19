package instances

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	"github.com/tristanwl/wheelhouse/server/internal/secrets"
	"github.com/tristanwl/wheelhouse/server/internal/store"
)

type testConfig struct {
	URL    string `json:"url" label:"URL" config:"required,url"`
	APIKey string `json:"apiKey" label:"API key" config:"required,secret"`
}

type testClient struct{ URL, Key string }

func testRegistry(t *testing.T) *core.Registry {
	t.Helper()
	mk := func(id string) core.Definition {
		return core.Definition{
			ID: id, Role: core.RoleMediaManager, DisplayName: id, Config: testConfig{},
			Create: func(cfg any, _ core.ProviderContext) any {
				c := cfg.(testConfig)
				return &testClient{URL: c.URL, Key: c.APIKey}
			},
		}
	}
	r, err := core.NewRegistry(mk("radarr"), mk("sonarr"))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

type env map[string]core.Instance

func (e env) Instances(context.Context) ([]core.Instance, error) {
	var out []core.Instance
	for _, i := range e {
		out = append(out, i)
	}
	return out, nil
}

type fixture struct {
	src    *Source
	db     *store.DB
	path   string
	key    secrets.Key
	env    env
	cipher *secrets.Cipher
}

const (
	keyA = "canary-api-key-aaaa1111"
	keyB = "canary-api-key-bbbb2222"
)

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "wheelhouse.db")
	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	key, err := secrets.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	c, err := db.UnlockSecrets(context.Background(), secrets.Loaded{Key: key}, true)
	if err != nil {
		t.Fatal(err)
	}
	e := env{}
	return &fixture{
		src: New(db, c, testRegistry(t), Options{Environment: e}), db: db, path: path, key: key, env: e, cipher: c,
	}
}

func ctx() context.Context { return context.Background() }

func radarr(url, key string) NewInstance {
	return NewInstance{ProviderID: "radarr", Name: "Radarr", Config: map[string]any{"url": url, "apiKey": key}}
}

func TestCreateStoresSecretsEncryptedAndReadsThemBack(t *testing.T) {
	f := newFixture(t)
	e, err := f.src.Create(ctx(), radarr("http://192.0.2.10:7878", keyA))
	if err != nil {
		t.Fatal(err)
	}
	if e.ID != "radarr" || e.Origin != OriginDatabase || !e.Enabled || e.Config["apiKey"] != keyA {
		t.Fatalf("created %+v", e)
	}
	got, err := f.src.Instances(ctx())
	if err != nil || len(got) != 1 || got[0].Config["apiKey"] != keyA || got[0].Config["url"] != "http://192.0.2.10:7878" {
		t.Fatalf("Instances = %+v, %v", got, err)
	}
	// The plaintext key is nowhere in the database files (main, WAL).
	_ = f.db.Close()
	for _, suffix := range []string{"", "-wal"} {
		raw, err := os.ReadFile(f.path + suffix)
		if err != nil {
			continue
		}
		if bytes.Contains(raw, []byte(keyA)) {
			t.Errorf("database%s holds the API key in plaintext", suffix)
		}
	}
}

func TestCreateValidatesAndRejectsBadIDs(t *testing.T) {
	f := newFixture(t)
	var ve *core.ValidationError
	var ie *InvalidError
	if _, err := f.src.Create(ctx(), NewInstance{ProviderID: "radarr", Name: "R", Config: map[string]any{"url": "ftp://x"}}); !errors.As(err, &ve) {
		t.Errorf("bad config: %v", err)
	}
	if _, err := f.src.Create(ctx(), NewInstance{ProviderID: "radarr", Name: "R", Config: map[string]any{"url": "http://u:p@h", "apiKey": keyA}}); !errors.As(err, &ve) || strings.Contains(err.Error(), "p@") {
		t.Errorf("URL credentials: %v", err)
	}
	if _, err := f.src.Create(ctx(), NewInstance{ProviderID: "nope", Name: "R"}); !errors.As(err, &ie) {
		t.Errorf("unknown provider: %v", err)
	}
	r := radarr("http://h", keyA)
	r.Config["extra"] = "x"
	if _, err := f.src.Create(ctx(), r); !errors.As(err, &ie) {
		t.Errorf("unknown setting: %v", err)
	}
	for _, id := range []string{"Radarr", "radarr_4k", "sonarr", "radarr-", "radarr--4k", "4k", "radarrx", "radarr/../x", strings.Repeat("a", 65)} {
		in := radarr("http://h", keyA)
		in.ID = id
		if _, err := f.src.Create(ctx(), in); !errors.As(err, &ie) {
			t.Errorf("id %q accepted: %v", id, err)
		}
	}
	in := radarr("http://h", keyA)
	in.Name = "  "
	if _, err := f.src.Create(ctx(), in); !errors.As(err, &ie) {
		t.Errorf("empty name: %v", err)
	}
	if list, _ := f.src.Instances(ctx()); len(list) != 0 {
		t.Errorf("failed creates left %d instances", len(list))
	}
}

func TestMultipleInstancesAndUniqueIDs(t *testing.T) {
	f := newFixture(t)
	if _, err := f.src.Create(ctx(), radarr("http://h1", keyA)); err != nil {
		t.Fatal(err)
	}
	in := radarr("http://h2", keyB)
	in.ID, in.Name = "radarr-4k", "Radarr 4K"
	if _, err := f.src.Create(ctx(), in); err != nil {
		t.Fatal(err)
	}
	if _, err := f.src.Create(ctx(), in); !errors.Is(err, ErrExists) {
		t.Errorf("duplicate: %v", err)
	}
	// No id: derived from the name once "radarr" is taken.
	d := radarr("http://h3", keyA)
	d.Name = "Radarr Kids"
	e, err := f.src.Create(ctx(), d)
	if err != nil || e.ID != "radarr-radarr-kids" {
		t.Fatalf("derived id: %v %q", err, e.ID)
	}
	list, _ := f.src.Instances(ctx())
	if len(list) != 3 || list[0].ID != "radarr" || list[1].ID != "radarr-4k" {
		t.Errorf("order: %+v", list)
	}
}

func TestUpdateKeepsOrReplacesSecrets(t *testing.T) {
	f := newFixture(t)
	if _, err := f.src.Create(ctx(), radarr("http://h1", keyA)); err != nil {
		t.Fatal(err)
	}
	// Omitted secret: the stored one stays.
	e, ch, err := f.src.Update(ctx(), "radarr", Patch{Config: map[string]any{"url": "http://H1:80/"}})
	if err != nil || e.Config["apiKey"] != keyA || e.Config["url"] != "http://H1:80/" {
		t.Fatalf("keep: %+v %v", e, err)
	}
	if len(ch.Fields) != 1 || ch.Fields[0] != "url" || len(ch.Secrets) != 0 {
		t.Errorf("change = %+v", ch)
	}
	// Explicit new value: replaced, and the change names the setting, not the value.
	e, ch, err = f.src.Update(ctx(), "radarr", Patch{Config: map[string]any{"url": "http://h2", "apiKey": keyB}})
	if err != nil || e.Config["apiKey"] != keyB {
		t.Fatalf("replace: %+v %v", e, err)
	}
	if len(ch.Secrets) != 1 || ch.Secrets[0] != "apiKey" || len(ch.Fields) != 1 {
		t.Errorf("change = %+v", ch)
	}
	// Name and enabled alone leave the settings alone.
	off := false
	name := "Movies"
	e, ch, err = f.src.Update(ctx(), "radarr", Patch{Name: &name, Enabled: &off})
	if err != nil || e.Enabled || e.Name != "Movies" || e.Config["apiKey"] != keyB || !ch.Name || !ch.Enabled {
		t.Fatalf("toggle: %+v %+v %v", e, ch, err)
	}
	// An invalid edit changes nothing.
	var ve *core.ValidationError
	if _, _, err := f.src.Update(ctx(), "radarr", Patch{Config: map[string]any{"url": "nope"}}); !errors.As(err, &ve) {
		t.Errorf("invalid update: %v", err)
	}
	if got, _ := f.src.Get(ctx(), "radarr"); got.Config["url"] != "http://h2" {
		t.Errorf("invalid update was applied: %v", got.Config)
	}
	if _, _, err := f.src.Update(ctx(), "ghost", Patch{Name: &name}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id: %v", err)
	}
}

func TestEnvironmentPrecedenceReadOnlyAndImport(t *testing.T) {
	f := newFixture(t)
	f.env["radarr"] = core.Instance{ID: "radarr", ProviderID: "radarr", Name: "Radarr", Enabled: true,
		Config: map[string]any{"url": "http://env-host", "apiKey": keyA}}
	f.env["sonarr"] = core.Instance{ID: "sonarr", ProviderID: "sonarr", Name: "Sonarr", Enabled: true,
		Config: map[string]any{"url": "http://env-sonarr", "apiKey": keyB}}

	entries, _ := f.src.Entries(ctx())
	if len(entries) != 2 {
		t.Fatalf("entries = %d", len(entries))
	}
	for _, e := range entries {
		if e.Origin != OriginEnvironment {
			t.Errorf("%s origin %s", e.ID, e.Origin)
		}
	}
	// Read-only.
	name := "x"
	if _, _, err := f.src.Update(ctx(), "radarr", Patch{Name: &name}); !errors.Is(err, ErrReadOnly) {
		t.Errorf("update env: %v", err)
	}
	if _, err := f.src.Delete(ctx(), "radarr"); !errors.Is(err, ErrReadOnly) {
		t.Errorf("delete env: %v", err)
	}
	// A new instance may not silently take an environment id.
	explicit := radarr("http://h", keyA)
	explicit.ID = "radarr"
	if _, err := f.src.Create(ctx(), explicit); !errors.Is(err, ErrEnvironmentID) {
		t.Errorf("create over env: %v", err)
	}

	// Import copies, then the database wins and the environment is ignored.
	imp, err := f.src.Import(ctx(), "radarr")
	if err != nil || imp.Origin != OriginDatabase || !imp.OverridesEnvironment || imp.Config["apiKey"] != keyA {
		t.Fatalf("import: %+v %v", imp, err)
	}
	if _, err := f.src.Import(ctx(), "radarr"); !errors.Is(err, ErrExists) {
		t.Errorf("second import: %v", err)
	}
	if _, err := f.src.Import(ctx(), "ghost"); !errors.Is(err, ErrNotFound) {
		t.Errorf("import unknown: %v", err)
	}
	f.env["radarr"] = core.Instance{ID: "radarr", ProviderID: "radarr", Name: "Radarr", Enabled: true,
		Config: map[string]any{"url": "http://changed-env", "apiKey": "other-env-key-0000"}}
	if _, _, err := f.src.Update(ctx(), "radarr", Patch{Config: map[string]any{"url": "http://db-host", "apiKey": keyA}}); err != nil {
		t.Fatal(err)
	}
	list, _ := f.src.Instances(ctx())
	if len(list) != 2 {
		t.Fatalf("merged = %d", len(list))
	}
	for _, i := range list {
		if i.ID == "radarr" && (i.Config["url"] != "http://db-host" || i.Config["apiKey"] != keyA) {
			t.Errorf("database should win: %v", i.Config)
		}
	}
	// Deleting the stored copy brings the environment one back.
	reverts, err := f.src.Delete(ctx(), "radarr")
	if err != nil || !reverts {
		t.Fatalf("delete: %v %v", reverts, err)
	}
	if e, _ := f.src.Get(ctx(), "radarr"); e.Origin != OriginEnvironment || e.Config["url"] != "http://changed-env" {
		t.Errorf("after delete: %+v", e)
	}
}

func TestSecretsAreBoundToTheirRow(t *testing.T) {
	f := newFixture(t)
	a := radarr("http://h1", keyA)
	b := radarr("http://h2", keyB)
	b.ID, b.Name = "radarr-4k", "4K"
	for _, in := range []NewInstance{a, b} {
		if _, err := f.src.Create(ctx(), in); err != nil {
			t.Fatal(err)
		}
	}
	// Copy radarr's sealed secrets onto radarr-4k: the associated data differs.
	if _, err := f.db.Exec(ctx(), `UPDATE instances SET secrets = (SELECT secrets FROM instances WHERE id = 'radarr') WHERE id = 'radarr-4k'`); err != nil {
		t.Fatal(err)
	}
	got, err := f.src.Get(ctx(), "radarr-4k")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Config["apiKey"]; ok {
		t.Error("a secret moved to another row was accepted")
	}
	if ok, _ := f.src.Get(ctx(), "radarr"); ok.Config["apiKey"] != keyA {
		t.Error("the original row broke")
	}
}

func TestRekeyAndForgetSecrets(t *testing.T) {
	f := newFixture(t)
	if _, err := f.src.Create(ctx(), radarr("http://h1", keyA)); err != nil {
		t.Fatal(err)
	}
	next, err := secrets.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	c2, n, err := f.db.Rekey(ctx(), f.cipher, next)
	if err != nil || n != 1 {
		t.Fatalf("rekey: %d %v", n, err)
	}
	src2 := New(f.db, c2, testRegistry(t), Options{})
	if e, _ := src2.Get(ctx(), "radarr"); e.Config["apiKey"] != keyA {
		t.Error("the secret did not survive a rekey")
	}
	// The old cipher can no longer read it (wrong key version).
	if e, _ := f.src.Get(ctx(), "radarr"); e.Config["apiKey"] != nil {
		t.Error("old key still reads")
	}
	// forget-secrets: the secret is gone, the instance remains and reports it missing.
	third, _ := secrets.GenerateKey()
	c3, n, err := f.db.ForgetSecrets(ctx(), third)
	if err != nil || n != 1 {
		t.Fatalf("forget: %d %v", n, err)
	}
	src3 := New(f.db, c3, testRegistry(t), Options{})
	e, _ := src3.Get(ctx(), "radarr")
	if e.Config["apiKey"] != nil || e.Config["url"] != "http://h1" {
		t.Errorf("after forget: %v", e.Config)
	}
	rt := core.NewRuntime(testRegistry(t), src3)
	list, _ := rt.ListInstances(ctx())
	if len(list) != 1 || list[0].Status != core.StatusInvalidConfig {
		t.Errorf("status after forget: %+v", list)
	}
}

func TestFeatureToggles(t *testing.T) {
	f := newFixture(t)
	if d, err := f.src.DisabledFeatures(ctx()); err != nil || len(d) != 0 {
		t.Fatalf("default: %v %v", d, err)
	}
	if err := f.src.SetFeatures(ctx(), map[string]bool{"library": false, "codecs": true}); err != nil {
		t.Fatal(err)
	}
	d, _ := f.src.DisabledFeatures(ctx())
	if len(d) != 1 || !d["library"] {
		t.Errorf("disabled = %v", d)
	}
	_ = f.src.SetFeatures(ctx(), map[string]bool{"library": true})
	if d, _ := f.src.DisabledFeatures(ctx()); len(d) != 0 {
		t.Errorf("re-enable: %v", d)
	}
}

// The runtime sees every change on its next call: a new instance appears, an
// edited one gets a new client, a deleted one disappears, and none of it races
// with requests that are in flight.
func TestRuntimePicksUpChangesWithoutRestart(t *testing.T) {
	f := newFixture(t)
	rt := core.NewRuntime(testRegistry(t), f.src)
	clients := func() map[string]*testClient {
		got, err := rt.Clients(ctx(), core.RoleMediaManager)
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]*testClient{}
		for _, c := range got {
			m[c.Instance.ID] = c.Client.(*testClient)
		}
		return m
	}
	if len(clients()) != 0 {
		t.Fatal("expected no clients")
	}
	if _, err := f.src.Create(ctx(), radarr("http://h1", keyA)); err != nil {
		t.Fatal(err)
	}
	first := clients()["radarr"]
	if first == nil || first.URL != "http://h1" {
		t.Fatalf("created instance missing: %+v", first)
	}
	if clients()["radarr"] != first {
		t.Error("an unchanged instance should reuse its client")
	}
	if _, _, err := f.src.Update(ctx(), "radarr", Patch{Config: map[string]any{"url": "http://h1", "apiKey": keyB}}); err != nil {
		t.Fatal(err)
	}
	second := clients()["radarr"]
	if second == first || second.Key != keyB {
		t.Errorf("an edit must build a new client: %+v", second)
	}
	off := false
	if _, _, err := f.src.Update(ctx(), "radarr", Patch{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	if len(clients()) != 0 {
		t.Error("a disabled instance must not be served")
	}
	if _, err := f.src.Delete(ctx(), "radarr"); err != nil {
		t.Fatal(err)
	}
	if list, _ := rt.ListInstances(ctx()); len(list) != 0 {
		t.Errorf("deleted instance listed: %+v", list)
	}

	// Concurrent edits and reads (run with -race).
	if _, err := f.src.Create(ctx(), radarr("http://h1", keyA)); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	deadline := time.Now().Add(300 * time.Millisecond)
	for w := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; time.Now().Before(deadline); i++ {
				if w%2 == 0 {
					key := keyA
					if i%2 == 0 {
						key = keyB
					}
					if _, _, err := f.src.Update(ctx(), "radarr", Patch{Config: map[string]any{"url": "http://h1", "apiKey": key}}); err != nil {
						t.Error(err)
						return
					}
				} else if got, err := rt.Clients(ctx(), core.RoleMediaManager); err != nil || len(got) != 1 {
					t.Errorf("clients: %d %v", len(got), err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestUnreadableInstanceDoesNotBreakTheRest(t *testing.T) {
	f := newFixture(t)
	if _, err := f.src.Create(ctx(), radarr("http://h1", keyA)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(ctx(), `UPDATE instances SET secrets = x'0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20' WHERE id = 'radarr'`); err != nil {
		t.Fatal(err)
	}
	list, err := f.src.Instances(ctx())
	if err != nil || len(list) != 1 || list[0].Config["apiKey"] != nil {
		t.Fatalf("damaged secrets: %+v %v", list, err)
	}
}
