package core

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func envInstances(t *testing.T, env map[string]string) []Instance {
	t.Helper()
	got, err := NewEnvConfigSource(env).Instances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func ids(instances []Instance) []string {
	out := make([]string, len(instances))
	for i, in := range instances {
		out[i] = in.ID
	}
	return out
}

func TestEnvReturnsNothingWhenNoVariablesAreSet(t *testing.T) {
	if got := envInstances(t, map[string]string{}); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
	if got := envInstances(t, nil); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestEnvMapsTMDBBaseURL(t *testing.T) {
	got := envInstances(t, map[string]string{"TMDB_API_KEY": "tk", "TMDB_BASE_URL": "http://tmdb.example.com/3"})
	want := map[string]any{"apiKey": "tk", "url": "http://tmdb.example.com/3"}
	if len(got) != 1 || !reflect.DeepEqual(got[0].Config, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestEnvMapsLegacyVariablesToStableIDs(t *testing.T) {
	got := envInstances(t, map[string]string{
		"SONARR_URL": "http://sonarr:8989", "SONARR_API_KEY": "sk",
		"RADARR_URL": "http://radarr:7878", "RADARR_API_KEY": "rk",
		"SABNZBD_URL": "http://sab:8080", "SABNZBD_API_KEY": "ak",
		"PLEX_URL": "http://plex:32400", "PLEX_TOKEN": "pt",
		"JELLYFIN_URL": "http://jellyfin:8096", "JELLYFIN_API_KEY": "jk",
		"TMDB_API_KEY": "tk",
	})
	if want := []string{"sonarr", "radarr", "sabnzbd", "plex", "jellyfin", "tmdb"}; !reflect.DeepEqual(ids(got), want) {
		t.Fatalf("ids = %v", ids(got))
	}
	want := Instance{
		ID: "sonarr", ProviderID: "sonarr", Name: "Sonarr", Enabled: true,
		Config: map[string]any{"url": "http://sonarr:8989", "apiKey": "sk"},
	}
	if !reflect.DeepEqual(got[0], want) {
		t.Fatalf("got %+v", got[0])
	}
	if !reflect.DeepEqual(got[3].Config, map[string]any{"url": "http://plex:32400", "token": "pt"}) {
		t.Fatalf("plex = %+v", got[3].Config)
	}
	wantJelly := Instance{
		ID: "jellyfin", ProviderID: "jellyfin", Name: "Jellyfin", Enabled: true,
		Config: map[string]any{"url": "http://jellyfin:8096", "apiKey": "jk"},
	}
	if !reflect.DeepEqual(got[4], wantJelly) {
		t.Fatalf("jellyfin = %+v", got[4])
	}
}

func TestEnvKeepsAHalfConfiguredProvider(t *testing.T) {
	got := envInstances(t, map[string]string{"SONARR_URL": "http://sonarr:8989"})
	if len(got) != 1 || !reflect.DeepEqual(got[0].Config, map[string]any{"url": "http://sonarr:8989"}) {
		t.Fatalf("got %+v", got)
	}
}

func TestEnvMapsQBittorrent(t *testing.T) {
	got := envInstances(t, map[string]string{
		"QBITTORRENT_URL": "http://qbittorrent:8080", "QBITTORRENT_USERNAME": "admin", "QBITTORRENT_PASSWORD": "pw",
	})
	want := []Instance{{
		ID: "qbittorrent", ProviderID: "qbittorrent", Name: "qBittorrent", Enabled: true,
		Config: map[string]any{"url": "http://qbittorrent:8080", "username": "admin", "password": "pw"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	half := envInstances(t, map[string]string{"QBITTORRENT_URL": "http://qbittorrent:8080"})
	if !reflect.DeepEqual(half[0].Config, map[string]any{"url": "http://qbittorrent:8080"}) {
		t.Fatalf("half = %+v", half[0].Config)
	}
}

func TestEnvTreatsEmptyVariablesAsUnset(t *testing.T) {
	if got := envInstances(t, map[string]string{"SONARR_URL": "", "SONARR_API_KEY": ""}); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestEnvReadsTheEnvironmentOnEveryCall(t *testing.T) {
	env := map[string]string{}
	source := NewEnvConfigSource(env)
	first, _ := source.Instances(context.Background())
	env["TMDB_API_KEY"] = "late"
	second, _ := source.Instances(context.Background())
	if len(first) != 0 || len(second) != 1 {
		t.Fatalf("first=%d second=%d", len(first), len(second))
	}
}

func TestProcessEnvSource(t *testing.T) {
	t.Setenv("SONARR_URL", "http://192.0.2.5:8989")
	t.Setenv("SONARR_API_KEY", "key=with=equals")
	t.Setenv("RADARR_4K_URL", "http://192.0.2.6:7878")
	got, err := NewProcessEnvConfigSource().Instances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"sonarr", "radarr-4k"}; !reflect.DeepEqual(ids(got), want) {
		t.Fatalf("ids = %v", ids(got))
	}
	if got[0].Config["apiKey"] != "key=with=equals" {
		t.Fatalf("config = %+v", got[0].Config)
	}
}

func TestEnvExtraInstances(t *testing.T) {
	t.Run("builds an instance from RADARR_<ID>_URL and _API_KEY", func(t *testing.T) {
		got := envInstances(t, map[string]string{
			"RADARR_URL": "http://radarr:7878", "RADARR_API_KEY": "rk",
			"RADARR_4K_URL": "http://radarr-4k:7878", "RADARR_4K_API_KEY": "rk4",
		})
		if want := []string{"radarr", "radarr-4k"}; !reflect.DeepEqual(ids(got), want) {
			t.Fatalf("ids = %v", ids(got))
		}
		want := Instance{
			ID: "radarr-4k", ProviderID: "radarr", Name: "Radarr 4K", Enabled: true,
			Config: map[string]any{"url": "http://radarr-4k:7878", "apiKey": "rk4"},
		}
		if !reflect.DeepEqual(got[1], want) {
			t.Fatalf("got %+v", got[1])
		}
	})

	t.Run("works for Sonarr and without the default instance", func(t *testing.T) {
		got := envInstances(t, map[string]string{
			"SONARR_ANIME_URL": "http://anime:8989", "SONARR_ANIME_API_KEY": "ak",
		})
		want := []Instance{{
			ID: "sonarr-anime", ProviderID: "sonarr", Name: "Sonarr ANIME", Enabled: true,
			Config: map[string]any{"url": "http://anime:8989", "apiKey": "ak"},
		}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("keeps a half-configured extra instance", func(t *testing.T) {
		got := envInstances(t, map[string]string{"RADARR_4K_URL": "http://radarr-4k:7878"})
		if len(got) != 1 || got[0].ID != "radarr-4k" ||
			!reflect.DeepEqual(got[0].Config, map[string]any{"url": "http://radarr-4k:7878"}) {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("does not mistake the default variables for extra instances", func(t *testing.T) {
		got := envInstances(t, map[string]string{
			"RADARR_URL": "http://radarr:7878", "RADARR_API_KEY": "rk",
			"SONARR_URL": "http://sonarr:8989", "SONARR_API_KEY": "sk",
		})
		if want := []string{"sonarr", "radarr"}; !reflect.DeepEqual(ids(got), want) {
			t.Fatalf("ids = %v", ids(got))
		}
	})

	t.Run("lists extras right after their default, in a stable order, ignoring empty values", func(t *testing.T) {
		got := envInstances(t, map[string]string{
			"RADARR_B_URL": "http://b", "RADARR_B_API_KEY": "k",
			"RADARR_A_URL": "http://a", "RADARR_A_API_KEY": "k",
			"RADARR_EMPTY_URL": "",
			"RADARR_URL":       "http://radarr", "RADARR_API_KEY": "k",
			"SABNZBD_URL": "http://sab", "SABNZBD_API_KEY": "k",
		})
		if want := []string{"radarr", "radarr-a", "radarr-b", "sabnzbd"}; !reflect.DeepEqual(ids(got), want) {
			t.Fatalf("ids = %v", ids(got))
		}
	})

	t.Run("turns underscores in the ID into hyphens (id) and spaces (name)", func(t *testing.T) {
		got := envInstances(t, map[string]string{"RADARR_UHD_EXTRA_URL": "http://x", "RADARR_UHD_EXTRA_API_KEY": "k"})
		if len(got) != 1 || got[0].ID != "radarr-uhd-extra" || got[0].Name != "Radarr UHD EXTRA" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("does not apply to single-instance providers", func(t *testing.T) {
		got := envInstances(t, map[string]string{"PLEX_X_URL": "http://x", "SABNZBD_X_URL": "http://x"})
		if len(got) != 0 {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("is not fooled by a prefix inside another variable", func(t *testing.T) {
		got := envInstances(t, map[string]string{"MYRADARR_4K_URL": "http://x", "RADARR_4K_URL_BACKUP": "http://x"})
		if len(got) != 0 {
			t.Fatalf("got %+v", got)
		}
	})
}

func TestInstanceNeverPrintsItsConfig(t *testing.T) {
	in := Instance{ID: "a", ProviderID: "p", Enabled: true, Config: map[string]any{"apiKey": canary}}
	for _, s := range []string{
		strings.TrimSpace(sprint("%v", in)), sprint("%+v", in), sprint("%#v", in), sprint("%s", in),
		sprint("%v", &in), sprint("%v", []Instance{in}),
	} {
		if strings.Contains(s, canary) {
			t.Fatalf("config printed: %s", s)
		}
	}
}
