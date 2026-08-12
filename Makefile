# datapin developer tasks. Run `make` (or `make help`) to list targets.
#
# Live-test credentials are read from the environment, or from a git-ignored
# .env file (copy .env.example → .env and fill it in). Nothing here contains
# secrets, so this file is safe to commit.

GO ?= go

# Load .env if present and export its vars to recipe commands.
-include .env
export

.DEFAULT_GOAL := help

COVDIR := coverage

.PHONY: help build test integration live live-repro fmt vet check cover man site

help: ## List available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

build: ## Build the datapin binary
	$(GO) build -o datapin .

man: build ## Generate man/datapin.1 from the live command tree
	./datapin man --out man/datapin.1

site: build ## Build datapin's own documentation site into public/
	./datapin site build

test: ## Unit tests (race detector)
	$(GO) test -race ./...

integration: ## Integration tests against the in-process fakeosf server
	$(GO) test -tags integration -count=1 ./integration/...

live: ## Live tests against a real OSF project (needs OSF_TEST_TOKEN/PROJECT[/COMPONENT])
	$(GO) test -tags live -count=1 -v ./integration/live/...

live-repro: ## Live: run only the cross-project 404 regression test
	$(GO) test -tags live -count=1 -v ./integration/live/ -run TestLive_ComponentPushNewVersion

fmt: ## Fail if anything is not gofmt-formatted
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then echo "not gofmt'd:"; echo "$$unformatted"; exit 1; fi

vet: ## go vet
	$(GO) vet ./...

check: fmt vet test integration ## Everything CI runs (except the live tier)

cover: ## Merged unit + integration coverage (real end-to-end numbers)
	rm -rf $(COVDIR); mkdir -p $(COVDIR)/unit $(COVDIR)/int
	$(GO) test ./... -cover -args -test.gocoverdir=$(abspath $(COVDIR)/unit)
	DATAPIN_COVERDIR=$(abspath $(COVDIR)/int) $(GO) test -tags integration -count=1 ./integration/...
	@echo "── merged coverage ──"
	$(GO) tool covdata percent -i=$(COVDIR)/unit,$(COVDIR)/int
	$(GO) tool covdata textfmt -i=$(COVDIR)/unit,$(COVDIR)/int -o=$(COVDIR)/coverage.txt
	@echo "full profile: $(COVDIR)/coverage.txt  (view: go tool cover -func=$(COVDIR)/coverage.txt)"
