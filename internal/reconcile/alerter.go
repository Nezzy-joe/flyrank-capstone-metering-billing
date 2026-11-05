package reconcile

import (
	"context"
	"log/slog"
)

// LogAlerter emits a structured, high-severity log line when reconciliation
// fails. No paid external monitoring dependency — an operator's log pipeline or
// alerting rules can match on this event.
type LogAlerter struct {
	logger *slog.Logger
}

func NewLogAlerter(logger *slog.Logger) *LogAlerter {
	return &LogAlerter{logger: logger}
}

func (a *LogAlerter) Alert(_ context.Context, err error) {
	a.logger.Error("RECONCILIATION_FAILURE_ALERT",
		"severity", "high",
		"component", "usage-reconciliation",
		"err", err.Error())
}
