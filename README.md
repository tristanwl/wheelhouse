# Wheelhouse

[![CI](https://github.com/tristanwl/wheelhouse/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/tristanwl/wheelhouse/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/tristanwl/wheelhouse?include_prereleases&sort=semver)](https://github.com/tristanwl/wheelhouse/releases)
[![Docker Hub](https://img.shields.io/docker/v/tristanwl/wheelhouse?sort=semver&logo=docker&label=docker)](https://hub.docker.com/r/tristanwl/wheelhouse)
[![License: GPL-3.0](https://img.shields.io/github/license/tristanwl/wheelhouse)](LICENSE)
[![Go 1.27+](https://img.shields.io/badge/go-%E2%89%A51.27-00ADD8?logo=go&logoColor=white)](server/go.mod)
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

This pulls the published image, [`tristanwl/wheelhouse`](https://hub.docker.com/r/tristanwl/wheelhouse)
on Docker Hub (linux/amd64). There is no `latest` tag while Wheelhouse is
alpha; pin a version, for example `tristanwl/wheelhouse:0.2.0`. To build from
source instead, uncomment `build: .` in the compose file and run
`docker compose up -d --build`.

### Running the image

One image, one process, one writable volume. Without Compose:

```sh
mkdir -p config && docker run -d --name wheelhouse --restart unless-stopped \
  -p 3050:3050 --env-file .env --user "$(id -u):$(id -g)" \
  -v "$PWD/config:/config" -v /path/to/movies:/movies -v /path/to/tv:/tv \
  tristanwl/wheelhouse:0.2.0
```

- **User.** The container runs as `1000:1000` by default and works as any
  numeric `--user UID:GID` (it needs no entry in `/etc/passwd`). The user must be
  able to **write `/config`** (it holds `settings.json`, your codec preferences),
  so create the directory yourself and `chown` it to that user: if Docker
  creates the host directory it is owned by root and settings cannot be saved. To
  convert files (Codecs → Convert) it also needs write access to the media.
- **Mounts.** `/movies` and `/tv` only matter for Convert, and must be the same
  in-container paths Sonarr and Radarr use. Another layout: set
  `WHEELHOUSE_MEDIA_ROOTS` (see below).
- **Hardware encoding** (optional): pass the GPU with `--device /dev/dri` and, if
  the render node is not accessible, `--group-add <render GID>` (`getent group
  render`). With rootless Podman use `--group-add keep-groups` instead; see the
  unit file below.
- **Hardening.** It runs with `--read-only --cap-drop ALL --security-opt
  no-new-privileges` as long as `/config` is writable and `/tmp` is a tmpfs
  (`--tmpfs /tmp`, used for GPU driver caches). Nothing else is written.
- **Network.** It listens on all interfaces on `PORT` (3050 in the image).
  `HOSTNAME`, which runtimes set to the container id, is ignored. To bind one
  address, set `WHEELHOUSE_LISTEN`, for example `WHEELHOUSE_LISTEN=127.0.0.1:3050`
  with host networking.
- **Health.** The image has a `HEALTHCHECK` (`wheelhouse healthcheck`, which asks
  `/healthz`); no curl is involved. `wheelhouse version` prints the version.

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
| `TMDB_BASE_URL` | no | Override the TMDB API root, including the version (default `https://api.themoviedb.org/3`). For a caching proxy or a test double; leave unset normally |
| `PORT` | no | TCP port to listen on (default `3000`; the image sets `3050`) |
| `WHEELHOUSE_LISTEN` | no | Address to listen on as `host:port` (for example `127.0.0.1:3050`); overrides `PORT`. Default: all interfaces on `PORT`. `HOSTNAME` is never used |
| `WHEELHOUSE_MEDIA_ROOTS` | no | Folders Convert may touch, separated like `PATH` (default `/movies:/tv`) |

The variable names are the same as in 0.1.x, so an existing `.env` keeps working.

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

Wheelhouse is a Go server (`server/`, standard library only) that serves a Vite
single-page UI (`web/`) embedded in the binary.

```sh
# Terminal 1: the server, configured with your services
set -a; . ./.env; set +a
make -C server run          # http://localhost:3000 (a placeholder page until the UI is embedded)

# Terminal 2: the UI with hot reload, proxying /api to the server
cd web && npm ci && npm run dev    # http://localhost:5173
```

Needs Go 1.27+ and Node 22.12+ (24 recommended, see `.nvmrc`). To build the
real thing: `cd web && npm run build && npm run embed`, then `make -C server
build` gives one binary at `server/wheelhouse`. `docker build -t wheelhouse .`
builds the image. `npm ci && make -C server build && npm run test:acceptance` in
the repository root runs the black-box acceptance suite against the binary
(`acceptance/README.md`); see [CONTRIBUTING.md](CONTRIBUTING.md) for the full
pre-PR checklist.

## Credits

This product uses the TMDB API but is not endorsed or certified by TMDB.

## License

[GPL-3.0](LICENSE).
