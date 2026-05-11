# Local Peer Serving — Design Spec

**Date:** 2026-05-02  
**Status:** Approved  
**Scope:** Allow any org member to run a model locally via Ollama and serve other org members through a secure, invite-only tunnel rooted in the customer's GKE cluster.

---

## 1. Overview

A user registers their laptop as a "local peer." A lightweight Go binary (`openserve-peer`) runs on their machine, connects outbound via WebSocket to a new in-cluster relay service (`peer-relay`), and proxies requests to their local Ollama instance. Other org members who are explicitly invited can call that peer using a model identifier like `peer-{id}/llama3:8b` over the standard OpenAI-compatible gateway.

All traffic flows through the customer's own GKE cluster. No prompt content ever leaves their VPC.

---

## 2. Components

### 2.1 `apps/peer-relay/` — New Go service

Responsibilities:
- Accept WebSocket connections from `openserve-peer` agents (authenticated by token)
- Maintain an in-memory registry of `peer_id → net.Conn`
- Expose an internal HTTP API for the gateway: `GET /internal/peers/{id}/online`, `POST /internal/forward/{id}` (streams response)
- Write `online=true/false` + `last_seen` + `models[]` to Postgres on connect/disconnect
- Enforce: max 1 active WebSocket per peer (new connection evicts old); configurable max peers per org (default: 10)
- Not separately internet-exposed — routed through existing ingress at path prefix `/peer-ws/`

### 2.2 `apps/peer-agent/` — Go binary

Responsibilities:
- Connect to `wss://{relay-domain}/peer-ws/connect` with `Authorization: Bearer <token>` header
- On connect: send a `hello` message with the list of Ollama models currently pulled (`GET localhost:11434/api/tags`)
- Receive forwarded HTTP requests over the WebSocket; validate model name is in registered list; proxy to `localhost:11434`; stream response back as chunked frames
- Reconnect with exponential backoff on disconnect
- Install via: `curl -fsSL https://{domain}/peer-agent/install.sh | sh -s -- --token=<token> --relay=https://{domain}`
- Install script verifies binary SHA256 before executing

### 2.3 DB migration `004_add_peers.sql`

```sql
CREATE TABLE IF NOT EXISTS peers (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    owner_id    TEXT NOT NULL,  -- user subject from JWT
    token_hash  TEXT NOT NULL,  -- Argon2id(time=1,mem=64MB,t=4,keyLen=32)
    models      TEXT[] NOT NULL DEFAULT '{}',
    online      BOOLEAN NOT NULL DEFAULT false,
    last_seen   TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS peer_invites (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    peer_id     UUID NOT NULL REFERENCES peers(id) ON DELETE CASCADE,
    api_key_id  UUID NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(peer_id, api_key_id)
);

CREATE INDEX IF NOT EXISTS peers_org_id_idx ON peers(org_id);
CREATE INDEX IF NOT EXISTS peer_invites_api_key_id_idx ON peer_invites(api_key_id);
```

### 2.4 Control API — new endpoints

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/v1/peers` | Register a new peer. Returns `{id, token}` — token shown once. |
| `GET` | `/api/v1/peers` | List org's peers (scoped to org via JWT). |
| `DELETE` | `/api/v1/peers/{id}` | Delete peer (owner or admin only). |
| `POST` | `/api/v1/peers/{id}/rotate-token` | Issue new token, invalidate old one. |
| `POST` | `/api/v1/peers/{id}/invites` | Invite an API key (`{api_key_id}`). |
| `DELETE` | `/api/v1/peers/{id}/invites/{key_id}` | Revoke an invite. |
| `GET` | `/api/v1/peers/{id}/invites` | List invited API keys for a peer. |

Token generation: `crypto/rand` 32 bytes → base64url. Stored as Argon2id hash (same params as API keys). Returned once in `POST /api/v1/peers` response.

### 2.5 Gateway routing changes

Gateway receives `model: "peer-{id}/{ollama-model}"`. Routing logic:

1. Parse model string — if prefix `peer-`, extract `peer_id` and `model_name`
2. Look up API key's invites: `SELECT 1 FROM peer_invites WHERE peer_id=$1 AND api_key_id=$2`
3. Call `peer-relay` internal endpoint `GET /internal/peers/{id}/online` — if `false`, return `503 {"error": "peer offline"}`
4. Forward full request to `POST /internal/forward/{id}` with `X-Peer-Model: {model_name}` header
5. Stream relay response back to caller

Rate limiting (Redis) still applies at step 2 using the API key's configured RPM/TPM limits.

---

## 3. Security Design

| Concern | Mitigation |
|---|---|
| Token in URL params → logs | Token sent as `Authorization: Bearer` header on WS upgrade only |
| Relay exposed as new surface | Routed through existing ingress (`/peer-ws/`), same TLS termination, no new LB |
| Prompt content in relay logs | Relay is a transparent byte forwarder; body excluded from all structured logs |
| Multiple WS connections per peer | Relay enforces single active connection per peer_id; new evicts old |
| Unknown model proxied to Ollama | peer-agent validates model in registered list; rejects unknown with 400 |
| Token compromise | `rotate-token` endpoint invalidates immediately; peer must restart |
| Mid-stream peer disconnect | Relay sends SSE `data: [DONE]` and closes response cleanly; no hung connections |
| Install script trust | Script served from customer's own GKE domain; binary SHA256 verified before execution |
| Unauthorized peer access | Gateway checks `peer_invites` before routing; peer-relay trusts gateway (internal network only) |
| Rate limit bypass | All peer requests go through gateway Redis rate limiter before reaching relay |

---

## 4. GUI — Local Peers Page

**Navigation:** New item "Local Peers" between Deployments and Settings.

### 4.1 Empty state
"Run a model from your laptop and share it with your team." + **Register Peer** button.

### 4.2 Register form
- Name field (e.g. "Alice's MacBook Pro")
- On submit: calls `POST /api/v1/peers`, displays one-time install command:
  ```
  curl -fsSL https://{domain}/peer-agent/install.sh | sh -s -- \
    --token=<token> \
    --relay=https://{domain}
  ```
- Warning: "This token is shown once. Save it now."

### 4.3 Peer list
Columns: Name | Status (🟢 Online / ⚪ Offline + last seen) | Models | Invited Keys | Actions

### 4.4 Peer detail (expandable row or side panel)
- Re-shows install command (without token — token not recoverable)
- **Rotate Token** button — generates new token, shows it once, peer must restart
- Model list: badges for each Ollama model registered (auto-updated when peer reconnects)
- Invite manager: searchable dropdown of org API keys → Add/Remove

---

## 5. Data Flow Detail

```
Registration:
  GUI → POST /api/v1/peers → control-api → INSERT peers → return {id, token}
  GUI shows: curl ... --token=<token>

Connection:
  peer-agent → WS /peer-ws/connect [Authorization: Bearer <token>]
  peer-relay → SELECT peers WHERE token_hash=argon2(token) → match
  peer-relay → UPDATE peers SET online=true, models=[], last_seen=now()
  peer-relay → keep WS open, register in memory map

Model list sync:
  peer-agent → sends {type:"hello", models:["llama3:8b","mistral:7b"]}
  peer-relay → UPDATE peers SET models='{llama3:8b,mistral:7b}'

Inference request:
  caller → POST /v1/chat/completions {model:"peer-abc123/llama3:8b"}
  gateway → parse peer_id=abc123, model=llama3:8b
  gateway → check peer_invites (Redis cache, 30s TTL)
  gateway → GET peer-relay/internal/peers/abc123/online → {online:true}
  gateway → POST peer-relay/internal/forward/abc123 [X-Peer-Model: llama3:8b] + body
  peer-relay → finds WS for abc123, sends request frame
  peer-agent → validates model in list → POST localhost:11434/v1/chat/completions
  peer-agent → streams tokens → WS frames → peer-relay → gateway → caller

Disconnect:
  peer-agent exits / WS closes
  peer-relay → UPDATE peers SET online=false, last_seen=now()
  any in-flight request → relay sends SSE error + closes
```

---

## 6. peer-agent Install Script

Served from control-api at `GET /peer-agent/install.sh`. Script:
1. Detects OS/arch (linux-amd64, linux-arm64, darwin-amd64, darwin-arm64)
2. Downloads binary from `GET /peer-agent/download/{os}/{arch}`
3. Verifies SHA256 against `GET /peer-agent/checksums.txt`
4. Installs to `/usr/local/bin/openserve-peer`
5. Runs: `openserve-peer --token=$TOKEN --relay=$RELAY`

Binaries are embedded in the control-api image using Go's `embed` package (or served as static assets via a Kubernetes ConfigMap for easy updates).

---

## 7. Helm Chart Changes

- Add `peer-relay` Deployment + ClusterIP Service to `charts/openserve/templates/`
- Add Ingress rule: `/{peer-ws}/*` → `peer-relay-svc:8080`
- Add `peerRelay.enabled` value (default: `true`)
- peer-relay needs Postgres credentials (same Secret as control-api)

---

## 8. Out of Scope (v1 of this feature)

- Multiple simultaneous peer connections per peer ID
- Peer-to-peer without going through the cluster (pure P2P)
- Persistent request queuing when peer is offline
- Usage metering for peer requests (no Pub/Sub emission in v1)
- Peer sharing across orgs
