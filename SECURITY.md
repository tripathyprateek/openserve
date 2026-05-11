# Security Policy

## Supported Versions

| Version | Supported |
|---------|-----------|
| latest (main) | ✅ |
| older releases | ❌ — please upgrade |

## Reporting a Vulnerability

**Do not open a public GitHub issue for security vulnerabilities.**

Email **security@openserve.io** with:

- A description of the vulnerability and its potential impact
- Steps to reproduce (proof-of-concept if possible)
- Affected component(s) (`gateway`, `control-api`, `operator`, `peer-relay`, `gui`)
- Your contact information for follow-up

We will acknowledge your report within **48 hours** and provide a remediation timeline within **5 business days**.

## Disclosure Policy

We follow coordinated disclosure:

1. Report received → acknowledged within 48 hours
2. Severity assessed → CVSS score assigned
3. Fix developed and reviewed
4. Patch released to affected versions
5. CVE requested (if warranted)
6. Public advisory published — you are credited unless you request anonymity

**Embargo period:** We ask that you keep the vulnerability confidential for **90 days** from initial report, or until we release a fix, whichever comes first.

## Scope

In scope:
- Authentication and authorization bypasses
- Data leakage (org isolation violations, prompt content exposure)
- Remote code execution in any openserve component
- Server-side request forgery (SSRF)
- SQL injection or parameterization bypasses
- Supply chain attacks (image tampering, manifest forgery)
- API key exposure or Argon2id bypass
- Denial-of-service on inference endpoints

Out of scope:
- Vulnerabilities in the customer's own GCP infrastructure
- Attacks requiring physical access to the cluster
- Social engineering of openserve team members
- Issues in third-party models served by vLLM (report to the model author)

## Security Architecture Invariants

The following invariants are enforced by design. A report showing any of these are violated is automatically **Critical** severity:

1. **No prompt content is ever logged** — audit_log stores metadata only, never request/response bodies
2. **All DB queries use positional parameters** — no string concatenation in SQL
3. **API key raw values are never stored** — only Argon2id hashes (time=1, mem=64MB, threads=4, keyLen=32)
4. **vLLM pod network egress is locked** — NetworkPolicy allows only GCS private access (199.36.153.8/30)
5. **No static GCP credentials** — Workload Identity binds GCP permissions to K8s ServiceAccounts
6. **Audit log is append-only** — rows are never updated or deleted; bucket lock enforces this in production
7. **Model weights are verified before serving** — SHA256 + cosign signature checked against catalog manifest
8. **All container images are signed** — cosign signatures + SLSA L3 provenance on every release

## Known Security Properties

| Control | Implementation |
|---------|---------------|
| Secret storage | Argon2id (time=1, mem=64MB, threads=4, keyLen=32) — never plaintext |
| Session tokens | HS256 JWT, short-lived (15 min), refreshed via OIDC |
| Transport | mTLS between all in-cluster services; TLS termination at load balancer |
| Audit trail | Append-only Postgres rows + GCS bucket lock + monthly hash-chain anchor |
| Supply chain | cosign + SBOM (syft) + SLSA L3 GitHub Actions for all images |
| GCP access | Workload Identity only — zero static service-account keys |
| Network isolation | NetworkPolicy restricts vLLM pod egress to GCS only |
| Model integrity | SHA256 + cosign on all catalog manifests before scheduling |

## CVE History

No CVEs have been issued against openserve. If you discover a first-party vulnerability, please follow the reporting process above.
