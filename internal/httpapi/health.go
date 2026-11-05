package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// Pinger is the narrow dependency the readiness check needs. *pgxpool.Pool
// satisfies it directly, so the handler verifies the database WITHOUT importing
// the database package or running any SQL itself — keeping the transport layer
// free of persistence concerns.
type Pinger interface {
	Ping(ctx context.Context) error
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// handleHealth is LIVENESS: the process is up and serving. It checks no
// dependencies, so it stays 200 even if the database is down.
func handleHealth() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// handleReady is READINESS: the service can serve real traffic, which requires a
// reachable database. It calls the Pinger with a short timeout and reports 503
// if the dependency is unavailable.
func handleReady(db Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		if err := db.Ping(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"status": "unavailable",
				"reason": "database not reachable",
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	}
}
