# Wheelhouse

[![CI](https://github.com/tristanwl/wheelhouse/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/tristanwl/wheelhouse/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/tristanwl/wheelhouse?include_prereleases&sort=semver)](https://github.com/tristanwl/wheelhouse/releases)
[![Docker Hub](https://img.shields.io/docker/v/tristanwl/wheelhouse?sort=semver&logo=docker&label=docker)](https://hub.docker.com/r/tristanwl/wheelhouse)
[![License: GPL-3.0](https://img.shields.io/github/license/tristanwl/wheelhouse)](LICENSE)
[![Node.js 22.12+](https://img.shields.io/badge/node-%E2%89%A522.12-339933?logo=node.js&logoColor=white)](.nvmrc)
![Status: alpha](https://img.shields.io/badge/status-alpha-orange)

A self-hosted dashboard and companion for your *arr media stack: live status
for Sonarr, Radarr, SABnzbd and Plex; storage usage per volume; library
browsing and deletion (including per-season TV deletes); movie discovery via
TMDB; and a codec analysis view that finds files worth converting to your
preferred codec per resolution tier and can run the conversion with ffmpeg,
using hardware encoding when your GPU supports it.

> **Status: early.** Wheelhouse works today with Sonarr, Radarr, SABnzbd and
> Plex, configured by environment variables. There is **no authentication
> yet** — run it only on a trusted network. Swappable providers (Jellyfin,
> qBittorrent, ...), a setup wizard, an admin page and local-user auth are
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
docker compose up -d
```

Then open http://localhost:3050.

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
| `SONARR_URL`, `SONARR_API_KEY` | yes | |
| `RADARR_URL`, `RADARR_API_KEY` | yes | |
| `SABNZBD_URL`, `SABNZBD_API_KEY` | yes | |
| `PLEX_URL`, `PLEX_TOKEN` | no | Status card only |
| `TMDB_API_KEY` | no | Enables Explore; free key from themoviedb.org |

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
