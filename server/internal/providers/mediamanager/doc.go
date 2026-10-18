// Package mediamanager is the media-manager role: the Go port of
// lib/providers/media-manager in the Next app. It covers the Sonarr and Radarr
// family (the providers live in the sonarr and radarr subpackages), with
// several instances of each ("radarr", "radarr-4k").
//
// The types stay arr-shaped on purpose: the role is Sonarr/Radarr only, so a
// neutral Movie/Series model would add nothing. The package holds
//
//   - the normalized types and the role interfaces (Base, MovieManager,
//     SeriesManager),
//   - the shared arr HTTP dialect (Arr: X-Api-Key header, JSON under /api/v3,
//     a short library-list cache, "API key incorrect" for a 401),
//   - helpers for the ones the routes use to talk to every configured instance
//     (ListManagers, CollectFromManagers, SummarizeManagers), and
//   - ready-made add payloads and season operations.
//
// Documents that are read, edited and written back (a series for PUT, a lookup
// result for add) keep every field the service sent, including the ones this
// package does not model, so a round trip never strips data.
//
// The reusable contract suite and the fake Sonarr/Radarr servers (driven by the
// sanitized real responses) are in the mmtest subpackage.
package mediamanager
