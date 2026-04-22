.DEFAULT_GOAL := help

GO          ?= go
GOFMT       ?= gofmt
GOLINT      ?= golangci-lint
COVER_FILE  ?= coverage.out
COVER_HTML  ?= coverage.html
COVER_MIN   ?= 70

.PHONY: help
help: ## Show available targets
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: build
build: ## Build the notioncli binary
	$(GO) build -v ./...

.PHONY: fmt
fmt: ## Format sources with gofmt
	$(GOFMT) -w .

.PHONY: fmt-check
fmt-check: ## Fail if any source file is not gofmt-clean
	@out=$$( $(GOFMT) -l . ); \
	if [ -n "$$out" ]; then \
		echo "gofmt violations:"; echo "$$out"; exit 1; \
	fi

.PHONY: vet
vet: ## go vet
	$(GO) vet ./...

.PHONY: lint
lint: ## golangci-lint (skips gracefully if not installed)
	@if command -v $(GOLINT) >/dev/null 2>&1; then \
		$(GOLINT) run ./...; \
	else \
		echo "golangci-lint not installed — skipping. Install: https://golangci-lint.run/welcome/install/"; \
	fi

.PHONY: test
test: ## Run unit tests
	$(GO) test ./...

.PHONY: test-race
test-race: ## Run tests with the race detector
	$(GO) test -race ./...

.PHONY: test-verbose
test-verbose: ## Verbose test run
	$(GO) test -v ./...

.PHONY: cover
cover: ## Generate coverage profile and print per-function coverage
	$(GO) test -coverprofile=$(COVER_FILE) -covermode=atomic ./...
	$(GO) tool cover -func=$(COVER_FILE) | tail -n 20
	@pct=$$( $(GO) tool cover -func=$(COVER_FILE) | awk '/^total:/ {print $$3}' | tr -d '%' ); \
	echo "Total coverage: $$pct% (gate: $(COVER_MIN)%)"; \
	awk "BEGIN {exit !($$pct < $(COVER_MIN))}" && { echo "Coverage below gate"; exit 1; } || true

.PHONY: cover-html
cover-html: cover ## Open HTML coverage report
	$(GO) tool cover -html=$(COVER_FILE) -o $(COVER_HTML)
	@echo "Coverage report written to $(COVER_HTML)"

.PHONY: check-test-gaps
check-test-gaps: ## List exported functions that lack a matching Test* function
	@bash scripts/check-test-coverage.sh

.PHONY: ci
ci: fmt-check vet test-race cover check-test-gaps ## Run everything CI runs

.PHONY: install-hooks
install-hooks: ## Install the repo's git pre-commit hook
	@bash scripts/install-hooks.sh

.PHONY: tidy
tidy: ## Tidy go.mod
	$(GO) mod tidy

.PHONY: clean
clean: ## Remove build and coverage artifacts
	rm -f $(COVER_FILE) $(COVER_HTML) notioncli

# ----- Lando runner (containerized Go toolchain) -----
# See .lando.yml. Use these when Go is not installed on the host, or to
# verify CI parity. All real work is defined in .lando.yml's `tooling:`
# block; the Makefile targets are thin shims.

.PHONY: lando-start
lando-start: ## Start the Lando appserver (builds on first run)
	lando start

.PHONY: lando-stop
lando-stop: ## Stop the Lando appserver
	lando stop

.PHONY: lando-rebuild
lando-rebuild: ## Rebuild the Lando appserver image
	lando rebuild -y

.PHONY: lando-test
lando-test: ## go test ./... inside Lando
	lando test

.PHONY: lando-test-race
lando-test-race: ## go test -race ./... inside Lando
	lando test-race

.PHONY: lando-cover
lando-cover: ## Coverage profile + summary inside Lando
	lando cover

.PHONY: lando-check-test-gaps
lando-check-test-gaps: ## Flag untested exported functions (via Lando)
	lando check-test-gaps

.PHONY: lando-lint
lando-lint: ## golangci-lint inside Lando
	lando lint

.PHONY: lando-ci
lando-ci: ## Everything CI runs, executed inside Lando
	lando ci

.PHONY: lando-shell
lando-shell: ## Open a bash shell in the Lando appserver
	lando shell
