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

GOLANGCI_LINT := $(BIN)/golangci-lint-$(GOLANGCI_LINT_VERSION)
GORELEASER    := $(BIN)/goreleaser-$(GORELEASER_VERSION)

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse HEAD 2>/dev/null)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X $(PKG)/internal/buildinfo.version=$(VERSION) \
	-X $(PKG)/internal/buildinfo.commit=$(COMMIT) \
	-X $(PKG)/internal/buildinfo.date=$(DATE)

# Fuzz targets as <package>:<FuzzName>.
FUZZ_TARGETS := \
	./internal/protocol/tests:FuzzStreamReceive \
	./internal/config:FuzzParse

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

.PHONY: fuzz
fuzz: ## Run every fuzz target for FUZZTIME (default 30s)
	@for t in $(FUZZ_TARGETS); do \
		pkg=$${t%%:*}; name=$${t##*:}; \
		echo "fuzz $$name ($$pkg, $(FUZZTIME))"; \
		$(GO) test -run='^$$' -fuzz="^$$name$$" -fuzztime=$(FUZZTIME) $$pkg || exit 1; \
	done

.PHONY: bench
bench: ## Run the benchmarks
	$(GO) test -run='^$$' -bench=. -benchmem ./...

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

.PHONY: check
check: vet lint race ## Everything CI checks: vet, lint, race tests
	$(GO) mod tidy -diff

.PHONY: snapshot
snapshot: $(GORELEASER) ## Build release archives locally into ./dist (no publishing)
	$(GORELEASER) release --snapshot --clean

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
