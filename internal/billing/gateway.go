// Package billing integrates Stripe in TEST MODE ONLY. Checkout talks to Stripe
// through the Gateway interface (mockable in tests); the webhook handler verifies
// signatures and syncs subscription state transactionally.
package billing

import (
	"context"
	"fmt"

	"github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/checkout/session"
)

// Gateway is the Stripe API surface Checkout needs. Mocked in unit tests.
type Gateway interface {
	CreateProCheckout(ctx context.Context, tenantID, successURL, cancelURL string) (url string, err error)
}

// StripeGateway is the real Stripe-backed Gateway (subscription Checkout).
type StripeGateway struct {
	secretKey  string
	proPriceID string
}

func NewStripeGateway(secretKey, proPriceID string) *StripeGateway {
	return &StripeGateway{secretKey: secretKey, proPriceID: proPriceID}
}

// CreateProCheckout creates a subscription Checkout session for the Pro price and
// returns its hosted URL. tenantID travels on client_reference_id + metadata so
// the webhook can attribute the completed session to the tenant.
func (g *StripeGateway) CreateProCheckout(ctx context.Context, tenantID, successURL, cancelURL string) (string, error) {
	stripe.Key = g.secretKey
	params := &stripe.CheckoutSessionParams{
		Mode: stripe.String(string(stripe.CheckoutSessionModeSubscription)),
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{Price: stripe.String(g.proPriceID), Quantity: stripe.Int64(1)},
		},
		SuccessURL:        stripe.String(successURL),
		CancelURL:         stripe.String(cancelURL),
		ClientReferenceID: stripe.String(tenantID),
	}
	params.Context = ctx
	params.AddMetadata("tenant_id", tenantID)

	s, err := session.New(params)
	if err != nil {
		return "", fmt.Errorf("stripe: create checkout session: %w", err)
	}
	return s.URL, nil
}
