package billing

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Webhook tests run locally without a Stripe account: we sign payloads with the
// shared secret exactly as Stripe does. They need a migrated PostgreSQL via
// TEST_DATABASE_URL and skip otherwise.

const (
	testSecret     = "whsec_test_secret_value"
	testProPriceID = "price_pro_test"
)

var billingCounter atomic.Int64

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func billingPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run billing webhook integration tests")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func newFreeTenant(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	ctx := context.Background()
	name := fmt.Sprintf("wh-%d-%d", time.Now().UnixNano(), billingCounter.Add(1))
	var id string
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id::text`, name).Scan(&id); err != nil {
		t.Fatalf("tenant: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO subscriptions (tenant_id, plan_id, status) VALUES ($1,'free','active')`, id); err != nil {
		t.Fatalf("sub: %v", err)
	}
	return id
}

func planStatus(t *testing.T, pool *pgxpool.Pool, tenantID string) (plan, status string) {
	t.Helper()
	if err := pool.QueryRow(context.Background(),
		`SELECT plan_id, status FROM subscriptions WHERE tenant_id=$1`, tenantID).Scan(&plan, &status); err != nil {
		t.Fatalf("read sub: %v", err)
	}
	return
}

func sign(payload []byte, secret string) string {
	ts := time.Now().Unix()
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d.%s", ts, payload)
	return fmt.Sprintf("t=%d,v1=%s", ts, hex.EncodeToString(mac.Sum(nil)))
}

func checkoutEvent(eventID, tenantID, customer, sub string) []byte {
	return []byte(fmt.Sprintf(
		`{"id":%q,"type":"checkout.session.completed","data":{"object":{"client_reference_id":%q,"customer":%q,"subscription":%q,"metadata":{"tenant_id":%q}}}}`,
		eventID, tenantID, customer, sub, tenantID))
}

func subEvent(eventID, eventType, subID, status, priceID string) []byte {
	return []byte(fmt.Sprintf(
		`{"id":%q,"type":%q,"data":{"object":{"id":%q,"status":%q,"items":{"data":[{"price":{"id":%q}}]}}}}`,
		eventID, eventType, subID, status, priceID))
}

func newWebhook(pool *pgxpool.Pool) *WebhookService {
	return NewWebhookService(pool, testSecret, testProPriceID, quiet())
}

func TestWebhook_Upgrade_FreeToPro(t *testing.T) {
	pool := billingPool(t)
	ws := newWebhook(pool)
	tenant := newFreeTenant(t, pool)

	payload := checkoutEvent("evt_up_"+tenant, tenant, "cus_1", "sub_"+tenant)
	status, err := ws.Process(context.Background(), payload, sign(payload, testSecret))
	if err != nil || status != 200 {
		t.Fatalf("process: status=%d err=%v, want 200", status, err)
	}
	plan, st := planStatus(t, pool, tenant)
	if plan != "pro" || st != "active" {
		t.Fatalf("after upgrade: plan=%s status=%s, want pro/active", plan, st)
	}
}

func TestWebhook_ForgedSignature_400_NoChange(t *testing.T) {
	pool := billingPool(t)
	ws := newWebhook(pool)
	tenant := newFreeTenant(t, pool)

	payload := checkoutEvent("evt_forged_"+tenant, tenant, "cus_x", "sub_x")
	// Sign with the WRONG secret.
	status, err := ws.Process(context.Background(), payload, sign(payload, "whsec_wrong"))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if status != 400 {
		t.Fatalf("status = %d, want 400", status)
	}
	plan, _ := planStatus(t, pool, tenant)
	if plan != "free" {
		t.Fatalf("forged event changed data: plan=%s, want free", plan)
	}
}

func TestWebhook_DuplicateEvent_ProcessedOnce(t *testing.T) {
	pool := billingPool(t)
	ws := newWebhook(pool)
	tenant := newFreeTenant(t, pool)

	payload := checkoutEvent("evt_dup_"+tenant, tenant, "cus_1", "sub_"+tenant)
	hdr := sign(payload, testSecret)

	s1, _ := ws.Process(context.Background(), payload, hdr)
	s2, _ := ws.Process(context.Background(), payload, hdr)
	if s1 != 200 || s2 != 200 {
		t.Fatalf("statuses = %d,%d; want 200,200", s1, s2)
	}
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM processed_stripe_events WHERE event_id=$1`, "evt_dup_"+tenant).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("processed_stripe_events rows = %d, want 1", n)
	}
}

func TestWebhook_SubscriptionDeleted_Downgrades(t *testing.T) {
	pool := billingPool(t)
	ws := newWebhook(pool)
	tenant := newFreeTenant(t, pool)
	sub := "sub_del_" + tenant

	up := checkoutEvent("evt_u_"+tenant, tenant, "cus_1", sub)
	if s, _ := ws.Process(context.Background(), up, sign(up, testSecret)); s != 200 {
		t.Fatalf("upgrade status %d", s)
	}
	del := subEvent("evt_del_"+tenant, "customer.subscription.deleted", sub, "canceled", testProPriceID)
	if s, _ := ws.Process(context.Background(), del, sign(del, testSecret)); s != 200 {
		t.Fatalf("delete status %d", s)
	}
	plan, st := planStatus(t, pool, tenant)
	if plan != "free" || st != "canceled" {
		t.Fatalf("after delete: plan=%s status=%s, want free/canceled", plan, st)
	}
}

func TestWebhook_SubscriptionUpdated_PastDue(t *testing.T) {
	pool := billingPool(t)
	ws := newWebhook(pool)
	tenant := newFreeTenant(t, pool)
	sub := "sub_upd_" + tenant

	up := checkoutEvent("evt_u2_"+tenant, tenant, "cus_1", sub)
	if s, _ := ws.Process(context.Background(), up, sign(up, testSecret)); s != 200 {
		t.Fatalf("upgrade status %d", s)
	}
	upd := subEvent("evt_upd_"+tenant, "customer.subscription.updated", sub, "past_due", testProPriceID)
	if s, _ := ws.Process(context.Background(), upd, sign(upd, testSecret)); s != 200 {
		t.Fatalf("update status %d", s)
	}
	plan, st := planStatus(t, pool, tenant)
	if plan != "pro" || st != "past_due" {
		t.Fatalf("after update: plan=%s status=%s, want pro/past_due", plan, st)
	}
}
