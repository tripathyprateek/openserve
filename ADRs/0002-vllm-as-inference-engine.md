# ADR 0002 — vLLM as the v1 inference engine

**Date:** 2026-04-29  
**Status:** Accepted  
**Deciders:** openserve founding team

## Context

openserve must choose a primary inference engine to run inside the customer's cluster. Candidates:

| Engine | License | Strengths | Weaknesses |
|---|---|---|---|
| **vLLM** | Apache 2.0 | Best throughput, paged attention, continuous batching, OpenAI-compatible API, broad model support, active community | Requires CUDA; higher memory than llama.cpp |
| TGI (HuggingFace) | Non-OSS (HFOIL) | Good HF integration | License changed mid-2024; risk for an OSS project |
| Ollama / llama.cpp | MIT | CPU + modest GPU, easy to run locally | Not production-grade for concurrent GPU workloads |
| SGLang | Apache 2.0 | Faster for structured generation; radix attention | Less mature ecosystem; fewer supported models in v1 |

## Decision

**vLLM** is the v1 inference engine.

The operator creates one vLLM `Deployment` per `ModelDeployment` CR, using the official `vllm/vllm-openai` container image pinned to a minor version. vLLM args (tensor parallelism, quantization, max model length, etc.) are expressed in the catalog manifest and passed through the CR spec.

vLLM exposes `/metrics` (Prometheus) natively; we scrape this via the Cloud Monitoring Prometheus integration rather than building a custom usage-meter sidecar in v1.

## Consequences

**Good:**
- OpenAI-compatible API — existing SDKs just work with a `base_url` change
- Paged attention + continuous batching dramatically improves multi-user throughput vs. naïve serving
- Apache 2.0 — no license risk for customers or for openserve itself
- Active upstream; model support improves weekly without our effort

**Bad:**
- CUDA only — no CPU-only fallback for tiny dev installs (we'll address with a `docker compose` + Ollama local-dev mode in v1.5)
- vLLM minor versions can break model loading; we pin the minor version in CI and run catalog model integration tests before bumping

## Alternatives considered

**SGLang as primary:** Rejected for v1 because model support coverage is narrower and the ecosystem is less mature. Good candidate to add as an alternative engine in v2 for agentic workloads.

**TGI:** Rejected due to license risk. HFOIL v1.0 restricts commercial use without a specific agreement, which is problematic for openserve shipping to enterprise customers under Apache 2.0.

**Multi-engine abstraction in v1:** Deferred. The operator will be designed with an `InferenceEngine` interface that the vLLM reconciler implements, making it possible to add SGLang, TGI-NG, etc. later without rewriting the core.
