package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
	"golang.org/x/oauth2"

	"github.com/openserve/openserve/apps/control-api/internal/auth"
	"github.com/openserve/openserve/apps/control-api/internal/catalog"
	"github.com/openserve/openserve/apps/control-api/internal/k8s"
	"github.com/openserve/openserve/apps/control-api/internal/keygen"
	"github.com/openserve/openserve/apps/control-api/internal/rag"
	"go.uber.org/zap"
)

// Deps contains dependencies for the handler.
type Deps struct {
	DB           *pgxpool.Pool
	Log          *zap.Logger
	OIDC         *auth.OIDCProvider
	JWTSecret    []byte
	Catalog      *catalog.Client
	GCPProject   string
	BQDataset    string
	OAuth2Config *oauth2.Config
	K8sClient    *k8s.Client
	BraveAPIKey  string
	EmbedClient  *rag.EmbedClient // nil if embedding not configured
	// DevEmail and DevMemberID are set only for local development.
	// When non-empty, RequireJWT is bypassed and all requests are treated as
	// this authenticated user. Never set in production.
	DevEmail    string
	DevMemberID string
}

// Handler handles HTTP requests.
type Handler struct {
	Deps
	embed *rag.EmbedClient
}

// New creates a new handler.
func New(deps Deps) *Handler {
	return &Handler{
		Deps: deps,
		embed: deps.EmbedClient,
	}
}

// slugify converts a string to a slug suitable for Kubernetes naming.
// Lowercases, replaces non-alphanumeric with hyphens, collapses hyphens, trims, and truncates to 40 chars.
func slugify(s string) string {
	s = strings.ToLower(s)
	// Replace non-alphanumeric with hyphens
	re := regexp.MustCompile(`[^a-z0-9]+`)
	s = re.ReplaceAllString(s, "-")
	// Collapse multiple hyphens
	re = regexp.MustCompile(`-+`)
	s = re.ReplaceAllString(s, "-")
	// Trim leading/trailing hyphens
	s = strings.Trim(s, "-")
	// Truncate to 40 chars
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}

// random4hex generates 2 crypto-random bytes as 4 hex characters.
func random4hex() string {
	b := make([]byte, 2)
	if _, err := rand.Read(b); err != nil {
		return "0000"
	}
	return hex.EncodeToString(b)
}

// writeJSON writes a JSON response.
func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// writeAuditLog writes an audit log entry.
func (h *Handler) writeAuditLog(ctx context.Context, orgID string, action, resourceType, resourceID string, details interface{}) {
	claims := auth.UserFromContext(ctx)
	if claims == nil {
		return
	}

	detailsJSON, _ := json.Marshal(details)
	ip := ctx.Value("ip_address")

	memberID := claims.Subject
	go func() {
		_, _ = h.DB.Exec(context.Background(),
			`INSERT INTO audit_log (org_id, actor_member_id, action, resource_type, resource_id, details, ip_address)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			orgID, memberID, action, resourceType, resourceID, detailsJSON, ip,
		)
	}()
}

// OIDCLogin redirects to the configured OIDC provider's authorization endpoint.
// Works with any standards-compliant OIDC provider (Google, Okta, Azure AD, GitHub, etc.)
func (h *Handler) OIDCLogin(w http.ResponseWriter, r *http.Request) {
	// Generate random state
	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		http.Error(w, "failed to generate state", http.StatusInternalServerError)
		return
	}
	state := hex.EncodeToString(stateBytes)

	// Store state in session cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "oidc_state",
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600, // 10 minutes
	})

	// Redirect to OIDC provider
	url := h.OAuth2Config.AuthCodeURL(state)
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

// OIDCCallback handles the callback from any OIDC provider.
func (h *Handler) OIDCCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	// Verify state from cookie
	stateCookie, err := r.Cookie("oidc_state")
	if err != nil || stateCookie.Value != state {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}

	// Exchange code for token
	token, err := h.OAuth2Config.Exchange(r.Context(), code)
	if err != nil {
		http.Error(w, "failed to exchange code", http.StatusBadRequest)
		return
	}

	// Get ID token
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		http.Error(w, "missing id_token", http.StatusBadRequest)
		return
	}

	// Verify ID token
	claims, err := h.OIDC.Verify(r.Context(), rawIDToken)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to verify token: %v", err), http.StatusBadRequest)
		return
	}

	// Upsert org and member.
	// google_domain is the canonical multi-tenancy key (UNIQUE in schema).
	// name is the display name and may be edited by admins later.
	emailDomain := auth.EmailDomain(claims.Email)
	if emailDomain == "" {
		http.Error(w, "invalid email", http.StatusBadRequest)
		return
	}

	// Run org provisioning + member upsert in a serializable transaction with
	// a row-level lock on the org so the "first user becomes admin" check is
	// atomic against concurrent first-logins from the same domain.
	tx, err := h.DB.Begin(r.Context())
	if err != nil {
		http.Error(w, "failed to begin tx", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(r.Context())

	var orgID string
	err = tx.QueryRow(r.Context(),
		`INSERT INTO orgs (name, google_domain) VALUES ($1, $2)
		 ON CONFLICT (google_domain) DO UPDATE SET google_domain = EXCLUDED.google_domain
		 RETURNING id`,
		emailDomain, emailDomain,
	).Scan(&orgID)
	if err != nil {
		http.Error(w, "failed to upsert org", http.StatusInternalServerError)
		return
	}

	// Lock the org row so concurrent first-logins serialize on COUNT+INSERT.
	_, err = tx.Exec(r.Context(), `SELECT 1 FROM orgs WHERE id = $1 FOR UPDATE`, orgID)
	if err != nil {
		http.Error(w, "failed to lock org", http.StatusInternalServerError)
		return
	}

	var memberCount int
	_ = tx.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM members WHERE org_id = $1`,
		orgID).Scan(&memberCount)
	defaultRole := "developer"
	if memberCount == 0 {
		defaultRole = "admin"
	}

	var memberID string
	err = tx.QueryRow(r.Context(),
		`INSERT INTO members (org_id, email, name, role, joined_at)
		 VALUES ($1, $2, $3, $4, now())
		 ON CONFLICT (org_id, email) DO UPDATE SET
		 	name = EXCLUDED.name,
		 	joined_at = COALESCE(members.joined_at, now())
		 RETURNING id`,
		orgID, claims.Email, claims.Name, defaultRole,
	).Scan(&memberID)
	if err != nil {
		http.Error(w, "failed to upsert member", http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		http.Error(w, "failed to commit tx", http.StatusInternalServerError)
		return
	}

	// Generate JWT
	jwtClaims := auth.Claims{
		Email: claims.Email,
		Name:  claims.Name,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   memberID,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
		},
	}

	token_, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwtClaims).SignedString(h.JWTSecret)
	if err != nil {
		http.Error(w, "failed to generate JWT", http.StatusInternalServerError)
		return
	}

	// Set cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "openserve_session",
		Value:    token_,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   86400, // 24 hours
	})

	// Redirect to dashboard or return success
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

// RequireJWT is middleware that requires a valid JWT.
// In dev mode (DevEmail set), it bypasses token validation and injects dev claims.
func (h *Handler) RequireJWT(next http.Handler) http.Handler {
	if h.DevEmail != "" {
		return auth.DevClaimsMiddleware(h.DevEmail, h.DevMemberID)(next)
	}
	return auth.JWTMiddleware(h.JWTSecret)(next)
}

// RequireRole is middleware that requires a specific role.
func (h *Handler) RequireRole(requiredRole string) func(http.Handler) http.Handler {
	// Higher number = more privileged. admin includes all lower roles.
	roleRank := map[string]int{
		"viewer": 1, "partner": 2, "developer": 3, "admin": 4,
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := auth.UserFromContext(r.Context())
			if claims == nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			var memberRole string
			err := h.DB.QueryRow(r.Context(),
				`SELECT role FROM members WHERE id = $1`,
				claims.Subject).Scan(&memberRole)
			if err != nil {
				http.Error(w, "member not found", http.StatusUnauthorized)
				return
			}
			if roleRank[memberRole] < roleRank[requiredRole] {
				http.Error(w, "forbidden: requires "+requiredRole+" role", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ListCatalog returns the model catalog.
func (h *Handler) ListCatalog(w http.ResponseWriter, r *http.Request) {
	models, err := h.Catalog.ListModels(r.Context())
	if err != nil {
		h.Log.Error("failed to list models", zap.Error(err))
		http.Error(w, "failed to list models", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, models)
}

// ListDeployments returns deployments for the org.
// Data is sourced from deployment_cache which is eventually consistent with the operator's CR status.
func (h *Handler) ListDeployments(w http.ResponseWriter, r *http.Request) {
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

	rows, _ := h.DB.Query(r.Context(),
		`SELECT id, model_ref, gpu_class, phase, endpoint, today_usd_spend, budget_paused_at, updated_at, web_search_enabled
		 FROM deployment_cache WHERE org_id = $1 ORDER BY updated_at DESC`,
		orgID,
	)
	defer rows.Close()

	var deployments []map[string]interface{}
	for rows.Next() {
		var id, modelRef, gpuClass, phase string
		var endpoint *string
		var spend float64
		var budgetPausedAt *time.Time
		var updatedAt time.Time
		var webSearchEnabled bool

		_ = rows.Scan(&id, &modelRef, &gpuClass, &phase, &endpoint, &spend, &budgetPausedAt, &updatedAt, &webSearchEnabled)
		deployments = append(deployments, map[string]interface{}{
			"id":                 id,
			"modelRef":           modelRef,
			"gpuClass":           gpuClass,
			"phase":              phase,
			"endpoint":           endpoint,
			"todayUsdSpend":      spend,
			"budgetPausedAt":     budgetPausedAt,
			"updatedAt":          updatedAt,
			"webSearchEnabled":   webSearchEnabled,
		})
	}

	if deployments == nil {
		deployments = []map[string]interface{}{}
	}

	writeJSON(w, http.StatusOK, deployments)
}

// CreateDeployment creates a new deployment.
func (h *Handler) CreateDeployment(w http.ResponseWriter, r *http.Request) {
	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		ModelRef          string   `json:"modelRef"`
		GPUClass          string   `json:"gpuClass"`
		ScaleToZero       bool     `json:"scaleToZero"`
		IdleTimeoutMin    int32    `json:"idleTimeoutMin"`
		MinReplicas       int32    `json:"minReplicas"`
		MaxReplicas       int32    `json:"maxReplicas"`
		DailyBudgetUSD    string   `json:"dailyBudgetUSD"`
		MaxInputTokens    int32    `json:"maxInputTokens"`
		MaxOutputTokens   int32    `json:"maxOutputTokens"`
		VLLMArgs          []string `json:"vllmArgs"`
		Description       string   `json:"description"`
		WebSearchEnabled  bool     `json:"webSearchEnabled"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	// Get org and email
	var orgID, email string
	err := h.DB.QueryRow(r.Context(),
		`SELECT org_id, email FROM members WHERE id = $1`,
		claims.Subject,
	).Scan(&orgID, &email)
	if err != nil {
		http.Error(w, "failed to get org", http.StatusInternalServerError)
		return
	}

	// Generate deployment name: slugify(modelRef) + "-" + random4hex()
	slug := slugify(req.ModelRef)
	deploymentID := slug + "-" + random4hex()
	// Ensure it doesn't exceed 63 chars (Kubernetes limit)
	if len(deploymentID) > 63 {
		deploymentID = deploymentID[:63]
	}

	// Create the ModelDeployment CR
	mdReq := k8s.CreateDeploymentRequest{
		Name:            deploymentID,
		ModelRef:        req.ModelRef,
		GPUClass:        req.GPUClass,
		ScaleToZero:     req.ScaleToZero,
		IdleTimeoutMin:  req.IdleTimeoutMin,
		MinReplicas:     req.MinReplicas,
		MaxReplicas:     req.MaxReplicas,
		DailyBudgetUSD:  req.DailyBudgetUSD,
		MaxInputTokens:  req.MaxInputTokens,
		MaxOutputTokens: req.MaxOutputTokens,
		VLLMArgs:        req.VLLMArgs,
		Description:     req.Description,
		OrgID:           orgID,
		CreatedByEmail:  email,
	}

	md, err := h.K8sClient.CreateModelDeployment(r.Context(), mdReq)
	if err != nil {
		h.Log.Error("failed to create deployment", zap.Error(err))
		http.Error(w, "failed to create deployment", http.StatusInternalServerError)
		return
	}

	// Insert into cache
	if _, err := h.DB.Exec(r.Context(),
		`INSERT INTO deployment_cache (id, org_id, model_ref, gpu_class, phase, web_search_enabled)
		 VALUES ($1, $2, $3, $4, 'Pending', $5)`,
		deploymentID, orgID, req.ModelRef, req.GPUClass, req.WebSearchEnabled,
	); err != nil {
		h.Log.Warn("cache update failed", zap.Error(err))
	}

	h.writeAuditLog(r.Context(), orgID, "create", "deployment", deploymentID, req)

	// Build endpoint URL (will be updated by operator when deployment is Running)
	response := map[string]interface{}{
		"id":         deploymentID,
		"modelRef":   req.ModelRef,
		"gpuClass":   req.GPUClass,
		"phase":      md.Status.Phase,
		"endpoint":   nil,
		"createdAt":  time.Now().UTC(),
	}

	writeJSON(w, http.StatusCreated, response)
}

// GetDeployment returns a specific deployment.
func (h *Handler) GetDeployment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var orgID, modelRef, gpuClass, phase string
	var endpoint *string
	var spend float64
	var budgetPausedAt *time.Time
	var updatedAt time.Time
	var webSearchEnabled bool

	err := h.DB.QueryRow(r.Context(),
		`SELECT org_id, model_ref, gpu_class, phase, endpoint, today_usd_spend, budget_paused_at, updated_at, web_search_enabled
		 FROM deployment_cache WHERE id = $1`,
		id,
	).Scan(&orgID, &modelRef, &gpuClass, &phase, &endpoint, &spend, &budgetPausedAt, &updatedAt, &webSearchEnabled)

	if err == pgx.ErrNoRows {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":                 id,
		"modelRef":           modelRef,
		"gpuClass":           gpuClass,
		"phase":              phase,
		"endpoint":           endpoint,
		"todayUsdSpend":      spend,
		"budgetPausedAt":     budgetPausedAt,
		"updatedAt":          updatedAt,
		"webSearchEnabled":   webSearchEnabled,
	})
}

// DeleteDeployment deletes a deployment.
func (h *Handler) DeleteDeployment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Get org
	var orgID string
	err := h.DB.QueryRow(r.Context(),
		`SELECT org_id FROM deployment_cache WHERE id = $1`,
		id,
	).Scan(&orgID)
	if err != nil {
		http.Error(w, "deployment not found", http.StatusNotFound)
		return
	}

	// Delete the ModelDeployment CR
	err = h.K8sClient.DeleteModelDeployment(r.Context(), id)
	if err != nil {
		h.Log.Error("failed to delete deployment CR", zap.Error(err))
		http.Error(w, "failed to delete deployment", http.StatusInternalServerError)
		return
	}

	// Delete from cache
	if _, err := h.DB.Exec(r.Context(),
		`DELETE FROM deployment_cache WHERE id = $1`,
		id,
	); err != nil {
		h.Log.Warn("cache update failed", zap.Error(err))
	}

	h.writeAuditLog(r.Context(), orgID, "delete", "deployment", id, nil)

	w.WriteHeader(http.StatusNoContent)
}

// ResumeDeployment resumes a paused deployment.
func (h *Handler) ResumeDeployment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var orgID string
	err := h.DB.QueryRow(r.Context(),
		`SELECT org_id FROM deployment_cache WHERE id = $1`,
		id,
	).Scan(&orgID)
	if err != nil {
		http.Error(w, "deployment not found", http.StatusNotFound)
		return
	}

	// Call the Kubernetes API to resume
	err = h.K8sClient.ResumeModelDeployment(r.Context(), id)
	if err != nil {
		h.Log.Error("failed to resume deployment", zap.Error(err))
		http.Error(w, "failed to resume deployment", http.StatusInternalServerError)
		return
	}

	// Update cache to reflect pending state
	if _, err := h.DB.Exec(r.Context(),
		`UPDATE deployment_cache SET phase = 'Pending', budget_paused_at = NULL WHERE id = $1`,
		id,
	); err != nil {
		h.Log.Warn("cache update failed", zap.Error(err))
	}

	h.writeAuditLog(r.Context(), orgID, "resume", "deployment", id, nil)

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "resumed",
	})
}

// ListAPIKeys returns API keys for the org.
func (h *Handler) ListAPIKeys(w http.ResponseWriter, r *http.Request) {
	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var orgID string
	_ = h.DB.QueryRow(r.Context(),
		`SELECT org_id FROM members WHERE id = $1`,
		claims.Subject,
	).Scan(&orgID)

	rows, _ := h.DB.Query(r.Context(),
		`SELECT id, display_name, key_prefix, role, rpm, tpm, expires_at, active, created_at, last_used_at
		 FROM api_keys WHERE org_id = $1 AND active = true`,
		orgID,
	)
	defer rows.Close()

	var keys []map[string]interface{}
	for rows.Next() {
		var id, displayName, keyPrefix, role string
		var rpm, tpm int
		var expiresAt *time.Time
		var active bool
		var createdAt, lastUsedAt *time.Time

		_ = rows.Scan(&id, &displayName, &keyPrefix, &role, &rpm, &tpm, &expiresAt, &active, &createdAt, &lastUsedAt)
		keys = append(keys, map[string]interface{}{
			"id":           id,
			"displayName":  displayName,
			"keyPrefix":    keyPrefix,
			"role":         role,
			"rpm":          rpm,
			"tpm":          tpm,
			"expiresAt":    expiresAt,
			"active":       active,
			"createdAt":    createdAt,
			"lastUsedAt":   lastUsedAt,
		})
	}

	writeJSON(w, http.StatusOK, keys)
}

// CreateAPIKey creates a new API key.
func (h *Handler) CreateAPIKey(w http.ResponseWriter, r *http.Request) {
	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		DisplayName string `json:"displayName"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	// Get org and member ID
	var orgID, memberID string
	err := h.DB.QueryRow(r.Context(),
		`SELECT org_id, id FROM members WHERE id = $1`,
		claims.Subject,
	).Scan(&orgID, &memberID)
	if err != nil {
		http.Error(w, "failed to get org", http.StatusInternalServerError)
		return
	}

	// Generate raw key and hash
	rawKey, keyHash, err := keygen.Generate()
	if err != nil {
		h.Log.Error("failed to generate API key", zap.Error(err))
		http.Error(w, "failed to generate API key", http.StatusInternalServerError)
		return
	}

	keyPrefix := keygen.Prefix(rawKey)

	// Insert into DB
	var keyID string
	err = h.DB.QueryRow(r.Context(),
		`INSERT INTO api_keys (org_id, created_by, display_name, key_prefix, key_hash)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		orgID, memberID, req.DisplayName, keyPrefix, keyHash,
	).Scan(&keyID)
	if err != nil {
		h.Log.Error("failed to insert API key", zap.Error(err))
		http.Error(w, "failed to create API key", http.StatusInternalServerError)
		return
	}

	h.writeAuditLog(r.Context(), orgID, "create_api_key", "api_key", keyID, map[string]string{
		"displayName": req.DisplayName,
	})

	writeJSON(w, http.StatusCreated, map[string]string{
		"key": rawKey,
		"id":  keyID,
	})
}

// DeleteAPIKey deletes an API key.
func (h *Handler) DeleteAPIKey(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var orgID string
	_ = h.DB.QueryRow(r.Context(),
		`SELECT org_id FROM api_keys WHERE id = $1`,
		id,
	).Scan(&orgID)

	if _, err := h.DB.Exec(r.Context(),
		`UPDATE api_keys SET active = false WHERE id = $1`,
		id,
	); err != nil {
		h.Log.Warn("cache update failed", zap.Error(err))
	}

	h.writeAuditLog(r.Context(), orgID, "delete_api_key", "api_key", id, nil)

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "deleted",
	})
}

// RotateAPIKey rotates an API key.
func (h *Handler) RotateAPIKey(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var orgID string
	err := h.DB.QueryRow(r.Context(),
		`SELECT org_id FROM api_keys WHERE id = $1`,
		id,
	).Scan(&orgID)
	if err != nil {
		http.Error(w, "api key not found", http.StatusNotFound)
		return
	}

	// Generate new raw key and hash
	rawKey, keyHash, err := keygen.Generate()
	if err != nil {
		h.Log.Error("failed to generate API key", zap.Error(err))
		http.Error(w, "failed to rotate API key", http.StatusInternalServerError)
		return
	}

	keyPrefix := keygen.Prefix(rawKey)

	// Update DB
	_, err = h.DB.Exec(r.Context(),
		`UPDATE api_keys SET key_prefix = $1, key_hash = $2 WHERE id = $3`,
		keyPrefix, keyHash, id,
	)
	if err != nil {
		h.Log.Error("failed to update API key", zap.Error(err))
		http.Error(w, "failed to rotate API key", http.StatusInternalServerError)
		return
	}

	h.writeAuditLog(r.Context(), orgID, "rotate_api_key", "api_key", id, nil)

	writeJSON(w, http.StatusOK, map[string]string{
		"key": rawKey,
	})
}

// GetUsage returns usage statistics for the org.
// GetUsage returns real inference usage statistics for the org.
func (h *Handler) GetUsage(w http.ResponseWriter, r *http.Request) {
	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var orgID string
	if err := h.DB.QueryRow(r.Context(),
		`SELECT org_id FROM members WHERE id = $1`,
		claims.Subject,
	).Scan(&orgID); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"dailyRequests":     []interface{}{},
			"totalRequests":     0,
			"totalThisMonth":    0,
			"totalInputTokens":  0,
			"totalOutputTokens": 0,
		})
		return
	}

	// Daily request counts + token sums for the last 30 days.
	rows, err := h.DB.Query(r.Context(),
		`SELECT DATE(created_at) AS day,
		        COUNT(*)              AS requests,
		        COALESCE(SUM(input_tokens), 0)  AS input_tokens,
		        COALESCE(SUM(output_tokens), 0) AS output_tokens
		 FROM inference_events
		 WHERE org_id = $1 AND created_at >= NOW() - INTERVAL '30 days'
		 GROUP BY day
		 ORDER BY day ASC`,
		orgID,
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()

	type DayStat struct {
		Date         string `json:"date"`
		Requests     int64  `json:"requests"`
		InputTokens  int64  `json:"inputTokens"`
		OutputTokens int64  `json:"outputTokens"`
	}
	var daily []DayStat
	var totalRequests, totalInput, totalOutput int64
	for rows.Next() {
		var day time.Time
		var reqs, inp, out int64
		if err := rows.Scan(&day, &reqs, &inp, &out); err != nil {
			continue
		}
		daily = append(daily, DayStat{
			Date:         day.Format("2006-01-02"),
			Requests:     reqs,
			InputTokens:  inp,
			OutputTokens: out,
		})
		totalRequests += reqs
		totalInput += inp
		totalOutput += out
	}
	if daily == nil {
		daily = []DayStat{}
	}

	// This-month totals.
	var thisMonthRequests int64
	_ = h.DB.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM inference_events
		 WHERE org_id = $1
		   AND DATE_TRUNC('month', created_at) = DATE_TRUNC('month', NOW())`,
		orgID,
	).Scan(&thisMonthRequests)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"dailyRequests":     daily,
		"totalRequests":     totalRequests,
		"totalThisMonth":    thisMonthRequests,
		"totalInputTokens":  totalInput,
		"totalOutputTokens": totalOutput,
	})
}

// GetPeerAgentInstallScript serves the peer-agent installation script.
// GET /peer-agent/install.sh
func (h *Handler) GetPeerAgentInstallScript(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if host == "" {
		host = "localhost:8080"
	}
	scheme := "https"
	if strings.HasPrefix(host, "localhost") || strings.HasPrefix(host, "127.0.0.1") {
		scheme = "http"
	}
	baseURL := scheme + "://" + host

	script := `#!/usr/bin/env sh
set -e

TOKEN=""
RELAY=""

for arg in "$@"; do
  case "$arg" in
    --token=*) TOKEN="${arg#*=}" ;;
    --relay=*) RELAY="${arg#*=}" ;;
  esac
done

if [ -z "$TOKEN" ] || [ -z "$RELAY" ]; then
  echo "Usage: install.sh --token=<token> --relay=<relay-url>" >&2
  exit 1
fi

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH" >&2; exit 1 ;;
esac

BINARY_URL="` + baseURL + `/peer-agent/download/$OS/$ARCH"
CHECKSUM_URL="` + baseURL + `/peer-agent/checksums.txt"
INSTALL_DIR="/usr/local/bin"
BINARY_NAME="openserve-peer"

echo "Downloading openserve-peer ($OS/$ARCH)..."
curl -fsSL -o "/tmp/$BINARY_NAME" "$BINARY_URL"
chmod +x "/tmp/$BINARY_NAME"

echo "Installing to $INSTALL_DIR/$BINARY_NAME..."
if [ -w "$INSTALL_DIR" ]; then
  mv "/tmp/$BINARY_NAME" "$INSTALL_DIR/$BINARY_NAME"
else
  sudo mv "/tmp/$BINARY_NAME" "$INSTALL_DIR/$BINARY_NAME"
fi

echo "Starting openserve-peer..."
exec "$INSTALL_DIR/$BINARY_NAME" --token="$TOKEN" --relay="$RELAY"
`

	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Content-Disposition", "inline; filename=install.sh")
	_, _ = fmt.Fprint(w, script)
}

// GetPeerAgentBinary serves a placeholder for the peer agent binary download.
// In production, real binaries are embedded via go:embed or served from GCS.
// GET /peer-agent/download/{os}/{arch}
func (h *Handler) GetPeerAgentBinary(w http.ResponseWriter, r *http.Request) {
	osName := chi.URLParam(r, "os")
	archName := chi.URLParam(r, "arch")

	// In local dev: return a helpful error. In production, this serves real binaries.
	writeJSON(w, http.StatusNotImplemented, map[string]string{
		"message": fmt.Sprintf("Binary download for %s/%s not yet available in this build. Build the peer-agent locally: cd apps/peer-agent && go build -o openserve-peer ./cmd/", osName, archName),
		"source":  "https://github.com/openserve/openserve",
	})
}

// GetAuditLog returns audit logs for the org.
func (h *Handler) GetAuditLog(w http.ResponseWriter, r *http.Request) {
	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var orgID string
	_ = h.DB.QueryRow(r.Context(),
		`SELECT org_id FROM members WHERE id = $1`,
		claims.Subject,
	).Scan(&orgID)

	// Parse query params
	limitStr := r.URL.Query().Get("limit")
	if limitStr == "" {
		limitStr = "50"
	}
	limit, _ := strconv.Atoi(limitStr)

	beforeStr := r.URL.Query().Get("before")

	const limitVal = 50
	query := `SELECT id, action, resource_type, resource_id, details, ip_address, created_at
	          FROM audit_log WHERE org_id = $1`
	var args []interface{}
	args = append(args, orgID)

	if beforeStr != "" {
		query += ` AND id < $2`
		args = append(args, beforeStr)
	}

	query += ` ORDER BY created_at DESC LIMIT $` + strconv.Itoa(len(args)+1)
	args = append(args, limit)

	rows, _ := h.DB.Query(r.Context(), query, args...)
	defer rows.Close()

	var logs []map[string]interface{}
	for rows.Next() {
		var id int64
		var action, resourceType, resourceID string
		var details []byte
		var ipAddress *string
		var createdAt time.Time

		_ = rows.Scan(&id, &action, &resourceType, &resourceID, &details, &ipAddress, &createdAt)
		logs = append(logs, map[string]interface{}{
			"id":           id,
			"action":       action,
			"resourceType": resourceType,
			"resourceId":   resourceID,
			"details":      json.RawMessage(details),
			"ipAddress":    ipAddress,
			"createdAt":    createdAt,
		})
	}

	writeJSON(w, http.StatusOK, logs)
}

// ListMembers returns members for the org.
func (h *Handler) ListMembers(w http.ResponseWriter, r *http.Request) {
	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var orgID string
	_ = h.DB.QueryRow(r.Context(),
		`SELECT org_id FROM members WHERE id = $1`,
		claims.Subject,
	).Scan(&orgID)

	rows, _ := h.DB.Query(r.Context(),
		`SELECT id, email, name, role, joined_at, created_at
		 FROM members WHERE org_id = $1`,
		orgID,
	)
	defer rows.Close()

	var members []map[string]interface{}
	for rows.Next() {
		var id, email, name, role string
		var joinedAt *time.Time
		var createdAt time.Time

		_ = rows.Scan(&id, &email, &name, &role, &joinedAt, &createdAt)
		members = append(members, map[string]interface{}{
			"id":        id,
			"email":     email,
			"name":      name,
			"role":      role,
			"joinedAt":  joinedAt,
			"createdAt": createdAt,
		})
	}

	writeJSON(w, http.StatusOK, members)
}

// GetSettings returns the org's current settings.
func (h *Handler) GetSettings(w http.ResponseWriter, r *http.Request) {
	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var orgID string
	_ = h.DB.QueryRow(r.Context(),
		`SELECT org_id FROM members WHERE id = $1`,
		claims.Subject,
	).Scan(&orgID)

	var name, googleDomain string
	var createdAt time.Time
	err := h.DB.QueryRow(r.Context(),
		`SELECT name, google_domain, created_at FROM orgs WHERE id = $1`,
		orgID,
	).Scan(&name, &googleDomain, &createdAt)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "org not found"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"orgId":        orgID,
		"name":         name,
		"googleDomain": googleDomain,
		"createdAt":    createdAt,
	})
}

// UpdateSettings allows updating the org name.
func (h *Handler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	var orgID string
	_ = h.DB.QueryRow(r.Context(),
		`SELECT org_id FROM members WHERE id = $1`,
		claims.Subject,
	).Scan(&orgID)

	_, err := h.DB.Exec(r.Context(),
		`UPDATE orgs SET name = $1 WHERE id = $2`,
		req.Name, orgID,
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	h.writeAuditLog(r.Context(), orgID, "update_settings", "org", orgID, map[string]string{"name": req.Name})
	w.WriteHeader(http.StatusNoContent)
}

// InviteMember invites a new member.
func (h *Handler) InviteMember(w http.ResponseWriter, r *http.Request) {
	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		Email string `json:"email"`
		Name  string `json:"name"`
		Role  string `json:"role"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	validRoles := map[string]bool{"admin": true, "developer": true, "partner": true, "viewer": true}
	if !validRoles[req.Role] {
		http.Error(w, "role must be one of: admin, developer, partner, viewer", http.StatusBadRequest)
		return
	}
	if req.Email == "" || req.Name == "" {
		http.Error(w, "email and name are required", http.StatusBadRequest)
		return
	}

	var orgID string
	_ = h.DB.QueryRow(r.Context(),
		`SELECT org_id FROM members WHERE id = $1`,
		claims.Subject,
	).Scan(&orgID)

	var newMemberID string
	err := h.DB.QueryRow(r.Context(),
		`INSERT INTO members (org_id, email, name, role, joined_at)
		 VALUES ($1, $2, $3, $4, now())
		 ON CONFLICT (org_id, email) DO UPDATE SET role = $4
		 RETURNING id`,
		orgID, req.Email, req.Name, req.Role,
	).Scan(&newMemberID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	h.writeAuditLog(r.Context(), orgID, "invite_member", "member", newMemberID, map[string]string{
		"email": req.Email,
		"role":  req.Role,
	})

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"id":    newMemberID,
		"email": req.Email,
	})
}

// RemoveMember removes a member.
func (h *Handler) RemoveMember(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var orgID string
	_ = h.DB.QueryRow(r.Context(),
		`SELECT org_id FROM members WHERE id = $1`,
		id,
	).Scan(&orgID)

	var callerRole string
	_ = h.DB.QueryRow(r.Context(),
		`SELECT role FROM members WHERE id = $1`,
		claims.Subject,
	).Scan(&callerRole)
	if callerRole != "admin" {
		http.Error(w, "forbidden: admin role required", http.StatusForbidden)
		return
	}

	if _, err := h.DB.Exec(r.Context(),
		`DELETE FROM members WHERE id = $1`,
		id,
	); err != nil {
		h.Log.Warn("cache update failed", zap.Error(err))
	}

	h.writeAuditLog(r.Context(), orgID, "remove_member", "member", id, nil)

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "removed",
	})
}

// ListWebhooks returns all webhooks for the org.
func (h *Handler) ListWebhooks(w http.ResponseWriter, r *http.Request) {
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
		`SELECT id, url, events, enabled, created_at FROM webhooks WHERE org_id = $1 ORDER BY created_at DESC`,
		orgID)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type webhook struct {
		ID        string   `json:"id"`
		URL       string   `json:"url"`
		Events    []string `json:"events"`
		Enabled   bool     `json:"enabled"`
		CreatedAt string   `json:"createdAt"`
	}
	var result []webhook
	for rows.Next() {
		var wh webhook
		var createdAt time.Time
		if err := rows.Scan(&wh.ID, &wh.URL, &wh.Events, &wh.Enabled, &createdAt); err != nil {
			continue
		}
		wh.CreatedAt = createdAt.Format(time.RFC3339)
		result = append(result, wh)
	}
	if result == nil {
		result = []webhook{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// CreateWebhook registers a new webhook.
func (h *Handler) CreateWebhook(w http.ResponseWriter, r *http.Request) {
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

	var req struct {
		URL    string   `json:"url"`
		Events []string `json:"events"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}

	parsedURL, err := url.Parse(req.URL)
	if err != nil || (parsedURL.Scheme != "https" && parsedURL.Scheme != "http") {
		http.Error(w, "webhook URL must be http or https", http.StatusBadRequest)
		return
	}

	secret := make([]byte, 32)
	rand.Read(secret)
	secretHex := hex.EncodeToString(secret)

	var id string
	err = h.DB.QueryRow(r.Context(),
		`INSERT INTO webhooks (org_id, url, secret, events) VALUES ($1, $2, $3, $4) RETURNING id`,
		orgID, req.URL, secretHex, req.Events).Scan(&id)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"id": id})
}

// DeleteWebhook removes a webhook.
func (h *Handler) DeleteWebhook(w http.ResponseWriter, r *http.Request) {
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

	id := chi.URLParam(r, "id")
	_, err = h.DB.Exec(r.Context(),
		`DELETE FROM webhooks WHERE id = $1 AND org_id = $2`, id, orgID)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// SearchWeb calls the Brave Search API and returns formatted snippets for context injection.
// POST /api/v1/search   body: {"query":"...","count":5}
func (h *Handler) SearchWeb(w http.ResponseWriter, r *http.Request) {
	if h.BraveAPIKey == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "web search not configured — set BRAVE_API_KEY",
		})
		return
	}

	var req struct {
		Query string `json:"query"`
		Count int    `json:"count"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Query == "" {
		http.Error(w, "body must be JSON with a non-empty 'query' field", http.StatusBadRequest)
		return
	}
	if req.Count <= 0 || req.Count > 10 {
		req.Count = 5
	}

	braveURL := fmt.Sprintf("https://api.search.brave.com/res/v1/web/search?q=%s&count=%d",
		url.QueryEscape(req.Query), req.Count)

	braveReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, braveURL, nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to build search request"})
		return
	}
	braveReq.Header.Set("Accept", "application/json")
	braveReq.Header.Set("Accept-Encoding", "gzip")
	braveReq.Header.Set("X-Subscription-Token", h.BraveAPIKey)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(braveReq)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "search request failed: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": fmt.Sprintf("Brave Search returned %d", resp.StatusCode),
		})
		return
	}

	var braveResp struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}

	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &braveResp); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to parse search response"})
		return
	}

	type SearchResult struct {
		Title       string `json:"title"`
		URL         string `json:"url"`
		Description string `json:"description"`
	}
	results := make([]SearchResult, 0, len(braveResp.Web.Results))
	for _, r := range braveResp.Web.Results {
		results = append(results, SearchResult{
			Title:       r.Title,
			URL:         r.URL,
			Description: r.Description,
		})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"query":   req.Query,
		"results": results,
	})
}

// ListConversations returns conversations for the member in a deployment.
// GET /api/v1/conversations?deploymentId=xxx
func (h *Handler) ListConversations(w http.ResponseWriter, r *http.Request) {
	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	deploymentId := r.URL.Query().Get("deploymentId")
	if deploymentId == "" {
		http.Error(w, "deploymentId query parameter required", http.StatusBadRequest)
		return
	}

	rows, err := h.DB.Query(r.Context(),
		`SELECT id, deployment_id, title, created_at, updated_at FROM conversations
		 WHERE member_id = $1 AND deployment_id = $2
		 ORDER BY updated_at DESC LIMIT 50`,
		claims.Subject, deploymentId,
	)
	if err != nil {
		h.Log.Error("failed to list conversations", zap.Error(err))
		http.Error(w, "failed to list conversations", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var conversations []map[string]interface{}
	for rows.Next() {
		var id, deploymentId, title string
		var createdAt, updatedAt time.Time

		if err := rows.Scan(&id, &deploymentId, &title, &createdAt, &updatedAt); err != nil {
			h.Log.Error("failed to scan conversation", zap.Error(err))
			continue
		}

		conversations = append(conversations, map[string]interface{}{
			"id":           id,
			"deploymentId": deploymentId,
			"title":        title,
			"createdAt":    createdAt,
			"updatedAt":    updatedAt,
		})
	}

	if conversations == nil {
		conversations = []map[string]interface{}{}
	}

	writeJSON(w, http.StatusOK, conversations)
}

// CreateConversation creates a new conversation.
// POST /api/v1/conversations with body: { deploymentId, title? }
func (h *Handler) CreateConversation(w http.ResponseWriter, r *http.Request) {
	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		DeploymentId string `json:"deploymentId"`
		Title        string `json:"title"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.DeploymentId == "" {
		http.Error(w, "deploymentId required", http.StatusBadRequest)
		return
	}

	title := req.Title
	if title == "" {
		title = "New conversation"
	}

	// Get org_id from member
	var orgID string
	err := h.DB.QueryRow(r.Context(),
		`SELECT org_id FROM members WHERE id = $1`,
		claims.Subject,
	).Scan(&orgID)
	if err != nil {
		http.Error(w, "failed to get org", http.StatusInternalServerError)
		return
	}

	var id, deploymentId, createdAt, updatedAt string
	err = h.DB.QueryRow(r.Context(),
		`INSERT INTO conversations (org_id, member_id, deployment_id, title)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, deployment_id, created_at, updated_at`,
		orgID, claims.Subject, req.DeploymentId, title,
	).Scan(&id, &deploymentId, &createdAt, &updatedAt)
	if err != nil {
		h.Log.Error("failed to create conversation", zap.Error(err))
		http.Error(w, "failed to create conversation", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":           id,
		"deploymentId": deploymentId,
		"title":        title,
		"createdAt":    createdAt,
		"updatedAt":    updatedAt,
	})
}

// GetConversation returns a conversation with all its messages.
// GET /api/v1/conversations/:id
func (h *Handler) GetConversation(w http.ResponseWriter, r *http.Request) {
	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conversationId := chi.URLParam(r, "id")

	// Fetch conversation and verify ownership
	var id, deploymentId, title, createdAt, updatedAt string
	err := h.DB.QueryRow(r.Context(),
		`SELECT id, deployment_id, title, created_at, updated_at FROM conversations
		 WHERE id = $1 AND member_id = $2`,
		conversationId, claims.Subject,
	).Scan(&id, &deploymentId, &title, &createdAt, &updatedAt)
	if err == pgx.ErrNoRows {
		http.Error(w, "conversation not found", http.StatusNotFound)
		return
	}
	if err != nil {
		h.Log.Error("failed to get conversation", zap.Error(err))
		http.Error(w, "failed to get conversation", http.StatusInternalServerError)
		return
	}

	// Fetch messages
	rows, err := h.DB.Query(r.Context(),
		`SELECT id, conversation_id, role, content, created_at FROM conversation_messages
		 WHERE conversation_id = $1
		 ORDER BY created_at ASC`,
		conversationId,
	)
	if err != nil {
		h.Log.Error("failed to get messages", zap.Error(err))
		http.Error(w, "failed to get messages", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var messages []map[string]interface{}
	for rows.Next() {
		var id, conversationId, role, content, createdAt string
		if err := rows.Scan(&id, &conversationId, &role, &content, &createdAt); err != nil {
			h.Log.Error("failed to scan message", zap.Error(err))
			continue
		}
		messages = append(messages, map[string]interface{}{
			"id":             id,
			"conversationId": conversationId,
			"role":           role,
			"content":        content,
			"createdAt":      createdAt,
		})
	}

	if messages == nil {
		messages = []map[string]interface{}{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":           id,
		"deploymentId": deploymentId,
		"title":        title,
		"createdAt":    createdAt,
		"updatedAt":    updatedAt,
		"messages":     messages,
	})
}

// AppendMessage appends a message to a conversation.
// POST /api/v1/conversations/:id/messages with body: { role, content }
func (h *Handler) AppendMessage(w http.ResponseWriter, r *http.Request) {
	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conversationId := chi.URLParam(r, "id")

	var req struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Role == "" || req.Content == "" {
		http.Error(w, "role and content required", http.StatusBadRequest)
		return
	}

	// Verify conversation ownership
	var conversationExists string
	err := h.DB.QueryRow(r.Context(),
		`SELECT id FROM conversations WHERE id = $1 AND member_id = $2`,
		conversationId, claims.Subject,
	).Scan(&conversationExists)
	if err == pgx.ErrNoRows {
		http.Error(w, "conversation not found", http.StatusNotFound)
		return
	}
	if err != nil {
		h.Log.Error("failed to verify conversation", zap.Error(err))
		http.Error(w, "failed to verify conversation", http.StatusInternalServerError)
		return
	}

	// Insert message and update conversation updated_at
	var msgId, createdAt string
	err = h.DB.QueryRow(r.Context(),
		`WITH inserted_message AS (
			INSERT INTO conversation_messages (conversation_id, role, content)
			VALUES ($1, $2, $3)
			RETURNING id, created_at
		)
		SELECT im.id, im.created_at FROM inserted_message im`,
		conversationId, req.Role, req.Content,
	).Scan(&msgId, &createdAt)
	if err != nil {
		h.Log.Error("failed to append message", zap.Error(err))
		http.Error(w, "failed to append message", http.StatusInternalServerError)
		return
	}

	// Update conversation updated_at
	_, err = h.DB.Exec(r.Context(),
		`UPDATE conversations SET updated_at = now() WHERE id = $1`,
		conversationId,
	)
	if err != nil {
		h.Log.Error("failed to update conversation timestamp", zap.Error(err))
		// Don't fail the request, message was created
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":             msgId,
		"conversationId": conversationId,
		"role":           req.Role,
		"content":        req.Content,
		"createdAt":      createdAt,
	})
}

// DeleteConversation deletes a conversation.
// DELETE /api/v1/conversations/:id
func (h *Handler) DeleteConversation(w http.ResponseWriter, r *http.Request) {
	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conversationId := chi.URLParam(r, "id")

	// Verify ownership and delete
	result, err := h.DB.Exec(r.Context(),
		`DELETE FROM conversations WHERE id = $1 AND member_id = $2`,
		conversationId, claims.Subject,
	)
	if err != nil {
		h.Log.Error("failed to delete conversation", zap.Error(err))
		http.Error(w, "failed to delete conversation", http.StatusInternalServerError)
		return
	}

	if result.RowsAffected() == 0 {
		http.Error(w, "conversation not found", http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
}

// ListPromptTemplates lists all prompt templates for the org.
// GET /api/v1/prompt-templates
func (h *Handler) ListPromptTemplates(w http.ResponseWriter, r *http.Request) {
	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var orgId string
	err := h.DB.QueryRow(r.Context(),
		`SELECT org_id FROM members WHERE id = $1`,
		claims.Subject,
	).Scan(&orgId)
	if err != nil {
		h.Log.Error("failed to get org from member", zap.Error(err))
		http.Error(w, "failed to get org", http.StatusInternalServerError)
		return
	}

	rows, err := h.DB.Query(r.Context(),
		`SELECT id, name, description, content, created_by, created_at
		 FROM prompt_templates
		 WHERE org_id = $1
		 ORDER BY created_at DESC`,
		orgId,
	)
	if err != nil {
		h.Log.Error("failed to query prompt templates", zap.Error(err))
		http.Error(w, "failed to list templates", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var templates []map[string]interface{}
	for rows.Next() {
		var id, name, description, content string
		var createdBy *string
		var createdAt time.Time

		err := rows.Scan(&id, &name, &description, &content, &createdBy, &createdAt)
		if err != nil {
			h.Log.Error("failed to scan prompt template", zap.Error(err))
			http.Error(w, "failed to list templates", http.StatusInternalServerError)
			return
		}

		templates = append(templates, map[string]interface{}{
			"id":          id,
			"name":        name,
			"description": description,
			"content":     content,
			"createdBy":   createdBy,
			"createdAt":   createdAt,
		})
	}

	if templates == nil {
		templates = []map[string]interface{}{}
	}

	writeJSON(w, http.StatusOK, templates)
}

// CreatePromptTemplate creates a new prompt template for the org.
// POST /api/v1/prompt-templates
func (h *Handler) CreatePromptTemplate(w http.ResponseWriter, r *http.Request) {
	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var orgId string
	err := h.DB.QueryRow(r.Context(),
		`SELECT org_id FROM members WHERE id = $1`,
		claims.Subject,
	).Scan(&orgId)
	if err != nil {
		h.Log.Error("failed to get org from member", zap.Error(err))
		http.Error(w, "failed to get org", http.StatusInternalServerError)
		return
	}

	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Content     string `json:"content"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Name == "" || req.Content == "" {
		http.Error(w, "name and content are required", http.StatusBadRequest)
		return
	}

	var templateId, createdAt string
	err = h.DB.QueryRow(r.Context(),
		`INSERT INTO prompt_templates (org_id, name, description, content, created_by)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, created_at`,
		orgId, req.Name, req.Description, req.Content, claims.Subject,
	).Scan(&templateId, &createdAt)
	if err != nil {
		if strings.Contains(err.Error(), "unique constraint") {
			http.Error(w, "a template with this name already exists", http.StatusConflict)
			return
		}
		h.Log.Error("failed to create prompt template", zap.Error(err))
		http.Error(w, "failed to create template", http.StatusInternalServerError)
		return
	}

	go h.writeAuditLog(r.Context(), orgId, "create", "prompt_template", templateId, map[string]string{"name": req.Name})

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"id":          templateId,
		"name":        req.Name,
		"description": req.Description,
		"content":     req.Content,
		"createdAt":   createdAt,
	})
}

// UpdatePromptTemplate updates a prompt template.
// PUT /api/v1/prompt-templates/:id
func (h *Handler) UpdatePromptTemplate(w http.ResponseWriter, r *http.Request) {
	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var orgId string
	err := h.DB.QueryRow(r.Context(),
		`SELECT org_id FROM members WHERE id = $1`,
		claims.Subject,
	).Scan(&orgId)
	if err != nil {
		h.Log.Error("failed to get org from member", zap.Error(err))
		http.Error(w, "failed to get org", http.StatusInternalServerError)
		return
	}

	templateId := chi.URLParam(r, "id")

	var req struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		Content     *string `json:"content"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Verify ownership first
	var exists bool
	err = h.DB.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM prompt_templates WHERE id = $1 AND org_id = $2)`,
		templateId, orgId,
	).Scan(&exists)
	if err != nil {
		h.Log.Error("failed to check template ownership", zap.Error(err))
		http.Error(w, "failed to update template", http.StatusInternalServerError)
		return
	}

	if !exists {
		http.Error(w, "template not found", http.StatusNotFound)
		return
	}

	// Update only non-nil fields
	updates := []string{}
	params := []interface{}{}
	paramIdx := 1

	if req.Name != nil {
		updates = append(updates, fmt.Sprintf("name = $%d", paramIdx))
		params = append(params, *req.Name)
		paramIdx++
	}

	if req.Description != nil {
		updates = append(updates, fmt.Sprintf("description = $%d", paramIdx))
		params = append(params, *req.Description)
		paramIdx++
	}

	if req.Content != nil {
		updates = append(updates, fmt.Sprintf("content = $%d", paramIdx))
		params = append(params, *req.Content)
		paramIdx++
	}

	if len(updates) == 0 {
		http.Error(w, "no fields to update", http.StatusBadRequest)
		return
	}

	updates = append(updates, fmt.Sprintf("updated_at = now()"))
	params = append(params, templateId)

	query := fmt.Sprintf(
		`UPDATE prompt_templates SET %s WHERE id = $%d`,
		strings.Join(updates, ", "),
		paramIdx,
	)

	_, err = h.DB.Exec(r.Context(), query, params...)
	if err != nil {
		if strings.Contains(err.Error(), "unique constraint") {
			http.Error(w, "a template with this name already exists", http.StatusConflict)
			return
		}
		h.Log.Error("failed to update prompt template", zap.Error(err))
		http.Error(w, "failed to update template", http.StatusInternalServerError)
		return
	}

	go h.writeAuditLog(r.Context(), orgId, "update", "prompt_template", templateId, nil)

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

// DeletePromptTemplate deletes a prompt template.
// DELETE /api/v1/prompt-templates/:id
func (h *Handler) DeletePromptTemplate(w http.ResponseWriter, r *http.Request) {
	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var orgId string
	err := h.DB.QueryRow(r.Context(),
		`SELECT org_id FROM members WHERE id = $1`,
		claims.Subject,
	).Scan(&orgId)
	if err != nil {
		h.Log.Error("failed to get org from member", zap.Error(err))
		http.Error(w, "failed to get org", http.StatusInternalServerError)
		return
	}

	templateId := chi.URLParam(r, "id")

	result, err := h.DB.Exec(r.Context(),
		`DELETE FROM prompt_templates WHERE id = $1 AND org_id = $2`,
		templateId, orgId,
	)
	if err != nil {
		h.Log.Error("failed to delete prompt template", zap.Error(err))
		http.Error(w, "failed to delete template", http.StatusInternalServerError)
		return
	}

	if result.RowsAffected() == 0 {
		http.Error(w, "template not found", http.StatusNotFound)
		return
	}

	go h.writeAuditLog(r.Context(), orgId, "delete", "prompt_template", templateId, nil)

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
}

// UploadDocument accepts multipart/form-data with a "file" field.
// It extracts text, chunks it, embeds chunks (if embed client configured),
// and stores everything in Postgres.
func (h *Handler) UploadDocument(w http.ResponseWriter, r *http.Request) {
	// Parse multipart (max 50MB)
	if err := r.ParseMultipartForm(50 << 20); err != nil {
		http.Error(w, "file too large (max 50MB)", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Validate extension
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(header.Filename), "."))
	if ext != "txt" && ext != "md" && ext != "pdf" {
		http.Error(w, "unsupported file type; use txt, md, or pdf", http.StatusBadRequest)
		return
	}

	// Get member + org
	claims := auth.UserFromContext(r.Context())
	var memberID, orgID string
	err = h.DB.QueryRow(r.Context(),
		`SELECT id::text, org_id::text FROM members WHERE id = $1`,
		claims.Subject).Scan(&memberID, &orgID)
	if err != nil {
		http.Error(w, "member not found", http.StatusNotFound)
		return
	}

	// Extract text
	text, err := rag.ExtractText(file, header.Filename, header.Size)
	if err != nil {
		http.Error(w, "text extraction failed: "+err.Error(), http.StatusUnprocessableEntity)
		return
	}

	// Create document record
	var docID string
	err = h.DB.QueryRow(r.Context(),
		`INSERT INTO knowledge_documents (org_id, name, file_type, file_size_bytes, status, created_by)
		 VALUES ($1, $2, $3, $4, 'processing', $5) RETURNING id::text`,
		orgID, header.Filename, ext, header.Size, memberID).Scan(&docID)
	if err != nil {
		http.Error(w, "failed to create document record", http.StatusInternalServerError)
		return
	}

	// Chunk and embed asynchronously
	go h.processDocument(context.Background(), docID, orgID, text)

	writeJSON(w, http.StatusAccepted, map[string]string{
		"id":     docID,
		"status": "processing",
	})
}

func (h *Handler) processDocument(ctx context.Context, docID, orgID, text string) {
	chunks := rag.Chunk(text, 0, 0)

	var embeddings [][]float32
	if h.embed != nil {
		var err error
		embeddings, err = h.embed.Embed(ctx, chunks)
		if err != nil {
			_, _ = h.DB.Exec(ctx,
				`UPDATE knowledge_documents SET status='error', error_message=$1 WHERE id=$2`,
				"embedding failed: "+err.Error(), docID)
			return
		}
	}

	// Wrap chunks + status update in a transaction so failure leaves no partial state.
	tx, err := h.DB.Begin(ctx)
	if err != nil {
		_, _ = h.DB.Exec(ctx,
			`UPDATE knowledge_documents SET status='error', error_message=$1 WHERE id=$2`,
			"begin tx: "+err.Error(), docID)
		return
	}
	defer tx.Rollback(ctx) // safe to call after Commit (returns ErrTxClosed)

	for i, chunk := range chunks {
		var embedding any = nil
		if embeddings != nil && i < len(embeddings) {
			embedding = pgvector.NewVector(embeddings[i])
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO knowledge_chunks (document_id, org_id, chunk_index, content, embedding)
			 VALUES ($1, $2, $3, $4, $5)`,
			docID, orgID, i, chunk, embedding)
		if err != nil {
			_, _ = h.DB.Exec(ctx,
				`UPDATE knowledge_documents SET status='error', error_message=$1 WHERE id=$2`,
				fmt.Sprintf("chunk %d insert failed: %s", i, err.Error()), docID)
			return
		}
	}

	_, err = tx.Exec(ctx,
		`UPDATE knowledge_documents SET status='ready', chunk_count=$1 WHERE id=$2`,
		len(chunks), docID)
	if err != nil {
		_, _ = h.DB.Exec(ctx,
			`UPDATE knowledge_documents SET status='error', error_message=$1 WHERE id=$2`,
			"status update failed: "+err.Error(), docID)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		_, _ = h.DB.Exec(ctx,
			`UPDATE knowledge_documents SET status='error', error_message=$1 WHERE id=$2`,
			"commit failed: "+err.Error(), docID)
	}
}

// ListDocuments returns all documents for the user's organization.
func (h *Handler) ListDocuments(w http.ResponseWriter, r *http.Request) {
	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var orgID string
	err := h.DB.QueryRow(r.Context(),
		`SELECT org_id::text FROM members WHERE id = $1`,
		claims.Subject).Scan(&orgID)
	if err != nil {
		http.Error(w, "organization not found", http.StatusNotFound)
		return
	}

	rows, err := h.DB.Query(r.Context(),
		`SELECT id::text, name, file_type, file_size_bytes, chunk_count, status, error_message, created_at
		 FROM knowledge_documents
		 WHERE org_id = $1
		 ORDER BY created_at DESC`,
		orgID)
	if err != nil {
		http.Error(w, "failed to list documents", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var docs []map[string]interface{}
	for rows.Next() {
		var id, name, fileType, status string
		var fileSize, chunkCount int
		var errorMsg *string
		var createdAt time.Time
		if err := rows.Scan(&id, &name, &fileType, &fileSize, &chunkCount, &status, &errorMsg, &createdAt); err != nil {
			continue
		}
		doc := map[string]interface{}{
			"id":            id,
			"name":          name,
			"fileType":      fileType,
			"fileSizeBytes": fileSize,
			"chunkCount":    chunkCount,
			"status":        status,
			"createdAt":     createdAt.UTC().Format(time.RFC3339),
		}
		if errorMsg != nil {
			doc["errorMessage"] = *errorMsg
		}
		docs = append(docs, doc)
	}

	writeJSON(w, http.StatusOK, docs)
}

// DeleteDocument removes a document and its chunks.
func (h *Handler) DeleteDocument(w http.ResponseWriter, r *http.Request) {
	claims := auth.UserFromContext(r.Context())
	docID := chi.URLParam(r, "id")

	// Verify org ownership
	var orgID string
	err := h.DB.QueryRow(r.Context(),
		`SELECT org_id::text FROM knowledge_documents WHERE id = $1`,
		docID).Scan(&orgID)
	if err == pgx.ErrNoRows {
		http.Error(w, "document not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "failed to verify ownership", http.StatusInternalServerError)
		return
	}

	// Verify user is in the same org
	var userOrgID string
	err = h.DB.QueryRow(r.Context(),
		`SELECT org_id::text FROM members WHERE id = $1`,
		claims.Subject).Scan(&userOrgID)
	if err != nil || userOrgID != orgID {
		http.Error(w, "unauthorized", http.StatusForbidden)
		return
	}

	result, err := h.DB.Exec(r.Context(),
		`DELETE FROM knowledge_documents WHERE id = $1`,
		docID)
	if err != nil {
		http.Error(w, "failed to delete document", http.StatusInternalServerError)
		return
	}

	if result.RowsAffected() == 0 {
		http.Error(w, "document not found", http.StatusNotFound)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// RetrieveContext takes a query string, embeds it, returns top-k chunks.
// POST /api/v1/rag/retrieve  body: { query, topK? }
func (h *Handler) RetrieveContext(w http.ResponseWriter, r *http.Request) {
	if h.embed == nil {
		http.Error(w, "embedding not configured", http.StatusServiceUnavailable)
		return
	}

	claims := auth.UserFromContext(r.Context())
	var orgID string
	err := h.DB.QueryRow(r.Context(),
		`SELECT org_id::text FROM members WHERE id = $1`,
		claims.Subject).Scan(&orgID)
	if err != nil {
		http.Error(w, "organization not found", http.StatusNotFound)
		return
	}

	var req struct {
		Query string `json:"query"`
		TopK  int    `json:"topK"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Query == "" {
		http.Error(w, "query is required", http.StatusBadRequest)
		return
	}

	// Embed query
	vecs, err := h.embed.Embed(r.Context(), []string{req.Query})
	if err != nil {
		http.Error(w, "failed to embed query: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Retrieve
	chunks, err := rag.Retrieve(r.Context(), h.DB, orgID, vecs[0], req.TopK)
	if err != nil {
		http.Error(w, "failed to retrieve context: "+err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, chunks)
}

// Logout clears the session cookie. Public endpoint — works whether or not
// the cookie is currently valid.
// POST /api/v1/auth/logout
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     "openserve_session",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged out"})
}

