package relay

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"crypto/rand"
	"encoding/hex"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

type Frame struct {
	Type    string   `json:"type"`
	ID      string   `json:"id,omitempty"`
	Models  []string `json:"models,omitempty"`
	Model   string   `json:"model,omitempty"`
	Body    string   `json:"body,omitempty"`
	Data    string   `json:"data,omitempty"`
	Message string   `json:"message,omitempty"`
}

type pendingReq struct {
	chunks chan string
	done   chan struct{}
	errCh  chan string
}

type PeerConn struct {
	peerID  string
	models  []string
	conn    *websocket.Conn
	mu      sync.Mutex
	pending map[string]*pendingReq
	log     *zap.Logger
}

func (pc *PeerConn) send(f Frame) error {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	return pc.conn.WriteJSON(f)
}

type Hub struct {
	mu    sync.RWMutex
	peers map[string]*PeerConn
	log   *zap.Logger
}

func NewHub(log *zap.Logger) *Hub {
	return &Hub{peers: make(map[string]*PeerConn), log: log}
}

func generateID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (h *Hub) Register(peerID string, pc *PeerConn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if old, ok := h.peers[peerID]; ok {
		h.log.Info("evicting old peer connection", zap.String("peer", peerID))
		old.conn.Close()
	}
	h.peers[peerID] = pc
}

func (h *Hub) Unregister(peerID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.peers, peerID)
}

func (h *Hub) IsOnline(peerID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.peers[peerID]
	return ok
}

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
	pc.pending[reqID] = pr
	pc.mu.Unlock()

	defer func() {
		pc.mu.Lock()
		delete(pc.pending, reqID)
		pc.mu.Unlock()
	}()

	if err := pc.send(Frame{
		Type:  "request",
		ID:    reqID,
		Model: model,
		Body:  base64.StdEncoding.EncodeToString(body),
	}); err != nil {
		return fmt.Errorf("send to peer: %w", err)
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, canFlush := w.(http.Flusher)

	for {
		select {
		case chunk := <-pr.chunks:
			if _, err := io.WriteString(w, chunk); err != nil {
				return nil
			}
			if canFlush {
				flusher.Flush()
			}
		case <-pr.done:
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

func (h *Hub) Dispatch(pc *PeerConn, f Frame) {
	pc.mu.Lock()
	pr, ok := pc.pending[f.ID]
	pc.mu.Unlock()
	if !ok {
		return
	}
	switch f.Type {
	case "chunk":
		select {
		case pr.chunks <- f.Data:
		default:
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

// writeJSON is a local helper (not exported).
func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
