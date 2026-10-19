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
