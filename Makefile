SHELL       := /bin/bash
GO          ?= go
SERVICES    := gateway orders worker
IMAGE_PREFIX ?= obs-lab
IMAGE_TAG   ?= dev
KIND_CLUSTER ?= obs-lab

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

.PHONY: images
images: ## Build a container image for every service
	@for svc in $(SERVICES); do \
		echo ">> building $(IMAGE_PREFIX)/$$svc:$(IMAGE_TAG)"; \
		docker build --build-arg SERVICE=$$svc -t $(IMAGE_PREFIX)/$$svc:$(IMAGE_TAG) . || exit 1; \
	done

.PHONY: kind-up
kind-up: ## Create the local kind cluster
	kind create cluster --config k8s/kind-config.yaml

.PHONY: kind-down
kind-down: ## Delete the local kind cluster
	kind delete cluster --name $(KIND_CLUSTER)

.PHONY: kind-load
kind-load: ## Load all service images into the kind cluster
	@for svc in $(SERVICES); do \
		echo ">> loading $(IMAGE_PREFIX)/$$svc:$(IMAGE_TAG) into kind"; \
		kind load docker-image $(IMAGE_PREFIX)/$$svc:$(IMAGE_TAG) --name $(KIND_CLUSTER) || exit 1; \
	done
