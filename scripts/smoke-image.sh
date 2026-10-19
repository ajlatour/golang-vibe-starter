#!/bin/sh
# Smoke-tests an API image as it ships: runs it, for one platform, against
# the stack's stubs, and checks the basics work end to end. The Release
# workflow runs it on the exact image it's about to publish, for each
# platform; see docs/releasing.md.
#
#   sh scripts/smoke-image.sh <image> <platform> <version>
#
# <image> is a reference Docker can run (a local tag, or a registry
# reference by digest), <platform> linux/amd64 or linux/arm64 (another
# machine's needs QEMU registered), and <version> what the image must say it
# is. The image's dependencies run in a compose project of their own,
# vibe-smoke, with no ports published (compose.smoke.yaml); the API's port is
# published on 127.0.0.1:${SMOKE_PORT:-18080}. Everything is removed after.
set -eu

image=${1:?usage: smoke-image.sh <image> <platform> <version>}
platform=${2:?usage: smoke-image.sh <image> <platform> <version>}
version=${3:?usage: smoke-image.sh <image> <platform> <version>}
port=${SMOKE_PORT:-18080}
base=http://127.0.0.1:$port
key=gvs_local-development-only # the stack's development key (compose.yaml)
container=vibe-smoke-api
compose="docker compose -p vibe-smoke -f compose.yaml -f compose.smoke.yaml"

cleanup() {
	docker rm -f "$container" >/dev/null 2>&1 || true
	$compose down >/dev/null 2>&1 || true
}
trap cleanup EXIT
cleanup

failures=0
check() {
	name=$1
	shift
	if "$@"; then
		echo "  ok    $name"
	else
		echo "  FAIL  $name"
		failures=$((failures + 1))
	fi
}

echo "Smoke-testing $image ($platform), want version $version"
# The stubs it runs against, and where the API finds them.
set -- mock
# feature:flags
set -- "$@" flagd
# end feature:flags
$compose up -d --wait "$@" >/dev/null 2>&1
set -- -e UPSTREAM_URL=http://mock:8080
# feature:flags
set -- "$@" -e FLAGS_URL=http://flagd:8016
# end feature:flags
network=$(docker inspect -f '{{range $k, $v := .NetworkSettings.Networks}}{{$k}}{{end}}' "$($compose ps -q mock)")
docker run -d --name "$container" --platform "$platform" --network "$network" \
	-p "127.0.0.1:$port:8080" "$@" \
	-e API_KEYS="dev:write:1f0d1e72ef1251a015cbec8ae3ad6880002ffe8ad4de82f4f496db34dc3e91dc" \
	-e LOG_FORMAT=json -e SHUTDOWN_TIMEOUT=10s \
	"$image" >/dev/null

# Emulated, the first start is slow: wait up to a minute for it to be ready.
ready=false
i=0
while [ $i -lt 120 ]; do
	if [ "$(curl -s -o /dev/null -w '%{http_code}' "$base/readyz")" = 200 ]; then
		ready=true
		break
	fi
	if [ "$(docker inspect -f '{{.State.Running}}' "$container" 2>/dev/null)" != true ]; then
		break
	fi
	sleep 0.5
	i=$((i + 1))
done
if [ "$ready" != true ]; then
	echo "  FAIL  the API never became ready; its logs:"
	docker logs "$container" 2>&1 | tail -n 30
	exit 1
fi

# status <want> <curl args...>: the response has that status.
status() {
	want=$1
	shift
	[ "$(curl -s -o /dev/null -w '%{http_code}' "$@")" = "$want" ]
}
# body_has <text> <curl args...>: the response body contains text.
body_has() {
	text=$1
	shift
	curl -s "$@" | grep -q "$text"
}
auth="Authorization: Bearer $key"

check "it is the $platform image" \
	[ "$(docker image inspect -f '{{.Os}}/{{.Architecture}}' "$(docker inspect -f '{{.Image}}' "$container")")" = "$platform" ]
check "it reports version $version" \
	sh -c "docker logs $container 2>&1 | grep '\"server started\"' | grep -q '\"version\":\"$version\"'"
check "it runs as a non-root user" \
	[ "$(docker inspect -f '{{.Config.User}}' "$(docker inspect -f '{{.Image}}' "$container")")" != "" ]
check "GET /healthz is 200" status 200 "$base/healthz"
check "GET /readyz is 200 (the upstream is reachable)" status 200 "$base/readyz"
check "GET /items without a key is 401" status 401 "$base/items"
check "GET /items lists the upstream's items" body_has '"widget"' -H "$auth" "$base/items"
check "GET /items/widget is 200 with an ETag" \
	sh -c "curl -s -D - -o /dev/null -H '$auth' $base/items/widget | grep -qi '^etag:'"
check "POST /items creates an item (201)" \
	status 201 -X POST -H "$auth" -H 'Content-Type: application/json' -d '{"name":"Smoke"}' "$base/items"
check "an unknown path is a JSON 404" body_has '"not_found"' -H "$auth" "$base/nope"

# A clean shutdown: SIGTERM, then exit 0 within SHUTDOWN_TIMEOUT.
docker stop -t 15 "$container" >/dev/null
check "it stops cleanly on SIGTERM (exit 0)" [ "$(docker inspect -f '{{.State.ExitCode}}' "$container")" = 0 ]
check "it logs that it stopped" sh -c "docker logs $container 2>&1 | grep -q '\"server stopped\"'"

if [ "$failures" -ne 0 ]; then
	echo "$failures checks failed; the API's logs:"
	docker logs "$container" 2>&1 | tail -n 30
	exit 1
fi
echo "All checks passed."
