#!/usr/bin/env bash
# Fails when a supply-chain input is not pinned:
#   - a `uses:` in .github/workflows/*.yml that is not a full 40-hex commit SHA
#     (local `./` actions are fine);
#   - an `image:` input in a workflow that is not pinned by @sha256 digest;
#   - a Dockerfile FROM without an @sha256 digest (`scratch` and earlier build
#     stages are fine).
# Justified exceptions go in scripts/check-pins.allow, one per line:
#     <file>: <exact reference>  # why
# Run from anywhere; pass a directory as $1 to check another tree (the tests do).
set -euo pipefail

root="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
allow="${CHECK_PINS_ALLOW:-$root/scripts/check-pins.allow}"
failures=0

allowed() { # file reference
	[ -f "$allow" ] || return 1
	local line entry
	while IFS= read -r line; do
		line="${line%%#*}"
		entry="$(sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//' <<<"$line")"
		[ -n "$entry" ] || continue
		[ "$entry" = "$1: $2" ] && return 0
	done <"$allow"
	return 1
}

fail() { # file line message
	echo "::error file=$1,line=$2::$3"
	failures=$((failures + 1))
}

shopt -s nullglob
for file in "$root"/.github/workflows/*.yml "$root"/.github/workflows/*.yaml; do
	rel="${file#"$root"/}"
	n=0
	while IFS= read -r line || [ -n "$line" ]; do
		n=$((n + 1))
		line="${line%%#*}"
		if [[ "$line" =~ ^[[:space:]-]*uses:[[:space:]]*([^[:space:]]+) ]]; then
			ref="${BASH_REMATCH[1]//[\"\']/}"
			case "$ref" in ./*) continue ;; esac
			if ! [[ "$ref" =~ @[0-9a-f]{40}$ ]] && ! allowed "$rel" "$ref"; then
				fail "$rel" "$n" "action '$ref' is not pinned to a full commit SHA"
			fi
		elif [[ "$line" =~ ^[[:space:]-]*image:[[:space:]]*([^[:space:]]+) ]]; then
			ref="${BASH_REMATCH[1]//[\"\']/}"
			# shellcheck disable=SC2016 # a literal "${{", not an expansion
			case "$ref" in '${{'*) continue ;; esac
			if ! [[ "$ref" =~ @sha256:[0-9a-f]{64}$ ]] && ! allowed "$rel" "$ref"; then
				fail "$rel" "$n" "image '$ref' is not pinned by @sha256 digest"
			fi
		fi
	done <"$file"
done

for file in "$root"/Dockerfile "$root"/Dockerfile.* "$root"/*/Dockerfile; do
	[ -f "$file" ] || continue
	rel="${file#"$root"/}"
	n=0
	stages=" "
	while IFS= read -r line || [ -n "$line" ]; do
		n=$((n + 1))
		# FROM [--platform=x] image [AS name]
		if [[ "$line" =~ ^[[:space:]]*[Ff][Rr][Oo][Mm][[:space:]]+(--[^[:space:]]+[[:space:]]+)*([^[:space:]]+)([[:space:]]+[Aa][Ss][[:space:]]+([^[:space:]]+))? ]]; then
			ref="${BASH_REMATCH[2]}"
			stage="${BASH_REMATCH[4]:-}"
			if [ "$ref" != scratch ] && [[ "$stages" != *" $ref "* ]] \
				&& ! [[ "$ref" =~ @sha256:[0-9a-f]{64}$ ]] && ! allowed "$rel" "$ref"; then
				fail "$rel" "$n" "FROM '$ref' has no @sha256 digest"
			fi
			[ -z "$stage" ] || stages="$stages$stage "
		fi
	done <"$file"
done

if [ "$failures" -gt 0 ]; then
	echo "check-pins: $failures unpinned reference(s); pin them or justify them in scripts/check-pins.allow" >&2
	exit 1
fi
echo "check-pins: all workflow actions, images and Dockerfile base images are pinned"
