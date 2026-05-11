# ADR 0003 — BYOC (Bring Your Own Cloud) tenancy model

**Date:** 2026-04-29  
**Status:** Accepted  
**Deciders:** openserve founding team

## Context

LLM serving platforms typically choose one of three tenancy models:

1. **Fully managed (operator owns the cloud):** Replicate, Together AI, Hugging Face Inference Endpoints. Operator holds the GPU fleet; users call an API. Simple UX, high operator cost, operator sees all traffic.

2. **BYOC (user brings their own cloud):** openserve, Anyscale, Databricks. Customer connects their own cloud account. Models run in their VPC. Operator charges for the control-plane software; customer pays cloud directly.

3. **Hybrid:** Customer can choose either model per deployment. Requires building both.

## Decision

**openserve v1 is BYOC-only, running in the customer's GCP project.**

This decision flows from the enterprise sales motion we are optimizing for: regulated industries (finance, healthcare, legal) and security-conscious enterprises will not accept their prompts leaving their own VPC. BYOC is not a compromise — it is the product's core differentiator.

The BYOC model has an important implication: **there is no multi-tenancy in openserve's code in v1.** Each customer runs a completely separate installation in their own GCP project, with their own GKE cluster, Cloud SQL instance, and GCS bucket. The "tenants" inside a single installation are the org's own teams, with logical isolation via Postgres rows + API key scoping.

The future hosted control plane (a separate closed-source repo) will manage fleets of these installations by receiving heartbeat + metrics only — no prompt or response data ever flows to the hosted control plane.

## Consequences

**Good:**
- Strongest possible data isolation — a selling point, not a caveat
- openserve never sees customer data, eliminating a major liability
- Each installation is independently operated; a bug in one customer's install does not affect others
- Simplifies our compliance story — customers own their own data residency

**Bad:**
- We cannot observe usage patterns for product analytics (by design)
- Each customer needs GKE/GPU quota; quota delays are a real sales blocker (mitigated by: setup wizard checks quota and links directly to the GCP quota request page)
- Updates require customers to `helm upgrade` (mitigated by: our hosted control plane will push upgrade recommendations and enable one-click upgrades in v2)

## Alternatives considered

**Operator-managed shared GPU fleet:** Rejected because it positions us against Replicate and Together AI, who have an 18-month head start and far more capital. BYOC is a category where we can win without a GPU fleet.

**Hybrid in v1:** Rejected as too complex for a solo/small team. Add in v3 once BYOC is stable.
