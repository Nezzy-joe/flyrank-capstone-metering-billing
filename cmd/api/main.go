// Command api is the HTTP service entrypoint and composition root: it loads
// config, opens the database pool, builds the server, and runs until a shutdown
// signal, then drains connections gracefully. It does NOT run migrations — that
// is a separate step (cmd/migrate / `make migrate-up`).
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"

	"github.com/Nezzy-joe/flyrank-capstone-metering-billing/internal/config"
	"github.com/Nezzy-joe/flyrank-capstone-metering-billing/internal/database"
	"github.com/Nezzy-joe/flyrank-capstone-metering-billing/internal/httpapi"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("fatal: %v", err)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// Cancelled on SIGINT/SIGTERM — drives graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Fail-fast: database.New pings before we start serving. An unreachable
	// database at startup prevents the API from starting at all; a database that
	// goes down *after* startup is reported by /ready (503), not a crash.
	// See DESIGN.md "Startup & readiness policy".
	pool, err := database.New(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	log.Printf("database pool ready (max_conns=%d)", cfg.DBMaxConns)

	srv := httpapi.NewServer(cfg, pool)

	serverErr := make(chan error, 1)
	go func() {
		log.Printf("listening on :%s (env=%s)", cfg.Port, cfg.AppEnv)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		return err
	case <-ctx.Done():
		log.Println("shutdown signal received")
	}

	// Graceful shutdown: stop accepting, let in-flight requests finish.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	log.Println("server stopped cleanly")
	return nil
}
