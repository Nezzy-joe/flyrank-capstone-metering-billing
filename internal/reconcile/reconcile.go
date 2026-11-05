// Package reconcile runs a background job that checks the fast usage_counters
// against the immutable usage_events ledger and reports any drift. It never
// writes to usage_events (the ledger is append-only, truth); it only reads and
// alerts. Collaborators are injected as interfaces so the loop is fully testable.
package reconcile

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Discrepancy is a counter/ledger mismatch for one tenant+period.
type Discrepancy struct {
	TenantID        string
	Period          string
	CounterAPICalls int64
	LedgerAPICalls  int64
	CounterTokens   int64
	LedgerTokens    int64
}

// Checker performs one reconciliation pass and returns any discrepancies.
type Checker interface {
	Check(ctx context.Context) ([]Discrepancy, error)
}

// Alerter is notified on either alert condition:
//   - a reconciliation run that ultimately fails after retry exhaustion, or
//   - an actual counter/ledger discrepancy detected by a successful run.
//
// In both cases it receives a describing error.
type Alerter interface {
	Alert(ctx context.Context, err error)
}

// Reconciler runs Checker on an interval with bounded retry + backoff.
type Reconciler struct {
	checker  Checker
	alerter  Alerter
	logger   *slog.Logger
	interval time.Duration

	// retry policy (exported-for-test via same package)
	maxRetries  int
	baseBackoff time.Duration
}

// New builds a Reconciler with a sane default retry policy.
func New(checker Checker, alerter Alerter, logger *slog.Logger, interval time.Duration) *Reconciler {
	return &Reconciler{
		checker:     checker,
		alerter:     alerter,
		logger:      logger,
		interval:    interval,
		maxRetries:  3,
		baseBackoff: time.Second,
	}
}

// Run executes a pass immediately, then every interval, until ctx is cancelled.
func (r *Reconciler) Run(ctx context.Context) {
	r.logger.Info("reconciler started", "interval", r.interval.String())
	r.runOnce(ctx)

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			r.logger.Info("reconciler stopped")
			return
		case <-ticker.C:
			r.runOnce(ctx)
		}
	}
}

func (r *Reconciler) runOnce(ctx context.Context) {
	discs, err := r.checkWithRetry(ctx)
	if err != nil {
		// Retries exhausted (or cancelled): emit a clear failure alert.
		if ctx.Err() == nil {
			r.alerter.Alert(ctx, err)
		}
		return
	}
	if len(discs) == 0 {
		r.logger.Info("reconciliation clean", "discrepancies", 0)
		return
	}
	for _, d := range discs {
		r.logger.Warn("usage discrepancy detected",
			"tenant", d.TenantID, "period", d.Period,
			"counter_api_calls", d.CounterAPICalls, "ledger_api_calls", d.LedgerAPICalls,
			"counter_tokens", d.CounterTokens, "ledger_tokens", d.LedgerTokens)
	}
	// A real counter/ledger drift is itself an alert condition — not just query failure.
	r.alerter.Alert(ctx, fmt.Errorf("reconciliation found %d counter/ledger discrepanc(ies)", len(discs)))
}

// checkWithRetry retries transient Checker errors with exponential backoff.
func (r *Reconciler) checkWithRetry(ctx context.Context) ([]Discrepancy, error) {
	var lastErr error
	backoff := r.baseBackoff
	for attempt := 0; attempt <= r.maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
		}
		discs, err := r.checker.Check(ctx)
		if err == nil {
			return discs, nil
		}
		lastErr = err
		r.logger.Warn("reconciliation attempt failed", "attempt", attempt+1, "err", err)
	}
	return nil, fmt.Errorf("reconciliation failed after %d attempts: %w", r.maxRetries+1, lastErr)
}
