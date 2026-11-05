package billing

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stripe/stripe-go/v81/webhook"

	"github.com/Nezzy-joe/flyrank-capstone-metering-billing/internal/repository"
)

// WebhookService verifies Stripe signatures and syncs subscription state. Each
// event is verified, deduplicated, and applied inside one transaction.
type WebhookService struct {
	pool       *pgxpool.Pool
	secret     string
	proPriceID string
	logger     *slog.Logger
}

func NewWebhookService(pool *pgxpool.Pool, secret, proPriceID string, logger *slog.Logger) *WebhookService {
	return &WebhookService{pool: pool, secret: secret, proPriceID: proPriceID, logger: logger}
}

// minimal views of the Stripe objects we act on
type checkoutSession struct {
	ClientReferenceID string            `json:"client_reference_id"`
	Customer          string            `json:"customer"`
	Subscription      string            `json:"subscription"`
	Metadata          map[string]string `json:"metadata"`
}

type subscriptionObject struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Customer string `json:"customer"`
	Items    struct {
		Data []struct {
			Price struct {
				ID string `json:"id"`
			} `json:"price"`
		} `json:"data"`
	} `json:"items"`
}

// Process verifies the signature and applies the event. It returns the HTTP
// status the handler should send. A bad signature yields 400 with NO database
// change; a duplicate event is acknowledged without reprocessing.
func (s *WebhookService) Process(ctx context.Context, payload []byte, sigHeader string) (int, error) {
	event, err := webhook.ConstructEventWithOptions(payload, sigHeader, s.secret,
		webhook.ConstructEventOptions{IgnoreAPIVersionMismatch: true})
	if err != nil {
		// Forged / unverifiable: reject, change nothing.
		return http.StatusBadRequest, nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return http.StatusInternalServerError, err
	}
	defer tx.Rollback(ctx)

	claimed, err := repository.ClaimStripeEvent(ctx, tx, event.ID, string(event.Type))
	if err != nil {
		return http.StatusInternalServerError, err
	}
	if !claimed {
		// Already processed — acknowledge idempotently, do nothing.
		if err := tx.Commit(ctx); err != nil {
			return http.StatusInternalServerError, err
		}
		return http.StatusOK, nil
	}

	switch string(event.Type) {
	case "checkout.session.completed":
		var cs checkoutSession
		if err := json.Unmarshal(event.Data.Raw, &cs); err != nil {
			return http.StatusInternalServerError, err
		}
		tenantID := cs.ClientReferenceID
		if tenantID == "" {
			tenantID = cs.Metadata["tenant_id"]
		}
		if tenantID != "" {
			if err := repository.SetSubscriptionFromCheckout(ctx, tx, tenantID, "pro", "active", cs.Customer, cs.Subscription); err != nil {
				return http.StatusInternalServerError, err
			}
		}

	case "customer.subscription.updated":
		var so subscriptionObject
		if err := json.Unmarshal(event.Data.Raw, &so); err != nil {
			return http.StatusInternalServerError, err
		}
		if tenantID, found, err := repository.FindTenantByStripeSubscription(ctx, tx, so.ID); err != nil {
			return http.StatusInternalServerError, err
		} else if found {
			if err := repository.UpdateSubscriptionStatus(ctx, tx, tenantID, s.planForSubscription(so), mapStatus(so.Status)); err != nil {
				return http.StatusInternalServerError, err
			}
		}

	case "customer.subscription.deleted":
		var so subscriptionObject
		if err := json.Unmarshal(event.Data.Raw, &so); err != nil {
			return http.StatusInternalServerError, err
		}
		if tenantID, found, err := repository.FindTenantByStripeSubscription(ctx, tx, so.ID); err != nil {
			return http.StatusInternalServerError, err
		} else if found {
			if err := repository.UpdateSubscriptionStatus(ctx, tx, tenantID, "free", "canceled"); err != nil {
				return http.StatusInternalServerError, err
			}
		}
	default:
		// Claimed but not a type we act on; recording it is enough.
	}

	if err := tx.Commit(ctx); err != nil {
		return http.StatusInternalServerError, err
	}
	return http.StatusOK, nil
}

// planForSubscription maps the subscription's price to our plan id.
func (s *WebhookService) planForSubscription(so subscriptionObject) string {
	for _, it := range so.Items.Data {
		if it.Price.ID == s.proPriceID {
			return "pro"
		}
	}
	return "free"
}

// mapStatus maps Stripe subscription statuses to our allowed set.
func mapStatus(stripeStatus string) string {
	switch stripeStatus {
	case "active", "trialing":
		return "active"
	case "past_due":
		return "past_due"
	default: // canceled, unpaid, incomplete, incomplete_expired, paused
		return "canceled"
	}
}
