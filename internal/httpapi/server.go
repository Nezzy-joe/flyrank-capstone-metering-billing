// Package httpapi is the transport layer: it wires routes to handlers and builds
// the *http.Server. It holds no business logic and touches no SQL. Future
// service/repository layers are injected here as dependencies.
package httpapi

import (
	"net/http"
	"time"

	"github.com/Nezzy-joe/flyrank-capstone-metering-billing/internal/config"
)

// NewServer builds the HTTP server with its routes. Go 1.22+ method-aware
// ServeMux patterns ("GET /health") give us routing with no external router.
func NewServer(cfg *config.Config, db Pinger) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("GET /health", handleHealth())
	mux.Handle("GET /ready", handleReady(db))

	return &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
}
