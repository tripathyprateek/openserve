package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/openserve/openserve/apps/control-api/internal/auth"
	"github.com/openserve/openserve/apps/control-api/internal/keygen"
)

// ListPeers returns all peers for the caller's org ordered by created_at DESC.
func (h *Handler) ListPeers(w http.ResponseWriter, r *http.Request) {
	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var orgID string
	err := h.DB.QueryRow(r.Context(),
		`SELECT org_id FROM members WHERE id = $1`,
		claims.Subject,
	).Scan(&orgID)
	if err != nil {
		http.Error(w, "failed to get org", http.StatusInternalServerError)
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

	type peerRow struct {
		ID        string     `json:"id"`
		Name      string     `json:"name"`
		OwnerID   string     `json:"ownerId"`
		Models    []string   `json:"models"`
		Online    bool       `json:"online"`
		LastSeen  *time.Time `json:"lastSeen"`
		CreatedAt time.Time  `json:"createdAt"`
	}

	var peers []peerRow
	for rows.Next() {
		var p peerRow
		var models []string
		if err := rows.Scan(&p.ID, &p.Name, &p.OwnerID, &models, &p.Online, &p.LastSeen, &p.CreatedAt); err != nil {
			continue
		}
		if models == nil {
			models = []string{}
		}
		p.Models = models
		peers = append(peers, p)
	}

	if peers == nil {
		peers = []peerRow{}
	}

	writeJSON(w, http.StatusOK, peers)
}

// CreatePeer creates a new peer. Requires {"name":"..."} body.
// Returns {"id":"...","token":"..."} with 201.
func (h *Handler) CreatePeer(w http.ResponseWriter, r *http.Request) {
	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		Name string `json:"name"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	var orgID, memberID string
	err := h.DB.QueryRow(r.Context(),
		`SELECT org_id, id FROM members WHERE id = $1`,
		claims.Subject,
	).Scan(&orgID, &memberID)
	if err != nil {
		http.Error(w, "failed to get org", http.StatusInternalServerError)
		return
	}

	// Generate token
	rawToken, tokenHash, err := keygen.Generate()
	if err != nil {
		h.Log.Error("failed to generate token", zap.Error(err))
		http.Error(w, "failed to generate token", http.StatusInternalServerError)
		return
	}

	// Insert peer
	var peerID string
	err = h.DB.QueryRow(r.Context(),
		`INSERT INTO peers (org_id, name, owner_id, token_hash)
		 VALUES ($1, $2, $3, $4) RETURNING id`,
		orgID, req.Name, memberID, tokenHash,
	).Scan(&peerID)
	if err != nil {
		h.Log.Error("failed to insert peer", zap.Error(err))
		http.Error(w, "failed to create peer", http.StatusInternalServerError)
		return
	}

	h.writeAuditLog(r.Context(), orgID, "peer.create", "peer", peerID, map[string]string{
		"name": req.Name,
	})

	writeJSON(w, http.StatusCreated, map[string]string{
		"id":    peerID,
		"token": rawToken,
	})
}

// DeletePeer deletes a peer by id. Only owner or admin can delete.
func (h *Handler) DeletePeer(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var orgID, ownerID string
	err := h.DB.QueryRow(r.Context(),
		`SELECT org_id, owner_id FROM peers WHERE id = $1`,
		id,
	).Scan(&orgID, &ownerID)
	if err == pgx.ErrNoRows {
		http.Error(w, "peer not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	// Check if caller is owner
	if ownerID != claims.Subject {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	_, _ = h.DB.Exec(r.Context(),
		`DELETE FROM peers WHERE id = $1`,
		id,
	)

	h.writeAuditLog(r.Context(), orgID, "peer.delete", "peer", id, nil)

	w.WriteHeader(http.StatusNoContent)
}

// RotatePeerToken rotates a peer's token. Sets online=false, returns {"token":"..."}.
func (h *Handler) RotatePeerToken(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var orgID, ownerID string
	err := h.DB.QueryRow(r.Context(),
		`SELECT org_id, owner_id FROM peers WHERE id = $1`,
		id,
	).Scan(&orgID, &ownerID)
	if err == pgx.ErrNoRows {
		http.Error(w, "peer not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	// Check if caller is owner
	if ownerID != claims.Subject {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	// Generate new token
	rawToken, tokenHash, err := keygen.Generate()
	if err != nil {
		h.Log.Error("failed to generate token", zap.Error(err))
		http.Error(w, "failed to generate token", http.StatusInternalServerError)
		return
	}

	// Update peer
	_, err = h.DB.Exec(r.Context(),
		`UPDATE peers SET token_hash = $1, online = false WHERE id = $2`,
		tokenHash, id,
	)
	if err != nil {
		h.Log.Error("failed to update peer", zap.Error(err))
		http.Error(w, "failed to rotate token", http.StatusInternalServerError)
		return
	}

	h.writeAuditLog(r.Context(), orgID, "peer.rotate_token", "peer", id, nil)

	writeJSON(w, http.StatusOK, map[string]string{
		"token": rawToken,
	})
}

// ListPeerInvites returns all peer invites for a peer, joined with api_keys.
// Returns [{"id", "keyId", "keyName"}].
func (h *Handler) ListPeerInvites(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Verify peer belongs to caller's org
	var orgID, ownerID string
	err := h.DB.QueryRow(r.Context(),
		`SELECT org_id, owner_id FROM peers WHERE id = $1`,
		id,
	).Scan(&orgID, &ownerID)
	if err == pgx.ErrNoRows {
		http.Error(w, "peer not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	// Check if caller is owner
	if ownerID != claims.Subject {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	rows, err := h.DB.Query(r.Context(),
		`SELECT pi.id, ak.id, ak.display_name
		 FROM peer_invites pi
		 JOIN api_keys ak ON pi.api_key_id = ak.id
		 WHERE pi.peer_id = $1
		 ORDER BY pi.created_at DESC`,
		id,
	)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type invite struct {
		ID      string `json:"id"`
		KeyID   string `json:"keyId"`
		KeyName string `json:"keyName"`
	}

	var invites []invite
	for rows.Next() {
		var inv invite
		if err := rows.Scan(&inv.ID, &inv.KeyID, &inv.KeyName); err != nil {
			continue
		}
		invites = append(invites, inv)
	}

	if invites == nil {
		invites = []invite{}
	}

	writeJSON(w, http.StatusOK, invites)
}

// CreatePeerInvite creates a new peer invite. Requires {"apiKeyId":"..."}.
// Verifies key belongs to same org. Returns 201.
func (h *Handler) CreatePeerInvite(w http.ResponseWriter, r *http.Request) {
	peerID := chi.URLParam(r, "id")

	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		APIKeyID string `json:"apiKeyId"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	// Verify peer belongs to caller's org
	var peerOrgID, ownerID string
	err := h.DB.QueryRow(r.Context(),
		`SELECT org_id, owner_id FROM peers WHERE id = $1`,
		peerID,
	).Scan(&peerOrgID, &ownerID)
	if err == pgx.ErrNoRows {
		http.Error(w, "peer not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	// Check if caller is owner
	if ownerID != claims.Subject {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	// Verify API key belongs to same org
	var keyOrgID string
	err = h.DB.QueryRow(r.Context(),
		`SELECT org_id FROM api_keys WHERE id = $1`,
		req.APIKeyID,
	).Scan(&keyOrgID)
	if err == pgx.ErrNoRows {
		http.Error(w, "api key not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	if keyOrgID != peerOrgID {
		http.Error(w, "api key does not belong to same org", http.StatusBadRequest)
		return
	}

	// Insert invite
	_, err = h.DB.Exec(r.Context(),
		`INSERT INTO peer_invites (peer_id, api_key_id) VALUES ($1, $2)`,
		peerID, req.APIKeyID,
	)
	if err != nil {
		h.Log.Error("failed to insert peer invite", zap.Error(err))
		http.Error(w, "failed to create peer invite", http.StatusInternalServerError)
		return
	}

	h.writeAuditLog(r.Context(), peerOrgID, "peer.invite.create", "peer_invite", peerID, map[string]string{
		"keyId": req.APIKeyID,
	})

	w.WriteHeader(http.StatusCreated)
}

// DeletePeerInvite deletes a peer invite by keyId. Returns 204.
func (h *Handler) DeletePeerInvite(w http.ResponseWriter, r *http.Request) {
	peerID := chi.URLParam(r, "id")
	keyID := chi.URLParam(r, "keyId")

	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Verify peer belongs to caller's org
	var orgID, ownerID string
	err := h.DB.QueryRow(r.Context(),
		`SELECT org_id, owner_id FROM peers WHERE id = $1`,
		peerID,
	).Scan(&orgID, &ownerID)
	if err == pgx.ErrNoRows {
		http.Error(w, "peer not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	// Check if caller is owner
	if ownerID != claims.Subject {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	_, _ = h.DB.Exec(r.Context(),
		`DELETE FROM peer_invites WHERE peer_id = $1 AND api_key_id = $2`,
		peerID, keyID,
	)

	h.writeAuditLog(r.Context(), orgID, "peer.invite.delete", "peer_invite", peerID, map[string]string{
		"keyId": keyID,
	})

	w.WriteHeader(http.StatusNoContent)
}
