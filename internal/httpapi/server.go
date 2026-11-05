// Package httpapi is the transport layer: it wires routes to handlers and builds
// the *http.Server. It holds no business logic and touches no SQL — services are
// injected via Deps.
package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/Nezzy-joe/flyrank-capstone-metering-billing/internal/config"
)

// Deps are the collaborators the HTTP layer needs. Interfaces keep handlers
// testable with fakes.
type Deps struct {
	Pinger   Pinger
	Resolver TenantResolver
	Meter    Meterer
	Usage    UsageReporter
	Logger   *slog.Logger
}

// NewServer builds the HTTP server and its routes. Public probes are unauthenticated;
// /v1/* require a Bearer API key via authMiddleware.
func NewServer(cfg *config.Config, d Deps) *http.Server {
	mux := http.NewServeMux()

	// Operational probes (no auth).
	mux.Handle("GET /health", handleHealth())
	mux.Handle("GET /ready", handleReady(d.Pinger))

	// Tenant API (auth required).
	authed := authMiddleware(d.Resolver, d.Logger)
	mux.Handle("POST /v1/generate", authed(handleGenerate(d.Meter, d.Logger)))
	mux.Handle("GET /v1/usage", authed(handleUsage(d.Usage, d.Logger)))

	return &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
}
