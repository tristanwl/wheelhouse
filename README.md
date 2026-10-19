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

> **Security model.** Every page and API route needs a signed-in user, and nothing
> works until the first administrator has been created ([First run](#first-run)).
> **Releases up to 0.2.x had no authentication at all; use 0.3.0 or later.** What
> is and is not protected is in the [threat model](docs/threat-model.md), what is
> deliberately not covered is in [Known limitations](docs/release-1.0-checklist.md#known-limitations),
> and how to run, back up and recover a server is in
> [docs/operations.md](docs/operations.md). When something does not work, start with
> [docs/troubleshooting.md](docs/troubleshooting.md). It is still a young project:
> do not expose it directly to the internet; put a TLS reverse proxy in front
> ([Behind a reverse proxy](#behind-a-reverse-proxy)).

Wheelhouse is a companion for the *arr stack, not a replacement for it, and
is not affiliated with or endorsed by Plex, Sonarr, Radarr, SABnzbd, Jellyfin
or TMDB.

## Quick start (Docker Compose)

```sh
git clone https://github.com/tristanwl/wheelhouse.git
cd wheelhouse
cp .env.example .env                               # optional settings; everything in it is commented out
cp docker-compose.example.yml docker-compose.yml   # adjust media paths (and the version tag)
mkdir config                                       # the volume with the database and key (create it yourself)
docker compose up -d
docker compose logs wheelhouse 2>&1 | grep setup_token   # the one-time setup token
```

Then open http://localhost:3050, enter the setup token and create the administrator
([First run](#first-run)), and add your services under **Admin** (no environment
variables are needed for that).

This pulls the published image, [`tristanwl/wheelhouse`](https://hub.docker.com/r/tristanwl/wheelhouse)
on Docker Hub. It is a multi-architecture image (linux/amd64 and linux/arm64
under the same tag, from the first release that carries this note; 0.2.x is
amd64 only), so Raspberry Pi, ARM NAS boxes and Apple Silicon hosts pull the
right one by themselves. On arm64, Intel VAAPI drivers are not part of the
image and hardware encoding is untested; software x264/x265 conversion works.
There is no `latest` tag while Wheelhouse is
a pre-release; pin a version, for example `tristanwl/wheelhouse:0.3.0`. To build from
source instead, uncomment `build: .` in the compose file and run
`docker compose up -d --build`. Release images from 1.0.0 on (earlier ones have
none) carry signed build provenance and an SBOM; see
[docs/verifying-releases.md](docs/verifying-releases.md) to verify what you pull.

### First run

A new server has no users. Until you create the administrator it answers only the
pages, `/healthz` and the setup routes (every other API route says `503 setup
required`), and the person who reaches it first must not be able to claim it, so
creating the administrator needs a **one-time setup token**:

- It is printed once in the log when the server starts with no users:
  `docker logs wheelhouse 2>&1 | grep setup_token` (`podman logs` likewise).
- It is also in `/config/setup-token` (mode 0600), which is deleted when setup
  succeeds: `docker exec wheelhouse cat /config/setup-token`.
- A new token is made at every start while there are no users, so a token from an
  old log line is dead.
- **Anyone who can read the logs, or the config directory, before setup completes
  can claim the instance.** Create the administrator before you share the port or the
  logs; the example Compose file publishes it on `127.0.0.1` only.

Open the UI: it shows the setup wizard (token, administrator name, password), signs you
in and lands on the dashboard with an "Add your services" checklist (it goes away once
one service exists). Under **Admin** you add Radarr, Sonarr, your download client and
media server (each form has a **Test connection** button, and keys are stored encrypted
and never shown again), switch features on and off, create users, end sessions, allow
the local network without a login, set the media folders for the codec converter and
read the audit log. Without the UI, use the API:

```sh
curl -s -X POST http://localhost:3050/api/auth/setup -H 'content-type: application/json' \
  -d '{"token":"<the token>","username":"admin","password":"<at least 12 characters>"}'
```

The password needs at least 12 characters and must not be a very common one; there
are no other rules. After that `POST /api/auth/login` signs in (the response holds
the CSRF token that state-changing requests send as `X-CSRF-Token`; the session
itself is an `HttpOnly` cookie).

Read [docs/operations.md](docs/operations.md) once: it covers backups, the master key
(**losing it loses your stored service credentials for good**), recovery with
`wheelhouse reset-password`, and running behind a TLS proxy
(`WHEELHOUSE_TRUSTED_PROXIES`). Something not working? [docs/troubleshooting.md](docs/troubleshooting.md).

### Adding services

Sign in as the administrator and open **Admin, Services**: pick a service (Sonarr,
Radarr, SABnzbd, qBittorrent, Plex, Jellyfin, TMDB), fill the form, press **Test
connection**, then save. The key is stored encrypted and never shown again; edit the
other fields later and leave the key box empty to keep it. A second Radarr or Sonarr
is just another entry (no variables needed). Services defined by environment
variables (`SONARR_URL`, see [Configuration](#configuration)) keep working and show
as "from environment"; **Import** moves one into the database, after which the
environment values for it are ignored. Instance addresses are trusted administrator
input: Wheelhouse refuses link-local and cloud-metadata addresses but allows your
LAN ([Known limitations](docs/release-1.0-checklist.md#known-limitations)).

### Local-network bypass (optional)

By default everybody signs in. If you want the people on your home network to use
Wheelhouse without a login, an administrator can switch on **Admin, Network** and give
the address ranges (for example `192.168.1.0/24`). Those requests then count as a
signed-in `user`: they can browse, add, delete and convert media but never open
configuration, users or secrets, and the admin pages still ask for the
administrator's login. Two things make it work or fail:

- Wheelhouse looks at the address the connection comes from. Behind a reverse proxy
  (or Docker setups that hide the client address) every request seems to come from
  the proxy, so list it under trusted proxies (`WHEELHOUSE_TRUSTED_PROXIES`) first.
  **Admin, Network** shows the address, host and range match of your own request.
- To stop attacks that make a browser on your network talk to Wheelhouse under a
  hostile name (DNS rebinding), only requests that use an IP address, `localhost` or a
  single-label name (`http://nas:3050`) get the bypass. A dotted name such as
  `nas.example.lan` must be added to the allowed host names
  (`WHEELHOUSE_ALLOWED_HOSTS`, or Admin, Network).

Details and the trade-offs: [docs/operations.md](docs/operations.md#local-network-bypass).

### Upgrading

1. Take a backup (`wheelhouse backup`, [below](#backups-and-the-master-key)) and make sure
   you have a copy of the master key.
2. Change the version tag in your compose file, unit file or `docker run` line and
   restart: `docker compose pull && docker compose up -d`. Your `/config` volume keeps
   everything; schema changes are applied on start and are forward only.
3. **There is no downgrade.** A database that a newer version has migrated is refused by
   an older one (`the database schema is newer than this version of Wheelhouse
   understands`); to go back, restore the backup you took in step 1 together with the key.
4. Coming from 0.2.x or earlier (no authentication): your installation has no users, so it
   shows setup with a token in the log and nothing else works until the administrator
   exists. There is no way to switch authentication off. `/config` must be owned by the
   container's user and **not writable by anyone else**.

Pin a version tag; there is no `latest` tag until 1.0.0.

### Running the image

One image, one process, one writable volume. Without Compose:

```sh
mkdir -p config && docker run -d --name wheelhouse --restart unless-stopped \
  -p 3050:3050 --env-file .env --user "$(id -u):$(id -g)" \
  -v "$PWD/config:/config" -v /path/to/movies:/movies -v /path/to/tv:/tv \
  tristanwl/wheelhouse:0.3.0
```

- **User.** The container runs as `1000:1000` by default and works as any
  numeric `--user UID:GID` (it needs no entry in `/etc/passwd`). The user must be
  able to **write `/config`** (it holds the database, the master key and
  `settings.json`), so create the directory yourself and `chown` it to that user:
  if Docker creates the host directory it is owned by root and the server cannot
  start. The directory must not be writable by anyone else (not `chmod 777`). To
  convert files (Codecs → Convert) it also needs write access to the media.
- **Mounts.** `/movies` and `/tv` only matter for Convert, and must be the same
  in-container paths Sonarr and Radarr use. Another layout: set
  `WHEELHOUSE_MEDIA_ROOTS` (see below) or, as an administrator, save the roots in the
  converter settings. HDR and Dolby Vision files are left out of the candidates by
  default (conversion writes SDR 8-bit); [docs/operations.md](docs/operations.md#media-roots-hdr-and-the-conversion-queue)
  explains the switch and what happens to queued jobs when the server restarts.
- **Hardware encoding** (optional): pass the GPU with `--device /dev/dri` and, if
  the render node is not accessible, `--group-add <render GID>` (`getent group
  render`). With rootless Podman use `--group-add keep-groups` instead; see the
  unit file below.
- **Hardening.** The image runs as a non-root user and works with `--read-only
  --cap-drop ALL --security-opt no-new-privileges` as long as `/config` is
  writable and `/tmp` is a tmpfs (`--tmpfs /tmp`, used for GPU driver caches);
  nothing else is written. The image cannot enforce those options, you set them:
  the example Compose file and unit file below have them on. Memory: the server
  sets its soft limit to 80% of the container's memory limit (`mem_limit`) when
  one is set, unless you set `GOMEMLIMIT` yourself.
- **Network.** It listens on all interfaces on `PORT` (3050 in the image).
  `HOSTNAME`, which runtimes set to the container id, is ignored. To bind one
  address, set `WHEELHOUSE_LISTEN`, for example `WHEELHOUSE_LISTEN=127.0.0.1:3050`
  with host networking.
- **Health.** The image has a `HEALTHCHECK` (`wheelhouse healthcheck`, which asks
  `/healthz`); no curl is involved. `wheelhouse version` prints the version.

**Podman.** `podman run` takes the same options as the `docker run` line above (`podman exec`,
`podman logs` for everything below). Rootless Podman needs `--userns=keep-id --user "$(id -u):$(id -g)"`
so the container's user can write your `./config`. For a systemd user service, see
[deploy/wheelhouse.service.example](deploy/wheelhouse.service.example).

### Behind a reverse proxy

Wheelhouse speaks plain HTTP; terminate TLS in a proxy you run (Caddy, nginx, Traefik) and
do not publish the plain port to the internet. Three settings matter, all explained with an
nginx example in [docs/operations.md](docs/operations.md#behind-a-reverse-proxy): pass the `Host`
header through unchanged, make the proxy **append** to `X-Forwarded-For`, and list the
proxy's address in `WHEELHOUSE_TRUSTED_PROXIES` so the session cookie becomes `Secure` and
the real client address is used for login limits, the audit log and the local-network bypass.

## Configuration

Everything about your services can be set in the app (**Admin, Services**), so you need
none of the service variables below. They remain supported, read-only, so an existing
`.env` keeps working; an instance saved in the database with the same id wins over the
environment one (see [Adding services](#adding-services)). The server settings are
environment variables only. This is the complete list of what the server reads
(`scripts/check-docs.py` fails CI when the code and this table drift apart):

| Variable | Default | Meaning | Kind |
|---|---|---|---|
| `PORT` | `3000` (image: `3050`) | TCP port to listen on, on all interfaces | setting |
| `WHEELHOUSE_LISTEN` | all interfaces on `PORT` | `host:port` to listen on, for example `127.0.0.1:3050`; overrides `PORT`. `HOSTNAME` is never used | setting |
| `WHEELHOUSE_CONFIG_DIR` | `/config` | Directory with the database, master key, setup token and `settings.json`. Must exist, be yours and not be writable by others | setting |
| `WHEELHOUSE_DB` | `<config dir>/wheelhouse.db` | SQLite file (accounts, sessions, audit log, stored services, queue) | setting |
| `WHEELHOUSE_SECRET_KEY_FILE` | none (`<config dir>/secret.key` is generated) | A file of 32 raw bytes (mode 0600 or 0400): the master key for secrets in the database. **Keep a copy: [key loss](docs/operations.md#key-loss)** | **secret** (a path) |
| `WHEELHOUSE_SECRET_KEY` | none | The same 32 bytes in base64. Used when no key file is set | **secret** |
| `WHEELHOUSE_TRUSTED_PROXIES` | empty | Comma-separated addresses and CIDR ranges of reverse proxies whose `X-Forwarded-Proto`, `X-Forwarded-For`, `Forwarded` and `X-Real-IP` are believed. Added to the proxies an admin stores. Empty: none trusted | read-only seed |
| `WHEELHOUSE_ALLOWED_HOSTS` | empty | Host names (`nas.example.lan`, `*.example.lan`) that may use the local-network bypass besides IP addresses, `localhost` and single-label names (DNS rebinding protection). An admin can store more | read-only seed |
| `WHEELHOUSE_MEDIA_ROOTS` | `/movies:/tv` | Folders Convert may touch, separated like `PATH`. Roots an admin saves in the app take precedence | read-only seed |
| `WHEELHOUSE_AUDIT_RETENTION_DAYS` | `180` | How long audit log entries are kept, in days: 30 to 3650 (an invalid value stops the server at start). Older entries are removed at start and daily; entries younger than 30 days can never be removed. See [operations.md](docs/operations.md#audit-log-retention) | setting |
| `WHEELHOUSE_MAX_LIBRARY_RESPONSE_MIB` | `96` | The largest list (in MiB of JSON) Wheelhouse reads from one Radarr or Sonarr: 16 to 512 (an invalid value stops the server at start). About 25,000 movies fit in the default. Raise it for a bigger library; Wheelhouse may then need more memory ([operations.md](docs/operations.md#large-libraries)) | setting |
| `WHEELHOUSE_DENIED_NETWORKS` | link-local and cloud-metadata addresses | Networks Wheelhouse refuses to connect to when it calls your services (includes `64:ff9b::/96`, `100.100.100.200`, `192.0.0.192`); `none` allows everything; private LAN ranges are always fine | setting |
| `GOMEMLIMIT` | 80% of the container memory limit, when one is set | Go's soft memory limit; set it yourself to override | setting |
| `SONARR_URL`, `SONARR_API_KEY` | none | TV library. Without it (and Radarr) Library and Codecs are hidden | read-only seed; the key is **secret** |
| `RADARR_URL`, `RADARR_API_KEY` | none | Movie library; also needed for Explore | read-only seed; the key is **secret** |
| `RADARR_<ID>_URL`, `RADARR_<ID>_API_KEY` | none | Extra instance: `RADARR_4K_URL` becomes `radarr-4k`, "Radarr 4K". Same for `SONARR_<ID>_*` | read-only seed; the key is **secret** |
| `SABNZBD_URL`, `SABNZBD_API_KEY` | none | Download client | read-only seed; the key is **secret** |
| `QBITTORRENT_URL`, `QBITTORRENT_USERNAME`, `QBITTORRENT_PASSWORD` | none | Download client (v4 and v5), with seeding. The URL's host and port must be what qBittorrent's web UI expects (a remapped Docker port makes it answer 401) | read-only seed; the password is **secret** |
| `PLEX_URL`, `PLEX_TOKEN` | none | Media server status card | read-only seed; the token is **secret** |
| `JELLYFIN_URL`, `JELLYFIN_API_KEY` | none | Media server status card; key from Dashboard, API Keys in Jellyfin. Can run next to Plex | read-only seed; the key is **secret** |
| `TMDB_API_KEY` | none | Enables Explore (with Radarr); free key from themoviedb.org | read-only seed; **secret** |
| `TMDB_BASE_URL` | `https://api.themoviedb.org/3` | TMDB API root including the version, for a caching proxy or a test double | read-only seed |

"Read-only seed" means the admin page shows the value as "from environment" and cannot edit it.
A **secret** is never shown by the app, logs or API; the file you keep it in is yours to protect
(anyone who can run `docker inspect` can read environment variables; prefer saving keys in the
app and, for the master key, a key file on a secret mount). The variable names are the same as
in 0.1.x, so an old `.env` keeps working. Variables only used by tests and development
tooling are in [CONTRIBUTING.md](CONTRIBUTING.md#environment-variables-for-development-and-tests).

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
reported as a configuration error. With the admin page you do not need this
convention: add a second instance under **Admin, Services**.

### Backups and the master key

Accounts, sessions, the audit log, your service settings, the converter settings and
the conversion queue live in a SQLite database in `/config`; stored provider secrets
are encrypted with a master key. Two commands are safe while the server runs; run them
inside the container, as the container's user:

```sh
docker exec wheelhouse wheelhouse backup /config/wheelhouse-backup.db   # never overwrites; mode 0600; copy it off the host
docker exec wheelhouse wheelhouse reset-password <user>                  # recovery: prints a one-time password
# Podman: the same with `podman exec`.
```

Two commands change the master key and **must run with the server stopped**, in a throwaway
container that mounts the same volume (a running server holds a lock on `/config` and they
refuse to run against it, because it keeps the old key in memory and would lose every
credential saved afterwards; and if the server cannot start there is nothing to `exec` into):

```sh
docker compose stop wheelhouse
docker compose run --rm wheelhouse rekey                  # new master key, re-encrypts every secret (take a backup first)
docker compose run --rm wheelhouse forget-secrets --yes   # only for a key lost for good: discards stored credentials
docker compose up -d
# Plain Docker: docker run --rm --user "$(id -u):$(id -g)" -v "$PWD/config:/config" tristanwl/wheelhouse:0.3.0 rekey
```

> **The master key is not in the backup.** The backup holds your service
> credentials only encrypted with it. If `/config/secret.key` (or the key you
> supplied) is lost, those credentials can **never** be recovered and you must
> enter every service credential again. Keep a copy of the key somewhere safe
> that is not next to your backups. Users and sessions are not affected.
> Everything about backups, the key, rotation and recovery is in
> [docs/operations.md](docs/operations.md).

The Codecs "Convert" feature reads and writes your media files directly, so
mount them at the same in-container paths Sonarr/Radarr use. Conversions
keep a `.bak` of the original and never delete it. See
[docs/architecture.md](docs/architecture.md) for how it works.

## Troubleshooting and known limits

[docs/troubleshooting.md](docs/troubleshooting.md) covers the setup token, lockouts, a lost key,
`/config` permissions, a bypass that does not work, services refused as unsafe addresses, a
database from a newer version, hardware encoding and a missing ffmpeg.
[Known limitations](docs/release-1.0-checklist.md#known-limitations) lists what Wheelhouse
deliberately does not do (no two-factor login, no API tokens, a library response cap and more).

## Development

Wheelhouse is a Go server (`server/`, standard library plus the SQLite driver and `golang.org/x/crypto` for Argon2id) that serves a Vite
single-page UI (`web/`) embedded in the binary.

```sh
# Terminal 1: the server; it keeps its database and key in server/.devconfig (git-ignored),
# or in WHEELHOUSE_CONFIG_DIR if you set one. Optional: service variables from a .env file
# (set -a; . ./.env; set +a), or add services in the app.
make -C server run          # http://localhost:3000 (a placeholder page until the UI is embedded)

# Terminal 2: the UI with hot reload, proxying /api to the server
cd web && npm ci && npm run dev    # http://localhost:5173
```

Needs Go 1.27+ and Node 22.12+ (24 recommended, see `.nvmrc`). To build the
real thing: `cd web && npm run build && npm run embed`, then `make -C server
build` gives one binary at `server/wheelhouse`. `docker build -t wheelhouse .`
builds the image. `npm ci && make -C server build && npm run test:acceptance` in
the repository root runs the black-box acceptance suite against the binary
(`acceptance/README.md`); `python3 scripts/check-docs.py` checks the documentation's links and the variable table; see [CONTRIBUTING.md](CONTRIBUTING.md) for the full
pre-PR checklist.

## Credits

This product uses the TMDB API but is not endorsed or certified by TMDB.

## License

[GPL-3.0](LICENSE).
