# Common commands for this repository. Run `make` or `make help` to list them.
#
# Each target is a few lines; read it to see the exact commands, which also
# work without make. CI calls these same targets, so a target that passes
# locally passes in CI.
#
# Written for GNU Make 3.81, the version macOS ships: no .ONESHELL or other
# newer features.

SHELL := /bin/bash

COMPOSE             := docker compose
COMPOSE_INTEGRATION := docker compose -f compose.yaml -f compose.integration.yaml

# Stamped into the API's log lines as "version". CI sets it to the commit SHA.
VERSION ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo devel)
export VERSION

API_URL      ?= http://localhost:8080
WIREMOCK_URL ?= http://localhost:8081
FLAGD_URL    ?= http://localhost:8016

# The fault stubs that `make fault` can switch on.
FAULTS := $(notdir $(basename $(wildcard wiremock/faults/*.json)))

# Redocly CLI for the spec targets, run in Docker (already needed for the stack)
# so there's nothing else to install. Pinned, so it behaves the same everywhere;
# run as your user, so files it writes aren't owned by root.
REDOCLY := docker run --rm --user $(shell id -u):$(shell id -g) -v "$(CURDIR):/spec" -w /spec redocly/cli:2.57.0

.DEFAULT_GOAL := help
.PHONY: help up down logs run test fmt fmt-check vet test-race check \
	integration integration-up integration-test integration-logs integration-down \
	live-test fault fault-reset spec spec-lint

##@ Help

help: ## List the targets
	@awk 'BEGIN { FS = ":.*## " } \
		/^##@/ { printf "\n%s\n", substr($$0, 5) } \
		/^[a-zA-Z_-]+:.*## / { printf "  make %-18s %s\n", $$1, $$2 }' $(MAKEFILE_LIST)
	@echo
	@echo "  Variables: FAULT=<name> for make fault; TAGS=@smoke or ARGS=... for the integration tests."

##@ Local stack

up: ## Start the API, WireMock and flagd in the background (rebuilds the API)
	$(COMPOSE) up -d --build --wait
	@echo "API: $(API_URL)   WireMock: $(WIREMOCK_URL)   flagd: $(FLAGD_URL)"

down: ## Stop the local stack
	$(COMPOSE) down

logs: ## Follow the stack's logs (Ctrl-C to stop)
	$(COMPOSE) logs -f

run: ## Run the API with go run, against the WireMock and flagd containers
	$(COMPOSE) up -d --wait mock flagd
	UPSTREAM_URL=$(WIREMOCK_URL) FLAGS_URL=$(FLAGD_URL) LOG_FORMAT=text \
		go run -ldflags "-X main.version=$(VERSION)" ./cmd/api

fault: ## Switch on a WireMock fault: make fault FAULT=unavailable
	@if [ -z "$(FAULT)" ] || [ ! -f "wiremock/faults/$(FAULT).json" ]; then \
		echo "usage: make fault FAULT=<name>, where <name> is one of: $(FAULTS)"; exit 2; fi
	@curl -fsS -o /dev/null -X POST $(WIREMOCK_URL)/__admin/mappings \
		-H 'Content-Type: application/json' --data @wiremock/faults/$(FAULT).json
	@echo "fault $(FAULT) is on; make fault-reset switches it off"

fault-reset: ## Switch off every fault (back to the mounted stubs)
	@curl -fsS -o /dev/null -X POST $(WIREMOCK_URL)/__admin/mappings/reset
	@echo "faults off"

##@ Checks and tests

test: ## Unit tests (no Docker)
	go test ./...

fmt: ## Format the code
	gofmt -w .

fmt-check: ## Fail if any file needs gofmt
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then echo "These files need gofmt (run make fmt):"; echo "$$unformatted"; exit 1; fi

vet: ## go vet, for the service and the integration module
	go vet ./...
	go vet -C integration ./...

test-race: ## Unit tests with the race detector
	go test -race ./...

check: fmt-check vet test-race ## Everything CI's fast job runs

live-test: ## The opt-in httpstore test against the running WireMock
	$(COMPOSE) up -d --wait mock
	LIVE_UPSTREAM_URL=$(WIREMOCK_URL) go test ./internal/item/httpstore -run Live -v -count=1

##@ Integration tests (Godog, against the whole stack)

integration: ## Start the test stack, run the suite, and always stop it again
	$(MAKE) integration-up
	@$(MAKE) integration-test; status=$$?; \
	if [ $$status -ne 0 ]; then $(MAKE) integration-logs; fi; \
	$(MAKE) integration-down; exit $$status

integration-up: ## Start the stack with the integration overrides
	$(COMPOSE_INTEGRATION) up -d --build --wait

integration-test: ## Run the suite against a running stack (TAGS=@smoke, ARGS=...)
	go test -C integration ./... -v -count=1 $(if $(TAGS),-godog.tags=$(TAGS)) $(ARGS)

integration-logs: ## Print the test stack's logs
	$(COMPOSE_INTEGRATION) logs --no-color

integration-down: ## Stop the test stack
	$(COMPOSE_INTEGRATION) down

##@ API spec (Redocly CLI, run in Docker)

spec: ## Render docs/openapi.json as an HTML page, bin/openapi.html
	@mkdir -p bin
	$(REDOCLY) build-docs docs/openapi.json -o bin/openapi.html
	@echo "open bin/openapi.html"

spec-lint: ## Lint docs/openapi.json with Redocly's recommended rules
	$(REDOCLY) lint docs/openapi.json
