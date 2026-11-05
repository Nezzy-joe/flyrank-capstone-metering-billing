package service

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// These are INTEGRATION tests: they need a migrated PostgreSQL reachable via
// TEST_DATABASE_URL. They skip otherwise, so `go test ./...` stays green offline.
// Run with: make test-integration (after make db-up && make migrate-up).

var nameCounter atomic.Int64

func uniqueName(prefix string) string {
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano(), nameCounter.Add(1))
}

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run service integration tests")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// newTenant creates an isolated tenant subscribed to planID.
func newTenant(t *testing.T, pool *pgxpool.Pool, planID string) string {
	t.Helper()
	ctx := context.Background()
	var id string
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id::text`, uniqueName("test")).Scan(&id); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	if planID != "" {
		if _, err := pool.Exec(ctx, `INSERT INTO subscriptions (tenant_id, plan_id, status) VALUES ($1, $2, 'active')`, id, planID); err != nil {
			t.Fatalf("subscribe: %v", err)
		}
	}
	return id
}

// ensureTinyPlan creates a plan with a tiny api-call limit for fast quota tests.
func ensureTinyPlan(t *testing.T, pool *pgxpool.Pool, apiLimit int64) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO plans (id, name, api_call_limit, ai_token_limit)
		 VALUES ('test_tiny', 'Tiny', $1, 1000000)
		 ON CONFLICT (id) DO UPDATE SET api_call_limit = $1, ai_token_limit = 1000000`, apiLimit)
	if err != nil {
		t.Fatalf("ensure tiny plan: %v", err)
	}
}

func countAPIEvents(t *testing.T, pool *pgxpool.Pool, tenantID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM usage_events WHERE tenant_id = $1 AND type = 'api_call'`, tenantID).Scan(&n); err != nil {
		t.Fatalf("count events: %v", err)
	}
	return n
}

func TestGenerate_IdempotentReplay_ByteIdentical(t *testing.T) {
	pool := testPool(t)
	svc := NewMeterService(pool, quietLogger())
	tenant := newTenant(t, pool, "free")
	in := GenerateInput{InputTokens: 1000, CachedInputTokens: 300, OutputTokens: 2500, ReasoningTokens: 200}

	r1, err := svc.Generate(context.Background(), tenant, "k", in)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	r2, err := svc.Generate(context.Background(), tenant, "k", in)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if r1.Status != 200 || r2.Status != 200 {
		t.Fatalf("statuses = %d, %d; want 200,200", r1.Status, r2.Status)
	}
	if string(r1.Body) != string(r2.Body) {
		t.Fatalf("replay not byte-identical:\n first=%s\n second=%s", r1.Body, r2.Body)
	}
	if n := countAPIEvents(t, pool, tenant); n != 1 {
		t.Fatalf("api_call events = %d, want exactly 1", n)
	}
}

func TestGenerate_PayloadMismatch_409(t *testing.T) {
	pool := testPool(t)
	svc := NewMeterService(pool, quietLogger())
	tenant := newTenant(t, pool, "free")

	if _, err := svc.Generate(context.Background(), tenant, "k", GenerateInput{OutputTokens: 10}); err != nil {
		t.Fatalf("first: %v", err)
	}
	r, err := svc.Generate(context.Background(), tenant, "k", GenerateInput{OutputTokens: 99})
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if r.Status != 409 {
		t.Fatalf("status = %d, want 409", r.Status)
	}
	if n := countAPIEvents(t, pool, tenant); n != 1 {
		t.Fatalf("api_call events = %d, want 1 (409 must not add one)", n)
	}
}

func TestGenerate_NoSubscription_402(t *testing.T) {
	pool := testPool(t)
	svc := NewMeterService(pool, quietLogger())
	tenant := newTenant(t, pool, "") // no subscription

	r, err := svc.Generate(context.Background(), tenant, "k", GenerateInput{OutputTokens: 1})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if r.Status != 402 {
		t.Fatalf("status = %d, want 402", r.Status)
	}
}

func TestGenerate_ConcurrentSameKey_OneEvent(t *testing.T) {
	pool := testPool(t)
	svc := NewMeterService(pool, quietLogger())
	tenant := newTenant(t, pool, "free")
	in := GenerateInput{OutputTokens: 100}

	const N = 25
	var wg sync.WaitGroup
	bodies := make([]string, N)
	statuses := make([]int, N)
	errs := make([]error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := svc.Generate(context.Background(), tenant, "same-key", in)
			statuses[i], bodies[i], errs[i] = r.Status, string(r.Body), err
		}(i)
	}
	wg.Wait()

	for i := 0; i < N; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
		if statuses[i] != 200 {
			t.Fatalf("goroutine %d status = %d, want 200", i, statuses[i])
		}
		if bodies[i] != bodies[0] {
			t.Fatalf("goroutine %d body differs from winner", i)
		}
	}
	if n := countAPIEvents(t, pool, tenant); n != 1 {
		t.Fatalf("api_call events = %d, want exactly 1 under %d concurrent identical requests", n, N)
	}
}

func TestGenerate_ConcurrentQuota_NoOvershoot(t *testing.T) {
	pool := testPool(t)
	ensureTinyPlan(t, pool, 5) // api-call limit of 5
	svc := NewMeterService(pool, quietLogger())
	tenant := newTenant(t, pool, "test_tiny")

	const N = 30 // far more than the limit
	var wg sync.WaitGroup
	var ok200, rejected429 atomic.Int64
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := svc.Generate(context.Background(), tenant, fmt.Sprintf("key-%d", i), GenerateInput{OutputTokens: 1})
			if err != nil {
				t.Errorf("goroutine %d: %v", i, err)
				return
			}
			switch r.Status {
			case 200:
				ok200.Add(1)
			case 429:
				rejected429.Add(1)
			default:
				t.Errorf("goroutine %d: unexpected status %d", i, r.Status)
			}
		}(i)
	}
	wg.Wait()

	if ok200.Load() != 5 {
		t.Fatalf("successes = %d, want exactly 5 (the limit)", ok200.Load())
	}
	if rejected429.Load() != N-5 {
		t.Fatalf("429s = %d, want %d", rejected429.Load(), N-5)
	}
	// The ledger and counter must never exceed the limit.
	if n := countAPIEvents(t, pool, tenant); n != 5 {
		t.Fatalf("api_call events = %d, want exactly 5 — quota overshoot!", n)
	}
	var used int64
	period := time.Now().UTC().Format("2006-01")
	_ = pool.QueryRow(context.Background(),
		`SELECT api_calls_used FROM usage_counters WHERE tenant_id=$1 AND period=$2`, tenant, period).Scan(&used)
	if used != 5 {
		t.Fatalf("counter api_calls_used = %d, want 5", used)
	}
}

func TestGenerate_QuotaBoundary_Sequential(t *testing.T) {
	pool := testPool(t)
	ensureTinyPlan(t, pool, 3)
	svc := NewMeterService(pool, quietLogger())
	tenant := newTenant(t, pool, "test_tiny")

	// Exactly at the boundary: 3 calls allowed.
	for i := 0; i < 3; i++ {
		r, err := svc.Generate(context.Background(), tenant, fmt.Sprintf("b%d", i), GenerateInput{OutputTokens: 1})
		if err != nil || r.Status != 200 {
			t.Fatalf("call %d: status=%d err=%v, want 200", i, r.Status, err)
		}
	}
	// One beyond the boundary: rejected.
	r, err := svc.Generate(context.Background(), tenant, "b-over", GenerateInput{OutputTokens: 1})
	if err != nil {
		t.Fatalf("over-limit: %v", err)
	}
	if r.Status != 429 {
		t.Fatalf("over-limit status = %d, want 429", r.Status)
	}
}
