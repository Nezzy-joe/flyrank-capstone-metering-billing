// Command api is the HTTP service entrypoint and composition root: it loads
// config, opens the database pool, builds the services + server, optionally starts
// the background reconciliation job, and runs until a shutdown signal, then drains
// gracefully. It does NOT run migrations — that is a separate step (cmd/migrate).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/Nezzy-joe/flyrank-capstone-metering-billing/internal/billing"
	"github.com/Nezzy-joe/flyrank-capstone-metering-billing/internal/config"
	"github.com/Nezzy-joe/flyrank-capstone-metering-billing/internal/database"
	"github.com/Nezzy-joe/flyrank-capstone-metering-billing/internal/httpapi"
	"github.com/Nezzy-joe/flyrank-capstone-metering-billing/internal/reconcile"
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

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Fail-fast: database.New pings before we serve. See DESIGN.md "Startup & readiness".
	pool, err := database.New(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	logger.Info("database pool ready", "max_conns", cfg.DBMaxConns)

	// Background reconciliation job (its own context so we can stop it before the
	// pool closes).
	reconCtx, reconCancel := context.WithCancel(context.Background())
	defer reconCancel()
	var bg sync.WaitGroup
	if cfg.ReconcileEnabled() {
		rec := reconcile.New(
			reconcile.NewDBChecker(pool, cfg.ReconcileBatchSize),
			reconcile.NewLogAlerter(logger),
			logger, cfg.ReconcileInterval)
		bg.Add(1)
		go func() { defer bg.Done(); rec.Run(reconCtx) }()
	}
	stopBackground := func() { reconCancel(); bg.Wait() }

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
		stopBackground()
		return err
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		stopBackground()
		return err
	}
	stopBackground()
	logger.Info("server stopped cleanly")
	return nil
}
