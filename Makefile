BINARY := wloop
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
MODULE := github.com/wloophq/wloop
GOLANGCI_LINT_VERSION := v2.15.0
GOLANGCI_LINT := bin/golangci-lint-$(GOLANGCI_LINT_VERSION)
BRAND_TEST_LDFLAGS := -X $(MODULE)/internal/brand.Name=zzbrand -X "$(MODULE)/internal/brand.Command=gh zzbrand" -X $(MODULE)/internal/brand.App=Zzbrand -X $(MODULE)/internal/brand.Org=zzbrandhq

.PHONY: build test lint fmt-check fmt fuzz clean eval-instructions test-brand scan-brand scan-real-data print-brand-ldflags docs-ref docs

build: ## Build the binary into ./bin
	@mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/$(BINARY)

test: ## Run the tests
	go test -race -timeout 30m ./...

test-brand: ## Run the tests built with the test brand
	go test -race -timeout 30m -ldflags '$(BRAND_TEST_LDFLAGS)' ./...

scan-brand: ## Check that a build with the test brand writes nothing that names the default brand
	@d=$$(mktemp -d) && trap 'rm -rf "$$d"' EXIT && \
	go build -ldflags '$(BRAND_TEST_LDFLAGS)' -o "$$d/zz/bin/zz" ./cmd/$(BINARY) && \
	LOOP_BRAND_SCAN_BIN="$$d/zz/bin/zz" go test -count=1 -ldflags '$(BRAND_TEST_LDFLAGS)' -run TestNothingWrittenNamesTheDefaultBrand ./internal/e2e

print-brand-ldflags: ## Print the test brand's ldflags, to update goldens
	@echo '$(BRAND_TEST_LDFLAGS)'

scan-real-data: ## Check that no commit since SCAN_BASE adds a term of the deny list at LOOP_DENY_LIST
	@test -n "$(SCAN_BASE)" || (echo "SCAN_BASE is not set"; exit 2)
	scripts/scan-real-data.sh $(SCAN_BASE)

lint: fmt-check $(GOLANGCI_LINT) ## gofmt and golangci-lint, for linux and darwin
	GOOS=linux $(GOLANGCI_LINT) run ./...
	GOOS=darwin $(GOLANGCI_LINT) run ./...

fmt-check: ## Check that every Go file is gofmt-formatted
	@test -z "$$(git ls-files -co --exclude-standard '*.go' | xargs gofmt -l)" || (echo "gofmt needed:"; git ls-files -co --exclude-standard '*.go' | xargs gofmt -l; exit 1)

$(GOLANGCI_LINT):
	@mkdir -p bin
	GOBIN=$(CURDIR)/bin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	mv bin/golangci-lint $@

fuzz: ## Fuzz the predicate compilers for 60 s each
	go test ./internal/intent/expr -run '^$$' -fuzz '^FuzzCompile$$' -fuzztime 60s
	go test ./internal/intent/expr -run '^$$' -fuzz '^FuzzCompileWorkflow$$' -fuzztime 60s

fmt: ## Format the code
	gofmt -w .

eval-instructions: ## Judge the rendered instructions of every eval scenario
	CGO_ENABLED=0 go run ./cmd/$(BINARY) eval-instructions

docs-ref: ## Regenerate the commands and facts reference pages of the documentation site
	@d=$$(mktemp -d) && trap 'rm -rf "$$d"' EXIT && go build -o "$$d/$(BINARY)" ./cmd/$(BINARY) && \
	node docs/scripts/commands.mjs "$$d/$(BINARY)" "$$d" > docs/src/content/docs/reference/commands.md
	@f=$$(go run ./internal/intent/builtin/cmd/factsdoc) && printf '%s\n' "$$f" | node docs/scripts/facts.mjs > docs/src/content/docs/reference/facts.md

docs: ## Build the documentation site into docs/dist
	cd docs && npm ci && npm run build

clean:
	rm -rf bin
