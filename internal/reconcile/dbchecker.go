package reconcile

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DBChecker compares usage_counters to the ledger in PostgreSQL. It is read-only
// (only SELECTs usage_events / usage_counters) and never mutates the ledger.
//
// It uses a FULL OUTER JOIN so BOTH directions of drift are caught:
//   - counter-only: a counter row with no matching ledger aggregate, and
//   - ledger-only:  ledger rows for a (tenant, period) with no counter row.
//
// It keyset-paginates over (tenant_id, period) in batches of batchSize so every
// tenant/period is examined on each run, with no row skipped or seen twice.
type DBChecker struct {
	pool      *pgxpool.Pool
	batchSize int
}

func NewDBChecker(pool *pgxpool.Pool, batchSize int) *DBChecker {
	if batchSize <= 0 {
		batchSize = 100
	}
	return &DBChecker{pool: pool, batchSize: batchSize}
}

const discrepancyQuery = `
WITH ledger AS (
    SELECT tenant_id,
           to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM') AS period,
           SUM(CASE WHEN type = 'api_call' THEN quantity ELSE 0 END)::bigint AS api_calls,
           SUM(input_tokens + cached_input_tokens + output_tokens + reasoning_tokens)::bigint AS tokens
    FROM usage_events
    GROUP BY tenant_id, to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM')
)
SELECT
    COALESCE(uc.tenant_id::text, le.tenant_id::text) AS tenant_id,
    COALESCE(uc.period, le.period)                   AS period,
    COALESCE(uc.api_calls_used, 0)                   AS counter_api,
    COALESCE(le.api_calls, 0)                        AS ledger_api,
    COALESCE(uc.ai_tokens_used, 0)                   AS counter_tokens,
    COALESCE(le.tokens, 0)                           AS ledger_tokens
FROM usage_counters uc
FULL OUTER JOIN ledger le
  ON le.tenant_id = uc.tenant_id AND le.period = uc.period
WHERE (COALESCE(uc.api_calls_used, 0) <> COALESCE(le.api_calls, 0)
    OR COALESCE(uc.ai_tokens_used, 0) <> COALESCE(le.tokens, 0))
  AND (COALESCE(uc.tenant_id::text, le.tenant_id::text), COALESCE(uc.period, le.period)) > ($1, $2)
ORDER BY tenant_id, period
LIMIT $3`

// Check paginates through the entire (tenant, period) space each run and returns
// every discrepancy. Keyset pagination (ordered by tenant_id, period; cursor is
// the last row seen) guarantees progress without skips or repeats.
func (c *DBChecker) Check(ctx context.Context) ([]Discrepancy, error) {
	var out []Discrepancy
	lastTenant, lastPeriod := "", ""

	for {
		rows, err := c.pool.Query(ctx, discrepancyQuery, lastTenant, lastPeriod, c.batchSize)
		if err != nil {
			return nil, err
		}
		page := 0
		for rows.Next() {
			var d Discrepancy
			if err := rows.Scan(&d.TenantID, &d.Period, &d.CounterAPICalls, &d.LedgerAPICalls, &d.CounterTokens, &d.LedgerTokens); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, d)
			lastTenant, lastPeriod = d.TenantID, d.Period
			page++
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		if page < c.batchSize {
			break // last (short) page reached
		}
	}
	return out, nil
}
