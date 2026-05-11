# Makefile for openserve monorepo
#
# Environment Variables:
#   VERSION              - Git tag or commit hash for image versioning (default: "dev")
#   REGISTRY             - Docker registry URL (default: "ghcr.io/openserve")
#   BUCKET               - GCS bucket for model caching (required for dev-operator)
#   DOMAIN               - Gateway domain (required for dev-operator)
#   GCP_PROJECT          - GCP project ID (required for dev-operator, Terraform)
#   POSTGRES_URL         - PostgreSQL connection string (required for dev-api, dev-gateway)
#   JWT_SECRET           - JWT signing secret (required for dev-api)
#   GOOGLE_CLIENT_ID     - Google OAuth client ID (required for dev-api)
#   REDIS_ADDR           - Redis server address (required for dev-gateway)

.PHONY: help tidy lint test test-short typecheck build generate docker-build docker-push \
	helm-lint helm-install dev-operator dev-api dev-gateway dev-gui fmt vuln clean \
	tf-init tf-plan tf-apply

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
REGISTRY ?= ghcr.io/openserve
COMPONENTS := operator control-api gateway gui
GO_MODULES := operator apps/control-api apps/gateway

# Default target
help:
	@awk 'BEGIN {FS = ":.*##"; printf "Usage:\n  make \033[36m<target>\033[0m\n\nTargets:\n"} /^[a-zA-Z_-]+:.*?##/ {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

tidy: ## Tidy Go module dependencies
	@for module in $(GO_MODULES); do \
		echo "Tidying $$module..."; \
		cd $$module && go mod tidy && cd ../..; \
	done

lint: ## Run linters (golangci-lint for Go, pnpm lint for GUI)
	@for module in $(GO_MODULES); do \
		echo "Linting $$module..."; \
		cd $$module && golangci-lint run && cd ../..; \
	done
	@echo "Linting apps/gui..."
	@cd apps/gui && pnpm lint

test: ## Run all tests with race detection and coverage
	@for module in $(GO_MODULES); do \
		echo "Testing $$module..."; \
		cd $$module && go test ./... -race -coverprofile=coverage.txt && cd ../..; \
	done

test-short: ## Run tests (skipping integration tests)
	@for module in $(GO_MODULES); do \
		echo "Testing $$module (short)..."; \
		cd $$module && go test ./... -short -race -coverprofile=coverage.txt && cd ../..; \
	done

typecheck: ## Type check GUI with TypeScript compiler
	@cd apps/gui && pnpm typecheck

build: ## Build all binaries locally into bin/
	@mkdir -p bin
	@echo "Building operator..."
	@cd operator && go build -o ../bin/manager ./cmd/...
	@echo "Building control-api..."
	@cd apps/control-api && go build -o ../../bin/control-api ./cmd/server/...
	@echo "Building gateway..."
	@cd apps/gateway && go build -o ../../bin/gateway ./cmd/...

generate: ## Generate CRD manifests, RBAC roles, and deepcopy code
	@echo "Generating operator CRDs and RBAC..."
	@cd operator && controller-gen object:headerFile="hack/boilerplate.go.txt" paths="./api/..."
	@cd operator && controller-gen crd paths="./api/..." output:crd:artifacts:config=../charts/openserve/crds
	@cd operator && controller-gen rbac:roleName=openserve-operator paths="./..." output:rbac:artifacts:config=config/rbac

docker-build: $(addprefix docker-build-,$(COMPONENTS)) ## Build all Docker images

docker-build-%:
	@echo "Building image for $*..."
	@docker buildx build \
		--build-arg VERSION=$(VERSION) \
		$(if $(filter gui,$*),--build-arg NEXT_PUBLIC_API_URL=https://api.example.com,) \
		-t $(REGISTRY)/openserve-$*:$(VERSION) \
		-f $(if $(filter operator,$*),operator,apps/$*)/Dockerfile \
		.

docker-push: $(addprefix docker-push-,$(COMPONENTS)) ## Push all Docker images to registry

docker-push-%:
	@echo "Pushing image for $*..."
	@docker push $(REGISTRY)/openserve-$*:$(VERSION)

helm-lint: ## Lint Helm chart
	@echo "Linting Helm chart..."
	@helm lint charts/openserve \
		--set domain=test.example.com \
		--set operator.image.tag=$(VERSION) \
		--set controlApi.image.tag=$(VERSION) \
		--set gateway.image.tag=$(VERSION) \
		--set gui.image.tag=$(VERSION)

helm-install: ## Install or upgrade Helm release
	@echo "Installing/upgrading openserve Helm release..."
	@helm upgrade --install openserve charts/openserve \
		-n openserve-system \
		--create-namespace \
		--set operator.image.tag=$(VERSION) \
		--set controlApi.image.tag=$(VERSION) \
		--set gateway.image.tag=$(VERSION) \
		--set gui.image.tag=$(VERSION)

dev-operator: ## Run operator locally (requires BUCKET, DOMAIN, GCP_PROJECT env vars)
	@cd operator && go run ./cmd/... \
		--model-cache-bucket=$(BUCKET) \
		--gateway-domain=$(DOMAIN) \
		--bigquery-dataset=openserve_usage \
		--gcp-project=$(GCP_PROJECT)

dev-api: ## Run control-api locally (requires POSTGRES_URL, JWT_SECRET, GOOGLE_CLIENT_ID env vars)
	@cd apps/control-api && go run ./cmd/server/... \
		--postgres-url=$(POSTGRES_URL) \
		--jwt-secret=$(JWT_SECRET) \
		--google-client-id=$(GOOGLE_CLIENT_ID)

dev-gateway: ## Run gateway locally (requires POSTGRES_URL, REDIS_ADDR env vars)
	@cd apps/gateway && go run ./cmd/... \
		--postgres-url=$(POSTGRES_URL) \
		--redis-addr=$(REDIS_ADDR)

dev-gui: ## Run GUI dev server
	@cd apps/gui && pnpm dev

fmt: ## Format code (gofmt for Go, pnpm format for GUI if available)
	@for module in $(GO_MODULES); do \
		echo "Formatting $$module..."; \
		cd $$module && gofmt -w -s . && cd ../..; \
	done
	@if [ -f apps/gui/package.json ] && grep -q '"format"' apps/gui/package.json; then \
		echo "Formatting apps/gui..."; \
		cd apps/gui && pnpm format; \
	fi

vuln: ## Check for vulnerabilities using govulncheck
	@for module in $(GO_MODULES); do \
		echo "Checking vulnerabilities in $$module..."; \
		cd $$module && govulncheck ./... && cd ../..; \
	done

clean: ## Clean build artifacts and coverage files
	@echo "Cleaning..."
	@rm -rf bin/
	@find . -name "coverage.txt" -delete
	@find . -name "*.out" -delete

tf-init: ## Initialize Terraform (GCP prerequisites)
	@cd examples/terraform/gcp-prereqs && terraform init

tf-plan: ## Plan Terraform changes (requires GCP_PROJECT env var)
	@cd examples/terraform/gcp-prereqs && terraform plan -var gcp_project=$(GCP_PROJECT)

tf-apply: ## Apply Terraform changes (requires GCP_PROJECT env var)
	@cd examples/terraform/gcp-prereqs && terraform apply -auto-approve -var gcp_project=$(GCP_PROJECT)
