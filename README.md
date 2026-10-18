# Wheelhouse

[![CI](https://github.com/tristanwl/wheelhouse/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/tristanwl/wheelhouse/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/tristanwl/wheelhouse?include_prereleases&sort=semver)](https://github.com/tristanwl/wheelhouse/releases)
[![Docker Hub](https://img.shields.io/docker/v/tristanwl/wheelhouse?sort=semver&logo=docker&label=docker)](https://hub.docker.com/r/tristanwl/wheelhouse)
[![License: GPL-3.0](https://img.shields.io/github/license/tristanwl/wheelhouse)](LICENSE)
[![Node.js 22.12+](https://img.shields.io/badge/node-%E2%89%A522.12-339933?logo=node.js&logoColor=white)](.nvmrc)
![Status: alpha](https://img.shields.io/badge/status-alpha-orange)

A self-hosted dashboard and companion for your *arr media stack: live status
for Sonarr, Radarr, SABnzbd, qBittorrent, Plex and Jellyfin; storage usage per volume; library
browsing and deletion (including per-season TV deletes); movie discovery via
TMDB; and a codec analysis view that finds files worth converting to your
preferred codec per resolution tier and can run the conversion with ffmpeg,
using hardware encoding when your GPU supports it.

> **Status: early.** Wheelhouse works today with Sonarr, Radarr, SABnzbd, qBittorrent and
> Plex, configured by environment variables. There is **no authentication
> yet** — run it only on a trusted network. Swappable providers (Jellyfin,
> ...), a setup wizard, an admin page and local-user auth are
> planned; see [docs/wheelhouse-spec.md](docs/wheelhouse-spec.md).

Wheelhouse is a companion for the *arr stack, not a replacement for it, and
is not affiliated with or endorsed by Plex, Sonarr, Radarr, SABnzbd, Jellyfin
or TMDB.

## Quick start (Docker Compose)

```sh
git clone https://github.com/tristanwl/wheelhouse.git
cd wheelhouse
cp .env.example .env                               # fill in URLs and API keys
cp docker-compose.example.yml docker-compose.yml   # adjust media paths
mkdir config                                       # settings volume (create it yourself)
docker compose up -d
```

Then open http://localhost:3050.

The container runs as a non-root user (UID/GID 1000 by default). If your media
or `./config` belong to someone else, set `user: "UID:GID"` in the compose
file. Create `./config` before the first start: if Docker creates it, it is
root-owned and Wheelhouse can't save settings.

This pulls the published image, [`tristanwl/wheelhouse`](https://hub.docker.com/r/tristanwl/wheelhouse)
on Docker Hub (linux/amd64). There is no `latest` tag while Wheelhouse is
alpha; pin a version, for example `tristanwl/wheelhouse:0.0.1`. To build from
source instead, uncomment `build: .` in the compose file and run
`docker compose up -d --build`.

Podman/systemd users: see [deploy/wheelhouse.service.example](deploy/wheelhouse.service.example).

## Configuration

Set via environment variables (see [.env.example](.env.example)):

| Variable | Required | Notes |
|---|---|---|
| `SONARR_URL`, `SONARR_API_KEY` | no | TV library; without it (and Radarr) Library and Codecs are hidden |
| `RADARR_URL`, `RADARR_API_KEY` | no | Movie library; also needed for Explore |
| `RADARR_<ID>_URL`, `RADARR_<ID>_API_KEY` | no | Extra instance, e.g. `RADARR_4K_URL` becomes `radarr-4k`, "Radarr 4K". Same for `SONARR_<ID>_*`. See below |
| `SABNZBD_URL`, `SABNZBD_API_KEY` | no | Download client; the dashboard shows "No download client configured" without it |
| `QBITTORRENT_URL`, `QBITTORRENT_USERNAME`, `QBITTORRENT_PASSWORD` | no | Download client (v4 and v5); shows torrents, including seeding, next to or instead of SABnzbd. The URL's host and port must be what qBittorrent's web UI expects (a remapped Docker port makes it answer 401) |
| `PLEX_URL`, `PLEX_TOKEN` | no | Media server status card only |
| `JELLYFIN_URL`, `JELLYFIN_API_KEY` | no | Media server status card only; key from Dashboard → API Keys in Jellyfin. Can run alongside Plex |
| `TMDB_API_KEY` | no | Enables Explore (with Radarr); free key from themoviedb.org |

### What unlocks what

The navigation only shows pages that can work with what you configured, and a
page you open directly says what it is missing.

| Page | Needs |
|---|---|
| Dashboard | nothing; it shows whatever is configured |
| Library | Sonarr or Radarr |
| Explore | `TMDB_API_KEY` and Radarr |
| Codecs | Sonarr or Radarr, and `ffmpeg` on the server (included in the container image) |

The search/add box in the header also needs Sonarr or Radarr. A service with a
missing or invalid setting doesn't count until it is fixed; the Dashboard shows
what is wrong.

### More than one Sonarr or Radarr

Add an instance by setting a URL and API key with an ID of your choice in the
name, for example:

```sh
RADARR_4K_URL=http://radarr-4k:7878
RADARR_4K_API_KEY=...
```

This creates the instance `radarr-4k`, shown as "Radarr 4K" (the ID is
lowercased for the id; underscores in it become `-`, and spaces in the name).
Library, storage, Explore's "In library" badge, search and the codec analysis
cover every instance, and a misconfigured or unreachable one is reported
without affecting the others. Setting only one of the two variables is
reported as a configuration error. This environment convention is interim;
Phase 2 moves instance configuration to the database.

The Codecs "Convert" feature reads and writes your media files directly, so
mount them at the same in-container paths Sonarr/Radarr use. Conversions
keep a `.bak` of the original and never delete it. See
[docs/architecture.md](docs/architecture.md) for how it works.

## Development

```sh
npm install
cp .env.example .env.local   # point at your services
npm run dev
```

Requires Node 22.12+ (24 recommended). `npm test` runs the tests; see
[CONTRIBUTING.md](CONTRIBUTING.md) for the full pre-PR checklist.

## Credits

This product uses the TMDB API but is not endorsed or certified by TMDB.

## License

[GPL-3.0](LICENSE).
