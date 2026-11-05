package billing

import (
	"context"
	"testing"
)

type mockGateway struct {
	url        string
	gotTenant  string
	gotSuccess string
	gotCancel  string
}

func (m *mockGateway) CreateProCheckout(ctx context.Context, tenantID, successURL, cancelURL string) (string, error) {
	m.gotTenant, m.gotSuccess, m.gotCancel = tenantID, successURL, cancelURL
	return m.url, nil
}

func TestCheckoutService_BuildsURLsAndDelegates(t *testing.T) {
	mg := &mockGateway{url: "https://checkout.stripe.test/s/abc"}
	svc := NewCheckoutService(mg, "https://app.example.test")

	url, err := svc.CreateProCheckout(context.Background(), "tenant-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != mg.url {
		t.Fatalf("url = %q, want %q", url, mg.url)
	}
	if mg.gotTenant != "tenant-1" {
		t.Fatalf("tenant = %q, want tenant-1", mg.gotTenant)
	}
	if mg.gotSuccess != "https://app.example.test/checkout/success" {
		t.Fatalf("success URL = %q", mg.gotSuccess)
	}
	if mg.gotCancel != "https://app.example.test/checkout/cancel" {
		t.Fatalf("cancel URL = %q", mg.gotCancel)
	}
}
