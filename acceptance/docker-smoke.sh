#!/bin/sh
# Smoke test for the container image: how it is actually run.
#
#   acceptance/docker-smoke.sh wheelhouse:test
#
# Starts the image with NO environment, as an arbitrary user, with a read-only
# root filesystem, all capabilities dropped, no-new-privileges, a tmpfs /tmp and
# a writable /config volume, and checks that
#   - `version` prints a real version,
#   - the container becomes healthy (the image's HEALTHCHECK, `wheelhouse healthcheck`),
#   - the process runs as the requested uid/gid and the root filesystem is read-only,
#   - every page and the health route answer 200 and only the Dashboard is enabled,
#   - a runtime-set HOSTNAME (the container id) does not break the listen address,
#   - settings.json is written to /config with the requested owner,
#   - SIGTERM stops it cleanly (exit code 0).
# The full black-box suite is run against the image separately, see
# acceptance/README.md ("Against a container image").
set -eu

image="${1:?usage: docker-smoke.sh IMAGE}"
uid=4242
gid=4343
name="wheelhouse-smoke-$$"
config=""

fail() {
	echo "FAIL: $*" >&2
	docker logs "$name" >&2 2>&1 || true
	exit 1
}
cleanup() {
	docker rm -f "$name" >/dev/null 2>&1 || true
	[ -z "$config" ] || rm -rf "$config" 2>/dev/null || true
}
trap cleanup EXIT

version="$(docker run --rm "$image" version)"
echo "version: $version"
case "$version" in "wheelhouse "?*) [ "$version" != "wheelhouse dev" ] || fail "version was not stamped" ;; *) fail "unexpected version output: $version" ;; esac

# The volume must be writable by the user chosen at run time: that is the one
# thing the operator has to arrange (docs: "Running the image").
config="$(mktemp -d)"
chmod 0777 "$config"

docker run -d --name "$name" \
	--user "$uid:$gid" \
	--read-only --cap-drop ALL --security-opt no-new-privileges \
	--tmpfs /tmp \
	-v "$config:/config" \
	--health-interval 1s --health-start-period 0s --health-retries 30 \
	-p 127.0.0.1::3050 \
	"$image" >/dev/null

i=0
until [ "$(docker inspect -f '{{.State.Health.Status}}' "$name")" = healthy ]; do
	i=$((i + 1))
	[ "$i" -le 40 ] || fail "container did not become healthy"
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

for path in / /library /explore /explore/popular /codecs /healthz /api/features /api/status /api/downloads /api/library /api/storage; do
	code="$(curl -s -o /dev/null -w '%{http_code}' "$base$path")"
	[ "$code" = 200 ] || fail "GET $path answered $code"
done
echo "all pages and routes answer 200"

features="$(curl -s "$base/api/features")"
enabled="$(echo "$features" | grep -o '"id":"[a-z]*","[^}]*"available":true' | grep -o '"id":"[a-z]*"' | tr -d '\n')"
[ "$enabled" = '"id":"dashboard"' ] || fail "features enabled with no environment: $enabled ($features)"
echo "only the Dashboard is enabled"

curl -s -X PUT -H 'content-type: application/json' -d '{"hd":"hevc","uhd":"h264"}' \
	"$base/api/codec-analysis/settings" | grep -q '"hd":"hevc"' || fail "settings were not saved"
[ -s "$config/settings.json" ] || fail "settings.json missing in /config"
[ "$(stat -c %u:%g "$config/settings.json")" = "$uid:$gid" ] || fail "settings.json owner is $(stat -c %u:%g "$config/settings.json")"
echo "settings.json written to /config as $uid:$gid"

docker stop --time 10 "$name" >/dev/null
[ "$(docker inspect -f '{{.State.ExitCode}}' "$name")" = 0 ] || fail "unclean shutdown (exit code $(docker inspect -f '{{.State.ExitCode}}' "$name"))"
echo "SIGTERM stops it cleanly"
echo "OK"
