# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Repository overview

openserve is a BYOC (Bring Your Own Cloud) LLM serving platform. Customers `helm install` openserve into their own GCP project; all inference stays inside their VPC. The repo is a Go + Next.js monorepo with four independent Go modules.

## Go modules — tidy each separately

There is no workspace file. Each module must be tidied from its own directory:

```bash
cd operator && go mod tidy
cd apps/control-api && go mod tidy
cd apps/gateway && go mod tidy
```

Module paths:
- `github.com/openserve/openserve/operator`
- `github.com/openserve/openserve/apps/control-api`
- `github.com/openserve/openserve/apps/gateway`

## Common commands

### Go (run from the module directory, not the repo root)

```bash
# Test all packages with race detector
go test ./... -race

# Test a single package
go test ./internal/controller/... -run TestModelDeploymentReconciler -v

# Lint (requires golangci-lint v1.57.2)
golangci-lint run

# Vulnerability scan
govulncheck ./...
```

### Operator — generate CRD manifests after changing types

After editing any file in `operator/api/v1alpha1/`, regenerate:

```bash
cd operator
go generate ./...
# or if controller-gen is installed:
controller-gen object:headerFile="hack/boilerplate.go.txt" paths="./api/..."
controller-gen rbac:roleName=openserve-operator crd paths="./api/..." output:crd:artifacts:config=config/crd/bases
```

### GUI

```bash
cd apps/gui
pnpm install          # install deps (uses pnpm, not npm/yarn)
pnpm dev              # dev server on :3000
pnpm build            # production build (output: standalone for Docker)
pnpm typecheck        # tsc --noEmit, run before pushing
pnpm lint             # eslint via next lint
```

### Helm

```bash
# Validate the chart (required values shown)
helm lint charts/openserve \
  --set domain=test.example.com,google.clientId=test,postgres.host=1.2.3.4,redis.host=1.2.3.5,gcs.bucket=test-bucket

# Render templates locally
helm template openserve charts/openserve --set domain=... | kubectl apply --dry-run=client -f -

# Update chart dependencies (cert-manager, KEDA subcharts)
helm dependency update charts/openserve
```

### Terraform

```bash
cd examples/terraform/gcp-prereqs
cp terraform.tfvars.example terraform.tfvars   # fill in gcp_project
terraform init
terraform plan
terraform apply
```

## Architecture

### Request flow

```
Internet → Cloud Armor → Load Balancer → gateway (Go)
                                              │
                         ┌────────────────────┴───────────────┐
                         │  1. validate API key (Postgres/Argon2id)
                         │  2. check rate limits (Redis sliding window)
                         │  3. resolve deployment → vLLM ClusterIP
                         │  4. reverse-proxy (SSE streaming, no buffer)
                         └───────────────────────────────────────┘
                                              │
                                     vllm-<model-id> pod
                                     (openserve-inference namespace)
```

The GUI talks only to the **control-api** (never directly to vLLM). The control-api talks to Postgres and the Kubernetes API. The gateway talks to Postgres (key validation) and Redis (rate limiting) — never to the control-api.

### CRD → infra ownership

The **operator** (`operator/`) is the single source of truth for inference infrastructure. It owns:
- `ModelDeployment` CR → creates a `vllm-<name>` Deployment + ClusterIP Service in `openserve-inference` namespace
- `APIKey` CR → syncs key metadata into the gateway's routing ConfigMap
- `BudgetPolicy` CR → polls BigQuery spend every 5 minutes; scales deployments to 0 when cap is hit

The **control-api** creates CRs via the Kubernetes API; the operator reconciles them into real resources. The `deployment_cache` table in Postgres is a read-through cache for the GUI — it is updated by the control-api when it writes CRs, and eventually consistent with the actual CR status.

### Gateway routing hot-reload

The gateway reads `routing.yaml` (a ConfigMap mounted as a file). The operator writes this ConfigMap whenever a `ModelDeployment` reaches `Running` phase or is deleted. The gateway uses `fsnotify` to reload routes without restart. The format is:

```yaml
routes:
  llama-3-8b-instruct: "vllm-llama-3-8b-instruct.openserve-inference.svc.cluster.local:8000"
```

### Security invariants — never break these

- **All DB queries use pgx named parameters** — no string concatenation in SQL, anywhere.
- **API key raw value is never stored** — only its Argon2id hash (`time=1, mem=64MB, threads=4, keyLen=32`). The raw key is returned once from `CreateAPIKey` and immediately discarded.
- **vLLM pods get a NetworkPolicy egress-allow only to GCS** (private Google access: `199.36.153.8/30`). Do not widen this — a compromised model cannot phone home.
- **No static GCP service-account keys** — all GCP API access uses Workload Identity. The `iam.gke.io/gcp-service-account` annotation on each Kubernetes ServiceAccount is the binding.
- **Audit log rows are append-only** — never update or delete rows from `audit_log`. The `writeAuditLog` helper in `handler.go` is fire-and-forget (goroutine), so it never blocks the HTTP response.

### Catalog manifests

`catalog/models/*.yaml` files are validated against `catalog/schema.json`. The `weightDigestSha256` field is a placeholder in the repo; real values are computed during the sign-off process (described in `catalog/README.md`) and should never be committed as placeholders to a production branch.

### Key files to understand before making changes

| What you're changing | Read first |
|---|---|
| Adding a new CRD field | `operator/api/v1alpha1/*_types.go` — kubebuilder markers control validation and kubectl column printing |
| Changing gateway auth logic | `apps/gateway/internal/auth/validator.go` + `apps/gateway/internal/proxy/proxy.go` |
| Adding a control-api endpoint | `apps/control-api/internal/handler/handler.go` — route registered in `cmd/server/main.go` |
| New model in catalog | `catalog/models/*.yaml` validated against `catalog/schema.json`; mirror entry in `apps/control-api/internal/catalog/default_models.json` |
| Changing GPU node pool config | `examples/terraform/gcp-prereqs/main.tf` (node pool) + `operator/internal/controller/modeldeployment_controller.go` (`gpuNodeSelector` map) — must stay in sync |
| Adding a DB column | Write a new migration in `apps/control-api/internal/db/migrations/` — files run in filename order; `schema_migrations` table tracks what's been applied |
| Adding a LoRA adapter | `operator/api/v1alpha1/modeldeployment_types.go` — add entry to `spec.loraAdapters`; operator auto-loads via vLLM API |
| Switching inference engine | `operator/api/v1alpha1/modeldeployment_types.go` — set `spec.engine: sglang\|vllm`; engine logic lives in `operator/internal/engine/` |
| Changing OIDC provider | Set `OIDC_ISSUER_URL` env var in Helm values; any RFC 8414 provider works |
| Adding a DB migration | Write `apps/control-api/internal/db/migrations/012_*.sql` — files run in filename order |
| Routing config with pod IPs | `operator/internal/gateway/sync.go` — operator writes `routing.yaml`; add `podRoutes` section for prefix-cache routing |

### Implementation status

All core controllers and handlers are fully implemented. The following intentional future improvements remain:

- `apps/gateway/internal/ratelimit/redis.go`: TPM rate limiting now uses **actual token counts** recorded post-request via `RecordTokens()`. The gateway extracts real `inputTokens + outputTokens` from the vLLM SSE stream and records them asynchronously after each request.
- `apps/gateway/internal/routing/router.go`: **Prefix-cache-aware routing** is implemented. The gateway hashes the system prompt prefix (first 512 chars) and uses consistent hashing to route requests to the same pod. Pod-level routing activates when `podRoutes` are present in the routing ConfigMap (operator writes these when `MaxReplicas > 1`). Falls back to service VIP otherwise.
- `operator/internal/engine/`: **InferenceEngine abstraction** — vLLM and SGLang are both implemented behind the `InferenceEngine` interface. Set `spec.engine: sglang` on a `ModelDeployment` to use SGLang. Defaults to vLLM.
- `operator/api/v1alpha1/modeldeployment_types.go`: **LoRA adapters** — add `spec.loraAdapters` to a `ModelDeployment` to hot-load fine-tuned adapters via vLLM's `/v1/load_lora_adapter` API. Loaded adapters tracked in `status.loadedLoRAAdapters`.
- `apps/control-api/internal/auth/oidc.go`: **Multi-provider OIDC** — supports any RFC 8414 compliant OIDC provider. Configure via `OIDC_ISSUER_URL` env var. Defaults to `https://accounts.google.com` for backward compatibility. Tested providers: Google Workspace, Okta, Azure AD, GitHub Actions.

Remaining deferred work (not yet implemented):
- Async batch inference (`BatchInferenceJob` CRD + Redis queue) — P2
- Kubernetes Gateway API migration (replace fsnotify routing with xDS) — P2
- AWS/Azure cloud adapters — v2/v3 per ADR 0001

### Engine image pinning

Each engine's image tag is pinned in one place:
- vLLM: `vllmImageTag` const in `operator/internal/engine/vllm.go`
- SGLang: `SGLangImageTag` const in `operator/internal/engine/sglang.go`

Bump engine tags there only — never in individual model manifests or Helm values.
