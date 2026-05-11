package relay

import (
	"context"
	"encoding/base64"
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
	CheckOrigin:     func(r *http.Request) bool { return true },
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
}

type Handler struct {
	DB  *pgxpool.Pool
	Hub *Hub
	Log *zap.Logger
}

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

// ConnectPeer handles GET /peer-ws/connect — WebSocket upgrade for peer agents.
func (h *Handler) ConnectPeer(w http.ResponseWriter, r *http.Request) {
	rawToken := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if rawToken == "" {
		http.Error(w, "missing token", http.StatusUnauthorized)
		return
	}

	rows, err := h.DB.Query(r.Context(), `SELECT id, org_id, token_hash FROM peers`)
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

	_, _ = h.DB.Exec(context.Background(),
		`UPDATE peers SET online = true, last_seen = now() WHERE id = $1`, matchedPeerID)

	defer func() {
		h.Hub.Unregister(matchedPeerID)
		_, _ = h.DB.Exec(context.Background(),
			`UPDATE peers SET online = false, last_seen = now() WHERE id = $1`, matchedPeerID)
		h.Log.Info("peer disconnected", zap.String("peer", matchedPeerID))
	}()

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if err := pc.send(Frame{Type: "ping"}); err != nil {
				return
			}
		}
	}()

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
			h.Hub.Dispatch(pc, f)
		}
	}
}

// PeerOnline handles GET /internal/peers/{id}/online
func (h *Handler) PeerOnline(w http.ResponseWriter, r *http.Request) {
	peerID := chi.URLParam(r, "id")
	online := h.Hub.IsOnline(peerID)
	writeJSON(w, http.StatusOK, map[string]bool{"online": online})
}

// ForwardToPeer handles POST /internal/forward/{id}
func (h *Handler) ForwardToPeer(w http.ResponseWriter, r *http.Request) {
	peerID := chi.URLParam(r, "id")
	model := r.Header.Get("X-Peer-Model")
	if model == "" {
		http.Error(w, "X-Peer-Model header required", http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024))
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	if err := h.Hub.Forward(peerID, model, body, w); err != nil {
		h.Log.Error("forward failed", zap.String("peer", peerID), zap.Error(err))
		if !strings.Contains(w.Header().Get("Content-Type"), "text/event-stream") {
			http.Error(w, "peer unavailable: "+err.Error(), http.StatusServiceUnavailable)
		}
	}
}
