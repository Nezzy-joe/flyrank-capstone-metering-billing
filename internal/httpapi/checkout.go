package httpapi

import (
	"context"
	"log/slog"
	"net/http"
)

// Checkouter creates a Pro Checkout session for a tenant.
type Checkouter interface {
	CreateProCheckout(ctx context.Context, tenantID string) (url string, err error)
}

func handleCheckout(co Checkouter, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if co == nil {
			writeError(w, http.StatusServiceUnavailable, "stripe_not_configured",
				"checkout is not configured (set STRIPE_SECRET_KEY and STRIPE_PRICE_PRO)")
			return
		}
		tenantID := tenantIDFrom(r.Context())
		url, err := co.CreateProCheckout(r.Context(), tenantID)
		if err != nil {
			logger.Error("checkout failed", "err", err)
			writeError(w, http.StatusBadGateway, "checkout_failed", "could not create checkout session")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"checkout_url": url})
	}
}
