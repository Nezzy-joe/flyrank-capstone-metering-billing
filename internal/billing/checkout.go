package billing

import "context"

// CheckoutService turns an authenticated tenant into a Pro Checkout URL.
type CheckoutService struct {
	gw      Gateway
	baseURL string
}

func NewCheckoutService(gw Gateway, baseURL string) *CheckoutService {
	return &CheckoutService{gw: gw, baseURL: baseURL}
}

// CreateProCheckout returns the hosted Checkout URL for upgrading to Pro.
func (c *CheckoutService) CreateProCheckout(ctx context.Context, tenantID string) (string, error) {
	success := c.baseURL + "/checkout/success"
	cancel := c.baseURL + "/checkout/cancel"
	return c.gw.CreateProCheckout(ctx, tenantID, success, cancel)
}
