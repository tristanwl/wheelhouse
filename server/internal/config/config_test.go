package config

import "testing"

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr() != "0.0.0.0:3000" {
		t.Fatalf("Addr() = %q, want 0.0.0.0:3000", cfg.Addr())
	}
}

func TestDBPath(t *testing.T) {
	if got := DBPath(env(nil), "/config"); got != "/config/wheelhouse.db" {
		t.Errorf("default = %q", got)
	}
	if got := DBPath(env(map[string]string{"WHEELHOUSE_DB": "/data/w.db"}), "/config"); got != "/data/w.db" {
		t.Errorf("override = %q", got)
	}
	if got := DBPath(env(map[string]string{"WHEELHOUSE_DB": ""}), "/config"); got != "/config/wheelhouse.db" {
		t.Errorf("empty = %q", got)
	}
}

func TestLoadPort(t *testing.T) {
	cfg, err := Load(env(map[string]string{"PORT": "8123"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr() != "0.0.0.0:8123" {
		t.Fatalf("Addr() = %q", cfg.Addr())
	}
}

// Container runtimes set HOSTNAME to the container id; it must never become
// the bind address.
func TestHostnameIsIgnored(t *testing.T) {
	cfg, err := Load(env(map[string]string{"PORT": "3050", "HOSTNAME": "4f2a9c1d7e3b"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr() != "0.0.0.0:3050" {
		t.Fatalf("Addr() = %q, want 0.0.0.0:3050", cfg.Addr())
	}
}

func TestLoadListen(t *testing.T) {
	for listen, want := range map[string]string{
		"127.0.0.1:8123": "127.0.0.1:8123",
		":4000":          "0.0.0.0:4000",
		"[::1]:5000":     "[::1]:5000",
		"[::]:5000":      "[::]:5000",
	} {
		// WHEELHOUSE_LISTEN wins over PORT and HOSTNAME.
		cfg, err := Load(env(map[string]string{"WHEELHOUSE_LISTEN": listen, "PORT": "1", "HOSTNAME": "x"}))
		if err != nil {
			t.Fatalf("%q: %v", listen, err)
		}
		if cfg.Addr() != want {
			t.Errorf("%q: Addr() = %q, want %q", listen, cfg.Addr(), want)
		}
	}
}

func TestLoadRejectsBadValues(t *testing.T) {
	for _, p := range []string{"abc", "0", "-1", "65536", "3000x", " 3000"} {
		if _, err := Load(env(map[string]string{"PORT": p})); err == nil {
			t.Errorf("PORT=%q: want an error", p)
		}
	}
	for _, l := range []string{"3050", "host", "1.2.3.4:", "1.2.3.4:0", "1.2.3.4:http", "::1:3050"} {
		if _, err := Load(env(map[string]string{"WHEELHOUSE_LISTEN": l})); err == nil {
			t.Errorf("WHEELHOUSE_LISTEN=%q: want an error", l)
		}
	}
}

func TestDialAddr(t *testing.T) {
	for listen, want := range map[string]string{
		"0.0.0.0:3050":    "127.0.0.1:3050",
		":3050":           "127.0.0.1:3050",
		"[::]:3050":       "127.0.0.1:3050",
		"192.0.2.10:81":   "192.0.2.10:81",
		"[2001:db8::1]:9": "[2001:db8::1]:9",
	} {
		cfg, err := Load(env(map[string]string{"WHEELHOUSE_LISTEN": listen}))
		if err != nil {
			t.Fatal(err)
		}
		if got := cfg.DialAddr(); got != want {
			t.Errorf("%q: DialAddr() = %q, want %q", listen, got, want)
		}
	}
}

func TestConfigDir(t *testing.T) {
	if got := ConfigDir(func(string) string { return "" }); got != "/config" {
		t.Errorf("default %q", got)
	}
	if got := ConfigDir(func(k string) string {
		if k == "WHEELHOUSE_CONFIG_DIR" {
			return "/srv/wheelhouse/"
		}
		return ""
	}); got != "/srv/wheelhouse" {
		t.Errorf("custom %q", got)
	}
}

func TestTrustedProxies(t *testing.T) {
	get := func(v string) func(string) string {
		return func(k string) string {
			if k == "WHEELHOUSE_TRUSTED_PROXIES" {
				return v
			}
			return ""
		}
	}
	if got, err := TrustedProxies(get("")); err != nil || len(got) != 0 {
		t.Errorf("default list: %v %v", got, err)
	}
	got, err := TrustedProxies(get(" 192.0.2.0/24, 198.51.100.7 ,2001:db8::/32,"))
	if err != nil || len(got) != 3 {
		t.Fatalf("%v %v", got, err)
	}
	for i, want := range []string{"192.0.2.0/24", "198.51.100.7/32", "2001:db8::/32"} {
		if got[i].String() != want {
			t.Errorf("entry %d: %s, want %s", i, got[i], want)
		}
	}
	for _, bad := range []string{"nonsense", "192.0.2.0/33", "192.0.2.1;192.0.2.2", "*"} {
		if _, err := TrustedProxies(get(bad)); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
