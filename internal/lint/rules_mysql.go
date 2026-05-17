package lint

import (
	"context"
	"fmt"
	"strings"

	"github.com/ddevilz/phasedb/internal/config"
	"github.com/ddevilz/phasedb/internal/db"
)

func init() {
	Register(&ruleAddColumnNotNull{})
	Register(&ruleDropColumnWithoutRollback{})
	Register(&ruleExplainFullScan{})
}

// ruleAddColumnNotNull warns (MySQL 8.0) or errors (MySQL ≤5.7) on ADD COLUMN NOT NULL without DEFAULT.
type ruleAddColumnNotNull struct{}

func (r *ruleAddColumnNotNull) Check(_ context.Context, m *config.MigrationFile, _ db.Adapter) []Finding {
	var findings []Finding
	for _, p := range m.Phases {
		sql := p.SQL
		if sql == "" {
			continue
		}
		for _, stmt := range strings.Split(sql, ";") {
			stmt = strings.TrimSpace(stmt)
			if stmt == "" {
				continue
			}
			if IsAddColumnNotNull(stmt) {
				findings = append(findings, Finding{
					Severity: SeverityWarning,
					Phase:    p.Name,
					Message:  "ADD COLUMN NOT NULL without DEFAULT — safe on MySQL 8.0+ but blocks writes on MySQL ≤5.7",
				})
			}
		}
	}
	return findings
}

// ruleDropColumnWithoutRollback warns when CONTRACT has a DROP COLUMN but no rollback_sql.
type ruleDropColumnWithoutRollback struct{}

func (r *ruleDropColumnWithoutRollback) Check(_ context.Context, m *config.MigrationFile, _ db.Adapter) []Finding {
	var findings []Finding
	for _, p := range m.Phases {
		if p.Name != "contract" {
			continue
		}
		if p.RollbackSQL != "" {
			continue
		}
		if strings.Contains(strings.ToUpper(p.SQL), "DROP COLUMN") {
			findings = append(findings, Finding{
				Severity: SeverityWarning,
				Phase:    p.Name,
				Message:  "DROP COLUMN in contract phase without rollback_sql — column loss is irreversible",
			})
		}
	}
	return findings
}

// ruleExplainFullScan warns if the backfill query would do a full table scan (access_type=ALL).
// Requires a live database adapter; skipped if adapter is nil.
type ruleExplainFullScan struct{}

func (r *ruleExplainFullScan) Check(ctx context.Context, m *config.MigrationFile, adapter db.Adapter) []Finding {
	if adapter == nil {
		return nil
	}
	var findings []Finding
	for _, p := range m.Phases {
		if p.Batch == nil {
			continue
		}
		// Replace {batch_size} with a small literal for EXPLAIN purposes
		q := strings.ReplaceAll(p.Batch.Query, "{batch_size}", "1")
		q = strings.ReplaceAll(q, "{last_id}", "0")
		result, err := adapter.RunEXPLAIN(ctx, q)
		if err != nil {
			findings = append(findings, Finding{
				Severity: SeverityWarning,
				Phase:    p.Name,
				Message:  fmt.Sprintf("EXPLAIN failed for batch query: %v", err),
			})
			continue
		}
		if result != nil && result.AccessType == "ALL" {
			findings = append(findings, Finding{
				Severity: SeverityWarning,
				Phase:    p.Name,
				Message:  "batch query EXPLAIN shows full table scan (access_type=ALL) — add an index",
			})
		}
	}
	return findings
}
