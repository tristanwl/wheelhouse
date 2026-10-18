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

func TestLoadFromEnv(t *testing.T) {
	cfg, err := Load(env(map[string]string{"PORT": "8123", "HOSTNAME": "127.0.0.1"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr() != "127.0.0.1:8123" {
		t.Fatalf("Addr() = %q", cfg.Addr())
	}
}

func TestLoadIPv6Host(t *testing.T) {
	cfg, err := Load(env(map[string]string{"HOSTNAME": "::1"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr() != "[::1]:3000" {
		t.Fatalf("Addr() = %q", cfg.Addr())
	}
}

func TestLoadRejectsBadPort(t *testing.T) {
	for _, p := range []string{"abc", "0", "-1", "65536", "3000x", " 3000"} {
		if _, err := Load(env(map[string]string{"PORT": p})); err == nil {
			t.Errorf("PORT=%q: want an error", p)
		}
	}
}
