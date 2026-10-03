# syntax=docker/dockerfile:1
# Build from the repo root: docker build -f cmd/api/Dockerfile .

FROM golang:1.27-alpine AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOTOOLCHAIN=local

# Module files first so the dependency layer is cached across code changes.
COPY go.mod go.sum* ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY flagd/ flagd/
COPY internal/ internal/
COPY cmd/api/ cmd/api/
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

# Static, no shell, runs as an unprivileged user. Healthchecks use the
# binary's own "healthcheck" subcommand since there is no curl/wget.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/api /api
EXPOSE 8080
ENTRYPOINT ["/api"]
