# Troubleshooting

Start with the log: `docker logs wheelhouse` (or `podman logs`, `docker compose logs
wheelhouse`, `journalctl --user -u container-wheelhouse`). A server that cannot start says
why in one `level=ERROR msg=fatal` line, and the text below is that text. Commands that
need the server stopped run in a throwaway container, see [operations.md](operations.md).

| Symptom | Section |
|---|---|
| The setup page asks for a token I do not have | [Setup token](#the-setup-token-is-not-found) |
| "invalid username or password" and I know it is right | [Locked out](#i-am-locked-out-or-forgot-the-password) |
| The server stops with "the secret key does not match this database" | [Lost key](#the-key-is-lost) |
| The server will not start: directory missing, writable by everyone, permission denied | [/config permissions](#config-permissions) |
| People on my network still get a login page | [Bypass](#the-local-network-bypass-does-not-work) |
| A service shows "address not allowed" | [Refused addresses](#a-service-address-is-refused) |
| "the database schema is newer than this version" | [Newer database](#the-database-is-newer-than-the-binary) |
| Convert is slow or hardware encoding does not work (arm64) | [Hardware encoding](#hardware-encoding-does-not-work-or-is-missing-on-arm64) |
| Codecs says ffmpeg is missing | [ffmpeg](#codecs-says-ffmpeg-is-missing) |
| Everything is 403 or I get logged out behind a proxy | [Reverse proxy](#behind-a-reverse-proxy-403-errors-and-logouts) |

## The setup token is not found

The token exists only while no administrator does. It is logged once per start
(`setup_token=` in the log line that begins `setup required`) and written to
`/config/setup-token`.

- Read it from the file: `docker exec wheelhouse cat /config/setup-token`.
- The log was rotated or you only see old lines: **restart the container**. Every start
  without users makes a new token and the old one stops working.
- The file is missing and the log says nothing: an administrator already exists (setup is
  over; sign in), or the server is a different one from the one you are looking at.
- "invalid setup token" at the wizard: you pasted a token from an earlier start, or a
  space or line break came along. Several wrong tries are throttled; wait a minute.
- If you cannot read the file because `/config` is not yours, fix the permissions first
  ([below](#config-permissions)).

## I am locked out, or forgot the password

- Five wrong passwords lock **that account** for 30 seconds, doubling with every further
  failure up to 15 minutes; twenty failures lock the **address** the same way. A locked login
  answers like a wrong one ("invalid username or password"), so you cannot tell: wait and
  try once. The counters are in memory and a restart clears them.
- Behind a reverse proxy that is not in `WHEELHOUSE_TRUSTED_PROXIES` every visitor has the
  proxy's address, so one person's typos lock everyone out. Add the proxy to the trusted list.
- Another administrator can reset your password (Admin, Users). If you are the only
  administrator: `docker exec wheelhouse wheelhouse reset-password <username>` prints a
  one-time password, ends that user's sessions, lifts the lockout and asks for a new
  password at the next login.
- The username is not case sensitive. Reserved names (`cli`, `local-network`, `system`, `anonymous`) cannot
  be used as accounts.

## The key is lost

At start the server stops with `the secret key does not match this database (a different key
file, or a restored database without its key)`. Look for the key first: `secret.key` in your
config directory, a password manager, the file `WHEELHOUSE_SECRET_KEY_FILE` points at. If it is
gone for good, stored service credentials cannot be recovered; users and sessions are fine:

```sh
docker compose stop wheelhouse
docker compose run --rm wheelhouse forget-secrets --yes   # discards stored API keys and passwords
docker compose up -d
```

Then add your services' keys again under Admin, Services. If you restored a **database
backup** and the key came from a different time, restore the key that matches that backup
instead. Details: [operations.md](operations.md#key-loss).

## /config permissions

| Message | Cause and fix |
|---|---|
| `the directory /config does not exist (is the config volume mounted?)` | The volume is not mounted, or the host directory was never created. Create it (`mkdir config`) and mount it at `/config`. |
| `the directory ... is writable by everyone (mode 0777); ... run: chmod o-w ...` | Someone ran `chmod 777`. Use `chmod 755` (or 700) and `chown` it to the container's user. Wheelhouse refuses a directory anyone can write to, because that would let them replace the database or the key. |
| `database: open .../wheelhouse.db: permission denied` | The container's user cannot write the directory. Docker creates a missing host directory as root: `sudo chown -R 1000:1000 config` (or your `--user`). With rootless Podman use `--userns=keep-id --user "$(id -u):$(id -g)"`. With SELinux add `:Z` to the volume (`-v ./config:/config:Z`). |
| a refused `secret.key` or database file (group or others can read it) | `chmod 600` the file. Wheelhouse will not use a key that others can read. |
| the setup page works but saving codec settings says the volume must be writable | The same permission problem, found later: `settings.json` could not be written. |

The image runs as `1000:1000` unless you pass `--user`; whatever user you choose must own
`/config`. Do not make the root filesystem writable to work around this: nothing else is written
(except `/tmp`).

## The local-network bypass does not work

You enabled **Admin, Network** with your range and people on the LAN still see the login page.
Open **Admin, Network** while signed in as administrator: it shows how Wheelhouse sees *your*
request (client address, whether it is in range, the host name used, whether that host is
allowed). Then:

1. **The address is not the one you expect.** Docker's userland proxy, rootless Podman, a
   `network_mode: bridge` setup reached from the same host, and any reverse proxy make the
   client look like the proxy or the Docker gateway (for example `172.17.0.1`). Either include
   that address in the range deliberately, or, for a reverse proxy, add the proxy to the trusted
   proxies so the forwarded address is used (the proxy must append to `X-Forwarded-For`).
2. **Host allow-list.** The bypass is only granted when the request's `Host` is an IP address,
   `localhost` or a single-label name (`nas`). `http://wheelhouse.example.lan:3050` is refused
   until you add that name (Allowed host names, or `WHEELHOUSE_ALLOWED_HOSTS=wheelhouse.example.lan`).
   Reaching it by IP address always works. This is the protection against DNS rebinding.
3. **Proxy headers.** Headers such as `X-Forwarded-For` are ignored unless the TCP peer is in
   the trusted proxy list. If the three headers (`X-Forwarded-For`, `Forwarded`, `X-Real-IP`)
   disagree, the client counts as unknown and gets no bypass.
4. **Stale tabs.** A signed-in session is never replaced by the bypass; sign out, or open a
   private window, to see what a LAN visitor sees.
5. The bypass never grants administrator rights and never covers setup: the admin pages always
   ask for the administrator's login.

## A service address is refused

A service whose status says `address not allowed (link-local and cloud-metadata addresses are
refused)` points at a range Wheelhouse will not connect to by default: link-local addresses
(`169.254.0.0/16`, `fe80::/10`), cloud metadata addresses and the NAT64 prefix. This stops an
administrator from using Wheelhouse to read cloud credentials. LAN and loopback ranges are fine.
A media server that really lives on a link-local address can be allowed with
`WHEELHOUSE_DENIED_NETWORKS` (a comma-separated list replaces the defaults; `none` allows
everything), which you set deliberately, in the server's environment.

Related messages: `must be an http or https URL` (include `http://`), `must not contain a username
or password` and `must not contain a query string or a fragment` (put keys in the key field, not in
the URL), `redirected to another host (not followed)` (use the address the service itself uses),
`must be entered again when the URL changes` (when you change a service's address, its stored key
is not sent to the new address: type the key again).

## The database is newer than the binary

`the database schema is newer than this version of Wheelhouse understands (database is at version
N, this binary knows M)`: a newer Wheelhouse migrated `wheelhouse.db` and you started an older one
(or an old image tag). Start the newer version again. To really go back, restore a backup made
before the upgrade **and** its master key; downgrades are not supported. A database whose earlier
migration history was changed is refused the same way.

## Hardware encoding does not work, or is missing on arm64

- The image contains ffmpeg and the Intel VAAPI drivers on amd64. On **arm64 the Intel drivers
  are not included and hardware encoding is untested**; software x264/x265 conversion works.
  The Convert dialog (Codecs page) offers "VAAPI (hardware)" or "NVENC (hardware)" only when it was detected, and "Software" always.
- Pass the device (`--device /dev/dri`) and give the container user access to the render node
  (`--group-add $(getent group render | cut -d: -f3)`; rootless Podman: `--group-add keep-groups`).
- Mount a tmpfs at `/tmp` when the root filesystem is read-only (drivers cache there).
- Software encoding is much slower and uses every core: that is expected.

## Codecs says ffmpeg is missing

The page lists what is missing ("ffmpeg"). The Docker image always includes ffmpeg. If you run
the binary yourself, install ffmpeg and ffprobe and make sure they are on the server's `PATH`
(restart afterwards; the check is made once). Without it Library and the rest still work.

## Behind a reverse proxy: 403 errors and logouts

- `cross-origin request refused`: the proxy changes the `Host` header. Pass it through
  (`proxy_set_header Host $host;`); Wheelhouse compares it with `Origin`.
- The session cookie is not `Secure` over HTTPS, or the audit log shows the proxy's address:
  put the proxy in `WHEELHOUSE_TRUSTED_PROXIES` so `X-Forwarded-Proto` and `X-Forwarded-For` are
  believed. Check the proxy appends to `X-Forwarded-For`
  ([operations.md](operations.md#behind-a-reverse-proxy)).
- Logged out after a few hours or days: sessions end after 12 hours idle and 30 days in total,
  by design.

## Still stuck

Open an issue (not for security problems: see [SECURITY.md](../SECURITY.md)) with the version
(`wheelhouse version`), how you run it, and the relevant log lines. Remove keys, tokens and
addresses you do not want public first; Wheelhouse itself never logs them.
