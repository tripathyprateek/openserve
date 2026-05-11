# Threat Model

This document describes the assets openserve protects, the trust boundaries it enforces, and the threats it mitigates. It is a living document; update it before any release that changes the attack surface.

Last reviewed: 2026-04-29

## Assets

| Asset | Sensitivity | Where stored |
|---|---|---|
| Model weights | High (IP of model provider) | Customer GCS bucket, CMEK-encrypted |
| API keys (raw) | Critical | Never stored — shown once to user, discarded |
| API key hashes | High | Cloud SQL Postgres, private IP |
| User credentials | Delegated to Google OIDC | Not stored by openserve |
| Prompts / responses | Radioactive — customer data | Not logged by default; customer Cloud Logging only if opted in |
| Audit logs | High | Locked Cloud Logging bucket (immutable) |
| Postgres data (orgs, users, keys, audit) | High | Cloud SQL private IP, CMEK |
| Redis rate-limit counters | Low | Memorystore private IP |

## Trust boundaries

```
[Internet]
    │  HTTPS + Cloud Armor (WAF/DDoS)
    ▼
[Load Balancer]
    │  TLS termination
    ▼
[GKE cluster — boundary 1]
    │  mTLS (cert-manager) between all services
    ├─ [openserve-gui]        trusted: serves authenticated users only
    ├─ [openserve-control-api] trusted: verifies OIDC JWT, writes audit log
    ├─ [openserve-gateway]    trusted: validates API keys, enforces limits
    └─ [vllm pods]            untrusted: runs third-party model code
         │  NetworkPolicy: egress only to GCS (weight cache) + metrics
         │  Pod security: non-root, read-only rootfs, all caps dropped
         ▼
    [Customer's LAN / VPN]   — not in scope
    
[Cloud SQL / Redis / GCS / BigQuery] — private IP / VPC only
[GCP Secret Manager]                 — Workload Identity, no static keys
[Google OIDC]                        — external IdP, trusted for identity
[Hugging Face / GCS mirror]          — trusted for weights only after SHA256+cosign verify
```

## Threat catalogue

### T1 — Compromised customer GCP IAM

**Vector**: Attacker obtains a GCP credential (phishing, leaked key) with roles in the customer's project.  
**Impact**: Could delete resources, exfiltrate model weights, read Cloud SQL.  
**Mitigations**:
- openserve's Workload Identity roles are least-privilege; no roles that grant data exfiltration beyond the openserve service account's own bucket.
- Audit log (immutable) captures all IAM changes; log-based alerts fire on any IAM grant modification.
- Model weights in GCS have a separate CMEK key; compromise of the openserve SA does not yield the key.

### T2 — Stolen or leaked API key

**Vector**: A partner's API key is exposed in logs, a public repo, or a compromised machine.  
**Impact**: Unauthorized inference calls until key is rotated; potential spend abuse.  
**Mitigations**:
- API keys have a prefix (`openserve_live_`); scanners (GitHub secret scanning, truffleHog) detect leaks automatically.
- Per-key RPM + TPM caps limit blast radius; budget auto-pause limits financial exposure.
- One-click key rotation in GUI; old key invalidated within 60s (Redis TTL).
- Optional: IP allowlist per key; expiration date required for partner keys.
- Audit log records every request's key ID (never the key value) with response code.

### T3 — Malicious or tampered model weights

**Vector**: Attacker poisons a model weight file in GCS, or a supply-chain attack on Hugging Face delivers malicious weights.  
**Impact**: Model serves attacker-controlled outputs, potentially exfiltrates context from the inference process.  
**Mitigations**:
- Operator verifies SHA256 hash of all weight files against the cosign-signed catalog manifest before scheduling the vLLM pod.
- Catalog manifests are signed by openserve maintainers using cosign (Sigstore) with hardware-backed keys.
- vLLM pods run with egress NetworkPolicy: only allowed destination is the GCS weight bucket. A compromised model cannot phone home.

### T4 — Cost-bomb / prompt injection for resource abuse

**Vector**: A malicious insider or compromised partner sends 1M-token prompts at maximum concurrency to drive up GPU spend.  
**Impact**: Unexpected GPU bill; potential service degradation for other users.  
**Mitigations**:
- Hard `maxInputTokens` + `maxOutputTokens` cap enforced in the gateway before the request reaches vLLM (no client-controlled override).
- Per-key TPM cap enforced in Redis; requests above cap receive 429.
- Per-deployment daily $/USD budget cap: operator scales to 0 replicas when exceeded; banner + alert fires.
- Anomaly alert: if 1h spend > 3× rolling 24h average, page the org admin.

### T5 — Container escape from vLLM pod

**Vector**: A vulnerability in vLLM, Python, or a model-loaded library achieves code execution outside the container.  
**Impact**: Access to node filesystem, other pods on the node, cluster API.  
**Mitigations**:
- All vLLM pods: `runAsNonRoot: true`, `readOnlyRootFilesystem: true`, `allowPrivilegeEscalation: false`, all capabilities dropped.
- NetworkPolicy restricts egress to GCS weight bucket + Prometheus metrics port only.
- vLLM pods run on dedicated GPU node pool with node taints; no non-GPU workloads on those nodes.
- Pod disruption budget and resource quotas prevent a rogue pod from starving the cluster.
- GKE Sandbox (gVisor) on GPU pools where supported (check GKE compatibility matrix per release).

### T6 — Audit log tampering

**Vector**: An attacker with Cloud Logging write access or a compromised openserve service account attempts to delete or modify audit records.  
**Impact**: Compliance failure; inability to reconstruct what happened during an incident.  
**Mitigations**:
- Audit bucket has **bucket lock** (retention lock); no principal can delete entries before the retention period.
- openserve's SA has `roles/logging.logWriter` only — cannot read or delete logs.
- Monthly hash-chain anchor: the digest of all audit records for a month is published to a public transparency log (GitHub releases). Verifier can detect any retroactive modification.
- Log-based alert: fires if any IAM policy change is made to the audit bucket.

### T7 — GUI / control API injection (XSS, SQLi)

**Vector**: Attacker injects malicious content via model name, deployment label, or API key description fields.  
**Impact**: XSS → session hijacking; SQLi → data exfiltration or privilege escalation.  
**Mitigations**:
- All database access via parameterised queries (pgx v5 named parameters); no string concatenation in SQL.
- GUI renders all user-supplied content as escaped text; Next.js default CSP headers.
- Input validation at the control-api layer: all string fields have max length + character allowlist.
- Automated SAST (CodeQL) and dependency scanning (govulncheck, npm audit) in CI.

## Out of scope

- Attacks on the underlying GKE control plane (Google's responsibility)
- Physical access to GCP data centres
- Attacks on the customer's Google Workspace IdP
- vLLM vulnerabilities in the upstream project (report to vLLM maintainers)

## Review cadence

This document must be reviewed and updated:
- Before every minor release
- Whenever a new component or external integration is added
- After any security incident

The reviewer signs off by adding their name and date to the "Last reviewed" line at the top.
