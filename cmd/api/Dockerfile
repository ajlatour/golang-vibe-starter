# syntax=docker/dockerfile:1
# Build from the repo root: docker build -f cmd/api/Dockerfile .

# A Go build cache to start from: empty, unless the build is given one with
# --build-context gocache=<dir>. CI does (scripts/build-api-image.sh), with
# the cache it kept from earlier runs, so only changed packages recompile.
FROM scratch AS gocache

# The build runs on the builder's own platform and cross-compiles for the
# target's, so a multi-platform image (the release's amd64 and arm64) needs
# no emulation. A plain build is for the builder's platform, as before.
FROM --platform=$BUILDPLATFORM golang:1.27.2-alpine@sha256:f92b6ef800e499660581efdabdf25d9d817a9d124eaf900924f0504e7e27e12d AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOTOOLCHAIN=local

# Module files first so the dependency layer is cached across code changes.
COPY go.mod go.sum* ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

# feature:flags
COPY flagd/ flagd/
# end feature:flags
COPY internal/ internal/
COPY cmd/api/ cmd/api/
# Stamped into every log line. .git isn't in the build context, so pass the
# commit in: docker build --build-arg VERSION=$(git rev-parse --short HEAD)
ARG VERSION=devel
ARG TARGETOS TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=bind,from=gocache,target=/tmp/gocache-seed \
    { cp -a /tmp/gocache-seed/. /root/.cache/go-build/ || \
      echo "seeding the Go build cache failed; building without it"; } && \
    GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/api ./cmd/api

# The Go build cache after the build, for CI to keep for the next run:
#   docker build --target gocache-export --output type=local,dest=<dir> ...
# It sits before the final stage, which a plain build produces.
FROM build AS gocache-filled
RUN --mount=type=cache,target=/root/.cache/go-build \
    mkdir /out-cache && cp -a /root/.cache/go-build/. /out-cache/
FROM scratch AS gocache-export
COPY --from=gocache-filled /out-cache /

# Static, no shell, runs as an unprivileged user. Healthchecks use the
# binary's own "healthcheck" subcommand since there is no curl/wget.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/api /api
EXPOSE 8080
ENTRYPOINT ["/api"]
