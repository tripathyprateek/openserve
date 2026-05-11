# ADR 0001 — GCP-first, then AWS, then Azure

**Date:** 2026-04-29  
**Status:** Accepted  
**Deciders:** openserve founding team

## Context

openserve must eventually support all three major clouds for enterprise buyers. The question is which cloud to build the v1 data plane on and in what order to add the others.

Factors:
- We are a solo/small team — supporting three clouds simultaneously in v1 is not feasible
- GCP's GPU availability (L4, A100) and GKE UX (Autopilot, node auto-provisioning) are strong for AI workloads
- AWS has the largest enterprise install base and the most mature cross-account IAM story
- Azure is required for Microsoft-shop buyers (large, but not the typical early AI adopter)

## Decision

**v1 ships GCP-only (GKE).** The operator and Helm chart are designed from the start with cloud-abstraction in mind (no hard GCP-SDK calls in the core reconcile path; GCS/BigQuery/Secret Manager are behind interfaces that can be swapped), but only GCP implementations ship in v1.

**v2 adds AWS (EKS, S3, RDS, Secrets Manager, CloudWatch).** The operator interface layer makes this a matter of writing new adapters rather than re-architecting.

**v3 adds Azure (AKS, Azure Blob, Azure SQL, Key Vault, Monitor).**

## Consequences

**Good:**
- v1 ships faster and is better-tested on one platform
- GKE's GPU UX is genuinely superior at this stage of the market
- GCP's Workload Identity is cleaner than AWS's IRSA for this use case

**Bad:**
- We will lose some early prospects who are AWS-only and unwilling to wait
- Building abstractions for cloud services adds ~20% overhead to the operator design

## Alternatives considered

**Build on AWS first:** Rejected because GKE's GPU node auto-provisioning and scale-to-zero story is ahead of EKS/Karpenter for our specific workload (spot GPU node pools with fast scale-out). We will build AWS second.

**Multi-cloud from day one via Crossplane:** Too much operational complexity for a v1 team. We add this abstraction when we have real customers on multiple clouds.
