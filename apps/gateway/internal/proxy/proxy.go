// Package proxy implements the core reverse-proxy logic for the gateway.
package proxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/openserve/openserve/apps/gateway/internal/auth"
	"github.com/openserve/openserve/apps/gateway/internal/ratelimit"
	"github.com/openserve/openserve/apps/gateway/internal/routing"
)

// Config holds the dependencies for the proxy handler.
type Config struct {
	KeyValidator auth.KeyValidator
	Limiter      ratelimit.Limiter
	Router       routing.Router
	Log          *zap.Logger
	PeerRelayURL string        // Internal URL of peer-relay, e.g. "http://peer-relay:8081"
	DB           *pgxpool.Pool // Postgres pool for peer invite checks
}

// Handler is an http.Handler that authenticates, rate-limits, and proxies
// requests to the appropriate vLLM backend.
type Handler struct {
	cfg Config
}

// New creates a new proxy Handler.
func New(cfg Config) *Handler {
	return &Handler{cfg: cfg}
}

// ServeHTTP is the main entry point for all gateway traffic.
//
// Supported URL shapes:
//
//	/inference/<deployment-id>/v1/<openai-path>   — explicit deployment in URL
//	/v1/<openai-path>                             — OpenAI-compat shorthand; deployment
//	                                                extracted from body "model" field
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	// Health probe — no auth required.
	if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
		w.WriteHeader(http.StatusOK)
		return
	}

	var deploymentID string
	var remainingPath string

	if strings.HasPrefix(r.URL.Path, "/inference/") {
		// Explicit deployment in URL: /inference/<deployment-id>/v1/...
		parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/inference/"), "/", 2)
		if len(parts) < 2 || parts[0] == "" {
			h.errJSON(w, http.StatusBadRequest, "invalid path: expected /inference/<deployment-id>/v1/...")
			return
		}
		deploymentID = parts[0]
		remainingPath = "/" + parts[1]
	} else if strings.HasPrefix(r.URL.Path, "/v1/") {
		// OpenAI-compatible shorthand: extract deployment from body "model" field.
		// We must peek at the body without consuming it.
		var peeked bytes.Buffer
		bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 8192))
		if err != nil {
			h.errJSON(w, http.StatusBadRequest, "failed to read request body")
			return
		}
		_ = r.Body.Close()
		r.Body = io.NopCloser(io.TeeReader(bytes.NewReader(bodyBytes), &peeked))
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes)) // restore full body

		var bodyObj struct {
			Model string `json:"model"`
		}
		if err := json.Unmarshal(bodyBytes, &bodyObj); err != nil || bodyObj.Model == "" {
			h.errJSON(w, http.StatusBadRequest, "missing or invalid 'model' field in request body")
			return
		}
		deploymentID = bodyObj.Model
		remainingPath = r.URL.Path
	} else {
		h.errJSON(w, http.StatusBadRequest, "invalid path: expected /inference/<deployment-id>/v1/... or /v1/...")
		return
	}

	// Extract and validate API key.
	rawKey := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if rawKey == "" {
		h.errJSON(w, http.StatusUnauthorized, "missing Authorization header")
		return
	}
	if !strings.HasPrefix(rawKey, "openserve_live_") {
		h.errJSON(w, http.StatusUnauthorized, "invalid API key format")
		return
	}

	keyInfo, err := h.cfg.KeyValidator.Validate(r.Context(), rawKey, deploymentID)
	if err != nil {
		h.cfg.Log.Warn("key validation failed", zap.String("deployment", deploymentID), zap.Error(err))
		h.errJSON(w, http.StatusUnauthorized, "invalid or expired API key")
		return
	}

	// Check if this is a peer deployment.
	if strings.HasPrefix(deploymentID, "peer-") {
		peerID := strings.TrimPrefix(deploymentID, "peer-")
		h.servePeer(w, r, peerID, keyInfo, start)
		return
	}

	// Rate limiting.
	allowed, retryAfter, err := h.cfg.Limiter.Allow(r.Context(), keyInfo.KeyID, keyInfo.RPM, keyInfo.TPM)
	if err != nil {
		h.cfg.Log.Error("rate limiter error", zap.Error(err))
		// Fail open on limiter errors to avoid blocking legitimate traffic.
	} else if !allowed {
		w.Header().Set("Retry-After", retryAfter.String())
		h.errJSON(w, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}

	// Extract system prompt prefix for cache-aware routing.
	cacheKey := extractCacheKey(r)
	upstream, err := h.cfg.Router.PrefixResolve(deploymentID, cacheKey)
	if err != nil {
		h.cfg.Log.Warn("routing failed", zap.String("deployment", deploymentID), zap.Error(err))
		h.errJSON(w, http.StatusServiceUnavailable, "model deployment not available or scaled to zero")
		return
	}

	// Add tracing header.
	requestID := r.Header.Get("X-Request-Id")
	if requestID == "" {
		requestID = generateRequestID()
	}
	w.Header().Set("X-Openserve-Request-Id", requestID)

	// Forward the path to vLLM. For /inference/<id>/v1/... we've already
	// set remainingPath to the /v1/... suffix; for /v1/... it stays as-is.
	r.URL.Path = remainingPath
	r.URL.RawPath = ""

	// Build upstream request manually so we can intercept the SSE stream.
	upstreamPath := strings.TrimPrefix(remainingPath, "/")
	upstreamURL := "http://" + upstream + "/" + upstreamPath
	if r.URL.RawQuery != "" {
		upstreamURL += "?" + r.URL.RawQuery
	}
	upReq, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL, r.Body)
	if err != nil {
		h.errJSON(w, http.StatusInternalServerError, "failed to build upstream request")
		return
	}
	// Copy headers (except Authorization which is already stripped at validation time).
	for k, vs := range r.Header {
		if strings.EqualFold(k, "Authorization") {
			continue
		}
		for _, v := range vs {
			upReq.Header.Add(k, v)
		}
	}
	upReq.Header.Set("X-Forwarded-For", r.RemoteAddr)
	upReq.Header.Set("X-Openserve-Key-Id", keyInfo.KeyID)
	upReq.Header.Set("X-Openserve-Request-Id", requestID)

	upClient := &http.Client{Timeout: 0}
	upResp, err := upClient.Do(upReq)
	if err != nil {
		h.errJSON(w, http.StatusBadGateway, "upstream model error: "+err.Error())
		return
	}
	defer upResp.Body.Close()

	// Copy response headers.
	for k, vs := range upResp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(upResp.StatusCode)

	// Stream SSE lines, sniff the usage object from the final chunk.
	var inputTokens, outputTokens int
	buf := make([]byte, 4096)
	var leftover []byte
	flusher, canFlush := w.(http.Flusher)
	for {
		n, readErr := upResp.Body.Read(buf)
		if n > 0 {
			chunk := append(leftover, buf[:n]...)
			leftover = nil
			// Scan lines for usage object.
			lines := strings.Split(string(chunk), "\n")
			for i, line := range lines {
				if i == len(lines)-1 && !strings.HasSuffix(string(chunk), "\n") {
					leftover = []byte(line)
					break
				}
				if strings.HasPrefix(line, "data: ") {
					data := strings.TrimPrefix(line, "data: ")
					data = strings.TrimSpace(data)
					if data != "[DONE]" && data != "" {
						var sseChunk struct {
							Usage *struct {
								PromptTokens     int `json:"prompt_tokens"`
								CompletionTokens int `json:"completion_tokens"`
							} `json:"usage"`
						}
						if jsonErr := json.Unmarshal([]byte(data), &sseChunk); jsonErr == nil && sseChunk.Usage != nil {
							inputTokens = sseChunk.Usage.PromptTokens
							outputTokens = sseChunk.Usage.CompletionTokens
						}
					}
				}
			}
			w.Write(buf[:n])
			if canFlush {
				flusher.Flush()
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			break
		}
	}

	// Record actual token consumption for TPM rate limiting.
	totalTokens := int32(inputTokens + outputTokens)
	if totalTokens > 0 {
		go func() {
			if err := h.cfg.Limiter.RecordTokens(context.Background(), keyInfo.KeyID, totalTokens); err != nil {
				h.cfg.Log.Warn("failed to record tokens for rate limit", zap.Error(err))
			}
		}()
	}

	durationMs := int(time.Since(start).Milliseconds())

	// Write inference event asynchronously — never block the response.
	if h.cfg.DB != nil && (inputTokens > 0 || outputTokens > 0) {
		go h.writeInferenceEvent(keyInfo, deploymentID, requestID, inputTokens, outputTokens, durationMs)
	}

	h.cfg.Log.Info("proxied request",
		zap.String("deployment", deploymentID),
		zap.String("keyId", keyInfo.KeyID),
		zap.String("requestId", requestID),
		zap.Duration("duration", time.Since(start)),
	)
}

func (h *Handler) servePeer(w http.ResponseWriter, r *http.Request, peerID string, keyInfo *auth.KeyInfo, start time.Time) {
	if h.cfg.PeerRelayURL == "" || h.cfg.DB == nil {
		h.errJSON(w, http.StatusServiceUnavailable, "peer relay not configured")
		return
	}

	// Verify the API key is invited to this peer.
	var count int
	if err := h.cfg.DB.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM peer_invites WHERE peer_id = $1 AND api_key_id = $2`,
		peerID, keyInfo.KeyID,
	).Scan(&count); err != nil || count == 0 {
		h.errJSON(w, http.StatusForbidden, "api key not invited to this peer")
		return
	}

	// Check peer is online.
	statusResp, err := http.Get(h.cfg.PeerRelayURL + "/internal/peers/" + peerID + "/online")
	if err != nil {
		h.errJSON(w, http.StatusServiceUnavailable, "peer relay unreachable")
		return
	}
	defer statusResp.Body.Close()
	var statusBody struct{ Online bool `json:"online"` }
	json.NewDecoder(statusResp.Body).Decode(&statusBody)
	if !statusBody.Online {
		h.errJSON(w, http.StatusServiceUnavailable, "peer is offline")
		return
	}

	// Read request body.
	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024))
	if err != nil {
		h.errJSON(w, http.StatusBadRequest, "failed to read body")
		return
	}

	// Extract model name, strip peer prefix if present.
	var bodyJSON struct{ Model string `json:"model"` }
	json.Unmarshal(bodyBytes, &bodyJSON)
	model := bodyJSON.Model
	if idx := strings.Index(model, "/"); idx >= 0 {
		model = model[idx+1:]
	}

	// Forward to peer-relay.
	forwardURL := h.cfg.PeerRelayURL + "/internal/forward/" + peerID
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, forwardURL, bytes.NewReader(bodyBytes))
	if err != nil {
		h.errJSON(w, http.StatusInternalServerError, "failed to create request")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Peer-Model", model)

	client := &http.Client{Timeout: 0}
	fwdResp, err := client.Do(req)
	if err != nil {
		h.errJSON(w, http.StatusServiceUnavailable, "peer forward failed: "+err.Error())
		return
	}
	defer fwdResp.Body.Close()

	// Stream back.
	for k, vs := range fwdResp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
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

func (h *Handler) errJSON(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func generateRequestID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		// Fallback only if rand fails (extremely unlikely)
		return fmt.Sprintf("req_%d", time.Now().UnixNano())
	}
	return "req_" + hex.EncodeToString(b)
}

// writeInferenceEvent records a completed inference request in Postgres.
// Called in a goroutine — must not reference the http.Request context (already cancelled).
func (h *Handler) writeInferenceEvent(keyInfo *auth.KeyInfo, deploymentID, requestID string, inputTokens, outputTokens, durationMs int) {
	ctx := context.Background()
	// Look up org_id from the key.
	var orgID string
	if err := h.cfg.DB.QueryRow(ctx,
		`SELECT org_id FROM api_keys WHERE id = $1`,
		keyInfo.KeyID,
	).Scan(&orgID); err != nil {
		h.cfg.Log.Warn("inference event: could not resolve org", zap.String("keyId", keyInfo.KeyID), zap.Error(err))
		return
	}
	_, err := h.cfg.DB.Exec(ctx,
		`INSERT INTO inference_events
		    (org_id, key_id, deployment_id, model, input_tokens, output_tokens, duration_ms, request_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		orgID, keyInfo.KeyID, deploymentID, deploymentID, inputTokens, outputTokens, durationMs, requestID,
	)
	if err != nil {
		h.cfg.Log.Warn("inference event write failed", zap.Error(err))
	}
}

// extractCacheKey returns a routing cache key from the request body's system prompt.
// It reads at most 512 bytes of the system prompt to compute a stable hash.
// If the body cannot be parsed or has no system prompt, returns an empty string
// (which causes PrefixResolve to fall back to service-level routing).
func extractCacheKey(r *http.Request) string {
	if r.Body == nil || r.ContentLength == 0 {
		return ""
	}

	// Peek at the body without consuming it (we need it for the upstream request too).
	// Read up to 8KB to find the system prompt.
	buf := make([]byte, 8192)
	n, _ := r.Body.Read(buf)
	if n == 0 {
		return ""
	}
	buf = buf[:n]

	// Put the body back so the upstream request can read it.
	r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(buf), r.Body))

	// Parse for system prompt.
	var body struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Prompt string `json:"prompt"` // completions API
	}
	if err := json.Unmarshal(buf, &body); err != nil {
		return ""
	}

	// Extract system message content (first 512 chars).
	for _, msg := range body.Messages {
		if msg.Role == "system" && len(msg.Content) > 0 {
			prefix := msg.Content
			if len(prefix) > 512 {
				prefix = prefix[:512]
			}
			h := fnv.New32a()
			h.Write([]byte(prefix))
			return fmt.Sprintf("%x", h.Sum32())
		}
	}

	// No system prompt: use first 512 chars of prompt (completions API).
	if len(body.Prompt) > 0 {
		prefix := body.Prompt
		if len(prefix) > 512 {
			prefix = prefix[:512]
		}
		h := fnv.New32a()
		h.Write([]byte(prefix))
		return fmt.Sprintf("%x", h.Sum32())
	}

	return ""
}
