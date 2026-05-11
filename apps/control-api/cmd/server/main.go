package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
	"golang.org/x/oauth2"

	"github.com/openserve/openserve/apps/control-api/internal/auth"
	"github.com/openserve/openserve/apps/control-api/internal/catalog"
	"github.com/openserve/openserve/apps/control-api/internal/db"
	"github.com/openserve/openserve/apps/control-api/internal/handler"
	mw "github.com/openserve/openserve/apps/control-api/internal/middleware"
	"github.com/openserve/openserve/apps/control-api/internal/rag"
)

func main() {
	var (
		addr                 string
		postgresURL          string
		jwtSecret            string
		oidcIssuerURL        string
		oidcClientID         string
		oidcClientSecret     string
		oidcDomains          string // comma-separated allowed email domains; empty = allow all
		// Deprecated aliases kept for backward compatibility
		googleClientID       string
		googleDomain         string
		catalogURL           string
		gcpProject           string
		bqDataset            string
		devEmail             string
		braveAPIKey          string
		embeddingEndpoint    string
		embeddingAPIKey      string
		embeddingModel       string
	)

	flag.StringVar(&addr, "addr", ":8080", "Listen address")
	flag.StringVar(&postgresURL, "postgres-url", os.Getenv("POSTGRES_URL"), "Postgres connection URL")
	flag.StringVar(&jwtSecret, "jwt-secret", os.Getenv("JWT_SECRET"), "JWT signing secret (32+ bytes)")
	flag.StringVar(&oidcIssuerURL, "oidc-issuer-url", os.Getenv("OIDC_ISSUER_URL"), "OIDC issuer URL (e.g. https://accounts.google.com, https://org.okta.com)")
	flag.StringVar(&oidcClientID, "oidc-client-id", os.Getenv("OIDC_CLIENT_ID"), "OIDC client ID")
	flag.StringVar(&oidcClientSecret, "oidc-client-secret", os.Getenv("OIDC_CLIENT_SECRET"), "OIDC client secret (required for OAuth2 token exchange)")
	flag.StringVar(&oidcDomains, "oidc-allowed-domains", os.Getenv("OIDC_ALLOWED_DOMAINS"), "Comma-separated allowed email domains (empty = allow all)")
	flag.StringVar(&googleClientID, "google-client-id", os.Getenv("GOOGLE_CLIENT_ID"), "[Deprecated] Use --oidc-client-id")
	flag.StringVar(&googleDomain, "google-domain", os.Getenv("GOOGLE_HOSTED_DOMAIN"), "[Deprecated] Use --oidc-allowed-domains")
	flag.StringVar(&catalogURL, "catalog-url", "https://catalog.openserve.io", "Model catalog base URL")
	flag.StringVar(&gcpProject, "gcp-project", os.Getenv("GCP_PROJECT"), "GCP project ID")
	flag.StringVar(&bqDataset, "bq-dataset", os.Getenv("BQ_DATASET"), "BigQuery dataset for usage")
	flag.StringVar(&devEmail, "dev-email", os.Getenv("DEV_EMAIL"),
		"LOCAL DEV ONLY: skip OIDC and authenticate all requests as this email address")
	flag.StringVar(&braveAPIKey, "brave-api-key", os.Getenv("BRAVE_API_KEY"), "Brave Search API key for web search grounding (optional)")
	flag.StringVar(&embeddingEndpoint, "embedding-endpoint", os.Getenv("EMBEDDING_ENDPOINT"), "OpenAI-compatible embedding endpoint URL (optional)")
	flag.StringVar(&embeddingAPIKey, "embedding-api-key", os.Getenv("EMBEDDING_API_KEY"), "API key for the embedding endpoint (optional)")
	flag.StringVar(&embeddingModel, "embedding-model", "nomic-embed-text-v1.5", "Model ID for embeddings")
	flag.Parse()

	// Backward compatibility: copy old Google-specific flags if new ones are unset.
	if oidcClientID == "" {
		oidcClientID = googleClientID
	}
	if oidcDomains == "" && googleDomain != "" {
		oidcDomains = googleDomain
	}
	if oidcIssuerURL == "" {
		oidcIssuerURL = "https://accounts.google.com"
	}

	log, _ := zap.NewProduction()
	defer log.Sync()

	if devEmail != "" {
		log.Warn("DEV MODE ACTIVE — all requests authenticated as " + devEmail + " — never use in production")
	}

	if postgresURL == "" || jwtSecret == "" {
		log.Fatal("required flags missing: --postgres-url, --jwt-secret")
	}
	if devEmail == "" && oidcClientID == "" {
		log.Fatal("--oidc-client-id (or --google-client-id) is required when not in dev mode")
	}

	pool, err := db.Connect(context.Background(), postgresURL)
	if err != nil {
		log.Fatal("postgres connect failed", zap.Error(err))
	}
	defer pool.Close()

	if err := db.Migrate(context.Background(), pool); err != nil {
		log.Fatal("db migration failed", zap.Error(err))
	}

	// In dev mode, provision a local org + admin member so all handlers can
	// resolve org_id / member_id from context without hitting OIDC.
	var devMemberID string
	if devEmail != "" {
		devMemberID, err = provisionDevUser(context.Background(), pool, devEmail)
		if err != nil {
			log.Fatal("failed to provision dev user", zap.Error(err))
		}
		log.Info("dev user ready", zap.String("email", devEmail), zap.String("memberID", devMemberID))
	}

	var oidcProvider *auth.OIDCProvider
	var oauth2Config *oauth2.Config
	if devEmail == "" {
		var allowedDomains []string
		for _, d := range strings.Split(oidcDomains, ",") {
			d = strings.TrimSpace(d)
			if d != "" {
				allowedDomains = append(allowedDomains, d)
			}
		}
		oidcProvider, err = auth.NewOIDCProvider(context.Background(), oidcIssuerURL, oidcClientID, allowedDomains)
		if err != nil {
			log.Fatal("oidc provider init failed", zap.String("issuer", oidcIssuerURL), zap.Error(err))
		}
		log.Info("OIDC provider ready", zap.String("issuer", oidcIssuerURL), zap.Int("allowedDomains", len(allowedDomains)))

		// Build OAuth2 config from OIDC provider
		oauth2Config = &oauth2.Config{
			ClientID:     oidcClientID,
			ClientSecret: oidcClientSecret,
			Endpoint:     oidcProvider.Provider().Endpoint(),
			Scopes:       []string{"openid", "profile", "email"},
			RedirectURL:  os.Getenv("OIDC_REDIRECT_URL"),
		}
		if oauth2Config.RedirectURL == "" {
			// Fallback for local development
			oauth2Config.RedirectURL = "http://localhost:8080/api/v1/auth/callback"
		}
	}

	catalogClient := catalog.NewClient(catalogURL)

	getEnvOr := func(key, fallback string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		return fallback
	}

	var embedClient *rag.EmbedClient
	if embeddingEndpoint != "" {
		embedClient = rag.NewEmbedClient(embeddingEndpoint, embeddingAPIKey, embeddingModel)
	}

	h := handler.New(handler.Deps{
		DB:            pool,
		Log:           log,
		OIDC:          oidcProvider,
		JWTSecret:     []byte(jwtSecret),
		Catalog:       catalogClient,
		GCPProject:    gcpProject,
		BQDataset:     bqDataset,
		OAuth2Config:  oauth2Config,
		BraveAPIKey:   braveAPIKey,
		DevEmail:      devEmail,
		DevMemberID:   devMemberID,
		EmbedClient:   embedClient,
	})

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	// CORS middleware — must come before routes.
	allowedOrigins := strings.Split(getEnvOr("ALLOWED_ORIGINS", "http://localhost:3000"), ",")
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			origin := req.Header.Get("Origin")
			for _, allowed := range allowedOrigins {
				if strings.TrimSpace(allowed) == origin {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Access-Control-Allow-Credentials", "true")
					w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
					w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
					w.Header().Set("Access-Control-Max-Age", "3600")
					break
				}
			}
			if req.Method == "OPTIONS" {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, req)
		})
	})

	// Health endpoints (no auth)
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	r.Get("/readyz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	// Auth endpoints (no JWT required) — rate limited
	authRL := mw.RateLimit(20)
	r.With(authRL).Get("/api/v1/auth/login", h.OIDCLogin)
	r.With(authRL).Post("/api/v1/auth/callback", h.OIDCCallback)
	r.Post("/api/v1/auth/logout", h.Logout)

	// Peer agent install script and binary (no auth required — token is in the curl command args)
	r.Get("/peer-agent/install.sh", h.GetPeerAgentInstallScript)
	r.Get("/peer-agent/download/{os}/{arch}", h.GetPeerAgentBinary)

	// Authenticated API routes
	r.Group(func(r chi.Router) {
		r.Use(h.RequireJWT)

		r.Get("/api/v1/catalog", h.ListCatalog)

		r.Get("/api/v1/deployments", h.ListDeployments)
		r.Post("/api/v1/deployments", h.CreateDeployment)
		r.Get("/api/v1/deployments/{id}", h.GetDeployment)
		r.Delete("/api/v1/deployments/{id}", h.DeleteDeployment)
		r.Post("/api/v1/deployments/{id}/resume", h.ResumeDeployment)

		r.Get("/api/v1/keys", h.ListAPIKeys)
		keyRL := mw.RateLimit(10)
		r.With(keyRL).Post("/api/v1/keys", h.CreateAPIKey)
		r.Delete("/api/v1/keys/{id}", h.DeleteAPIKey)
		r.Post("/api/v1/keys/{id}/rotate", h.RotateAPIKey)

		r.Get("/api/v1/usage", h.GetUsage)
		r.Get("/api/v1/audit", h.GetAuditLog)

		r.Get("/api/v1/settings", h.GetSettings)
		r.Post("/api/v1/settings", h.UpdateSettings)

		r.Get("/api/v1/webhooks", h.ListWebhooks)
		r.Post("/api/v1/webhooks", h.CreateWebhook)
		r.Delete("/api/v1/webhooks/{id}", h.DeleteWebhook)

		r.Get("/api/v1/peers", h.ListPeers)
		r.Post("/api/v1/peers", h.CreatePeer)
		r.Delete("/api/v1/peers/{id}", h.DeletePeer)
		r.Post("/api/v1/peers/{id}/rotate-token", h.RotatePeerToken)
		r.Get("/api/v1/peers/{id}/invites", h.ListPeerInvites)
		r.Post("/api/v1/peers/{id}/invites", h.CreatePeerInvite)
		r.Delete("/api/v1/peers/{id}/invites/{keyId}", h.DeletePeerInvite)

		r.Post("/api/v1/search", h.SearchWeb)

		r.Get("/api/v1/conversations", h.ListConversations)
		r.Post("/api/v1/conversations", h.CreateConversation)
		r.Get("/api/v1/conversations/{id}", h.GetConversation)
		r.Post("/api/v1/conversations/{id}/messages", h.AppendMessage)
		r.Delete("/api/v1/conversations/{id}", h.DeleteConversation)

		r.Get("/api/v1/prompt-templates", h.ListPromptTemplates)
		r.Post("/api/v1/prompt-templates", h.CreatePromptTemplate)
		r.Put("/api/v1/prompt-templates/{id}", h.UpdatePromptTemplate)
		r.Delete("/api/v1/prompt-templates/{id}", h.DeletePromptTemplate)

		r.Post("/api/v1/documents", h.UploadDocument)
		r.Get("/api/v1/documents", h.ListDocuments)
		r.Delete("/api/v1/documents/{id}", h.DeleteDocument)
		r.Post("/api/v1/rag/retrieve", h.RetrieveContext)

		// Admin-only routes
		r.Group(func(r chi.Router) {
			r.Use(h.RequireRole("admin"))
			r.Get("/api/v1/members", h.ListMembers)
			r.Post("/api/v1/members/invite", h.InviteMember)
			r.Delete("/api/v1/members/{id}", h.RemoveMember)
		})
	})

	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Info("starting control-api", zap.String("addr", addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("server error", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Error("graceful shutdown failed", zap.Error(err))
	}
}

// provisionDevUser upserts a dev org and admin member for local development.
// Returns the member ID to embed in the dev auth claims.
func provisionDevUser(ctx context.Context, pool *pgxpool.Pool, email string) (string, error) {
	// Upsert a dev org
	var orgID string
	err := pool.QueryRow(ctx,
		`INSERT INTO orgs (name, google_domain)
		 VALUES ('Dev Org', 'localhost')
		 ON CONFLICT DO NOTHING
		 RETURNING id`,
	).Scan(&orgID)
	if err != nil {
		// Org already exists — fetch it
		if err2 := pool.QueryRow(ctx, `SELECT id FROM orgs LIMIT 1`).Scan(&orgID); err2 != nil {
			return "", fmt.Errorf("provisionDevUser: get org: %w", err2)
		}
	}

	// Upsert the dev member as admin
	var memberID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO members (org_id, email, name, role, joined_at)
		 VALUES ($1, $2, 'Dev User', 'admin', now())
		 ON CONFLICT (org_id, email) DO UPDATE SET role = 'admin', joined_at = COALESCE(members.joined_at, now())
		 RETURNING id`,
		orgID, email,
	).Scan(&memberID); err != nil {
		return "", fmt.Errorf("provisionDevUser: upsert member: %w", err)
	}

	return memberID, nil
}
