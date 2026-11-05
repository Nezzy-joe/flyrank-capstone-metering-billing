// Command api is the HTTP service entrypoint and composition root: it loads
// config, opens the database pool, builds the services + server, and runs until a
// shutdown signal, then drains connections gracefully. It does NOT run migrations
// — that is a separate step (cmd/migrate / `make migrate-up`).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/Nezzy-joe/flyrank-capstone-metering-billing/internal/billing"
	"github.com/Nezzy-joe/flyrank-capstone-metering-billing/internal/config"
	"github.com/Nezzy-joe/flyrank-capstone-metering-billing/internal/database"
	"github.com/Nezzy-joe/flyrank-capstone-metering-billing/internal/httpapi"
	"github.com/Nezzy-joe/flyrank-capstone-metering-billing/internal/service"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// Cancelled on SIGINT/SIGTERM — drives graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Fail-fast: database.New pings before we serve. An unreachable database at
	// startup prevents the API from starting; a database that goes down later is
	// reported by /ready (503), not a crash. See DESIGN.md "Startup & readiness".
	pool, err := database.New(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	logger.Info("database pool ready", "max_conns", cfg.DBMaxConns)

	// Stripe features are optional; nil deps make their endpoints return 503.
	var checkout httpapi.Checkouter
	var webhookProc httpapi.WebhookProcessor
	if cfg.StripeCheckoutEnabled() {
		gw := billing.NewStripeGateway(cfg.StripeSecretKey, cfg.StripeProPriceID)
		checkout = billing.NewCheckoutService(gw, cfg.PublicBaseURL)
		logger.Info("stripe checkout enabled")
	}
	if cfg.StripeWebhookEnabled() {
		webhookProc = billing.NewWebhookService(pool, cfg.StripeWebhookSecret, cfg.StripeProPriceID, logger)
		logger.Info("stripe webhooks enabled")
	}

	srv := httpapi.NewServer(cfg, httpapi.Deps{
		Pinger:   pool,
		Resolver: service.NewAuthService(pool),
		Meter:    service.NewMeterService(pool, logger),
		Usage:    service.NewUsageService(pool),
		Checkout: checkout,
		Webhook:  webhookProc,
		Logger:   logger,
	})

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("listening", "port", cfg.Port, "env", cfg.AppEnv)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		return err
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	logger.Info("server stopped cleanly")
	return nil
}
