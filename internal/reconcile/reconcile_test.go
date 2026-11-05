package reconcile

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// scriptChecker returns scripted results per call (last entry repeats).
type scriptChecker struct {
	mu      sync.Mutex
	results []func() ([]Discrepancy, error)
	calls   int
}

func (c *scriptChecker) Check(context.Context) ([]Discrepancy, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	i := c.calls
	c.calls++
	if i >= len(c.results) {
		i = len(c.results) - 1
	}
	return c.results[i]()
}

func (c *scriptChecker) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

type fakeAlerter struct {
	mu    sync.Mutex
	count int
}

func (a *fakeAlerter) Alert(context.Context, error) {
	a.mu.Lock()
	a.count++
	a.mu.Unlock()
}

func (a *fakeAlerter) alerts() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.count
}

func ok(discs []Discrepancy) func() ([]Discrepancy, error) {
	return func() ([]Discrepancy, error) { return discs, nil }
}
func fail() func() ([]Discrepancy, error) {
	return func() ([]Discrepancy, error) { return nil, errors.New("transient db error") }
}

func newTestReconciler(c Checker, a Alerter) *Reconciler {
	r := New(c, a, quiet(), time.Hour)
	r.baseBackoff = time.Millisecond // fast tests
	return r
}

func TestRunOnce_Success_NoAlert(t *testing.T) {
	c := &scriptChecker{results: []func() ([]Discrepancy, error){ok([]Discrepancy{{TenantID: "t", Period: "2026-11"}})}}
	a := &fakeAlerter{}
	newTestReconciler(c, a).runOnce(context.Background())
	if a.alerts() != 0 {
		t.Fatalf("alerts = %d, want 0", a.alerts())
	}
	if c.count() != 1 {
		t.Fatalf("checks = %d, want 1", c.count())
	}
}

func TestRunOnce_TransientThenSuccess_NoAlert(t *testing.T) {
	c := &scriptChecker{results: []func() ([]Discrepancy, error){fail(), ok(nil)}}
	a := &fakeAlerter{}
	newTestReconciler(c, a).runOnce(context.Background())
	if a.alerts() != 0 {
		t.Fatalf("alerts = %d, want 0 (recovered after retry)", a.alerts())
	}
	if c.count() != 2 {
		t.Fatalf("checks = %d, want 2", c.count())
	}
}

func TestRunOnce_RetriesExhausted_Alerts(t *testing.T) {
	c := &scriptChecker{results: []func() ([]Discrepancy, error){fail()}}
	a := &fakeAlerter{}
	r := newTestReconciler(c, a)
	r.runOnce(context.Background())
	if a.alerts() != 1 {
		t.Fatalf("alerts = %d, want 1", a.alerts())
	}
	if c.count() != r.maxRetries+1 {
		t.Fatalf("checks = %d, want %d (initial + retries)", c.count(), r.maxRetries+1)
	}
}

func TestRun_GracefulStop(t *testing.T) {
	c := &scriptChecker{results: []func() ([]Discrepancy, error){ok(nil)}}
	r := New(c, &fakeAlerter{}, quiet(), 5*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop within 2s of cancellation")
	}
}

// Integration: DBChecker flags a counter that disagrees with the ledger.
func TestDBChecker_DetectsMismatch(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run the DBChecker integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	var tid string
	name := fmt.Sprintf("recon-%d", time.Now().UnixNano())
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id::text`, name).Scan(&tid); err != nil {
		t.Fatalf("tenant: %v", err)
	}
	period := time.Now().UTC().Format("2006-01")
	// Counter claims usage the (empty) ledger does not back — a deliberate mismatch.
	if _, err := pool.Exec(ctx, `INSERT INTO usage_counters (tenant_id, period, api_calls_used, ai_tokens_used) VALUES ($1,$2,5,500)`, tid, period); err != nil {
		t.Fatalf("counter: %v", err)
	}

	discs, err := NewDBChecker(pool, 1000).Check(ctx)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	found := false
	for _, d := range discs {
		if d.TenantID == tid {
			found = true
			if d.CounterAPICalls != 5 || d.LedgerAPICalls != 0 || d.CounterTokens != 500 || d.LedgerTokens != 0 {
				t.Fatalf("unexpected discrepancy values: %+v", d)
			}
		}
	}
	if !found {
		t.Fatalf("expected a discrepancy for tenant %s", tid)
	}
}
