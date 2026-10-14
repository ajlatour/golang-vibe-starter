#!/bin/sh
# Builds the API's image, starting the Go compile from a kept build cache when
# there is one, and keeps the cache afterwards. CI uses it with actions/cache,
# so only the packages a change touches recompile; locally a plain
# docker build (or compose) does the same with BuildKit's own cache.
#
#   sh scripts/build-api-image.sh <tag> [source dir]
#
# GOCACHE_DIR (if set) is the directory the cache is read from and, unless
# GOCACHE_KEEP=no, written back to. Writing replaces the directory, so no
# other build may be reading it then: build in parallel with GOCACHE_KEEP=no,
# and keep the cache with one more run of this script after they're done (a
# rebuild that's all cached, so only the copy runs).
set -eu

tag=${1:?usage: build-api-image.sh <tag> [source dir]}
src=${2:-.}
version=${VERSION:-devel}

seed=
if [ -n "${GOCACHE_DIR:-}" ]; then
	mkdir -p "$GOCACHE_DIR"
	seed="--build-context gocache=$GOCACHE_DIR"
fi

# The build's full output goes to a log, shown only if it fails.
log=$(mktemp)
# shellcheck disable=SC2086 # $seed is two words or none
if ! docker build --progress=plain --load $seed --build-arg VERSION="$version" \
	-f "$src/cmd/api/Dockerfile" -t "$tag" "$src" >"$log" 2>&1; then
	echo "building $tag from $src failed:" >&2
	tail -n 60 "$log" >&2
	rm -f "$log"
	exit 1
fi
rm -f "$log"

if [ -n "${GOCACHE_DIR:-}" ] && [ "${GOCACHE_KEEP:-}" != no ]; then
	# The export stage reuses the build above, so this only copies the cache
	# out. A base without the stage (older code) just has nothing to keep.
	out=$(mktemp -d)
	# $seed is an option and its value, or nothing: split, not quoted.
	# shellcheck disable=SC2086
	if docker build --quiet $seed --build-arg VERSION="$version" -f "$src/cmd/api/Dockerfile" \
		--target gocache-export --output "type=local,dest=$out" "$src" >/dev/null 2>&1; then
		rm -rf "$GOCACHE_DIR" && mv "$out" "$GOCACHE_DIR"
	else
		rm -rf "$out"
	fi
fi
