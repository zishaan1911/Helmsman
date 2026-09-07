BIN        := bin
CMDS       := pipeline risk-reviewer health-watcher detector containerizer manifest-generator gitops-writer
COVERAGE   := coverage.out
GOFILES    := $(shell find . -name '*.go' -not -path './$(BIN)/*')

# Coverage is measured over ./pkg/... — the packages that hold the logic.
# cmd/ is thin flag parsing by design (see CONTRIBUTING.md) and counting it
# would just dilute the number. -coverpkg means internal/e2e's cross-package
# run counts toward it too.
COVER_PKGS := ./pkg/...

# Fail CI if statement coverage drops below this. Raise it as coverage
# improves; never lower it to make a red build green.
COVER_MIN  := 80

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Compile every package
	go build ./...

.PHONY: binaries
binaries: ## Build all CLIs into bin/
	@mkdir -p $(BIN)
	@for cmd in $(CMDS); do \
		echo "  building $$cmd"; \
		go build -o $(BIN)/$$cmd ./cmd/$$cmd || exit 1; \
	done

.PHONY: test
test: ## Run the test suite with the race detector
	go test -race ./...

.PHONY: cover
cover: ## Run tests with coverage and print a per-function summary
	go test -race -coverprofile=$(COVERAGE) -covermode=atomic -coverpkg=$(COVER_PKGS) ./...
	@go tool cover -func=$(COVERAGE) | tail -n 1

.PHONY: cover-html
cover-html: cover ## Open the coverage report in a browser
	go tool cover -html=$(COVERAGE)

.PHONY: cover-check
cover-check: cover ## Fail if coverage is below COVER_MIN
	@total=$$(go tool cover -func=$(COVERAGE) | awk '/^total:/ {print $$3}' | tr -d '%'); \
	echo "total coverage: $$total% (minimum $(COVER_MIN)%)"; \
	awk -v t="$$total" -v m="$(COVER_MIN)" 'BEGIN { exit (t+0 >= m+0) ? 0 : 1 }' \
		|| { echo "coverage $$total% is below the $(COVER_MIN)% minimum"; exit 1; }

.PHONY: fmt
fmt: ## Format all Go files
	gofmt -w $(GOFILES)

.PHONY: lint
lint: ## Check formatting and run go vet
	@out="$$(gofmt -l .)"; \
	if [ -n "$$out" ]; then \
		echo "not gofmt'd:"; echo "$$out"; \
		echo "run 'make fmt'"; \
		exit 1; \
	fi
	go vet ./...

.PHONY: vuln
vuln: ## Scan for known vulnerabilities
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

.PHONY: tidy
tidy: ## Tidy go.mod
	go mod tidy

.PHONY: check
check: lint build test ## Everything CI runs on a pull request

.PHONY: demo
demo: binaries ## Run the pipeline against examples/sample-app in a scratch GitOps repo
	@rm -rf /tmp/gitops-demo /tmp/sample-app-demo
	@git init -q -b main /tmp/gitops-demo
	@git -C /tmp/gitops-demo commit -q --allow-empty -m init
	@cp -r examples/sample-app /tmp/sample-app-demo
	./$(BIN)/pipeline \
		-app-repo /tmp/sample-app-demo \
		-gitops-repo /tmp/gitops-demo \
		-app sample-app \
		-image registry.example.com/sample-app:sha-demo \
		-env staging \
		-gitops-repo-url https://github.com/you/gitops-repo.git
	@echo
	@git -C /tmp/gitops-demo show --stat HEAD

.PHONY: clean
clean: ## Remove build and coverage artifacts
	rm -rf $(BIN) $(COVERAGE)
