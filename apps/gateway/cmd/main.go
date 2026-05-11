// Package main is the openserve API gateway.
//
// The gateway is a thin Go reverse proxy that sits in front of all vLLM inference
// deployments. For each incoming request it:
//  1. Extracts the API key from the Authorization header ("Bearer openserve_live_...")
//  2. Validates the key against the Postgres hash store (Argon2id)
//  3. Checks rate limits (RPM + TPM) using Redis sliding-window counters
//  4. Enforces max-input-tokens / max-output-tokens caps on the request body
//  5. Rewrites the URL to the target vLLM ClusterIP service
//  6. Proxies the request, streaming the response (SSE) without buffering
//  7. Records per-request metrics (key ID, model, tokens, latency) to Prometheus
//
// Design note: see ADR 0004 for why we chose a Go proxy over Envoy + ext_authz.
package main

import (
	"context"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/openserve/openserve/apps/gateway/internal/auth"
	"github.com/openserve/openserve/apps/gateway/internal/proxy"
	"github.com/openserve/openserve/apps/gateway/internal/ratelimit"
	"github.com/openserve/openserve/apps/gateway/internal/routing"
)

func main() {
	var (
		addr           string
		postgresURL    string
		redisAddr      string
		routingCfg     string
		peerRelayURL   string
	)

	flag.StringVar(&addr, "addr", ":8080", "Listen address for the gateway")
	flag.StringVar(&postgresURL, "postgres-url", os.Getenv("POSTGRES_URL"), "Postgres URL for API key validation")
	flag.StringVar(&redisAddr, "redis-addr", os.Getenv("REDIS_ADDR"), "Redis address for rate-limit counters (host:port)")
	flag.StringVar(&routingCfg, "routing-config", "/config/routing.yaml",
		"Path to routing config file (hot-reloaded on change by the operator)")
	flag.StringVar(&peerRelayURL, "peer-relay-url", os.Getenv("PEER_RELAY_INTERNAL_URL"), "Internal URL of peer-relay service")
	flag.Parse()

	log, _ := zap.NewProduction()
	defer log.Sync()

	if postgresURL == "" || redisAddr == "" {
		log.Fatal("required flags missing: --postgres-url, --redis-addr")
	}

	keyValidator, err := auth.NewKeyValidator(context.Background(), postgresURL)
	if err != nil {
		log.Fatal("key validator init failed", zap.Error(err))
	}

	limiter, err := ratelimit.NewRedisLimiter(redisAddr)
	if err != nil {
		log.Fatal("rate limiter init failed", zap.Error(err))
	}

	router, err := routing.NewFileRouter(routingCfg)
	if err != nil {
		log.Fatal("router init failed", zap.Error(err))
	}

	// Get the Postgres pool from the key validator for peer relay checks.
	pgPool := keyValidator.(*auth.PostgresKeyValidator).Pool()

	p := proxy.New(proxy.Config{
		KeyValidator: keyValidator,
		Limiter:      limiter,
		Router:       router,
		Log:          log,
		PeerRelayURL: peerRelayURL,
		DB:           pgPool,
	})

	srv := &http.Server{
		Addr:    addr,
		Handler: p,
		// WriteTimeout must be long enough for streaming responses.
		// vLLM can stream for minutes on large outputs.
		WriteTimeout: 10 * time.Minute,
		ReadTimeout:  30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Info("starting gateway", zap.String("addr", addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("server error", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}
