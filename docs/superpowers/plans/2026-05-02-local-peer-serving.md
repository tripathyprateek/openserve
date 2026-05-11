# Local Peer Serving Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow org members to run a model locally via Ollama and serve other org members through a secure, invite-only WebSocket tunnel rooted in the customer's GKE cluster.

**Architecture:** A new `peer-relay` Go service runs in GKE and accepts outbound WebSocket connections from `openserve-peer` agent binaries running on developer laptops. The gateway routes `peer-{id}/{model}` requests to the relay's internal HTTP API, which forwards them over the live WebSocket to the peer agent, which proxies to local Ollama. A new GUI page lets owners register peers, get an install command, and invite API keys.

**Tech Stack:** Go 1.22, `github.com/gorilla/websocket`, `github.com/go-chi/chi/v5`, `github.com/jackc/pgx/v5`, Next.js 14 (App Router), SWR, Tailwind, shadcn/ui

---

## File Map

### New files
| File | Responsibility |
|---|---|
| `apps/control-api/internal/db/migrations/004_add_peers.sql` | `peers` + `peer_invites` tables |
| `apps/control-api/internal/handler/peers.go` | All peer handler methods |
| `apps/peer-relay/go.mod` | peer-relay module |
| `apps/peer-relay/cmd/main.go` | peer-relay entrypoint |
| `apps/peer-relay/internal/relay/hub.go` | In-memory peer connection registry |
| `apps/peer-relay/internal/relay/handler.go` | WebSocket accept + internal HTTP forward handlers |
| `apps/peer-agent/go.mod` | peer-agent module |
| `apps/peer-agent/cmd/main.go` | peer-agent entrypoint — connect, hello, proxy loop |
| `apps/gui/app/(main)/peers/page.tsx` | Local Peers GUI page |

### Modified files
| File | Change |
|---|---|
| `apps/control-api/cmd/server/main.go` | Register 7 new peer routes |
| `apps/gui/lib/api.ts` | Add peer API functions + types |
| `apps/gui/app/(main)/layout.tsx` (or nav component) | Add "Local Peers" nav item |
| `apps/gateway/internal/proxy/proxy.go` | Add peer model routing branch |

---

## Task A — DB Migration + Control API Peer CRUD

**Files:**
- Create: `apps/control-api/internal/db/migrations/004_add_peers.sql`
- Create: `apps/control-api/internal/handler/peers.go`
- Modify: `apps/control-api/cmd/server/main.go`

### Context for this task

Read these files first:
- `apps/control-api/internal/handler/handler.go` — learn `writeJSON`, `writeAuditLog`, `Deps`, handler struct, `auth.UserFromContext`
- `apps/control-api/internal/keygen/keygen.go` — `keygen.Generate()`, `keygen.Hash()`, `keygen.Verify()`
- `apps/control-api/cmd/server/main.go` — see how routes are registered in chi groups
- `apps/control-api/internal/db/migrations/003_add_webhooks.sql` — migration file pattern

The security invariants from CLAUDE.md:
- All DB queries use pgx positional params (`$1`, `$2`, ...) — NO string concatenation in SQL
- Token raw value stored nowhere; only Argon2id hash stored (use `keygen.Hash()`)
- Audit log rows are append-only; use `writeAuditLog` for all privileged actions

---

- [ ] **Step A1: Write the migration**

Create `apps/control-api/internal/db/migrations/004_add_peers.sql`:

```sql
CREATE TABLE IF NOT EXISTS peers (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    owner_id    TEXT NOT NULL,
    token_hash  TEXT NOT NULL,
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

- [ ] **Step A2: Create `apps/control-api/internal/handler/peers.go`**

```go
package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/openserve/openserve/apps/control-api/internal/auth"
	"github.com/openserve/openserve/apps/control-api/internal/keygen"
)

// peerRow is a row from the peers table.
type peerRow struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	OwnerID   string    `json:"ownerId"`
	Models    []string  `json:"models"`
	Online    bool      `json:"online"`
	LastSeen  *time.Time `json:"lastSeen"`
	CreatedAt time.Time `json:"createdAt"`
}

// peerInviteRow is a row from peer_invites joined with api_keys.
type peerInviteRow struct {
	ID       string `json:"id"`
	KeyID    string `json:"keyId"`
	KeyName  string `json:"keyName"`
}

// getOrgAndOwner returns (orgID, ownerID, error) for the authenticated user.
// ownerID is the members.id for the caller (used as owner_id).
func (h *Handler) getOrgAndOwner(r *http.Request) (orgID, ownerID string, err error) {
	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		return "", "", nil
	}
	err = h.DB.QueryRow(r.Context(),
		`SELECT org_id, id FROM members WHERE id = $1`,
		claims.Subject,
	).Scan(&orgID, &ownerID)
	return
}

// ListPeers returns all peers for the caller's org.
func (h *Handler) ListPeers(w http.ResponseWriter, r *http.Request) {
	orgID, _, err := h.getOrgAndOwner(r)
	if err != nil || orgID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	rows, err := h.DB.Query(r.Context(),
		`SELECT id, name, owner_id, models, online, last_seen, created_at
		 FROM peers WHERE org_id = $1 ORDER BY created_at DESC`,
		orgID,
	)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	peers := make([]peerRow, 0)
	for rows.Next() {
		var p peerRow
		if err := rows.Scan(&p.ID, &p.Name, &p.OwnerID, &p.Models, &p.Online, &p.LastSeen, &p.CreatedAt); err != nil {
			http.Error(w, "scan error", http.StatusInternalServerError)
			return
		}
		peers = append(peers, p)
	}

	writeJSON(w, http.StatusOK, peers)
}

// CreatePeer registers a new peer and returns the one-time raw token.
func (h *Handler) CreatePeer(w http.ResponseWriter, r *http.Request) {
	orgID, ownerID, err := h.getOrgAndOwner(r)
	if err != nil || orgID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	rawToken, tokenHash, err := keygen.Generate()
	if err != nil {
		http.Error(w, "failed to generate token", http.StatusInternalServerError)
		return
	}

	var id string
	err = h.DB.QueryRow(r.Context(),
		`INSERT INTO peers (org_id, name, owner_id, token_hash)
		 VALUES ($1, $2, $3, $4) RETURNING id`,
		orgID, req.Name, ownerID, tokenHash,
	).Scan(&id)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	h.writeAuditLog(r.Context(), orgID, "peer.create", "peer", id, map[string]string{"name": req.Name})

	writeJSON(w, http.StatusCreated, map[string]string{
		"id":    id,
		"token": rawToken,
	})
}

// DeletePeer removes a peer (owner or admin only).
func (h *Handler) DeletePeer(w http.ResponseWriter, r *http.Request) {
	peerID := chi.URLParam(r, "id")
	orgID, ownerID, err := h.getOrgAndOwner(r)
	if err != nil || orgID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Allow deletion only if caller is owner OR admin role.
	var role string
	_ = h.DB.QueryRow(r.Context(),
		`SELECT role FROM members WHERE id = $1`, ownerID,
	).Scan(&role)

	var peerOwnerID string
	err = h.DB.QueryRow(r.Context(),
		`SELECT owner_id FROM peers WHERE id = $1 AND org_id = $2`,
		peerID, orgID,
	).Scan(&peerOwnerID)
	if err != nil {
		http.Error(w, "peer not found", http.StatusNotFound)
		return
	}

	if peerOwnerID != ownerID && role != "admin" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	_, err = h.DB.Exec(r.Context(),
		`DELETE FROM peers WHERE id = $1 AND org_id = $2`, peerID, orgID,
	)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	h.writeAuditLog(r.Context(), orgID, "peer.delete", "peer", peerID, nil)
	w.WriteHeader(http.StatusNoContent)
}

// RotatePeerToken issues a new token for a peer, invalidating the old one.
func (h *Handler) RotatePeerToken(w http.ResponseWriter, r *http.Request) {
	peerID := chi.URLParam(r, "id")
	orgID, ownerID, err := h.getOrgAndOwner(r)
	if err != nil || orgID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var peerOwnerID string
	err = h.DB.QueryRow(r.Context(),
		`SELECT owner_id FROM peers WHERE id = $1 AND org_id = $2`,
		peerID, orgID,
	).Scan(&peerOwnerID)
	if err != nil {
		http.Error(w, "peer not found", http.StatusNotFound)
		return
	}

	var role string
	_ = h.DB.QueryRow(r.Context(), `SELECT role FROM members WHERE id = $1`, ownerID).Scan(&role)
	if peerOwnerID != ownerID && role != "admin" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	rawToken, tokenHash, err := keygen.Generate()
	if err != nil {
		http.Error(w, "failed to generate token", http.StatusInternalServerError)
		return
	}

	_, err = h.DB.Exec(r.Context(),
		`UPDATE peers SET token_hash = $1, online = false, last_seen = now() WHERE id = $2 AND org_id = $3`,
		tokenHash, peerID, orgID,
	)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	h.writeAuditLog(r.Context(), orgID, "peer.rotate_token", "peer", peerID, nil)
	writeJSON(w, http.StatusOK, map[string]string{"token": rawToken})
}

// ListPeerInvites returns all API keys invited to a peer.
func (h *Handler) ListPeerInvites(w http.ResponseWriter, r *http.Request) {
	peerID := chi.URLParam(r, "id")
	orgID, _, err := h.getOrgAndOwner(r)
	if err != nil || orgID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Verify peer belongs to this org.
	var count int
	_ = h.DB.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM peers WHERE id = $1 AND org_id = $2`, peerID, orgID,
	).Scan(&count)
	if count == 0 {
		http.Error(w, "peer not found", http.StatusNotFound)
		return
	}

	rows, err := h.DB.Query(r.Context(),
		`SELECT pi.id, pi.api_key_id, ak.name
		 FROM peer_invites pi
		 JOIN api_keys ak ON ak.id = pi.api_key_id
		 WHERE pi.peer_id = $1`,
		peerID,
	)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	invites := make([]peerInviteRow, 0)
	for rows.Next() {
		var inv peerInviteRow
		if err := rows.Scan(&inv.ID, &inv.KeyID, &inv.KeyName); err != nil {
			http.Error(w, "scan error", http.StatusInternalServerError)
			return
		}
		invites = append(invites, inv)
	}

	writeJSON(w, http.StatusOK, invites)
}

// CreatePeerInvite invites an API key to access a peer.
func (h *Handler) CreatePeerInvite(w http.ResponseWriter, r *http.Request) {
	peerID := chi.URLParam(r, "id")
	orgID, ownerID, err := h.getOrgAndOwner(r)
	if err != nil || orgID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var peerOwnerID string
	err = h.DB.QueryRow(r.Context(),
		`SELECT owner_id FROM peers WHERE id = $1 AND org_id = $2`, peerID, orgID,
	).Scan(&peerOwnerID)
	if err != nil {
		http.Error(w, "peer not found", http.StatusNotFound)
		return
	}

	var role string
	_ = h.DB.QueryRow(r.Context(), `SELECT role FROM members WHERE id = $1`, ownerID).Scan(&role)
	if peerOwnerID != ownerID && role != "admin" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	var req struct {
		APIKeyID string `json:"apiKeyId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.APIKeyID == "" {
		http.Error(w, "apiKeyId is required", http.StatusBadRequest)
		return
	}

	// Verify key belongs to same org.
	var keyOrgID string
	err = h.DB.QueryRow(r.Context(),
		`SELECT org_id FROM api_keys WHERE id = $1`, req.APIKeyID,
	).Scan(&keyOrgID)
	if err != nil || keyOrgID != orgID {
		http.Error(w, "api key not found in org", http.StatusBadRequest)
		return
	}

	var inviteID string
	err = h.DB.QueryRow(r.Context(),
		`INSERT INTO peer_invites (peer_id, api_key_id) VALUES ($1, $2)
		 ON CONFLICT (peer_id, api_key_id) DO NOTHING
		 RETURNING id`,
		peerID, req.APIKeyID,
	).Scan(&inviteID)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	h.writeAuditLog(r.Context(), orgID, "peer.invite", "peer", peerID, map[string]string{"keyId": req.APIKeyID})
	writeJSON(w, http.StatusCreated, map[string]string{"id": inviteID})
}

// DeletePeerInvite revokes an API key's access to a peer.
func (h *Handler) DeletePeerInvite(w http.ResponseWriter, r *http.Request) {
	peerID := chi.URLParam(r, "id")
	keyID := chi.URLParam(r, "keyId")
	orgID, _, err := h.getOrgAndOwner(r)
	if err != nil || orgID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	_, err = h.DB.Exec(r.Context(),
		`DELETE FROM peer_invites
		 WHERE peer_id = $1 AND api_key_id = $2
		   AND peer_id IN (SELECT id FROM peers WHERE org_id = $3)`,
		peerID, keyID, orgID,
	)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	h.writeAuditLog(r.Context(), orgID, "peer.revoke_invite", "peer", peerID, map[string]string{"keyId": keyID})
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step A3: Register peer routes in `apps/control-api/cmd/server/main.go`**

In the authenticated `r.Group(func(r chi.Router) {...})` block, after the webhooks routes, add:

```go
r.Get("/api/v1/peers", h.ListPeers)
r.Post("/api/v1/peers", h.CreatePeer)
r.Delete("/api/v1/peers/{id}", h.DeletePeer)
r.Post("/api/v1/peers/{id}/rotate-token", h.RotatePeerToken)
r.Get("/api/v1/peers/{id}/invites", h.ListPeerInvites)
r.Post("/api/v1/peers/{id}/invites", h.CreatePeerInvite)
r.Delete("/api/v1/peers/{id}/invites/{keyId}", h.DeletePeerInvite)
```

- [ ] **Step A4: Build and verify**

```bash
cd apps/control-api
go build ./...
```

Expected: no errors. If `keygen` import path fails, check it is `github.com/openserve/openserve/apps/control-api/internal/keygen`.

---

## Task B — peer-relay Service

**Files:**
- Create: `apps/peer-relay/go.mod`
- Create: `apps/peer-relay/internal/relay/hub.go`
- Create: `apps/peer-relay/internal/relay/handler.go`
- Create: `apps/peer-relay/cmd/main.go`

### Context for this task

The peer-relay is a **new Go module** — it does NOT share the control-api module. It has two roles:
1. **WebSocket server** (public-facing, behind ingress): accepts connections from peer agents
2. **Internal HTTP server** (cluster-internal only): accepts forwarding requests from the gateway

The WebSocket protocol (JSON frames over the WS connection):

**Peer → Relay:**
- `{"type":"hello","models":["llama3:8b","mistral:7b"]}` — sent immediately on connect; relay updates DB
- `{"type":"chunk","id":"<req_id>","data":"<raw SSE line>"}` — one frame per SSE line from Ollama
- `{"type":"done","id":"<req_id>"}` — signals end of response stream
- `{"type":"error","id":"<req_id>","message":"..."}` — error proxying to Ollama

**Relay → Peer:**
- `{"type":"ping"}` — keepalive every 30s
- `{"type":"request","id":"<req_id>","model":"llama3:8b","body":"<base64 of JSON request body>"}` — forwarded inference request

Security: Token sent in `Authorization: Bearer <token>` header on WS upgrade. Relay calls Argon2id verify (using the same `keygen.Verify()` logic). Token must be loaded from Postgres on each new WS connection.

---

- [ ] **Step B1: Create `apps/peer-relay/go.mod`**

```
module github.com/openserve/openserve/apps/peer-relay

go 1.22

require (
	github.com/go-chi/chi/v5 v5.0.12
	github.com/gorilla/websocket v1.5.1
	github.com/jackc/pgx/v5 v5.5.5
	go.uber.org/zap v1.27.0
	golang.org/x/crypto v0.22.0
)
```

Then run: `cd apps/peer-relay && go mod tidy`

- [ ] **Step B2: Create `apps/peer-relay/internal/relay/hub.go`**

```go
package relay

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// Frame is a JSON message sent over the WebSocket.
type Frame struct {
	Type    string `json:"type"`
	ID      string `json:"id,omitempty"`
	Models  []string `json:"models,omitempty"`
	Model   string `json:"model,omitempty"`
	Body    string `json:"body,omitempty"` // base64-encoded JSON request body
	Data    string `json:"data,omitempty"` // raw SSE line for chunk frames
	Message string `json:"message,omitempty"`
}

// pendingReq tracks an in-flight forwarded request.
type pendingReq struct {
	chunks chan string // receives raw SSE data lines
	done   chan struct{}
	errCh  chan string
}

// PeerConn represents a connected peer agent.
type PeerConn struct {
	peerID  string
	models  []string
	conn    *websocket.Conn
	mu      sync.Mutex
	pending map[string]*pendingReq
	log     *zap.Logger
}

// send writes a frame to the WebSocket (thread-safe).
func (pc *PeerConn) send(f Frame) error {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	return pc.conn.WriteJSON(f)
}

// Hub manages all active peer connections.
type Hub struct {
	mu    sync.RWMutex
	peers map[string]*PeerConn // peer_id → conn
	log   *zap.Logger
}

// NewHub creates a new Hub.
func NewHub(log *zap.Logger) *Hub {
	return &Hub{peers: make(map[string]*PeerConn), log: log}
}

// Register adds a peer connection, evicting any existing connection for the same peer.
func (h *Hub) Register(peerID string, pc *PeerConn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if old, ok := h.peers[peerID]; ok {
		h.log.Info("evicting old peer connection", zap.String("peer", peerID))
		old.conn.Close()
	}
	h.peers[peerID] = pc
}

// Unregister removes a peer connection.
func (h *Hub) Unregister(peerID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.peers, peerID)
}

// IsOnline reports whether a peer has an active connection.
func (h *Hub) IsOnline(peerID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.peers[peerID]
	return ok
}

// Forward sends a request to the peer and streams the response into w.
// The caller must have verified the peer is online before calling Forward.
func (h *Hub) Forward(peerID, model string, body []byte, w http.ResponseWriter) error {
	h.mu.RLock()
	pc, ok := h.peers[peerID]
	h.mu.RUnlock()
	if !ok {
		return fmt.Errorf("peer %s not connected", peerID)
	}

	reqID := generateID()
	pr := &pendingReq{
		chunks: make(chan string, 256),
		done:   make(chan struct{}),
		errCh:  make(chan string, 1),
	}

	pc.mu.Lock()
	if pc.pending == nil {
		pc.pending = make(map[string]*pendingReq)
	}
	pc.pending[reqID] = pr
	pc.mu.Unlock()

	defer func() {
		pc.mu.Lock()
		delete(pc.pending, reqID)
		pc.mu.Unlock()
	}()

	// Send request frame to peer agent.
	if err := pc.send(Frame{
		Type:  "request",
		ID:    reqID,
		Model: model,
		Body:  base64.StdEncoding.EncodeToString(body),
	}); err != nil {
		return fmt.Errorf("send to peer: %w", err)
	}

	// Set SSE headers.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, canFlush := w.(http.Flusher)

	// Stream chunks back to caller.
	for {
		select {
		case chunk := <-pr.chunks:
			if _, err := io.WriteString(w, chunk); err != nil {
				return nil // client disconnected
			}
			if canFlush {
				flusher.Flush()
			}
		case <-pr.done:
			// Write final SSE done marker if not already in stream.
			io.WriteString(w, "data: [DONE]\n\n")
			if canFlush {
				flusher.Flush()
			}
			return nil
		case errMsg := <-pr.errCh:
			return fmt.Errorf("peer error: %s", errMsg)
		}
	}
}

// dispatch routes an inbound frame from a peer to the appropriate pending request.
func (h *Hub) dispatch(pc *PeerConn, f Frame) {
	pc.mu.Lock()
	pr, ok := pc.pending[f.ID]
	pc.mu.Unlock()
	if !ok {
		return // stale or unknown request ID
	}

	switch f.Type {
	case "chunk":
		select {
		case pr.chunks <- f.Data:
		default:
			// buffer full — drop (shouldn't happen with 256-cap buffer)
		}
	case "done":
		close(pr.done)
	case "error":
		select {
		case pr.errCh <- f.Message:
		default:
		}
	}
}
```

- [ ] **Step B3: Create `apps/peer-relay/internal/relay/handler.go`**

```go
package relay

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
	"golang.org/x/crypto/argon2"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
}

// Handler holds dependencies for both the public WS and internal HTTP handlers.
type Handler struct {
	DB  *pgxpool.Pool
	Hub *Hub
	Log *zap.Logger
}

// generateID returns a random 16-byte hex string for request IDs.
func generateID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// verifyArgon2Token verifies rawToken against the storedHash (same format as keygen package).
func verifyArgon2Token(rawToken, storedHash string) error {
	var version, m, t, p uint32
	var saltB64, hashB64 string
	_, err := fmt.Sscanf(storedHash,
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		&version, &m, &t, &p, &saltB64, &hashB64)
	if err != nil {
		return fmt.Errorf("invalid hash format")
	}
	salt, _ := base64.RawStdEncoding.DecodeString(saltB64)
	computedHash := argon2.IDKey([]byte(rawToken), salt, t, m, uint8(p), 32)
	stored, _ := base64.RawStdEncoding.DecodeString(hashB64)
	if len(computedHash) != len(stored) {
		return fmt.Errorf("hash mismatch")
	}
	var diff byte
	for i := range computedHash {
		diff |= computedHash[i] ^ stored[i]
	}
	if diff != 0 {
		return fmt.Errorf("invalid token")
	}
	return nil
}

// ConnectPeer handles WebSocket connections from peer agents.
// Path: GET /peer-ws/connect
// Auth: Authorization: Bearer <raw_token>
func (h *Handler) ConnectPeer(w http.ResponseWriter, r *http.Request) {
	rawToken := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if rawToken == "" {
		http.Error(w, "missing token", http.StatusUnauthorized)
		return
	}

	// Look up peer by token hash prefix — we hash the raw token to find the peer.
	// We load all peers in this org and verify — or we store a fast-lookup prefix.
	// For simplicity: load token_hash for all peers and verify. Peers are few.
	// In production, store prefix like API keys.
	rows, err := h.DB.Query(r.Context(),
		`SELECT id, org_id, token_hash FROM peers WHERE online = false OR online = true`)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var matchedPeerID, matchedOrgID string
	for rows.Next() {
		var peerID, orgID, tokenHash string
		if err := rows.Scan(&peerID, &orgID, &tokenHash); err != nil {
			continue
		}
		if verifyArgon2Token(rawToken, tokenHash) == nil {
			matchedPeerID = peerID
			matchedOrgID = orgID
			break
		}
	}
	rows.Close()

	if matchedPeerID == "" {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.Log.Error("ws upgrade failed", zap.Error(err))
		return
	}

	pc := &PeerConn{
		peerID:  matchedPeerID,
		conn:    conn,
		pending: make(map[string]*pendingReq),
		log:     h.Log,
	}

	h.Hub.Register(matchedPeerID, pc)
	h.Log.Info("peer connected", zap.String("peer", matchedPeerID), zap.String("org", matchedOrgID))

	// Mark online in DB.
	_, _ = h.DB.Exec(context.Background(),
		`UPDATE peers SET online = true, last_seen = now() WHERE id = $1`, matchedPeerID)

	defer func() {
		h.Hub.Unregister(matchedPeerID)
		_, _ = h.DB.Exec(context.Background(),
			`UPDATE peers SET online = false, last_seen = now() WHERE id = $1`, matchedPeerID)
		h.Log.Info("peer disconnected", zap.String("peer", matchedPeerID))
	}()

	// Keepalive ping goroutine.
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if err := pc.send(Frame{Type: "ping"}); err != nil {
				return
			}
		}
	}()

	// Read loop: receive hello + chunk/done/error frames.
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			break
		}

		var f Frame
		if err := json.Unmarshal(msg, &f); err != nil {
			continue
		}

		switch f.Type {
		case "hello":
			pc.models = f.Models
			_, _ = h.DB.Exec(context.Background(),
				`UPDATE peers SET models = $1, last_seen = now() WHERE id = $2`,
				f.Models, matchedPeerID)
		case "chunk", "done", "error":
			h.Hub.dispatch(pc, f)
		}
	}
}

// PeerOnline handles internal status checks from the gateway.
// Path: GET /internal/peers/{id}/online
func (h *Handler) PeerOnline(w http.ResponseWriter, r *http.Request) {
	peerID := chi.URLParam(r, "id")
	online := h.Hub.IsOnline(peerID)
	writeJSON(w, http.StatusOK, map[string]bool{"online": online})
}

// ForwardToPeer handles forwarding requests from the gateway to a peer.
// Path: POST /internal/forward/{id}
// Header: X-Peer-Model: llama3:8b
func (h *Handler) ForwardToPeer(w http.ResponseWriter, r *http.Request) {
	peerID := chi.URLParam(r, "id")
	model := r.Header.Get("X-Peer-Model")
	if model == "" {
		http.Error(w, "X-Peer-Model header required", http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024)) // 10MB limit
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	if err := h.Hub.Forward(peerID, model, body, w); err != nil {
		h.Log.Error("forward failed", zap.String("peer", peerID), zap.Error(err))
		if !isResponseStarted(w) {
			http.Error(w, "peer unavailable: "+err.Error(), http.StatusServiceUnavailable)
		}
	}
}

func isResponseStarted(w http.ResponseWriter) bool {
	// If headers have been sent (e.g. SSE headers), we can't write an error.
	// We use a simple heuristic: check if Content-Type is text/event-stream.
	return strings.Contains(w.Header().Get("Content-Type"), "text/event-stream")
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
```

- [ ] **Step B4: Create `apps/peer-relay/cmd/main.go`**

```go
package main

import (
	"context"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/openserve/openserve/apps/peer-relay/internal/relay"
)

func main() {
	addr := flag.String("addr", ":8080", "Listen address")
	internalAddr := flag.String("internal-addr", ":8081", "Internal HTTP listen address")
	postgresURL := flag.String("postgres-url", os.Getenv("POSTGRES_URL"), "Postgres connection URL")
	flag.Parse()

	log, _ := zap.NewProduction()
	defer log.Sync()

	if *postgresURL == "" {
		log.Fatal("--postgres-url is required")
	}

	pool, err := pgxpool.New(context.Background(), *postgresURL)
	if err != nil {
		log.Fatal("postgres connect failed", zap.Error(err))
	}
	defer pool.Close()

	hub := relay.NewHub(log)
	h := &relay.Handler{DB: pool, Hub: hub, Log: log}

	// Public router — WebSocket endpoint for peer agents.
	pubR := chi.NewRouter()
	pubR.Use(middleware.Recoverer)
	pubR.Get("/peer-ws/connect", h.ConnectPeer)
	pubR.Get("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })

	// Internal router — for gateway forwarding only (not exposed externally).
	intR := chi.NewRouter()
	intR.Use(middleware.Recoverer)
	intR.Get("/internal/peers/{id}/online", h.PeerOnline)
	intR.Post("/internal/forward/{id}", h.ForwardToPeer)

	pubSrv := &http.Server{Addr: *addr, Handler: pubR, ReadTimeout: 0, WriteTimeout: 0}
	intSrv := &http.Server{Addr: *internalAddr, Handler: intR, ReadTimeout: 5 * time.Second, WriteTimeout: 300 * time.Second}

	go func() { log.Fatal("public server", zap.Error(pubSrv.ListenAndServe())) }()
	go func() { log.Fatal("internal server", zap.Error(intSrv.ListenAndServe())) }()

	log.Info("peer-relay started", zap.String("public", *addr), zap.String("internal", *internalAddr))

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pubSrv.Shutdown(ctx)
	intSrv.Shutdown(ctx)
}
```

- [ ] **Step B5: Build and verify**

```bash
cd apps/peer-relay
go mod tidy
go build ./...
```

Expected: no errors.

---

## Task C — peer-agent Binary

**Files:**
- Create: `apps/peer-agent/go.mod`
- Create: `apps/peer-agent/cmd/main.go`

### Context for this task

The peer-agent is a Go binary users install on their laptops. It:
1. Connects to `wss://{relay}/peer-ws/connect` with the token in the `Authorization` header
2. Discovers Ollama models from `GET http://localhost:11434/api/tags`
3. Sends `{"type":"hello","models":[...]}` on connect
4. Listens for `{"type":"request","id":"...","model":"...","body":"<base64>"}` frames
5. Validates the model is in its registered list (rejects with error frame if not)
6. Proxies to `POST http://localhost:11434/v1/chat/completions` with the decoded body
7. Streams each SSE line back as `{"type":"chunk","id":"...","data":"<line>"}` frames
8. Sends `{"type":"done","id":"..."}` when the Ollama response ends
9. Reconnects with exponential backoff if the WebSocket drops

---

- [ ] **Step C1: Create `apps/peer-agent/go.mod`**

```
module github.com/openserve/openserve/apps/peer-agent

go 1.22

require (
	github.com/gorilla/websocket v1.5.1
)
```

Then: `cd apps/peer-agent && go mod tidy`

- [ ] **Step C2: Create `apps/peer-agent/cmd/main.go`**

```go
package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// Frame matches the relay's WebSocket protocol.
type Frame struct {
	Type    string   `json:"type"`
	ID      string   `json:"id,omitempty"`
	Models  []string `json:"models,omitempty"`
	Model   string   `json:"model,omitempty"`
	Body    string   `json:"body,omitempty"`
	Data    string   `json:"data,omitempty"`
	Message string   `json:"message,omitempty"`
}

func main() {
	token := flag.String("token", "", "Peer registration token (required)")
	relayURL := flag.String("relay", "", "openserve relay URL e.g. https://openserve.example.com (required)")
	ollamaURL := flag.String("ollama", "http://localhost:11434", "Local Ollama URL")
	flag.Parse()

	if *token == "" || *relayURL == "" {
		log.Fatal("--token and --relay are required")
	}

	for {
		if err := run(*token, *relayURL, *ollamaURL); err != nil {
			log.Printf("disconnected: %v — retrying in 5s", err)
			time.Sleep(5 * time.Second)
		}
	}
}

func run(token, relayBase, ollamaBase string) error {
	// Build WSS URL from relay base.
	u, err := url.Parse(relayBase)
	if err != nil {
		return fmt.Errorf("invalid relay URL: %w", err)
	}
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	u.Path = "/peer-ws/connect"

	header := http.Header{"Authorization": {"Bearer " + token}}
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), header)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()
	log.Printf("connected to relay at %s", u.String())

	// Discover Ollama models.
	models, err := listOllamaModels(ollamaBase)
	if err != nil {
		return fmt.Errorf("list ollama models: %w", err)
	}
	log.Printf("registered models: %v", models)
	modelSet := make(map[string]bool, len(models))
	for _, m := range models {
		modelSet[m] = true
	}

	// Send hello.
	if err := conn.WriteJSON(Frame{Type: "hello", Models: models}); err != nil {
		return fmt.Errorf("hello: %w", err)
	}

	// Read loop.
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}

		var f Frame
		if err := json.Unmarshal(msg, &f); err != nil {
			continue
		}

		switch f.Type {
		case "ping":
			conn.WriteJSON(Frame{Type: "pong"})
		case "request":
			go handleRequest(conn, f, modelSet, ollamaBase)
		}
	}
}

func handleRequest(conn *websocket.Conn, f Frame, modelSet map[string]bool, ollamaBase string) {
	send := func(frame Frame) {
		conn.WriteJSON(frame)
	}

	// Validate model.
	if !modelSet[f.Model] {
		send(Frame{Type: "error", ID: f.ID, Message: fmt.Sprintf("model %q not available", f.Model)})
		return
	}

	// Decode base64 body.
	bodyBytes, err := base64.StdEncoding.DecodeString(f.Body)
	if err != nil {
		send(Frame{Type: "error", ID: f.ID, Message: "invalid body encoding"})
		return
	}

	// Ensure stream:true in the body.
	var reqBody map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &reqBody); err != nil {
		send(Frame{Type: "error", ID: f.ID, Message: "invalid JSON body"})
		return
	}
	reqBody["model"] = f.Model
	reqBody["stream"] = true
	bodyBytes, _ = json.Marshal(reqBody)

	// Call Ollama.
	resp, err := http.Post(ollamaBase+"/v1/chat/completions", "application/json", bytes.NewReader(bodyBytes))
	if err != nil {
		send(Frame{Type: "error", ID: f.ID, Message: "ollama error: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		send(Frame{Type: "error", ID: f.ID, Message: fmt.Sprintf("ollama %d: %s", resp.StatusCode, string(body))})
		return
	}

	// Stream SSE lines back as chunk frames.
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			send(Frame{Type: "chunk", ID: f.ID, Data: "\n"})
			continue
		}
		send(Frame{Type: "chunk", ID: f.ID, Data: line + "\n"})
	}
	send(Frame{Type: "done", ID: f.ID})
}

// listOllamaModels returns model names from Ollama's /api/tags endpoint.
func listOllamaModels(ollamaBase string) ([]string, error) {
	resp, err := http.Get(ollamaBase + "/api/tags")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	names := make([]string, len(result.Models))
	for i, m := range result.Models {
		names[i] = m.Name
	}
	return names, nil
}
```

- [ ] **Step C3: Build and verify**

```bash
cd apps/peer-agent
go mod tidy
go build ./cmd/...
```

Expected: produces binary `cmd` or `main`. No errors.

---

## Task D — GUI Local Peers Page

**Files:**
- Modify: `apps/gui/lib/api.ts`
- Create: `apps/gui/app/(main)/peers/page.tsx`
- Modify: nav component (find it in `apps/gui/components/` or `apps/gui/app/(main)/layout.tsx`)

### Context for this task

Read these files first:
- `apps/gui/lib/api.ts` — see `fetchAPI`, `listWebhooks`, `createWebhook` for the API client pattern
- `apps/gui/app/(main)/settings/page.tsx` — see how SWR + form state works in this codebase
- `apps/gui/app/(main)/layout.tsx` — find where the nav links are to add "Local Peers"
- `apps/gui/components/ui/` — Badge, Button, Input, Label, Separator are available

The existing API client pattern (`fetchAPI`) handles auth headers automatically. Use `useSWR` with a key of the endpoint path. Use `mutate()` after any write operation.

---

- [ ] **Step D1: Add peer API types and functions to `apps/gui/lib/api.ts`**

Add these to the end of the existing `api.ts` file (do not replace existing content):

```typescript
// ── Peers ──────────────────────────────────────────────────────────────────

export interface Peer {
  id: string
  name: string
  ownerId: string
  models: string[]
  online: boolean
  lastSeen: string | null
  createdAt: string
}

export interface PeerInvite {
  id: string
  keyId: string
  keyName: string
}

export interface CreatePeerResponse {
  id: string
  token: string
}

export async function listPeers(): Promise<Peer[]> {
  return fetchAPI<Peer[]>("/api/v1/peers")
}

export async function createPeer(name: string): Promise<CreatePeerResponse> {
  return fetchAPI<CreatePeerResponse>("/api/v1/peers", {
    method: "POST",
    body: JSON.stringify({ name }),
  })
}

export async function deletePeer(id: string): Promise<void> {
  return fetchAPI<void>(`/api/v1/peers/${id}`, { method: "DELETE" })
}

export async function rotatePeerToken(id: string): Promise<{ token: string }> {
  return fetchAPI<{ token: string }>(`/api/v1/peers/${id}/rotate-token`, { method: "POST" })
}

export async function listPeerInvites(peerId: string): Promise<PeerInvite[]> {
  return fetchAPI<PeerInvite[]>(`/api/v1/peers/${peerId}/invites`)
}

export async function createPeerInvite(peerId: string, apiKeyId: string): Promise<{ id: string }> {
  return fetchAPI<{ id: string }>(`/api/v1/peers/${peerId}/invites`, {
    method: "POST",
    body: JSON.stringify({ apiKeyId }),
  })
}

export async function deletePeerInvite(peerId: string, keyId: string): Promise<void> {
  return fetchAPI<void>(`/api/v1/peers/${peerId}/invites/${keyId}`, { method: "DELETE" })
}
```

- [ ] **Step D2: Create `apps/gui/app/(main)/peers/page.tsx`**

```tsx
"use client"

import { useState } from "react"
import useSWR from "swr"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Badge } from "@/components/ui/badge"
import { Separator } from "@/components/ui/separator"
import { Monitor, Trash2, RotateCw, Copy, ChevronDown, ChevronUp, Plus } from "lucide-react"
import {
  listPeers,
  createPeer,
  deletePeer,
  rotatePeerToken,
  listPeerInvites,
  createPeerInvite,
  deletePeerInvite,
  listAPIKeys,
  type Peer,
} from "@/lib/api"

function InstallCommand({ token, domain }: { token: string; domain: string }) {
  const [copied, setCopied] = useState(false)
  const cmd = `curl -fsSL https://${domain}/peer-agent/install.sh | sh -s -- --token=${token} --relay=https://${domain}`

  const copy = () => {
    navigator.clipboard.writeText(cmd)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  return (
    <div className="space-y-1">
      <Label className="text-xs text-muted-foreground">Install command (token shown once)</Label>
      <div className="relative">
        <pre className="rounded-lg bg-zinc-900 text-zinc-100 text-xs p-3 pr-10 overflow-x-auto whitespace-pre-wrap break-all">
          {cmd}
        </pre>
        <button onClick={copy} className="absolute top-2 right-2 text-zinc-400 hover:text-zinc-200">
          <Copy className="w-4 h-4" />
        </button>
      </div>
      {copied && <p className="text-xs text-green-600">Copied!</p>}
      <p className="text-xs text-amber-600 font-medium">⚠ This token will not be shown again. Save it now.</p>
    </div>
  )
}

function PeerRow({ peer, onDelete, domain }: { peer: Peer; onDelete: () => void; domain: string }) {
  const [expanded, setExpanded] = useState(false)
  const [newToken, setNewToken] = useState<string | null>(null)
  const [rotating, setRotating] = useState(false)
  const { data: invites = [], mutate: mutateInvites } = useSWR(
    expanded ? `/api/v1/peers/${peer.id}/invites` : null,
    () => listPeerInvites(peer.id)
  )
  const { data: keys = [] } = useSWR("/api/v1/keys", () => listAPIKeys())

  const handleRotate = async () => {
    setRotating(true)
    try {
      const { token } = await rotatePeerToken(peer.id)
      setNewToken(token)
    } finally {
      setRotating(false)
    }
  }

  return (
    <div className="border rounded-lg overflow-hidden">
      <div className="flex items-center gap-3 px-4 py-3">
        <Monitor className="w-4 h-4 text-muted-foreground flex-shrink-0" />
        <div className="flex-1 min-w-0">
          <p className="font-medium text-sm truncate">{peer.name}</p>
          <p className="text-xs text-muted-foreground">
            {peer.lastSeen ? `Last seen ${new Date(peer.lastSeen).toLocaleString()}` : "Never connected"}
          </p>
        </div>
        <Badge variant={peer.online ? "default" : "secondary"} className="flex-shrink-0">
          {peer.online ? "🟢 Online" : "⚪ Offline"}
        </Badge>
        <div className="flex flex-wrap gap-1 max-w-[200px]">
          {(peer.models ?? []).slice(0, 3).map((m) => (
            <Badge key={m} variant="outline" className="text-xs">
              {m}
            </Badge>
          ))}
          {(peer.models ?? []).length > 3 && (
            <Badge variant="outline" className="text-xs">+{peer.models.length - 3}</Badge>
          )}
        </div>
        <Button variant="ghost" size="sm" onClick={() => setExpanded(!expanded)}>
          {expanded ? <ChevronUp className="w-4 h-4" /> : <ChevronDown className="w-4 h-4" />}
        </Button>
        <Button
          variant="ghost"
          size="sm"
          className="text-red-600 hover:text-red-700"
          onClick={onDelete}
        >
          <Trash2 className="w-4 h-4" />
        </Button>
      </div>

      {expanded && (
        <div className="border-t bg-muted/30 px-4 py-4 space-y-4">
          {newToken ? (
            <InstallCommand token={newToken} domain={domain} />
          ) : (
            <div className="flex items-center gap-2">
              <Button variant="outline" size="sm" onClick={handleRotate} disabled={rotating}>
                <RotateCw className={`w-3 h-3 mr-1 ${rotating ? "animate-spin" : ""}`} />
                Rotate Token
              </Button>
              <span className="text-xs text-muted-foreground">Generates a new token; peer must restart.</span>
            </div>
          )}

          <Separator />

          <div className="space-y-2">
            <Label className="text-sm font-medium">Invited API Keys</Label>
            {invites.length === 0 ? (
              <p className="text-xs text-muted-foreground">No API keys invited yet.</p>
            ) : (
              <div className="space-y-1">
                {invites.map((inv) => (
                  <div key={inv.id} className="flex items-center justify-between text-sm">
                    <span className="font-mono text-xs">{inv.keyName}</span>
                    <Button
                      variant="ghost"
                      size="sm"
                      className="text-red-600 hover:text-red-700 h-6 px-2"
                      onClick={async () => {
                        await deletePeerInvite(peer.id, inv.keyId)
                        mutateInvites()
                      }}
                    >
                      Remove
                    </Button>
                  </div>
                ))}
              </div>
            )}

            <div className="flex gap-2 mt-2">
              <select
                className="flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm"
                defaultValue=""
                onChange={async (e) => {
                  if (!e.target.value) return
                  await createPeerInvite(peer.id, e.target.value)
                  mutateInvites()
                  e.target.value = ""
                }}
              >
                <option value="">Add API key…</option>
                {keys
                  .filter((k) => !invites.some((inv) => inv.keyId === k.id))
                  .map((k) => (
                    <option key={k.id} value={k.id}>{k.name}</option>
                  ))}
              </select>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

export default function PeersPage() {
  const { data: peers = [], mutate } = useSWR("/api/v1/peers", () => listPeers())
  const [showForm, setShowForm] = useState(false)
  const [name, setName] = useState("")
  const [loading, setLoading] = useState(false)
  const [newPeer, setNewPeer] = useState<{ token: string; domain: string } | null>(null)

  const domain = typeof window !== "undefined" ? window.location.hostname : "openserve.example.com"

  const handleCreate = async () => {
    if (!name.trim()) return
    setLoading(true)
    try {
      const result = await createPeer(name.trim())
      setNewPeer({ token: result.token, domain })
      setName("")
      setShowForm(false)
      mutate()
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="p-6 max-w-2xl mx-auto space-y-8">
      <div>
        <h1 className="text-2xl font-semibold flex items-center gap-2">
          <Monitor className="w-6 h-6" /> Local Peers
        </h1>
        <p className="text-muted-foreground text-sm mt-1">
          Run a model on your laptop and share it with your team via a secure tunnel.
        </p>
      </div>

      {newPeer && (
        <div className="border border-green-300 rounded-lg p-4 bg-green-50 space-y-3">
          <p className="text-sm font-medium text-green-900">Peer registered! Run this on your machine:</p>
          <InstallCommand token={newPeer.token} domain={newPeer.domain} />
          <Button variant="outline" size="sm" onClick={() => setNewPeer(null)}>Dismiss</Button>
        </div>
      )}

      <div className="space-y-3">
        {peers.map((peer) => (
          <PeerRow
            key={peer.id}
            peer={peer}
            domain={domain}
            onDelete={async () => {
              await deletePeer(peer.id)
              mutate()
            }}
          />
        ))}

        {peers.length === 0 && !showForm && (
          <div className="text-center py-12 text-muted-foreground border rounded-lg">
            <Monitor className="w-8 h-8 mx-auto mb-2 opacity-40" />
            <p className="text-sm">No local peers registered yet.</p>
            <p className="text-xs mt-1">Register your laptop to serve Ollama models to your team.</p>
          </div>
        )}
      </div>

      {showForm ? (
        <div className="border rounded-lg p-4 space-y-3 bg-muted/30">
          <Label htmlFor="peer-name">Peer Name</Label>
          <Input
            id="peer-name"
            placeholder="e.g. Alice's MacBook Pro"
            value={name}
            onChange={(e) => setName(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && handleCreate()}
            disabled={loading}
          />
          <div className="flex gap-2">
            <Button onClick={handleCreate} disabled={loading || !name.trim()}>
              {loading ? "Registering…" : "Register"}
            </Button>
            <Button variant="outline" onClick={() => setShowForm(false)} disabled={loading}>
              Cancel
            </Button>
          </div>
        </div>
      ) : (
        <Button onClick={() => setShowForm(true)}>
          <Plus className="w-4 h-4 mr-2" /> Register Peer
        </Button>
      )}
    </div>
  )
}
```

- [ ] **Step D3: Add "Local Peers" to the nav**

Find the nav component (likely `apps/gui/app/(main)/layout.tsx` or `apps/gui/components/nav.tsx`). Look for where "Deployments", "Settings" etc are listed. Add:

```tsx
<Link href="/peers" className={...}>
  <Monitor className="w-4 h-4" />
  Local Peers
</Link>
```

Use the same className pattern as the adjacent nav items.

- [ ] **Step D4: Type-check the GUI**

```bash
cd apps/gui
pnpm typecheck
```

Fix any type errors before completing this task.

---

## Task E — Gateway Peer Routing

**Files:**
- Modify: `apps/gateway/internal/proxy/proxy.go`

### Context for this task

Read these files first:
- `apps/gateway/internal/proxy/proxy.go` — full file. Understand `ServeHTTP`, how `deploymentID` is extracted from path, how `Router.Resolve` works.
- `apps/gateway/internal/auth/validator.go` — understand `KeyValidator.Validate` and `KeyInfo`
- `apps/gateway/internal/routing/routing.go` — understand the `Router` interface

The gateway currently routes `/inference/{deployment-id}/v1/...` to vLLM. We need to detect `peer-{id}` as the deployment-id prefix and route to peer-relay's internal HTTP instead.

The peer-relay internal HTTP runs at `PEER_RELAY_INTERNAL_URL` (env var, e.g. `http://peer-relay:8081`).

**Peer routing logic:** When `deploymentID` starts with `peer-`, extract the peer UUID from it (format: `peer-{uuid}`). Read the model from the request body's `"model"` field. Call peer-relay's `GET /internal/peers/{uuid}/online`. If online, forward the request to `POST /internal/forward/{uuid}` with `X-Peer-Model: {model}` header. Also check `peer_invites` table via the gateway's Postgres connection to verify the API key is invited.

---

- [ ] **Step E1: Add peer-relay URL to gateway Config**

In `apps/gateway/cmd/main.go` (read the file first to understand its flag pattern), add:

```go
peerRelayURL := flag.String("peer-relay-url", os.Getenv("PEER_RELAY_INTERNAL_URL"), "Internal URL of peer-relay service")
```

Pass it into the proxy Config. Add `PeerRelayURL string` to `proxy.Config`.

- [ ] **Step E2: Add peer routing branch in `proxy.go`'s `ServeHTTP`**

After the `deploymentID` extraction and API key validation (but before vLLM routing), add:

```go
// Peer routing: deployment IDs starting with "peer-" route to a local peer agent.
if strings.HasPrefix(deploymentID, "peer-") {
	peerID := strings.TrimPrefix(deploymentID, "peer-")
	h.servePeer(w, r, peerID, keyInfo, start)
	return
}
```

- [ ] **Step E3: Add `servePeer` method to `proxy.go`**

```go
// servePeer forwards a request to a local peer agent via the peer-relay.
func (h *Handler) servePeer(w http.ResponseWriter, r *http.Request, peerID string, keyInfo *auth.KeyInfo, start time.Time) {
	if h.cfg.PeerRelayURL == "" {
		h.errJSON(w, http.StatusServiceUnavailable, "peer relay not configured")
		return
	}

	// Check the API key is invited to this peer (Postgres lookup).
	// Use the gateway's existing DB pool (add DB *pgxpool.Pool to proxy.Config if not present).
	var count int
	err := h.cfg.DB.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM peer_invites pi
		 JOIN api_keys ak ON ak.id = pi.api_key_id
		 WHERE pi.peer_id = $1 AND ak.id = $2`,
		peerID, keyInfo.KeyID,
	).Scan(&count)
	if err != nil || count == 0 {
		h.errJSON(w, http.StatusForbidden, "api key not invited to this peer")
		return
	}

	// Check peer is online.
	statusURL := h.cfg.PeerRelayURL + "/internal/peers/" + peerID + "/online"
	resp, err := http.Get(statusURL)
	if err != nil || resp.StatusCode != http.StatusOK {
		h.errJSON(w, http.StatusServiceUnavailable, "peer relay unreachable")
		return
	}
	var statusBody struct{ Online bool }
	json.NewDecoder(resp.Body).Decode(&statusBody)
	resp.Body.Close()
	if !statusBody.Online {
		h.errJSON(w, http.StatusServiceUnavailable, "peer is offline")
		return
	}

	// Read model from request body.
	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024))
	if err != nil {
		h.errJSON(w, http.StatusBadRequest, "failed to read body")
		return
	}
	var bodyJSON struct{ Model string `json:"model"` }
	json.Unmarshal(bodyBytes, &bodyJSON)
	model := bodyJSON.Model
	// Strip "peer-{id}/" prefix if model was set that way.
	if idx := strings.Index(model, "/"); idx >= 0 {
		model = model[idx+1:]
	}

	// Forward to peer-relay.
	forwardURL := h.cfg.PeerRelayURL + "/internal/forward/" + peerID
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, forwardURL, bytes.NewReader(bodyBytes))
	if err != nil {
		h.errJSON(w, http.StatusInternalServerError, "failed to create forward request")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Peer-Model", model)

	client := &http.Client{Timeout: 0} // no timeout — streaming
	fwdResp, err := client.Do(req)
	if err != nil {
		h.errJSON(w, http.StatusServiceUnavailable, "peer forward failed")
		return
	}
	defer fwdResp.Body.Close()

	// Stream response back.
	w.Header().Set("Content-Type", fwdResp.Header.Get("Content-Type"))
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(fwdResp.StatusCode)
	io.Copy(w, fwdResp.Body)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	h.cfg.Log.Info("peer request complete",
		zap.String("peer", peerID),
		zap.String("model", model),
		zap.Duration("duration", time.Since(start)),
	)
}
```

Note: add `"bytes"` and `"io"` to imports if not already present. Add `DB *pgxpool.Pool` to `proxy.Config` and pass it from the gateway's main.

- [ ] **Step E4: Build gateway**

```bash
cd apps/gateway
go build ./...
```

Expected: no errors. Fix any import or type errors.

---

## Integration Checklist

After all tasks complete, verify end-to-end:

- [ ] `POST /api/v1/peers` returns `{id, token}` — token is 47+ chars starting with `openserve_live_`
- [ ] The peers table has a row with `online=false`
- [ ] `GET /api/v1/peers` lists the peer
- [ ] Running `openserve-peer --token=<token> --relay=http://localhost:8080 --ollama=http://localhost:11434` connects and marks `online=true` in DB
- [ ] `GET /internal/peers/{id}/online` returns `{"online":true}`
- [ ] GUI `/peers` page renders, shows register form, shows new peer after creation
- [ ] Peer row expands to show invite manager
