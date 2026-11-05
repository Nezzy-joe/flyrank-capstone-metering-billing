package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
)

const maxWebhookBody = 1 << 20 // 1 MiB

// WebhookProcessor verifies and applies a Stripe webhook, returning the HTTP
// status to send.
type WebhookProcessor interface {
	Process(ctx context.Context, payload []byte, signature string) (status int, err error)
}

func handleWebhook(wp WebhookProcessor, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if wp == nil {
			writeError(w, http.StatusServiceUnavailable, "stripe_not_configured",
				"webhooks are not configured (set STRIPE_WEBHOOK_SECRET)")
			return
		}
		// The raw body is required for signature verification.
		r.Body = http.MaxBytesReader(w, r.Body, maxWebhookBody)
		payload, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_body", "could not read request body")
			return
		}

		status, err := wp.Process(r.Context(), payload, r.Header.Get("Stripe-Signature"))
		if err != nil {
			logger.Error("webhook processing error", "err", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(status)
		if status == http.StatusOK {
			_, _ = w.Write([]byte(`{"received":true}`))
		}
	}
}
