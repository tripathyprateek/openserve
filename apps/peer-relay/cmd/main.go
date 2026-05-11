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
	addr := flag.String("addr", ":8080", "Public listen address")
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

	pubR := chi.NewRouter()
	pubR.Use(middleware.Recoverer)
	pubR.Get("/peer-ws/connect", h.ConnectPeer)
	pubR.Get("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })

	intR := chi.NewRouter()
	intR.Use(middleware.Recoverer)
	intR.Get("/internal/peers/{id}/online", h.PeerOnline)
	intR.Post("/internal/forward/{id}", h.ForwardToPeer)

	pubSrv := &http.Server{Addr: *addr, Handler: pubR}
	intSrv := &http.Server{Addr: *internalAddr, Handler: intR, ReadTimeout: 5 * time.Second, WriteTimeout: 300 * time.Second}

	go func() {
		log.Info("peer-relay public listening", zap.String("addr", *addr))
		if err := pubSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("public server error", zap.Error(err))
		}
	}()
	go func() {
		log.Info("peer-relay internal listening", zap.String("addr", *internalAddr))
		if err := intSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("internal server error", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pubSrv.Shutdown(ctx)
	intSrv.Shutdown(ctx)
}
