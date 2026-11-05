package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// ClaimStripeEvent records a Stripe event id, returning false if already present
// (so each event is processed at most once).
func ClaimStripeEvent(ctx context.Context, q Querier, eventID, eventType string) (claimed bool, err error) {
	var got string
	err = q.QueryRow(ctx,
		`INSERT INTO processed_stripe_events (event_id, event_type) VALUES ($1, $2)
		 ON CONFLICT (event_id) DO NOTHING RETURNING event_id`,
		eventID, eventType).Scan(&got)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// FindTenantByStripeSubscription maps a Stripe subscription id to its tenant.
func FindTenantByStripeSubscription(ctx context.Context, q Querier, stripeSubID string) (tenantID string, found bool, err error) {
	err = q.QueryRow(ctx,
		`SELECT tenant_id::text FROM subscriptions WHERE stripe_subscription_id = $1`,
		stripeSubID).Scan(&tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return tenantID, true, nil
}

// SetSubscriptionFromCheckout writes plan/status plus the Stripe ids after a
// completed checkout.
func SetSubscriptionFromCheckout(ctx context.Context, q Querier, tenantID, planID, status, stripeCustomerID, stripeSubID string) error {
	_, err := q.Exec(ctx,
		`UPDATE subscriptions
		    SET plan_id = $2, status = $3, stripe_customer_id = $4, stripe_subscription_id = $5, updated_at = now()
		  WHERE tenant_id = $1`,
		tenantID, planID, status, stripeCustomerID, stripeSubID)
	return err
}

// UpdateSubscriptionStatus updates plan/status (used by subscription webhooks).
func UpdateSubscriptionStatus(ctx context.Context, q Querier, tenantID, planID, status string) error {
	_, err := q.Exec(ctx,
		`UPDATE subscriptions SET plan_id = $2, status = $3, updated_at = now() WHERE tenant_id = $1`,
		tenantID, planID, status)
	return err
}
