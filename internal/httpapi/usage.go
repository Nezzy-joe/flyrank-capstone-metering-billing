package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/Nezzy-joe/flyrank-capstone-metering-billing/internal/service"
)

// UsageReporter is the usage dependency for the usage handler.
type UsageReporter interface {
	Usage(ctx context.Context, tenantID string) (service.UsageView, error)
}

func handleUsage(reporter UsageReporter, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID := tenantIDFrom(r.Context())
		view, err := reporter.Usage(r.Context(), tenantID)
		if err != nil {
			if errors.Is(err, service.ErrNoSubscription) {
				writeError(w, http.StatusPaymentRequired, "payment_required", "no active subscription")
				return
			}
			logger.Error("usage failed", "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "failed to load usage")
			return
		}
		writeJSON(w, http.StatusOK, view)
	}
}
