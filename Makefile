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
PPROF_URL    ?= http://localhost:6061

# Performance tests. PROFILE picks the k6 profile (perf/profiles.json) for
# make perf and perf-profile, which have their own defaults. The comparisons
# measure the working tree against BENCH_BASE, interleaving BENCH_COUNT
# benchmark rounds (benchstat wants at least 6) and LOAD_ROUNDS k6 runs.
BENCH_COUNT ?= 6
LOAD_ROUNDS ?= 2
BENCH_BASE  ?= main
BENCH_OUT   := bin/bench
LOAD_OUT    := bin/perf/compare
BENCHSTAT   := go run golang.org/x/perf/cmd/benchstat@v0.0.0-20260929162123-406019bb8b68

# The fault stubs that `make fault` can switch on.
FAULTS := $(notdir $(basename $(wildcard wiremock/faults/*.json)))

# pandoc renders the Markdown docs, run in Docker like Redocly below.
PANDOC   := docker run --rm --user $(shell id -u):$(shell id -g) -v "$(CURDIR):/data" -w /data --entrypoint sh pandoc/core:3.7
DOCS_OUT := bin/docs

# Redocly CLI for the spec targets, run in Docker (already needed for the stack)
# so there's nothing else to install. Pinned, so it behaves the same everywhere;
# run as your user, so files it writes aren't owned by root.
REDOCLY := docker run --rm --user $(shell id -u):$(shell id -g) -v "$(CURDIR):/spec" -w /spec redocly/cli:2.57.0

.DEFAULT_GOAL := help
.PHONY: help up down logs run test fmt fmt-check vet test-race check \
	integration integration-up integration-test integration-logs integration-down \
	live-test fault fault-reset docs docs-open docs-check godoc spec spec-lint \
	perf perf-profile bench bench-compare load-compare perf-compare

##@ Help

help: ## List the targets
	@awk 'BEGIN { FS = ":.*## " } \
		/^##@/ { printf "\n%s\n", substr($$0, 5) } \
		/^[a-zA-Z_-]+:.*## / { printf "  make %-18s %s\n", $$1, $$2 }' $(MAKEFILE_LIST)
	@echo
	@echo "  Variables: FAULT=<name> for make fault; TAGS=@smoke or ARGS=... for the integration tests;"
	@echo "  PROFILE=<name> for make perf: $$(sed -n 's/^  "\([a-z-]*\)": {/\1/p' perf/profiles.json | tr '\n' ' ')"

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
	go test -C perf/report ./...

fmt: ## Format the code
	gofmt -w .

fmt-check: ## Fail if any file needs gofmt
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then echo "These files need gofmt (run make fmt):"; echo "$$unformatted"; exit 1; fi

vet: ## go vet, for the service and the integration and perf report modules
	go vet ./...
	go vet -C integration ./...
	go vet -C perf/report ./...

test-race: ## Unit tests with the race detector
	go test -race ./...
	go test -C perf/report -race ./...

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
	@# Pull WireMock and flagd while the API builds, rather than one after the other.
	$(COMPOSE_INTEGRATION) pull --quiet --ignore-buildable & pull=$$!; \
		$(COMPOSE_INTEGRATION) build && wait $$pull
	$(COMPOSE_INTEGRATION) up -d --wait

integration-test: ## Run the suite against a running stack (TAGS=@smoke, ARGS=...)
	go test -C integration ./... -v -count=1 $(if $(TAGS),-godog.tags=$(TAGS)) $(ARGS)

integration-logs: ## Print the test stack's logs
	$(COMPOSE_INTEGRATION) logs --no-color

integration-down: ## Stop the test stack
	$(COMPOSE_INTEGRATION) down

##@ Performance (see docs/performance.md)

perf: ## Start the stack and run a k6 profile (default PROFILE=smoke): make perf PROFILE=load
	$(COMPOSE) up -d --build --wait
	sh perf/run.sh $(or $(PROFILE),smoke)

perf-profile: ## Run a k6 profile and capture CPU, heap, allocation and goroutine profiles (default PROFILE=load)
	$(COMPOSE) up -d --build --wait
	sh perf/profile.sh $(or $(PROFILE),load) $(PPROF_URL) bin/perf/pprof-$$(date +%Y%m%d-%H%M%S)

bench: ## Run the Go benchmarks, BENCH_COUNT times each, into bin/bench/current.txt
	@mkdir -p $(BENCH_OUT)
	go test -run '^$$' -bench . -benchmem -count $(BENCH_COUNT) ./... | tee $(BENCH_OUT)/current.txt

bench-compare: ## Run the benchmarks here and at BENCH_BASE (default main), interleaved, and compare them
	sh perf/bench-ab.sh $(BENCH_BASE) $(BENCH_COUNT) $(BENCH_OUT)
	$(BENCHSTAT) base=$(BENCH_OUT)/base.txt head=$(BENCH_OUT)/head.txt | tee $(BENCH_OUT)/compare.txt

load-compare: ## Run the k6 compare profile against this tree and BENCH_BASE, interleaved
	sh perf/load-ab.sh $(BENCH_BASE) $(LOAD_ROUNDS) $(LOAD_OUT) $(or $(PROFILE),compare)

perf-compare: ## Both comparisons, written up as the pull request comment CI posts: bin/perf/report.md
	$(MAKE) bench-compare
	@status=0; $(MAKE) load-compare || status=$$?; \
	go build -C perf/report -o $(CURDIR)/bin/perf-report . && bin/perf-report -base-name $(BENCH_BASE) \
		-bench-base $(BENCH_OUT)/base.txt -bench-head $(BENCH_OUT)/head.txt \
		-load $(LOAD_OUT) $$([ $$status -ne 0 ] && echo -load-failed) > bin/perf/report.md || status=$$?; \
	echo "The report is in bin/perf/report.md"; exit $$status

##@ Documentation (rendered in Docker; nothing to install)

docs: ## Render the README, guides, changelog and API spec as a local site in bin/docs
	@# Start empty, so a page the build didn't produce can't survive from an earlier run.
	rm -rf $(DOCS_OUT)
	$(PANDOC) docs/site/build.sh $(DOCS_OUT)
	$(REDOCLY) build-docs docs/openapi.json -o $(DOCS_OUT)/api.html
	@echo "open $(DOCS_OUT)/index.html, or run make docs-open"

docs-open: docs ## Build the docs site and open it in your browser
	@if [ "$$(uname)" = Darwin ]; then open $(DOCS_OUT)/index.html; \
	elif command -v xdg-open >/dev/null; then xdg-open $(DOCS_OUT)/index.html; \
	else echo "open $(DOCS_OUT)/index.html in a browser"; fi

docs-check: docs ## Build the docs site and check every page exists and every link resolves
	DOCS_SITE=$(DOCS_OUT) go test ./docs/site -count=1 -v

godoc: ## Browse the Go package documentation at http://localhost:6060 (Ctrl-C to stop)
	go run golang.org/x/pkgsite/cmd/pkgsite@v0.5.0 -http=localhost:6060 .

spec: ## Render docs/openapi.json as an HTML page, bin/openapi.html
	@mkdir -p bin
	$(REDOCLY) build-docs docs/openapi.json -o bin/openapi.html
	@echo "open bin/openapi.html"

spec-lint: ## Lint docs/openapi.json with Redocly's recommended rules
	$(REDOCLY) lint docs/openapi.json
