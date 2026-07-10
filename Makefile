SHELL := /bin/bash
GO    ?= go

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-16s\033[0m %s\n",$$1,$$2}'

.PHONY: test
test: ## Run all Go unit tests
	$(GO) test ./...

.PHONY: test-masking
test-masking: ## Run the PII-masking proof suite (verbose)
	$(GO) test ./pkg/masking/ -v

.PHONY: tidy
tidy: ## Sync go.mod/go.sum (needs network)
	$(GO) mod tidy

.PHONY: vet
vet: ## go vet
	$(GO) vet ./...

.PHONY: tools
tools: ## Install kind/kubectl/helm/terraform (Phase 4)
	@echo "Phase 4 target — installs cluster tooling"

.PHONY: kind-up
kind-up: ## Create the local kind cluster
	kind create cluster --config k8s/kind-config.yaml

.PHONY: kind-down
kind-down: ## Delete the local kind cluster
	kind delete cluster --name obs-lab
