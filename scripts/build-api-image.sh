#!/bin/sh
# Builds the API's image, starting the Go compile from a kept build cache when
# there is one, and keeps the cache afterwards. CI uses it with actions/cache,
# so only the packages a change touches recompile; locally a plain
# docker build (or compose) does the same with BuildKit's own cache.
#
#   sh scripts/build-api-image.sh <tag> [source dir]
#
# GOCACHE_DIR (if set) is the directory the cache is read from and, unless
# GOCACHE_KEEP=no, written back to (two builds running at once mustn't both
# write it).
set -eu

tag=${1:?usage: build-api-image.sh <tag> [source dir]}
src=${2:-.}
version=${VERSION:-devel}

seed=
if [ -n "${GOCACHE_DIR:-}" ]; then
	mkdir -p "$GOCACHE_DIR"
	seed="--build-context gocache=$GOCACHE_DIR"
fi

# shellcheck disable=SC2086 # $seed is two words or none
docker build --quiet --load $seed --build-arg VERSION="$version" -f "$src/cmd/api/Dockerfile" -t "$tag" "$src" >/dev/null

if [ -n "${GOCACHE_DIR:-}" ] && [ "${GOCACHE_KEEP:-}" != no ]; then
	# The export stage reuses the build above, so this only copies the cache
	# out. A base without the stage (older code) just has nothing to keep.
	out=$(mktemp -d)
	if docker build --quiet $seed --build-arg VERSION="$version" -f "$src/cmd/api/Dockerfile" \
		--target gocache-export --output "type=local,dest=$out" "$src" >/dev/null 2>&1; then
		rm -rf "$GOCACHE_DIR" && mv "$out" "$GOCACHE_DIR"
	else
		rm -rf "$out"
	fi
fi
