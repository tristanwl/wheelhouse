#!/bin/sh
# Serves Wheelhouse from a container image, for the acceptance suite:
#
#   WHEELHOUSE_CMD="acceptance/docker-server.sh wheelhouse:test" npm run test:acceptance
#
# The suite starts one server per scenario and gives each its own environment,
# so it needs a *command* it can start and stop, not a URL. This starts the image
# with host networking (the fake services listen on 127.0.0.1 of the host and the
# server under test on 127.0.0.1:$PORT), the same hardening the production
# deployment can use, and the current user's uid/gid, which also proves the image
# runs as an arbitrary user. The suite's scratch directory is mounted at the same
# path, because the hard-link scenario creates its media files there and
# WHEELHOUSE_MEDIA_ROOTS points at it.
#
# SIGTERM (how the suite stops a server) stops the container. A SIGKILL cannot
# be caught and leaves it running: remove stragglers with
#   docker ps -aq --filter name=wheelhouse-acceptance | xargs -r docker rm -f
set -eu

image="${1:?usage: docker-server.sh IMAGE}"
: "${PORT:?PORT must be set}"

scratch="$(realpath "${TMPDIR:-/tmp}")"
config="$(mktemp -d "$scratch/wheelhouse-config.XXXXXX")"
name="wheelhouse-acceptance-$$-$PORT"

# Pass on the variables the suite sets for the server, by name only.
set -- --rm --name "$name" --network host \
	--user "$(id -u):$(id -g)" \
	--read-only --cap-drop ALL --security-opt no-new-privileges \
	-v "$config:/config" -v "$scratch:$scratch"
for var in $(env | cut -d= -f1 | grep -E '^(PORT|HOSTNAME|WHEELHOUSE_[A-Z_]+|SONARR_[A-Z0-9_]+|RADARR_[A-Z0-9_]+|SABNZBD_[A-Z0-9_]+|QBITTORRENT_[A-Z0-9_]+|PLEX_[A-Z0-9_]+|JELLYFIN_[A-Z0-9_]+|TMDB_[A-Z0-9_]+)$'); do
	set -- "$@" -e "$var"
done

cleanup() {
	docker stop --time 3 "$name" >/dev/null 2>&1 || true
	rm -rf "$config"
}
trap 'cleanup; exit 0' TERM INT
trap cleanup EXIT

docker run "$@" "$image" &
wait $!
