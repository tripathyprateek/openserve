# ADR 0004 — Go reverse proxy gateway instead of Envoy + ext_authz

**Date:** 2026-04-29  
**Status:** Accepted  
**Deciders:** openserve founding team

## Context

openserve needs an API gateway that sits in front of vLLM deployments to:
1. Authenticate API keys (Argon2id hash lookup)
2. Enforce per-key rate limits (RPM + TPM)
3. Enforce max-tokens-per-request caps
4. Stream responses without buffering (SSE for chat completions)
5. Add `x-openserve-request-id` tracing header
6. Emit per-request usage metadata (key ID, input/output token counts) to Prometheus

Two approaches were evaluated:

**Option A: Envoy proxy + ext_authz gRPC sidecar**  
Envoy handles TLS termination, routing, and retries. A custom Go gRPC service implements the ext_authz API for key validation and rate limiting. Industry-standard; Istio uses this pattern.

**Option B: Single Go reverse proxy (net/http + httputil.ReverseProxy)**  
A single Go binary handles routing, key auth, rate limiting, and proxying. Uses Redis for distributed rate-limit counters. No Envoy configuration needed.

## Decision

**Option B — single Go reverse proxy** for v1.

## Rationale

For a solo/small team with 10–15 hrs/week, Envoy configuration complexity is a significant tax:
- Envoy's xDS API and filter chain configuration is 200+ lines of YAML for a non-trivial setup
- The ext_authz gRPC protocol adds a full service boundary with its own deployment, health checks, and compatibility surface
- Debugging Envoy filter chains is notoriously time-consuming
- SSE streaming (required for chat completions) needs specific Envoy buffer configuration that is easy to get wrong

A Go reverse proxy using `net/http/httputil.ReverseProxy` with a custom `Director` and `ModifyResponse` is ~400 lines of idiomatic Go, handles streaming correctly by default, is easy to test, and is trivially debuggable with standard Go tooling.

We lose: advanced Envoy features (circuit breaking, retries with exact semantics, hot-reload without restart). We gain: shipping faster and avoiding operational complexity.

The gateway will be designed with a clear `Authenticator` and `RateLimiter` interface so that a future version can swap to Envoy + ext_authz or use the Gateway API CRD natively (Envoy Gateway, Cilium Gateway API) without rewriting the business logic.

## Consequences

**Good:**
- v1 gateway is done in ~1 week instead of ~3 weeks
- Single binary; straightforward Kubernetes Deployment; easy to add integration tests
- SSE streaming handled correctly by `httputil.ReverseProxy` with no special configuration

**Bad:**
- No hot-reload of routing config; gateway restart required for new deployments (acceptable: operator triggers a rolling update)
- No built-in circuit breaking; add in v2 if needed (Redis rate limits provide blast-radius control in v1)
- Not the "enterprise-grade" answer an Envoy-based solution would be; if asked, explain the roadmap to Envoy Gateway

## Migration path

In v2, if we need true hot-reload or advanced traffic shaping, we introduce the Gateway API (`GatewayClass`, `HTTPRoute`) with Envoy Gateway or Cilium as the implementation, and replace the Go gateway binary with an ext_authz service that wraps the same `Authenticator` + `RateLimiter` business logic. The migration is additive.
