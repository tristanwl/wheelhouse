# Operating Wheelhouse

What to back up, what to protect, and how to recover. Everything here runs inside
the container as the container's user (`docker exec wheelhouse wheelhouse ...`,
or `podman exec`); being able to do that is the proof that you own the server.

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
Copy the file out of `/config`. To restore: stop Wheelhouse, copy the backup over
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
  backup, run `wheelhouse rekey`. It makes a new key and re-encrypts every stored
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
   `wheelhouse forget-secrets --yes` (with the key you now have configured, or
   the one Wheelhouse just generated). It discards every stored service
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

## Where things are logged

The access log has method, path, status, size, duration, client address and
request id: no query strings, headers or bodies. Authentication events (setup,
logins, failures, lockouts, logouts, password changes and resets, user changes,
session revocations, library deletes and conversions) are in the audit log, which
administrators read through `GET /api/audit`, with the acting user and address and
never a password, token or request body. The one secret that is logged is the
setup token, once at start, while no account exists.
