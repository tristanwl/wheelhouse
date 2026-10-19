#!/bin/sh
# Smoke test for the container image: how it is actually run.
#
#   acceptance/docker-smoke.sh wheelhouse:test
#
# Starts the image with NO environment, as an arbitrary user, with a read-only
# root filesystem, all capabilities dropped, no-new-privileges, a tmpfs /tmp and
# a /config volume owned by that user (a tmpfs with that owner and mode 0700: the
# server refuses a config directory anyone can write to), and checks that
#   - `version` prints a real version,
#   - the container becomes healthy (the image's HEALTHCHECK, `wheelhouse healthcheck`),
#   - the process runs as the requested uid/gid and the root filesystem is read-only,
#   - a fresh container serves the pages and health and nothing else (503 setup
#     required) until the administrator is created with the one-time setup token,
#     after which the API needs a login (401), and with one every route answers 200
#     and only the Dashboard is enabled,
#   - a runtime-set HOSTNAME (the container id) does not break the listen address,
#   - the database, the master key and settings.json are written to /config with
#     the requested owner (the first two mode 0600), the setup token file is gone,
#   - `wheelhouse reset-password` works inside the container,
#   - SIGTERM stops it cleanly (exit code 0).
# SMOKE_HEALTH_ATTEMPTS (default 40, half a second each) is how long to wait for
# the container to become healthy; CI raises it for the emulated arm64 image.
# The full black-box suite is run against the image separately, see
# acceptance/README.md ("Against a container image").
set -eu

image="${1:?usage: docker-smoke.sh IMAGE}"
uid=4242
gid=4343
name="wheelhouse-smoke-$$"
work=""

fail() {
	echo "FAIL: $*" >&2
	docker logs "$name" >&2 2>&1 || true
	exit 1
}
cleanup() {
	docker rm -f "$name" >/dev/null 2>&1 || true
	[ -z "$work" ] || rm -rf "$work" 2>/dev/null || true
}
trap cleanup EXIT

version="$(docker run --rm "$image" version)"
echo "version: $version"
case "$version" in "wheelhouse "?*) [ "$version" != "wheelhouse dev" ] || fail "version was not stamped" ;; *) fail "unexpected version output: $version" ;; esac

# The volume must belong to the user chosen at run time, and must not be
# writable by anyone else: that is the one thing the operator has to arrange
# (docs: "Running the image"). A tmpfs with that owner stands in for the volume;
# its files are read with `docker exec`.
work="$(mktemp -d)"

docker run -d --name "$name" \
	--user "$uid:$gid" \
	--read-only --cap-drop ALL --security-opt no-new-privileges \
	--tmpfs /tmp \
	--tmpfs "/config:rw,uid=$uid,gid=$gid,mode=0700" \
	--health-interval 1s --health-start-period 0s --health-retries 240 \
	-p 127.0.0.1::3050 \
	"$image" >/dev/null

i=0
until [ "$(docker inspect -f '{{.State.Health.Status}}' "$name")" = healthy ]; do
	i=$((i + 1))
	[ "$i" -le "${SMOKE_HEALTH_ATTEMPTS:-40}" ] || fail "container did not become healthy"
	[ "$(docker inspect -f '{{.State.Running}}' "$name")" = true ] || fail "container exited"
	sleep 0.5
done
echo "healthy after ~$((i / 2))s"

addr="$(docker port "$name" 3050/tcp | head -n 1)"
base="http://$addr"

[ "$(docker exec "$name" id -u):$(docker exec "$name" id -g)" = "$uid:$gid" ] || fail "not running as $uid:$gid"
echo "runs as $uid:$gid"
if docker exec "$name" touch /rootfs-must-be-read-only 2>/dev/null; then fail "root filesystem is writable"; fi
echo "root filesystem is read-only"

for path in / /library /explore /explore/popular /codecs /healthz; do
	code="$(curl -s -o /dev/null -w '%{http_code}' "$base$path")"
	[ "$code" = 200 ] || fail "GET $path answered $code"
done
echo "all pages and the health route answer 200"

# A fresh container serves nothing else until the administrator exists.
for path in /api/features /api/status /api/downloads /api/library /api/storage; do
	code="$(curl -s -o /dev/null -w '%{http_code}' "$base$path")"
	[ "$code" = 503 ] || fail "GET $path answered $code before setup (want 503 setup required)"
done
[ "$(curl -s "$base/api/auth/setup")" = '{"setupRequired":true}' ] || fail "setup status"
echo "only setup is served before the administrator exists"

incfg() { docker exec "$name" "$@"; }
token_file=/config/setup-token
incfg test -s "$token_file" || fail "no setup token in /config"
[ "$(incfg stat -c %a "$token_file")" = 600 ] || fail "setup-token mode is $(incfg stat -c %a "$token_file")"
token="$(incfg cat "$token_file")"
password="smoke-test-passphrase-0001"
code="$(curl -s -o /dev/null -w '%{http_code}' -X POST -H 'content-type: application/json' \
	-d "{\"token\":\"wrong\",\"username\":\"admin\",\"password\":\"$password\"}" "$base/api/auth/setup")"
[ "$code" = 403 ] || fail "setup with a wrong token answered $code"
code="$(curl -s -o /dev/null -w '%{http_code}' -X POST -H 'content-type: application/json' \
	-d "{\"token\":\"$token\",\"username\":\"admin\",\"password\":\"$password\"}" "$base/api/auth/setup")"
[ "$code" = 201 ] || fail "setup answered $code"
! incfg test -e "$token_file" || fail "the setup token file is still there after setup"
echo "setup needs the token, creates the administrator and removes the token file"

[ "$(curl -s -o /dev/null -w '%{http_code}' "$base/api/status")" = 401 ] || fail "an unauthenticated API call is not 401"
jar="$work/cookies.txt"
login="$(curl -s -c "$jar" -X POST -H 'content-type: application/json' \
	-d "{\"username\":\"admin\",\"password\":\"$password\"}" "$base/api/auth/login")"
csrf="$(echo "$login" | sed -n 's/.*"csrfToken":"\([^"]*\)".*/\1/p')"
[ -n "$csrf" ] || fail "login failed: $login"
grep -q 'HttpOnly' "$jar" || fail "the session cookie is not HttpOnly"
echo "login works"

for path in /api/features /api/status /api/downloads /api/library /api/storage; do
	code="$(curl -s -b "$jar" -o /dev/null -w '%{http_code}' "$base$path")"
	[ "$code" = 200 ] || fail "GET $path answered $code"
done
echo "all API routes answer 200 once signed in"

features="$(curl -s -b "$jar" "$base/api/features")"
enabled="$(echo "$features" | grep -o '"id":"[a-z]*","[^}]*"available":true' | grep -o '"id":"[a-z]*"' | tr -d '\n')"
[ "$enabled" = '"id":"dashboard"' ] || fail "features enabled with no environment: $enabled ($features)"
echo "only the Dashboard is enabled"

# A mutation without the CSRF token is refused; with it, it works.
code="$(curl -s -b "$jar" -o /dev/null -w '%{http_code}' -X PUT -H 'content-type: application/json' \
	-d '{"hd":"hevc","uhd":"h264"}' "$base/api/codec-analysis/settings")"
[ "$code" = 403 ] || fail "a mutation without the CSRF token answered $code"
curl -s -b "$jar" -X PUT -H 'content-type: application/json' -H "x-csrf-token: $csrf" -d '{"hd":"hevc","uhd":"h264"}' \
	"$base/api/codec-analysis/settings" | grep -q '"hd":"hevc"' || fail "settings were not saved"
incfg test -s /config/settings.json || fail "settings.json missing in /config"
for f in settings.json wheelhouse.db secret.key; do
	[ "$(incfg stat -c %u:%g "/config/$f")" = "$uid:$gid" ] || fail "$f owner is $(incfg stat -c %u:%g "/config/$f")"
done
for f in wheelhouse.db secret.key; do
	[ "$(incfg stat -c %a "/config/$f")" = 600 ] || fail "$f mode is $(incfg stat -c %a "/config/$f")"
done
echo "the database, the master key (mode 0600) and settings.json are in /config as $uid:$gid"

out="$(docker exec "$name" wheelhouse reset-password admin)" || fail "reset-password failed"
echo "$out" | grep -q 'one-time password' || fail "reset-password printed: $out"
[ "$(curl -s -b "$jar" -o /dev/null -w '%{http_code}' "$base/api/status")" = 401 ] || fail "the session survived reset-password"
echo "reset-password works inside the container"

docker stop --time 10 "$name" >/dev/null
[ "$(docker inspect -f '{{.State.ExitCode}}' "$name")" = 0 ] || fail "unclean shutdown (exit code $(docker inspect -f '{{.State.ExitCode}}' "$name"))"
echo "SIGTERM stops it cleanly"
echo "OK"
