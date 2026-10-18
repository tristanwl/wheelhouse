package core

import (
	"context"
	"os"
	"regexp"
	"slices"
	"strings"
)

type envMapping struct {
	providerID string
	instanceID string
	name       string
	// vars maps a config field to its environment variable.
	vars map[string]string
}

// envMappings are the legacy single-instance environment variables, in the same
// order as ENV_MAPPINGS in lib/providers/core/config-source.ts. Each provider
// yields one instance whose id is stable (so existing routes keep working).
var envMappings = []envMapping{
	{"sonarr", "sonarr", "Sonarr", map[string]string{"url": "SONARR_URL", "apiKey": "SONARR_API_KEY"}},
	{"radarr", "radarr", "Radarr", map[string]string{"url": "RADARR_URL", "apiKey": "RADARR_API_KEY"}},
	{"sabnzbd", "sabnzbd", "SABnzbd", map[string]string{"url": "SABNZBD_URL", "apiKey": "SABNZBD_API_KEY"}},
	{"qbittorrent", "qbittorrent", "qBittorrent", map[string]string{
		"url":      "QBITTORRENT_URL",
		"username": "QBITTORRENT_USERNAME",
		"password": "QBITTORRENT_PASSWORD",
	}},
	{"plex", "plex", "Plex", map[string]string{"url": "PLEX_URL", "token": "PLEX_TOKEN"}},
	{"jellyfin", "jellyfin", "Jellyfin", map[string]string{"url": "JELLYFIN_URL", "apiKey": "JELLYFIN_API_KEY"}},
	{"tmdb", "tmdb", "TMDB", map[string]string{"apiKey": "TMDB_API_KEY", "url": "TMDB_BASE_URL"}},
}

// multiInstance lists the providers that can have more than one instance.
// Interim, until the database config source: SONARR_<ID>_URL and
// SONARR_<ID>_API_KEY define an extra instance "sonarr-<id>" named "Sonarr <ID>"
// (RADARR_4K_URL becomes "radarr-4k", "Radarr 4K"). Underscores inside <ID>
// become "-" in the id and a space in the name.
var multiInstance = map[string]struct{ prefix, displayName string }{
	"sonarr": {"SONARR", "Sonarr"},
	"radarr": {"RADARR", "Radarr"},
}

var extraFields = map[string]string{"URL": "url", "API_KEY": "apiKey"}

// EnvConfigSource builds instances from environment variables. A provider
// becomes an instance as soon as any of its variables is set, so a
// half-configured service (a URL without its key) shows up as "invalid config"
// instead of silently vanishing. Empty variables count as unset. Sonarr and
// Radarr also accept extra instances. The environment is read on every call, so
// late-set variables are seen.
type EnvConfigSource struct {
	env func() map[string]string
}

// NewEnvConfigSource reads from a map (tests, or a caller-managed snapshot). The
// map is read on every call; do not mutate it concurrently.
func NewEnvConfigSource(env map[string]string) *EnvConfigSource {
	return &EnvConfigSource{env: func() map[string]string { return env }}
}

// NewProcessEnvConfigSource reads the process environment.
func NewProcessEnvConfigSource() *EnvConfigSource {
	return &EnvConfigSource{env: func() map[string]string {
		environ := os.Environ()
		m := make(map[string]string, len(environ))
		for _, kv := range environ {
			if k, v, ok := strings.Cut(kv, "="); ok {
				m[k] = v
			}
		}
		return m
	}}
}

// Instances implements ConfigSource.
func (s *EnvConfigSource) Instances(context.Context) ([]Instance, error) {
	env := s.env()
	var instances []Instance
	for _, m := range envMappings {
		config := map[string]any{}
		for field, variable := range m.vars {
			if v := env[variable]; v != "" {
				config[field] = v
			}
		}
		if len(config) > 0 {
			instances = append(instances, Instance{
				ID: m.instanceID, ProviderID: m.providerID, Name: m.name, Enabled: true, Config: config,
			})
		}
		if multi, ok := multiInstance[m.providerID]; ok {
			instances = append(instances, extraInstances(env, m.providerID, multi.prefix, multi.displayName)...)
		}
	}
	return instances, nil
}

func extraInstances(env map[string]string, providerID, prefix, displayName string) []Instance {
	pattern := regexp.MustCompile(`^` + prefix + `_([A-Za-z0-9][A-Za-z0-9_]*?)_(URL|API_KEY)$`)
	type found struct {
		name   string
		config map[string]any
	}
	byKey := map[string]*found{}
	var keys []string // first-seen order, which follows the sorted variable names
	names := make([]string, 0, len(env))
	for name := range env {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, variable := range names {
		match := pattern.FindStringSubmatch(variable)
		value := env[variable]
		if match == nil || value == "" {
			continue
		}
		idText := match[1]
		key := strings.ToLower(idText)
		entry := byKey[key]
		if entry == nil {
			// Parity: the display name keeps the case of the first variable seen in
			// sorted order, so RADARR_4k_URL plus RADARR_4K_API_KEY is named after
			// whichever sorts first.
			entry = &found{name: displayName + " " + strings.ReplaceAll(idText, "_", " "), config: map[string]any{}}
			byKey[key] = entry
			keys = append(keys, key)
		}
		entry.config[extraFields[match[2]]] = value
	}
	out := make([]Instance, 0, len(keys))
	for _, key := range keys {
		e := byKey[key]
		out = append(out, Instance{
			ID:         providerID + "-" + strings.ReplaceAll(key, "_", "-"),
			ProviderID: providerID,
			Name:       e.name,
			Enabled:    true,
			Config:     e.config,
		})
	}
	return out
}
