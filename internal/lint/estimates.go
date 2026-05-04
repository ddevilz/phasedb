package lint

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/ddevilz/phasedb/internal/config"
	"github.com/ddevilz/phasedb/internal/db"
)

// safeIdentifier matches MySQL table/column identifiers: letters, digits, underscores only.
var safeIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// DryRunEstimate holds estimated migration metrics.
type DryRunEstimate struct {
	TableName             string
	RowCount              int64
	EstimatedBackfillSecs float64
	GateTimeoutAdequate   bool
	GateTimeoutWarning    string
}

// Estimate computes a dry-run estimate for a migration.
// Requires a live adapter; returns an error if the adapter is nil.
func Estimate(ctx context.Context, m *config.MigrationFile, adapter db.Adapter) ([]DryRunEstimate, error) {
	if adapter == nil {
		return nil, fmt.Errorf("estimate requires a live database connection")
	}

	var estimates []DryRunEstimate

	for _, p := range m.Phases {
		if p.Batch == nil {
			continue
		}

		table := extractTableName(p.Batch.Query)
		if table == "" {
			continue
		}
		if !safeIdentifier.MatchString(table) {
			return nil, fmt.Errorf("table name %q extracted from batch query contains unsafe characters", table)
		}

		rowCount, err := adapter.QueryScalar(ctx, fmt.Sprintf("SELECT COUNT(*) FROM `%s`", table))
		if err != nil {
			return nil, fmt.Errorf("row count for %s: %w", table, err)
		}

		// Estimated duration: rows / (batch_size / (delay_ms / 1000.0))
		batchSize := float64(p.Batch.Size)
		delayS := float64(p.Batch.DelayMs) / 1000.0
		if delayS <= 0 {
			delayS = 0.001 // avoid divide-by-zero; near-zero delay
		}
		batchesPerSec := 1.0 / delayS
		rowsPerSec := batchSize * batchesPerSec
		var estSecs float64
		if rowsPerSec > 0 {
			estSecs = float64(rowCount) / rowsPerSec
		}

		est := DryRunEstimate{
			TableName:             table,
			RowCount:              rowCount,
			EstimatedBackfillSecs: estSecs,
			GateTimeoutAdequate:   true,
		}

		// Warn if gate timeout < estimate * 1.5
		for _, gp := range m.Phases {
			if gp.Name == "gate" && gp.WaitUntil != nil {
				timeoutSecs := float64(gp.WaitUntil.TimeoutMinutes) * 60
				if timeoutSecs > 0 && timeoutSecs < estSecs*1.5 {
					est.GateTimeoutAdequate = false
					est.GateTimeoutWarning = fmt.Sprintf(
						"gate timeout_minutes=%d (%.0fs) < estimated backfill duration * 1.5 (%.0fs)",
						gp.WaitUntil.TimeoutMinutes, timeoutSecs, estSecs*1.5,
					)
				}
			}
		}

		estimates = append(estimates, est)
	}

	return estimates, nil
}

// extractTableName extracts the table name from an UPDATE statement.
// Only handles simple "UPDATE <table> SET ..." form.
func extractTableName(query string) string {
	q := strings.TrimSpace(strings.ToUpper(query))
	if !strings.HasPrefix(q, "UPDATE") {
		return ""
	}
	rest := strings.TrimSpace(query[6:])
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return ""
	}
	return strings.Trim(fields[0], "`\"")
}
