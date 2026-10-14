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
# The Go tools: static analysis (ADR-0026), govulncheck and benchstat, pinned
# with their dependencies' checksums in tools/go.mod (ADR-0045), and built
# from it into bin/tools. Building them is cached like any build, so nothing
# is fetched once it's done; Dependabot proposes their updates.
TOOLS := $(CURDIR)/bin/tools

BENCH_COUNT ?= 6
LOAD_ROUNDS ?= 2
BENCH_BASE  ?= main
BENCH_OUT   := bin/bench
LOAD_OUT    := bin/perf/compare
BENCHSTAT   := $(TOOLS)/benchstat

# The fault stubs that `make fault` can switch on.
FAULTS := $(notdir $(basename $(wildcard wiremock/faults/*.json)))

# pandoc renders the Markdown docs, run in Docker like Redocly below.
PANDOC   := docker run --rm --user $(shell id -u):$(shell id -g) -v "$(CURDIR):/data" -w /data --entrypoint sh $(shell sh scripts/image.sh pandoc)
DOCS_OUT := bin/docs

# Redocly CLI for the spec targets, run in Docker (already needed for the stack)
# so there's nothing else to install. Pinned, so it behaves the same everywhere;
# run as your user, so files it writes aren't owned by root.
REDOCLY := docker run --rm --user $(shell id -u):$(shell id -g) -v "$(CURDIR):/spec" -w /spec $(shell sh scripts/image.sh redocly)

.DEFAULT_GOAL := help
.PHONY: help up down logs dev run test fmt fmt-check vet test-race cover check \
	integration integration-up integration-test integration-logs integration-down \
	lint tools api-key vuln fuzz live-test fault fault-reset docs docs-open docs-check docs-release godoc spec spec-lint \
	api-compat smoke release-prepare release-tag perf perf-profile perf-soak bench bench-compare load-compare perf-compare perf-data perf-trends monitor

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

down: ## Stop the local stack, monitoring included
	@# Naming the profiles stops their containers too, which a plain down leaves running.
	$(COMPOSE) --profile monitoring --profile perf down

logs: ## Follow the stack's logs (Ctrl-C to stop)
	$(COMPOSE) logs -f

# The local stack's development keys (compose.yaml), so scripts/api and
# docs/curl.md work against make dev too.
DEV_API_KEYS := dev:write:1f0d1e72ef1251a015cbec8ae3ad6880002ffe8ad4de82f4f496db34dc3e91dc,dev-reader:read:1cf20667ccf4aa0982ca024c319ee0cf77cadf20e85bd2fc51264075af57b2d4

dev: ## Run the API with go run and an in-memory store, on :8080: nothing else to start, no Docker
	STORE=memory API_KEYS=$(DEV_API_KEYS) LOG_FORMAT=text \
		go run -ldflags "-X main.version=$(VERSION)" ./cmd/api

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
	@curl -fsS -o /dev/null -X POST $(WIREMOCK_URL)/__admin/scenarios/reset
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

# The coverage floor, across the service's packages: a change that lowers the
# total below it fails CI. Raise it as coverage rises; don't lower it.
COVER_MIN ?= 90

cover: ## The unit tests with the race detector, and coverage across packages (bin/cover.out), failing under COVER_MIN% in total
	@mkdir -p bin
	go test -race -covermode=atomic -coverpkg=./cmd/...,./internal/... -coverprofile=bin/cover.out ./...
	@sh scripts/coverage.sh bin/cover.out $(COVER_MIN)

check: fmt-check vet test-race ## Everything CI's fast job runs

tools: ## Build the Go tools pinned in tools/go.mod into bin/tools
	go -C tools build -o $(TOOLS)/ tool

# A tool in Docker, as your user, removed after.
DOCKER_RUN  := docker run --rm --user $(shell id -u):$(shell id -g)

lint: tools ## staticcheck (every check), modernize, gosec, go mod tidy, shellcheck and actionlint, on every module
	@# staticcheck with every check, style included: the code is clean of them,
	@# and stays so. modernize flags code a newer Go says more simply.
	$(TOOLS)/staticcheck -checks all ./...
	cd integration && $(TOOLS)/staticcheck -checks all ./...
	cd perf/report && $(TOOLS)/staticcheck -checks all ./...
	$(TOOLS)/modernize -test ./...
	cd integration && $(TOOLS)/modernize -test ./...
	cd perf/report && $(TOOLS)/modernize -test ./...
	$(TOOLS)/gosec -quiet ./...
	cd integration && $(TOOLS)/gosec -quiet ./...
	@# The report tool's job is reading the files named on its command line,
	@# which is all G304 (file inclusion via a variable) would flag there.
	cd perf/report && $(TOOLS)/gosec -quiet -exclude=G304 ./...
	@# go.mod and go.sum say exactly what the code needs.
	go mod tidy -diff
	go -C integration mod tidy -diff
	go -C perf/report mod tidy -diff
	go -C tools mod tidy -diff
	@# The shell scripts, and the workflows (with the shell in their steps).
	$(DOCKER_RUN) -v "$(CURDIR):/mnt" -w /mnt $(shell sh scripts/image.sh shellcheck) $$(git ls-files '*.sh') scripts/api
	$(DOCKER_RUN) -v "$(CURDIR):/repo" -w /repo $(shell sh scripts/image.sh actionlint) -color=false

api-key: ## Generate an API key and its API_KEYS entry: make api-key NAME=ci SCOPE=write
	@if [ -z "$(NAME)" ] || [ -z "$(SCOPE)" ]; then echo "usage: make api-key NAME=<who> SCOPE=read|write"; exit 2; fi
	@go run ./cmd/apikey $(NAME) $(SCOPE)

vuln: tools ## Check all three modules for known vulnerabilities the code can reach
	$(TOOLS)/govulncheck ./...
	cd integration && $(TOOLS)/govulncheck ./...
	cd perf/report && $(TOOLS)/govulncheck ./...

# The fuzz tests, as package:Function. go test fuzzes one at a time.
FUZZ_TARGETS := internal/httpx:FuzzDecodeJSON internal/httpx:FuzzCorrelationID internal/tracectx:FuzzParse \
	internal/headerguard:FuzzMiddleware internal/item:FuzzParseID \
	internal/apikey:FuzzParseKeys internal/httpmetrics:FuzzObserve
FUZZTIME ?= 30s

fuzz: ## Fuzz the code that handles untrusted input, FUZZTIME (30s) per target
	@# A target that doesn't exist (renamed, say) would otherwise "pass" having
	@# fuzzed nothing, so each must be there first.
	@for t in $(FUZZ_TARGETS); do \
		pkg=$${t%%:*}; fn=$${t#*:}; \
		go test ./$$pkg -list "^$$fn$$" | grep -qx "$$fn" || { echo "fuzz: no $$fn in $$pkg: update FUZZ_TARGETS"; exit 1; }; \
		echo "fuzzing $$fn in $$pkg for $(FUZZTIME)"; \
		go test ./$$pkg -run '^$$' -fuzz "^$$fn$$" -fuzztime $(FUZZTIME) 2>&1 | tail -n 3 || exit 1; \
	done

live-test: ## The opt-in httpstore test against the running WireMock
	$(COMPOSE) up -d --wait mock
	LIVE_UPSTREAM_URL=$(WIREMOCK_URL) go test ./internal/item/httpstore -run Live -v -count=1

##@ Integration tests (Godog, against the whole stack)

integration: ## Start the test stack, run the suite, and always stop it again
	$(MAKE) integration-up
	@$(MAKE) integration-test; status=$$?; \
	if [ $$status -ne 0 ]; then $(MAKE) integration-logs; fi; \
	$(MAKE) integration-down; exit $$status

integration-up: ## Start the stack with the integration overrides (BUILD=--no-build to use a built image)
	$(COMPOSE_INTEGRATION) up -d $(or $(BUILD),--build) --wait

integration-test: ## Run the suite against a running stack (TAGS=@smoke, ARGS=...)
	go test -C integration ./... -v -count=1 $(if $(TAGS),-godog.tags=$(TAGS)) $(ARGS)

integration-logs: ## Print the test stack's logs
	$(COMPOSE_INTEGRATION) logs --no-color

integration-down: ## Stop the test stack
	$(COMPOSE_INTEGRATION) down

##@ Performance (see docs/performance.md)

monitor: ## Start the stack with monitoring: a Grafana dashboard of load, containers and the Go runtime
	$(COMPOSE) --profile monitoring up -d --build --wait
	@echo "Grafana: http://localhost:3000 (the performance dashboard)   Prometheus: http://localhost:9090"
	@echo "Load runs (make perf PROFILE=load) show up on the dashboard as they happen. make down stops it all."

perf: ## Start the stack and run a k6 profile (default PROFILE=smoke): make perf PROFILE=load
	$(COMPOSE) up -d --build --wait
	sh perf/run.sh $(or $(PROFILE),smoke)

perf-profile: ## Run a k6 profile and capture CPU, heap, allocation and goroutine profiles (default PROFILE=load)
	$(COMPOSE) up -d --build --wait
	sh perf/profile.sh $(or $(PROFILE),load) $(PPROF_URL) bin/perf/pprof-$$(date +%Y%m%d-%H%M%S)

perf-soak: ## Run the 30-minute soak profile and check the API's heap and goroutines don't grow (the nightly Soak workflow)
	$(COMPOSE) up -d --build --wait
	sh perf/soak.sh bin/perf/soak $(PROFILE)
	go run -C perf/report . soak -samples $(CURDIR)/bin/perf/soak/soak.samples.tsv $(SOAK_ARGS)

bench: ## Run the Go benchmarks, BENCH_COUNT times each, into bin/bench/current.txt
	@mkdir -p $(BENCH_OUT)
	go test -run '^$$' -bench . -benchmem -count $(BENCH_COUNT) ./... | tee $(BENCH_OUT)/current.txt

# The records on the perf-data branch, as `report trends` reads them.
TRENDS_DATA := bin/perf/trends-data
TRENDS_ARGS  = -records $(TRENDS_DATA)/records $$([ -d $(TRENDS_DATA)/ci ] && echo -ci-records $(TRENDS_DATA)/ci)

perf-data: ## Fetch the perf-data branch's records into bin/perf/trends-data, and build the report tool
	git fetch -q origin perf-data
	rm -rf $(TRENDS_DATA) && mkdir -p $(TRENDS_DATA)
	git archive FETCH_HEAD records | tar -x -C $(TRENDS_DATA)
	@# CI times (ci-times.yml) are charted once there are any.
	@if git cat-file -e FETCH_HEAD:ci 2>/dev/null; then git archive FETCH_HEAD ci | tar -x -C $(TRENDS_DATA); fi
	go build -C perf/report -o $(CURDIR)/bin/perf-report .

perf-trends: perf-data ## Chart main's performance and CI times over time from the perf-data branch: bin/perf/trends.html
	bin/perf-report trends $(TRENDS_ARGS) > bin/perf/trends.html
	@echo "open bin/perf/trends.html"

bench-compare: tools ## Run the benchmarks here and at BENCH_BASE (default main), interleaved, and compare them
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

##@ Releasing (see docs/releasing.md)

api-compat: ## What changed in the API since the last release, and whether the changelog says so for a breaking change
	sh scripts/api-compat.sh pr

smoke: ## Build the image for this machine and smoke-test it as the Release workflow does (PLATFORM=linux/amd64 for another)
	docker buildx build --platform $(or $(PLATFORM),linux/$(shell docker version -f '{{.Server.Arch}}')) --load --build-arg VERSION=v0.0.0-smoke -f cmd/api/Dockerfile -t vibe-smoke:local .
	sh scripts/smoke-image.sh vibe-smoke:local $(or $(PLATFORM),linux/$(shell docker version -f '{{.Server.Arch}}')) v0.0.0-smoke

release-prepare: ## Turn Unreleased into VERSION on a branch, for a pull request: make release-prepare VERSION=v0.1.0
	@echo "$(VERSION)" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$$' || { echo "usage: make release-prepare VERSION=vX.Y.Z"; exit 2; }
	@[ "$$(git rev-parse --abbrev-ref HEAD)" = main ] || { echo "switch to main first"; exit 1; }
	@[ -z "$$(git status --porcelain)" ] || { echo "commit or stash your changes first"; exit 1; }
	git pull -q --ff-only origin main
	git fetch -q --tags origin
	sh scripts/api-compat.sh release $(VERSION)
	git checkout -q -b release-$(VERSION)
	sh scripts/changelog.sh cut $(VERSION)
	sh scripts/spec-version.sh $(VERSION)
	git commit -q -am "Release $(VERSION)"
	@echo "Now push release-$(VERSION), open a pull request, and once it's merged: make release-tag VERSION=$(VERSION)"

release-tag: ## Tag VERSION on main and push the tag, which publishes the release: make release-tag VERSION=v0.1.0
	@echo "$(VERSION)" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$$' || { echo "usage: make release-tag VERSION=vX.Y.Z"; exit 2; }
	@[ "$$(git rev-parse --abbrev-ref HEAD)" = main ] || { echo "switch to main first"; exit 1; }
	git pull -q --ff-only origin main
	@sh scripts/changelog.sh notes $(VERSION) >/dev/null
	git tag -a $(VERSION) -m "Release $(VERSION)"
	git push origin $(VERSION)
	@echo "Pushed $(VERSION): the Release workflow builds, publishes and announces it."

##@ Documentation (rendered in Docker; nothing to install)

docs: ## Render the README, guides, changelog and API spec as a local site in bin/docs
	@# Start empty, so a page the build didn't produce can't survive from an earlier run.
	rm -rf $(DOCS_OUT)
	$(PANDOC) docs/site/build.sh $(DOCS_OUT) $(HISTORY)
	$(REDOCLY) build-docs docs/openapi.json -o $(DOCS_OUT)/api.html
	@echo "open $(DOCS_OUT)/index.html, or run make docs-open"

docs-open: docs ## Build the docs site and open it in your browser
	@if [ "$$(uname)" = Darwin ]; then open $(DOCS_OUT)/index.html; \
	elif command -v xdg-open >/dev/null; then xdg-open $(DOCS_OUT)/index.html; \
	else echo "open $(DOCS_OUT)/index.html in a browser"; fi

docs-check: docs ## Build the docs site and check every page exists and every link resolves
	DOCS_SITE=$(DOCS_OUT) go test ./docs/site -count=1 -v

docs-release: perf-data ## Build and check the docs site with its Performance history charted from perf-data, as a release attaches it
	bin/perf-report trends -fragment $(TRENDS_ARGS) > bin/perf/history.html
	$(MAKE) docs-check HISTORY=bin/perf/history.html

godoc: ## Browse the Go package documentation at http://localhost:6060 (Ctrl-C to stop)
	go run golang.org/x/pkgsite/cmd/pkgsite@v0.5.0 -http=localhost:6060 .

spec: ## Render docs/openapi.json as an HTML page, bin/openapi.html
	@mkdir -p bin
	$(REDOCLY) build-docs docs/openapi.json -o bin/openapi.html
	@echo "open bin/openapi.html"

spec-lint: ## Lint docs/openapi.json with Redocly's recommended rules
	$(REDOCLY) lint docs/openapi.json
