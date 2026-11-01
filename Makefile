# Hive developer tasks. `make help` lists them.
#
# Tool versions are pinned here and installed into ./bin on first use, so
# every machine and CI run uses the same linter and formatter.

GO            ?= go
BIN           := $(CURDIR)/bin
PKG           := github.com/admirable-oss/hive
FUZZTIME      ?= 30s

GOLANGCI_LINT_VERSION ?= v2.14.0
GORELEASER_VERSION    ?= v2.18.2
GOVULNCHECK_VERSION   ?= v1.9.0

GOLANGCI_LINT := $(BIN)/golangci-lint-$(GOLANGCI_LINT_VERSION)
GORELEASER    := $(BIN)/goreleaser-$(GORELEASER_VERSION)

# Annotated tags only: hive releases are annotated (git tag -a), while the
# official plugins' release tags in this repository (v0.1.0, …) are
# lightweight and must never become hive's version.
VERSION ?= $(shell git describe --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse HEAD 2>/dev/null)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X $(PKG)/internal/buildinfo.version=$(VERSION) \
	-X $(PKG)/internal/buildinfo.commit=$(COMMIT) \
	-X $(PKG)/internal/buildinfo.date=$(DATE)

# Fuzz targets as <package>:<FuzzName>.
FUZZ_TARGETS := \
	./internal/protocol/tests:FuzzStreamReceive \
	./internal/config:FuzzParse \
	./internal/vt:FuzzDecodeFrame \
	./internal/vt:FuzzKittyGraphicsParser \
	./internal/vt:FuzzTitleFilter \
	./internal/plugin:FuzzParseManifest \
	./internal/agent:FuzzParseManifest \
	./internal/discovery:FuzzParseMessage \
	./internal/task:FuzzParsePlan \
	./internal/workflow:FuzzParseWorkflow \
	./internal/workflow:FuzzExpressions \
	./internal/presence:FuzzRegistry \
	./internal/insights:FuzzReadTranscript

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ {printf "  \033[33m%-10s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: build
build: ## Build ./hive with version information
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o hive ./cmd/hive

.PHONY: install
install: ## Install hive into $GOBIN
	CGO_ENABLED=0 $(GO) install -trimpath -ldflags '$(LDFLAGS)' ./cmd/hive

.PHONY: test
test: ## Run the tests
	$(GO) test -count=1 ./...

.PHONY: race
race: ## Run the tests with the race detector (what CI runs)
	$(GO) test -race -count=1 ./...

.PHONY: short
short: ## Run the tests, skipping end-to-end CLI runs
	$(GO) test -short -count=1 ./...

.PHONY: cover
cover: ## Run the tests with coverage and print a summary
	$(GO) test -race -count=1 -coverpkg=./... -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out | tail -1

# A target is run once more when it fails with nothing but the fuzzing
# engine's own "context deadline exceeded" (the -fuzztime deadline landing
# mid-call to a worker) and no failing input written: a toolchain flake, not
# a finding. Any other failure, or a new testdata/fuzz input, fails at once.
.PHONY: fuzz
fuzz: ## Run every fuzz target for FUZZTIME (default 30s)
	@for t in $(FUZZ_TARGETS); do \
		pkg=$${t%%:*}; name=$${t##*:}; \
		echo "fuzz $$name ($$pkg, $(FUZZTIME))"; \
		out=$$(mktemp); \
		$(GO) test -run='^$$' -fuzz="^$$name$$" -fuzztime=$(FUZZTIME) $$pkg > $$out 2>&1; rc=$$?; cat $$out; \
		if [ $$rc -ne 0 ]; then \
			if grep -q "context deadline exceeded" $$out && ! grep -q "Failing input written" $$out; then \
				echo "fuzz $$name: the engine's deadline flake; once more"; \
				$(GO) test -run='^$$' -fuzz="^$$name$$" -fuzztime=$(FUZZTIME) $$pkg || { rm -f $$out; exit 1; }; \
			else rm -f $$out; exit 1; fi; \
		fi; \
		rm -f $$out; \
	done

.PHONY: bench
bench: ## Run the benchmarks (BENCH_OUT=file keeps them for bench-compare)
	$(GO) test -run='^$$' -bench=. -benchmem -count=5 ./... | tee $(or $(BENCH_OUT),/dev/null)

.PHONY: bench-compare
bench-compare: ## Compare benchmarks with main: make bench-compare
	@tmp=$$(mktemp -d); \
	git worktree add -q $$tmp/main main && \
	(cd $$tmp/main && $(GO) test -run='^$$' -bench=. -benchmem -count=5 ./... > $$tmp/old.txt); \
	$(GO) test -run='^$$' -bench=. -benchmem -count=5 ./... > $$tmp/new.txt; \
	$(GO) run golang.org/x/perf/cmd/benchstat@latest $$tmp/old.txt $$tmp/new.txt; \
	git worktree remove --force $$tmp/main; rm -rf $$tmp

.PHONY: perf
perf: ## Check the performance budgets (frame time, idle CPU, input latency)
	$(GO) test -count=1 -run 'TestFrameTime100Panes|TestRuntime_IdleCPUWith100Panes|TestInputLatency16Panes' -v ./internal/tui/mux ./internal/runtime/tests

.PHONY: chaos
chaos: ## Run the chaos soak for HIVE_CHAOS (default 20s)
	$(GO) test -count=1 -timeout 80h -run '^TestChaos$$' -v ./cmd/hive

.PHONY: upgrade
upgrade: ## Upgrade from earlier releases and back: make upgrade FROM="v1.0.0-rc.1" (tags or binaries)
	HIVE_UPGRADE_FROM="$(or $(FROM),$(shell git tag -l 'v1.*'))" $(GO) test -count=1 -timeout 30m -run '^TestM19UpgradeFromEarlierReleases$$' -v ./cmd/hive

.PHONY: vet
vet: ## Run go vet
	$(GO) vet ./...

.PHONY: lint
lint: $(GOLANGCI_LINT) ## Run the linters (golangci-lint, pinned)
	$(GOLANGCI_LINT) run ./...

.PHONY: fmt
fmt: $(GOLANGCI_LINT) ## Format the code (gofumpt + goimports)
	$(GOLANGCI_LINT) fmt ./...

.PHONY: tidy
tidy: ## Tidy go.mod and go.sum
	$(GO) mod tidy

.PHONY: vuln
vuln: ## Report known vulnerabilities the code can reach (govulncheck, pinned)
	$(GO) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

.PHONY: check
check: vet lint race ## Everything CI checks: vet, lint, race tests
	$(GO) mod tidy -diff

.PHONY: snapshot
snapshot: $(GORELEASER) ## Build release archives locally into ./dist (no publishing, unsigned)
	GORELEASER_CURRENT_TAG=$$(git describe --abbrev=0 2>/dev/null || echo v0.0.0) $(GORELEASER) release --snapshot --clean --skip=sign,sbom

.PHONY: reproducible
reproducible: $(GORELEASER) ## Build the release archives twice and check they are identical
	GORELEASER=$(GORELEASER) sh .github/scripts/reproducible.sh

.PHONY: clean
clean: ## Remove build output
	rm -rf hive dist coverage.out

$(GOLANGCI_LINT):
	@mkdir -p $(BIN)
	GOBIN=$(BIN) $(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	mv $(BIN)/golangci-lint $@

$(GORELEASER):
	@mkdir -p $(BIN)
	GOBIN=$(BIN) $(GO) install github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION)
	mv $(BIN)/goreleaser $@
