GOLANGCI_LINT_VERSION ?= v2.2.1
GOLANGCI_LINT_BIN ?= $(shell go env GOPATH)/bin/golangci-lint

$(GOLANGCI_LINT_BIN):
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

.PHONY: lint
lint: $(GOLANGCI_LINT_BIN) ## Run golangci-lint on the codebase
	$(GOLANGCI_LINT_BIN) run --timeout 10m ./...

.PHONY: lint-fix
lint-fix: $(GOLANGCI_LINT_BIN) ## Run golangci-lint with auto-fix
	$(GOLANGCI_LINT_BIN) run --fix --timeout 10m ./...

.PHONY: fmt
fmt: $(GOLANGCI_LINT_BIN) ## Format code using golangci-lint formatters
	$(GOLANGCI_LINT_BIN) fmt ./...

.PHONY: verify
verify: lint ## Verify code conventions
.PHONY: verify

.PHONY: test-unit
test-unit: ## Run unit tests
	go test $(TESTFLAGS) ./...
.PHONY: test-unit

.PHONY: check
check: verify test-unit ## Run verify and unit tests
.PHONY: check

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf _output .cache
.PHONY: clean

# E2E Tests
# Use GINKGO_FLAGS to pass extra flags, e.g.: make test-e2e-hpa GINKGO_FLAGS="--label-filter=slow"
GINKGO_FLAGS ?=

.PHONY: test-e2e
test-e2e: ## Run all E2E tests
	go test -v -timeout 30m ./test/e2e/... -args -ginkgo.v $(GINKGO_FLAGS)

.PHONY: test-e2e-vpa
test-e2e-vpa: ## Run VPA E2E tests
	go test -v -timeout 30m ./test/e2e/vpa/... -args -ginkgo.v $(GINKGO_FLAGS)

.PHONY: test-e2e-hpa
test-e2e-hpa: ## Run HPA E2E tests
	go test -v -timeout 30m ./test/e2e/hpa/... -args -ginkgo.v $(GINKGO_FLAGS)

.PHONY: test-e2e-cas
test-e2e-cas: ## Run Cluster Autoscaler E2E tests
	make -C cas test-e2e

.PHONY: test-e2e-cro
test-e2e-cro: ## Run CRO E2E tests
	go test -v -timeout 30m ./test/e2e/cro/... -args -ginkgo.v $(GINKGO_FLAGS)

.PHONY: test-e2e-cma
test-e2e-cma: ## Run CMA E2E tests
	go test -v -timeout 30m ./test/e2e/cma/... -args -ginkgo.v $(GINKGO_FLAGS)
