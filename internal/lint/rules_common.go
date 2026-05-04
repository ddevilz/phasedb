package lint

import (
	"context"
	"fmt"
	"strings"

	"github.com/ddevilz/phasedb/internal/config"
	"github.com/ddevilz/phasedb/internal/db"
)

func init() {
	Register(&ruleGateRequiresWaitUntil{})
	Register(&ruleRollbackSQLRequired{})
	Register(&ruleBatchSizePlaceholder{})
	Register(&ruleBackfillIdempotency{})
	Register(&ruleNoUserLimitInBatch{})
}

// ruleGateRequiresWaitUntil errors if a gate phase has no wait_until block.
type ruleGateRequiresWaitUntil struct{}

func (r *ruleGateRequiresWaitUntil) Check(_ context.Context, m *config.MigrationFile, _ db.Adapter) []Finding {
	var findings []Finding
	for _, p := range m.Phases {
		if p.Name == "gate" && p.WaitUntil == nil {
			findings = append(findings, Finding{SeverityError, p.Name, "gate phase missing wait_until block"})
		}
	}
	return findings
}

// ruleRollbackSQLRequired errors if on_failure:rollback is set but rollback_sql is absent.
type ruleRollbackSQLRequired struct{}

func (r *ruleRollbackSQLRequired) Check(_ context.Context, m *config.MigrationFile, _ db.Adapter) []Finding {
	var findings []Finding
	for _, p := range m.Phases {
		if p.OnFailure == "rollback" && p.RollbackSQL == "" {
			findings = append(findings, Finding{SeverityError, p.Name,
				fmt.Sprintf("on_failure: rollback requires rollback_sql in phase %q", p.Name)})
		}
	}
	return findings
}

// ruleBatchSizePlaceholder errors if a backfill query is missing {batch_size}.
type ruleBatchSizePlaceholder struct{}

func (r *ruleBatchSizePlaceholder) Check(_ context.Context, m *config.MigrationFile, _ db.Adapter) []Finding {
	var findings []Finding
	for _, p := range m.Phases {
		if p.Batch != nil && !strings.Contains(p.Batch.Query, "{batch_size}") {
			findings = append(findings, Finding{SeverityError, p.Name, "batch query missing {batch_size} placeholder"})
		}
	}
	return findings
}

// ruleBackfillIdempotency errors if the backfill WHERE clause doesn't exclude already-processed rows.
type ruleBackfillIdempotency struct{}

func (r *ruleBackfillIdempotency) Check(_ context.Context, m *config.MigrationFile, _ db.Adapter) []Finding {
	var findings []Finding
	for _, p := range m.Phases {
		if p.Batch == nil {
			continue
		}
		q := strings.ToUpper(p.Batch.Query)
		setIdx := strings.Index(q, "SET ")
		whereIdx := strings.Index(q, "WHERE ")
		if setIdx < 0 {
			continue
		}
		if whereIdx < 0 {
			findings = append(findings, Finding{SeverityError, p.Name,
				"backfill query missing WHERE clause — without WHERE all rows are processed on every batch"})
			continue
		}
		if setIdx >= whereIdx {
			continue // non-standard clause order; skip — structural check only
		}
		setClause := q[setIdx+4 : whereIdx]
		whereClause := q[whereIdx+6:]
		eqIdx := strings.Index(setClause, " =")
		if eqIdx < 0 {
			continue
		}
		col := strings.TrimSpace(setClause[:eqIdx])
		if !strings.Contains(whereClause, col+" IS NULL") &&
			!strings.Contains(whereClause, col+"=") &&
			!strings.Contains(whereClause, col+" =") {
			findings = append(findings, Finding{SeverityError, p.Name,
				fmt.Sprintf("backfill WHERE clause must exclude already-processed rows: %s IS NULL or %s = <unprocessed_sentinel>", col, col)})
		}
	}
	return findings
}

// ruleNoUserLimitInBatch errors if the backfill query contains a user-supplied LIMIT or ORDER BY
// (the runner appends LIMIT via {batch_size}; a hand-written LIMIT is a mistake).
type ruleNoUserLimitInBatch struct{}

func (r *ruleNoUserLimitInBatch) Check(_ context.Context, m *config.MigrationFile, _ db.Adapter) []Finding {
	var findings []Finding
	for _, p := range m.Phases {
		if p.Batch == nil {
			continue
		}
		q := strings.ToUpper(p.Batch.Query)
		// Remove the valid "LIMIT {batch_size}" pattern before checking for bare LIMIT
		noPlaceholder := strings.ReplaceAll(q, "LIMIT {BATCH_SIZE}", "")
		if strings.Contains(noPlaceholder, "LIMIT") {
			findings = append(findings, Finding{SeverityError, p.Name, "backfill query must not contain a literal LIMIT clause; use {batch_size} placeholder instead"})
		}
		if strings.Contains(q, "ORDER BY") {
			findings = append(findings, Finding{SeverityError, p.Name, "backfill query must not contain ORDER BY"})
		}
	}
	return findings
}
