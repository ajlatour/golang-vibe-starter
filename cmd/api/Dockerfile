# syntax=docker/dockerfile:1
# Build from the repo root: docker build -f cmd/api/Dockerfile .

FROM golang:1.27-alpine@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOTOOLCHAIN=local

# Module files first so the dependency layer is cached across code changes.
COPY go.mod go.sum* ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY flagd/ flagd/
COPY internal/ internal/
COPY cmd/api/ cmd/api/
# Stamped into every log line. .git isn't in the build context, so pass the
# commit in: docker build --build-arg VERSION=$(git rev-parse --short HEAD)
ARG VERSION=devel
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/api ./cmd/api

# Static, no shell, runs as an unprivileged user. Healthchecks use the
# binary's own "healthcheck" subcommand since there is no curl/wget.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/api /api
EXPOSE 8080
ENTRYPOINT ["/api"]
