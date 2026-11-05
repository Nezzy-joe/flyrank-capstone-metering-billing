package reconcile

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DBChecker compares usage_counters to the ledger in PostgreSQL. It is read-only;
// it never touches usage_events.
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

// Check returns up to batchSize counter/ledger mismatches. The join aggregates
// the ledger by (tenant, UTC month) and keeps only rows where a counter disagrees.
func (c *DBChecker) Check(ctx context.Context) ([]Discrepancy, error) {
	rows, err := c.pool.Query(ctx,
		`SELECT uc.tenant_id::text, uc.period,
		        uc.api_calls_used, COALESCE(le.api_calls, 0),
		        uc.ai_tokens_used, COALESCE(le.tokens, 0)
		 FROM usage_counters uc
		 LEFT JOIN (
		   SELECT tenant_id,
		          to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM') AS period,
		          SUM(CASE WHEN type = 'api_call' THEN quantity ELSE 0 END) AS api_calls,
		          SUM(input_tokens + cached_input_tokens + output_tokens + reasoning_tokens) AS tokens
		   FROM usage_events
		   GROUP BY tenant_id, to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM')
		 ) le ON le.tenant_id = uc.tenant_id AND le.period = uc.period
		 WHERE uc.api_calls_used <> COALESCE(le.api_calls, 0)
		    OR uc.ai_tokens_used <> COALESCE(le.tokens, 0)
		 ORDER BY uc.tenant_id, uc.period
		 LIMIT $1`, c.batchSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Discrepancy
	for rows.Next() {
		var d Discrepancy
		if err := rows.Scan(&d.TenantID, &d.Period, &d.CounterAPICalls, &d.LedgerAPICalls, &d.CounterTokens, &d.LedgerTokens); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
