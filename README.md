# openserve

Open-source LLM serving platform — deploy curated open-source models into your own cloud (BYOC).

Customers install openserve in their own GCP project (AWS and Azure in v2/v3). Models run entirely inside their VPC. Prompts and responses never leave their infrastructure.

## How it works

```
helm install openserve openserve/openserve \
  --set domain=ai.your-company.com \
  --set google.clientId=<your-workspace-oauth-client-id>
```

That's it. After the setup wizard completes (< 5 min), your team can:

- Browse the curated catalog of 20+ vetted open-source LLMs
- Deploy any model to their own GKE cluster with one click
- Call the model via an OpenAI-compatible API endpoint
- Invite employees and external partners with scoped API keys
- Monitor spend in real time with per-deployment budget caps that auto-pause on breach

## Architecture

openserve consists of four components that run inside the customer's Kubernetes cluster:

| Component | Language | Purpose |
|---|---|---|
| **operator** | Go | Kubernetes controller; reconciles `ModelDeployment`, `ApiKey`, `BudgetPolicy` CRDs |
| **control-api** | Go | REST API consumed by the GUI; wraps the K8s API + Postgres |
| **gateway** | Go | Reverse proxy in front of vLLM; validates API keys, enforces rate limits, streams responses |
| **gui** | TypeScript / Next.js | Web UI: catalog, deployments, members, usage, audit |

The inference engine is **vLLM** (Apache 2.0), served on NVIDIA L4 or A100-40G GPU node pools.

```
Customer's GCP project
├─ GKE cluster
│  ├─ openserve-operator      (Kubernetes controller)
│  ├─ openserve-control-api   (REST API + OIDC auth)
│  ├─ openserve-gateway       (API key auth + rate limit + proxy)
│  ├─ openserve-gui           (Next.js web app)
│  └─ vllm-<model-id>         (one pod per deployed model, scale-to-zero)
├─ Cloud SQL (Postgres)       (org/user/key/audit data, private IP)
├─ Memorystore (Redis)        (rate-limit counters)
├─ GCS bucket                 (model weight cache, CMEK encrypted)
└─ BigQuery                   (token usage + spend data)
```

No prompt or response content leaves the VPC. The only external call at runtime is the initial weight pull from Hugging Face (or a GCS mirror) — verified against a SHA256 + cosign-signed catalog manifest before the pod starts.

## Quick start

### Prerequisites

- GCP project with billing enabled
- GKE cluster with GPU node pools (L4 for 7B–13B, A100-40G for 30B–70B)
- `helm` ≥ 3.14, `kubectl` pointed at the cluster
- A Google Workspace domain (for OIDC login)
- Cloud SQL (Postgres 15), Memorystore (Redis 7), and GCS bucket — see [terraform/gcp-prereqs](examples/terraform/gcp-prereqs/)

### Install

```bash
helm repo add openserve https://charts.openserve.io
helm repo update

helm install openserve openserve/openserve \
  --namespace openserve-system --create-namespace \
  --set domain=ai.acme.com \
  --set google.clientId=123456789.apps.googleusercontent.com \
  --set postgres.host=10.0.0.5 \
  --set postgres.secret=openserve-postgres-secret \
  --set redis.host=10.0.0.6 \
  --set gcs.bucket=acme-openserve-models \
  --set billing.bigqueryDataset=openserve_usage
```

Browse to `https://ai.acme.com`, sign in with your Google Workspace account, and follow the setup wizard.

### Deploy a model

From the GUI: **Catalog → Llama 3 8B Instruct → Deploy**.

Or via the API:

```bash
curl -X POST https://ai.acme.com/api/v1/deployments \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "modelRef": "llama-3-8b-instruct",
    "gpuClass": "l4",
    "scaleToZero": true,
    "idleTimeoutMin": 10,
    "budget": {"dailyUsdCap": 50},
    "limits": {"maxInputTokens": 8192, "maxOutputTokens": 4096}
  }'
```

### Call the model

```bash
curl https://ai.acme.com/inference/llama-3-8b-instruct/v1/chat/completions \
  -H "Authorization: Bearer openserve_live_<your-key>" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "llama-3-8b-instruct",
    "messages": [{"role": "user", "content": "Hello!"}],
    "stream": true
  }'
```

The endpoint is OpenAI API-compatible. Point any OpenAI SDK at it by setting `base_url` and `api_key`.

## Security

- All model weights verified against SHA256 + cosign-signed catalog manifests before serving
- API keys: Argon2id-hashed, scoped per deployment, rate-limited (RPM + TPM), IP-allowlist optional
- All intra-cluster traffic: mTLS via cert-manager
- Cloud SQL + Redis: private IP only, never public
- Audit log: append-only Cloud Logging bucket with bucket lock (immutable) + monthly hash-chain anchor
- Workload Identity throughout — no static service-account keys

See [SECURITY.md](SECURITY.md) for the vulnerability disclosure process and [THREAT_MODEL.md](THREAT_MODEL.md) for the full threat model.

## Requesting a model

The catalog ships ~25 popular models. To request an addition, [open an issue](https://github.com/openserve/openserve/issues/new?template=request-a-model.yml). We vet the license, sign the manifest, and add it — typically within a few days.

## How openserve compares to HuggingFace Inference Endpoints

HuggingFace Inference Endpoints is a managed-cloud product — HF owns the infrastructure, your prompts flow through their servers. openserve is different:

| | openserve | HF Inference Endpoints |
|---|---|---|
| **Prompt data location** | Your VPC — never leaves | HF-managed cloud |
| **Hard budget auto-pause** | Yes — $/day cap, auto scales to 0 | No — no automatic spend cap |
| **Per-key rate limits** | Yes — RPM + TPM + IP allowlist + expiry | Per-endpoint only, no per-key |
| **Inference engine** | vLLM (actively maintained) | TGI (maintenance mode) or vLLM beta |
| **Regions** | Any GCP region your org allows | 4 fixed regions |
| **Open source** | Yes (Apache 2.0) | No (SaaS only) |
| **Kubernetes-native / GitOps** | Yes — Helm chart + CRDs | No |

See [docs/comparison-huggingface-inference-endpoints.md](docs/comparison-huggingface-inference-endpoints.md) for a full feature-by-feature breakdown.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). PRs require one review even from maintainers (branch protection enforced).

## License

Apache 2.0. See [LICENSE](LICENSE).

The hosted control plane (fleet management SaaS) is separately licensed — see [openserve.io/cloud](https://openserve.io/cloud).
