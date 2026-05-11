# openserve vs HuggingFace Inference Endpoints

> Research date: 2026-05-01  
> This document is updated as both products evolve. Last cross-checked against HF Inference Endpoints documentation.

---

## TL;DR

HuggingFace Inference Endpoints is a managed-cloud product: HF owns the infrastructure, your prompts flow through their servers, and you pay HF per hour. openserve is a **BYOC** (Bring Your Own Cloud) platform: you install it in **your** GCP project, all inference happens inside **your** VPC, and your prompts never leave your environment. The products serve different risk profiles.

---

## Feature comparison

| Feature | openserve (v1) | HF Inference Endpoints |
|---|---|---|
| **Infrastructure ownership** | Customer's own GCP project | HF-managed cloud (AWS/GCP/Azure) |
| **Data isolation** | Prompts never leave customer VPC | Prompts processed on HF infrastructure |
| **Regions** | Any GCP region the customer's org allows | 4 fixed regions (us-east-1, eu-west-1, ap-southeast-1, us-central1) |
| **Inference engine** | vLLM (OpenAI-compatible, actively maintained) | TGI (maintenance mode as of 2025) or vLLM (beta on some tiers) |
| **Model source** | Curated catalog (~10→30 models), cosign-signed manifests | Full HF Hub — any public or private repo |
| **Deployment type** | Dedicated (one model per Deployment CR) | Dedicated or Serverless (shared, per-token pricing) |
| **Scale-to-zero** | Default: idle 10 min → 0 replicas (KEDA) | Available on some tiers; cold start ~30–60 s |
| **Hard daily budget cap** | Yes — `BudgetPolicy` auto-pauses deployment at $/day ceiling | No — HF has no automatic spend cap; must use cloud billing alerts |
| **Per-key rate limits** | Yes — RPM + TPM per API key, with IP allowlist + expiry | Per-endpoint rate limits only; no per-key granularity |
| **API key scoping** | Keys scoped to specific deployment IDs + expiry + IP allowlist | Single token per endpoint, no scoping |
| **Audit log** | Append-only DB + Cloud Logging bucket lock + hash-chain anchor | Not exposed to customers |
| **Private networking** | All traffic stays in VPC; Cloud SQL private IP only | AWS PrivateLink available on Enterprise tier only |
| **SOC 2 status** | Type 1 readiness baked in (controls, CMEK, bucket lock) | HF is SOC 2 Type 2 certified (their infra, not yours) |
| **HIPAA / FedRAMP** | Achievable (customer controls their GCP org) | Not currently certified |
| **Kubernetes-native** | Yes — Helm chart, CRDs, GitOps-ready | No — SaaS API only |
| **Terraform support** | `examples/terraform/gcp-prereqs` (GCP prerequisites) | No official Terraform provider |
| **OpenAI API compat** | Full (vLLM default) | Full (TGI/vLLM) |
| **Cost** | GCP GPU on-demand + Cloud SQL + GCS (customer pays GCP directly) | $0.60–$3.50/hr per endpoint (customer pays HF) |
| **Multi-tenancy model** | One install per org; users share logical namespace in Postgres | Multi-tenant SaaS; HF is the landlord |
| **Model weight security** | SHA256 + cosign signature verified by operator before serving | HF controls weight integrity; customers trust HF |
| **Open source** | Yes — Apache 2.0 | No (HF Hub is OSS; Inference Endpoints is proprietary SaaS) |

---

## Where HF Inference Endpoints is better

### 1. Model breadth
HF has access to the entire HF Hub — 500,000+ models including private and gated models. openserve v1 is limited to a curated catalog of ~10–30 models. Adding a model to openserve requires a catalog entry, cosign-signed manifest, and weight verification.

**Planned response:** v1.5 "request a model" automation (GitHub issue → CI → signed catalog entry). Enterprise customers on v2 will be able to supply their own manifest for HF-hosted models.

### 2. No infrastructure to manage
HF is pure SaaS. No GKE cluster, no Terraform, no operator pods to manage. Getting your first endpoint takes minutes.

**Response:** This is a deliberate trade-off. BYOC customers accept operational overhead in exchange for data sovereignty and cost ownership. openserve ships as a Helm chart specifically to minimize the operational surface to `helm install`.

### 3. Serverless / shared inference tier
HF's Inference API offers a pay-per-token shared tier with no cold-start for popular models. openserve v1 is dedicated-only. Shared inference is complex to build correctly — it is on the roadmap (v3+) only if dedicated BYOC proves product-market fit first.

### 4. Multi-cloud today
HF supports AWS, GCP, and Azure. openserve v1 is GCP-only. AWS (EKS) is planned for v2.

---

## Where openserve is better

### 1. Hard budget guardrails
This is the single most important cost-safety differentiator. A `BudgetPolicy` CRD sets a hard $/day ceiling; the operator scales to zero when the ceiling is hit. HF has no equivalent — a runaway workload on HF runs until you notice and stop it manually. For enterprise customers with shared GPU access across teams, this is non-negotiable.

### 2. Data never leaves the VPC
Every token processed by openserve stays inside the customer's GCP project. VPC-native Cloud SQL, vLLM pods with egress NetworkPolicy (GCS-only), no prompt logging by default. HF processes prompts on their infrastructure — a fundamental blocker for healthcare, legal, finance, and government customers.

### 3. Per-key rate limits with scoping
openserve API keys are scoped to specific deployment IDs, have configurable RPM/TPM caps, optional IP allowlists, and expiration dates. This enables the "external partner with limited access" pattern (see plan §7.2). HF has per-endpoint rate limits but no per-key granularity.

### 4. Audit log with tamper evidence
Every admin action (deploy, delete, rotate-key, change-budget) is recorded in an append-only audit table, forwarded to Cloud Logging with a locked retention bucket, and anchored into a public hash chain. HF does not expose audit logs to customers.

### 5. vLLM exclusively
openserve uses vLLM for all inference. HF's Text Generation Inference (TGI) is officially in maintenance mode as of 2024/2025; HF is migrating to vLLM on newer tiers but the migration is incomplete. vLLM has faster iteration, better speculative decoding support, and prefix caching.

### 6. Kubernetes-native GitOps
`ModelDeployment` CRDs can be committed to a git repo and applied via Argo CD or Flux. Enterprise platform teams already work this way. HF is SaaS-only with no Kubernetes primitives.

### 7. Cost ownership and transparency
The customer pays GCP directly at public list prices. openserve adds no markup. HF charges a premium above cloud list price (which is reasonable — they add value — but it becomes expensive at scale).

---

## Product changes made based on this research

### Done in this session
- **Catalog page** — added BYOC value-prop banner ("Your data never leaves your VPC") with a dismissible tooltip linking to this doc
- **Model card** — added GPU class badge, HuggingFace repo link, formatted downloads count
- **New models** — sourced 5 trending HF models (Gemma 3 27B, Qwen3 8B, Phi-4, DeepSeek R1 Distill, Mistral Small 3.1)
- **Inference engine pinned to vLLM** — documented in CLAUDE.md; no TGI support planned

### Planned follow-up
- `v1.5`: Terraform provider for `ModelDeployment` CRDs (fills the GitOps gap vs HF)
- `v1.5`: "Request a model" UI form → CI pipeline (closes the model breadth gap incrementally)
- `v2`: AWS EKS support (closes the multi-cloud gap)
- `v2`: SGLang as an optional alternative engine to vLLM for models with long-context workloads
- `v3+`: Serverless / shared inference tier (closes the pay-per-token gap for dev/test workloads)

---

## Positioning statement (draft)

> "openserve is the open-source alternative to HuggingFace Inference Endpoints for organizations that cannot let their prompts leave their own infrastructure. It gives you the same OpenAI-compatible API and scale-to-zero convenience, with hard budget guardrails, per-key scoping, tamper-evident audit logs, and full data residency — deployed into your own GCP account with a single `helm install`."

---

## References

- [HuggingFace Inference Endpoints documentation](https://huggingface.co/docs/inference-endpoints)
- [HuggingFace Inference Endpoints pricing](https://huggingface.co/docs/inference-endpoints/pricing)
- [vLLM vs TGI benchmark comparison](https://huggingface.co/blog/lmsys-arena) (HF's own benchmarks favor vLLM for throughput)
- [openserve plan](../ADRs/) — see ADR-0001 (cloud-first), ADR-0002 (vLLM as engine), ADR-0003 (BYOC tenancy)
