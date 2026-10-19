# Operating Wheelhouse

What to back up, what to protect, and how to recover. The commands run as the
container's user; being able to run them is the proof that you own the server. Two
ways to run one, depending on whether the server is up:

- **Server running** (`backup`, `reset-password`; the others refuse): `docker exec wheelhouse wheelhouse <command>`
  (or `podman exec`).
- **Server stopped or unable to start** (`rekey`, `forget-secrets`, and anything after a
  failed start, because there is no container to `exec` into): a throwaway container on
  the same volume. With Compose: `docker compose stop wheelhouse`, then
  `docker compose run --rm wheelhouse <command>`, then `docker compose up -d`. With plain
  Docker: `docker run --rm --user "$(id -u):$(id -g)" -v "$PWD/config:/config"
  tristanwl/wheelhouse:<version> <command>` (add `--env-file`/`-e` for a key supplied by
  environment variable or `-v` for a key file). Podman: the same with `podman`, plus
  `--userns=keep-id` when rootless.

`rekey` and `forget-secrets` change the key the database is sealed with, so they **refuse to
run while the server is running** (see [Locking](#locking)): the server keeps the old key in
memory and would seal anything saved afterwards with a key the database no longer trusts.

## Locking

While it runs, the server holds an exclusive advisory lock (`flock`) on
`/config/wheelhouse.lock`. `rekey` and `forget-secrets` take the same lock before they
touch anything and stop with a message when they cannot (`refusing to run: a Wheelhouse
server is running on ...`): stop the container first, or run the command in a throwaway
container on the same volume while the server is stopped (above). A second server on the
same `/config` is refused for the same reason. `backup` and `reset-password` do not need
the lock and keep working while the server runs (`backup` uses SQLite's online backup).

- **A stale lock file is harmless.** The lock belongs to the open file, not to the file's
  existence or contents, so the kernel releases it when the server dies for any reason
  (clean stop, crash, `kill -9`, the container removed). `wheelhouse.lock` stays in the
  directory with the last PID in it, which is only a hint for the refusal message. Never
  delete it to "unlock" anything; there is nothing to unlock.
- **It works across containers** that mount the same local directory or volume (the lock is
  in the host kernel), which is exactly the `docker compose run --rm` case.
- **Network filesystems.** On NFS (v3 with lockd, v4) Linux forwards `flock` to the server,
  and it works when the NFS locking service does. On a share or mount that cannot lock
  (some SMB/CIFS, FUSE and older NFS setups) the server starts anyway and logs a warning
  (`this server does not hold the configuration lock`), and the commands print a warning:
  there the lock is not protecting you, and "stop the server first" is on you. Keep `/config`
  on a local disk or volume, which SQLite's WAL mode needs anyway.
- **Defence in depth.** If the key in the database changes under a running server anyway
  (a lock that did not work, someone editing the database), the server notices on the next
  save: it refuses to save, answers `503` with "restart Wheelhouse", logs that the secret key
  changed while it was running, and shows the affected services as needing a restart instead
  of "required". Restarting loads the new key.

## Key loss

> **KEY LOSS: read this before you rely on backups.**
>
> **The master key is not in your backups.** `wheelhouse backup` copies the
> database, and the database holds your service credentials (API keys, tokens,
> qBittorrent and other passwords) only *encrypted* with the master key.
>
> **If the key is lost, those credentials can never be recovered**, by anyone,
> from the database or from a backup. The server will not start against a
> database whose key does not match, and the only way forward is to discard the
> stored credentials and **enter every service credential again**.
>
> The key is `/config/secret.key` (generated at first start), or the file or
> environment value you supplied with `WHEELHOUSE_SECRET_KEY_FILE` or
> `WHEELHOUSE_SECRET_KEY`. **Keep a copy of it somewhere safe that is not your
> backup**: a password manager, or a secret store. A backup that sits next to its
> key protects nothing if the disk is stolen; a key kept only on the disk it
> protects is lost with that disk.
>
> Users and sessions are *not* affected by a lost key: passwords are stored as
> Argon2id hashes and session tokens as SHA-256 hashes, never encrypted, so
> nothing about accounts depends on the key (a test, `TestAccountsNeverDependOnTheMasterKey`,
> keeps that true). Only the stored service credentials are lost.

## What to back up

| Item | Where | In `wheelhouse backup`? |
|---|---|---|
| Accounts, sessions, audit log, stored settings | `/config/wheelhouse.db` | yes |
| Service credentials (encrypted) | inside the database | yes, encrypted |
| **Master key** | `/config/secret.key`, or your key file or secret | **no** |
| Codec preferences | `/config/settings.json` | no (copy the file) |
| Media roots, HDR switch, conversion job history | inside the database | yes |
| Your `.env` / compose file (service URLs and keys) | wherever you keep it | no |

Checklist:

1. Run `wheelhouse backup <file>` on a schedule (below) and copy the result off the host.
2. Put a copy of **the master key** somewhere separate from those backups (a
   password manager entry works: `base64 -w0 /config/secret.key` is a text form).
3. Keep `settings.json` and your environment file.
4. Know the recovery paths below before you need them.

## Backups

```sh
docker exec wheelhouse wheelhouse backup /config/wheelhouse-backup.db
```

The backup is a consistent snapshot taken while the server runs, written with
mode 0600, and it never overwrites an existing file (use a new name each time,
for example with the date). The command's output repeats the key warning above.
Copy the file out of `/config` (`docker cp wheelhouse:/config/wheelhouse-backup.db .`, or
read it from the host directory you mounted), and delete it from `/config` once it is safe
elsewhere. To restore: stop Wheelhouse, copy the backup over
`wheelhouse.db` (delete a leftover `wheelhouse.db-wal` and `-shm`), put the
**matching master key** back, and start it. A backup restored with a different
key is the key-loss case below.

## The master key

- **Where.** In order: `WHEELHOUSE_SECRET_KEY_FILE` (a file of exactly 32 raw
  bytes, mode `0600` or `0400`), `WHEELHOUSE_SECRET_KEY` (the same 32 bytes in
  base64), or `/config/secret.key`, which Wheelhouse generates on its first start
  (mode 0600). The first one that is set decides; a set but unusable one is an
  error, never a silent fall-through.
- **How to keep it.** Mode 0600 or 0400, owned by the user the container runs as.
  Wheelhouse refuses a key file that group or others can read. Best: store the key
  in a password manager and supply it as a file from a secret mount
  (`WHEELHOUSE_SECRET_KEY_FILE`), so it is not on the same volume as the
  database. With the generated `secret.key` beside the database, the key protects
  a copied database or backup but **not** a copy of the whole `/config` volume;
  see the [threat model](threat-model.md).
- **Make one yourself:** `head -c 32 /dev/urandom > wheelhouse.key && chmod 600 wheelhouse.key`.
- **Rotate it** (for example after it may have leaked): stop Wheelhouse, take a
  backup, run `wheelhouse rekey` (in a throwaway container, see above). It makes a new key and re-encrypts every stored
  secret in one transaction; any failure leaves the old key in force. For a
  generated key it replaces `secret.key` itself; for a key you supply, use
  `wheelhouse rekey --new-key-out /config/new.key`, install that file as your key,
  and delete it. **Old backups need the old key**, so keep it as long as you keep
  the backups that need it.

### If the key is lost

1. Look first: `secret.key` in your `/config` volume, your password manager, the
   file your `WHEELHOUSE_SECRET_KEY_FILE` points at, a copy next to an old backup.
   If you find it, put it back and start Wheelhouse. Nothing is lost.
2. If it is gone for good, Wheelhouse stops at start with
   `the secret key does not match this database` and tells you what to do. Run
   `wheelhouse forget-secrets --yes` in a throwaway container (see above) with the
   key you now have configured, or the one Wheelhouse just generated. It discards every stored service
   credential, makes the current key the database's key, and refuses to run if the
   current key actually works. It does not touch users, sessions or the audit
   log. Start Wheelhouse and enter your service credentials again.
3. This is why the key needs its own backup.

## Recovering access

- **First start, or an upgrade from 0.2.x.** With no accounts, Wheelhouse serves
  only the setup page and the API's setup and health routes. It generates a
  one-time **setup token**, prints it once in the log (`setup_token=...`) and
  writes it to `/config/setup-token` (mode 0600). Creating the administrator
  needs it. The file is deleted when setup succeeds, and a new token is made at
  every start while no account exists, so an old log line is useless.
  `docker logs wheelhouse 2>&1 | grep setup_token` or `docker exec wheelhouse cat /config/setup-token`.
  **Anyone who can read the logs (or the config directory) before setup
  completes can claim the instance**, so do not expose the port to untrusted
  networks, and do not ship logs of a fresh start to a shared system, until you
  have created the administrator. The example Compose file publishes the port on
  `127.0.0.1` only for this reason.
- **A forgotten or locked-out password.** Another administrator can reset it
  (`POST /api/users/{id}/password`). If you have no other administrator:
  `docker exec wheelhouse wheelhouse reset-password <username>` prints a
  generated one-time password **once**, ends that user's sessions, lifts any
  login lockout and makes the account choose a new password at the next login.
  The reset is recorded in the audit log as done by `cli`.
- **Login lockouts.** Five wrong passwords lock an account for 30 seconds,
  doubling with each further failure up to 15 minutes; twenty failures lock the
  client address in the same way. The counters are in memory, so a restart clears
  them. A locked login answers exactly like a wrong password: you cannot tell
  that you are locked, you just wait. `reset-password` also lifts it.

## The `/config` directory

- It must be owned by the user the container runs as, and **must not be writable
  by anyone else** (no `chmod 777`). Wheelhouse refuses to start when the
  directory is world-writable, because anyone who can write there can replace the
  database or the key. Use `chown` and mode `0700` or `0755`.
- It holds `wheelhouse.db` (and `-wal`, `-shm` while running), `secret.key`,
  `settings.json` and, until setup, `setup-token`. The database and the key are
  created with mode 0600 and refused if group or others can read them.
- The image makes `/config` owned by `1000:1000`. For another `--user`, `chown`
  the host directory (or volume) to that user.

## Behind a reverse proxy

Wheelhouse serves plain HTTP; terminate TLS at a proxy you run. The session cookie
is `Secure` (and named `__Host-wheelhouse`) only when the request arrived over
HTTPS. Behind a proxy that means telling Wheelhouse which proxy to believe:

```
WHEELHOUSE_TRUSTED_PROXIES=192.0.2.10,198.51.100.0/24
```

A comma-separated list of addresses and CIDR ranges. A request from one of them
that says `X-Forwarded-Proto: https` is treated as HTTPS; the header from anyone
else is ignored. The default is empty (nothing trusted). The proxy must also pass
the `Host` header through unchanged: the cross-site check compares `Origin` with
it. Do not expose the plain port to the internet.

An nginx server block that does all of this:

```nginx
location / {
    proxy_pass http://127.0.0.1:3050;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
}
```

`$proxy_add_x_forwarded_for` appends the address nginx saw to whatever the client
sent. **A proxy that sets or passes the header without appending makes
client-supplied values trusted**: a client could then write any address into
`X-Forwarded-For` and pick its own identity in the audit log, dodge the per-address
login limit, or look like a machine inside the local-network bypass range. Check
what your proxy does before adding it to the trusted list (Caddy and Traefik
append by default; with nginx you must write the line above). Wheelhouse sends
`Strict-Transport-Security: max-age=31536000` only on requests that arrive over
HTTPS (directly, or from a trusted proxy that says `X-Forwarded-Proto: https`).

The same list decides whose `X-Forwarded-For`, `Forwarded` and `X-Real-IP` are
believed for the client address (audit log, login throttling, the local-network
bypass). An administrator can store more proxies in the database
(`PUT /api/admin/network`); the two lists are added together, and the environment
entries cannot be edited there. Only the right-most address that is not itself a
trusted proxy is used, so make the proxy append to (not replace) the chain it
received, and send consistent headers: if `X-Forwarded-For`, `Forwarded` and
`X-Real-IP` name different clients, Wheelhouse treats the client as unknown.

**Login limits and a shared address.** Failed logins are counted per account (5
before a lock) and per client address (20 before a lock; an IPv6 client counts as
its /64). Behind a reverse proxy that is not in the trusted list every client has
the proxy's address, so twenty wrong passwords from anyone lock everyone out of
logging in from there for 30 seconds and up. If many people share one address (a
NAT, or a proxy), add the proxy to the trusted proxies so the real client address
is used.

## Local-network bypass

Off by default. An administrator can let people on the home network use the app
without logging in: `PUT /api/admin/network` with `{"enabled": true, "cidrs":
["192.168.1.0/24"], "trustedProxies": []}` (or use Admin, Network in the UI). A request from inside a range is a signed-in `user`: it can browse, add,
delete and convert media, never open configuration, users or secrets, and the
admin pages still ask for the administrator's login. Ranges wider than private
address space are allowed but returned as `warnings`; `0.0.0.0/0` and `::/0` are
refused. **Host names:** a web page on the internet can make a browser on your network
send requests that come from inside your range but name the attacker's own domain
in `Host` and `Origin` (DNS rebinding), which would look like a normal same-origin
request. So the bypass is only granted to requests whose `Host` is an IP address,
`localhost`, a single-label name (`nas`), or a name on the allowed host names list
(`allowedHosts` in `PUT /api/admin/network`, Admin, Network, "Allowed host names";
up to 64 names, `*.example.lan` allowed as a leading wildcard label, `*` and bare
top-level domains refused; the environment variable `WHEELHOUSE_ALLOWED_HOSTS`, a
comma-separated list, adds read-only entries). When you turn the bypass on, the host
name you used to reach the admin page is added for you (`addedHost` in the response).
Anyone who opens the app by another name from inside the range (for example
`http://nas.example.lan:3050` before you listed it) is asked to sign in as usual.
Signed-in sessions are not affected. **Upgrade note:** an installation that already
had the bypass on and is reached by a dotted host name must add that name once. Behind a reverse proxy, list the proxy under trusted proxies first, or
every request will look like it comes from the proxy. Scripts that used the 0.2.x
API with no credentials must call `GET /api/auth/session` first and send the
returned `csrfToken` as `X-CSRF-Token` on POST, PUT, PATCH and DELETE. See the
threat model for what this trades away.

## Media roots, HDR and the conversion queue

**Media roots.** Convert only touches files inside the media roots. An
administrator sets them with `PUT /api/admin/converter` (`{"mediaRoots":
["/movies", "/tv"], "includeHdr": false}`; or Admin, Media and converter in the UI);
`GET` shows `effectiveRoots` and the `source`: `database` (saved by an
administrator), `environment` (`WHEELHOUSE_MEDIA_ROOTS`, read-only) or `default`
(`/movies` and `/tv`). The database wins; an empty `mediaRoots` removes the saved
roots and the environment or default applies again. A change applies at once. On
save each root must be an absolute path without `..`, must exist and be a
directory inside the container (mount it first), must not be `/`, a system folder
(`/etc`, `/proc`, `/sys`, `/dev`, `/root`, `/boot`, `/bin`, `/sbin`, `/usr`,
`/lib*`, `/run`, `/var/lib`) or the config directory, and must not be the same
folder as, or inside or around, another root (at most 32). A job already running
keeps going, but its file is checked against the current roots again before the
swap: take its folder out of the roots and it fails without touching the file.
The paths Radarr and Sonarr report must still resolve inside a root.

**HDR and Dolby Vision.** Conversion writes SDR 8-bit video and does not tone map,
so converting an HDR10, HLG or Dolby Vision file would lose its HDR and look grey
on an HDR display. Such files are therefore left out of the candidates (the page
says how many). With `includeHdr` on, they appear marked as HDR and converting
one needs an explicit acknowledgement; the server decides from the file itself, so
a file Radarr reports as SDR that is not, or one whose HDR cannot be determined
(`unknown`, for example ffprobe fails), is treated the same way. The original is
kept as `.bak` as for every conversion. A backup is never overwritten: converting the same file again keeps the earlier original and uses `.bak.1`, `.bak.2` and so on (the job shows the exact name). Nothing deletes backups; clear them out yourself when you are sure.

**The queue after a restart.** Jobs are saved in the database. When the server
starts (also after a graceful stop): a job that was running becomes `failed` with
`interrupted: the server stopped while this conversion was running` (it is not
resumed; start it again from the page) and its partial file is removed, a swap
that died halfway is repaired (only a swap the server had recorded in its database; a `.bak` file it did not record is never renamed back), jobs still `queued` stay queued and
run in order, and finished jobs are listed for 30 days, at most the newest 200
(`converter.RetainFinishedFor` and `RetainFinishedMax`). The job list shows who
started each job (a user, `local-network` for the bypass, or `cli`). Starting,
cancelling and finishing (including an interruption found at start) are in the
audit log as `conversion.start`, `conversion.cancel` and `conversion.finish`.

## Where things are logged

The access log has method, path, status, size, duration, client address and
request id: no query strings, headers or bodies. Authentication events (setup,
logins, failures, lockouts, logouts, password changes and resets, user changes,
session revocations, library deletes and conversions) are in the audit log, which
administrators read through `GET /api/audit`, with the acting user and address and
never a password, token or request body. The one secret that is logged is the
setup token, once at start, while no account exists.
