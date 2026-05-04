package lint_test

import (
	"context"
	"testing"

	"github.com/ddevilz/phasedb/internal/config"
	"github.com/ddevilz/phasedb/internal/lint"
)

func TestGateRequiresWaitUntil(t *testing.T) {
	m := &config.MigrationFile{
		Name:     "V1",
		Database: "mysql",
		Phases:   []config.Phase{{Name: "gate"}},
	}
	l := &lint.Linter{Migration: m}
	result := l.Run(context.Background())
	if !result.HasError {
		t.Fatal("expected ERROR for gate missing wait_until")
	}
}

func TestRollbackSQLRequired(t *testing.T) {
	m := &config.MigrationFile{
		Name:     "V1",
		Database: "mysql",
		Phases:   []config.Phase{{Name: "expand", OnFailure: "rollback"}},
	}
	l := &lint.Linter{Migration: m}
	result := l.Run(context.Background())
	if !result.HasError {
		t.Fatal("expected ERROR for missing rollback_sql")
	}
}

func TestBatchSizePlaceholderMissing(t *testing.T) {
	m := &config.MigrationFile{
		Name:     "V1",
		Database: "mysql",
		Phases: []config.Phase{{
			Name: "backfill",
			Batch: &config.BatchConfig{
				Query:    "UPDATE t SET c = 1 WHERE c IS NULL",
				Size:     100,
				DoneWhen: "SELECT COUNT(*) FROM t WHERE c IS NULL",
			},
		}},
	}
	l := &lint.Linter{Migration: m}
	result := l.Run(context.Background())
	if !result.HasError {
		t.Fatal("expected ERROR for missing {batch_size}")
	}
}

func TestBatchIdempotency_Valid(t *testing.T) {
	m := &config.MigrationFile{
		Name:     "V1",
		Database: "mysql",
		Phases: []config.Phase{{
			Name: "backfill",
			Batch: &config.BatchConfig{
				Query:    "UPDATE t SET col = 1 WHERE col IS NULL LIMIT {batch_size}",
				Size:     100,
				DoneWhen: "SELECT COUNT(*) FROM t WHERE col IS NULL",
			},
		}},
	}
	l := &lint.Linter{Migration: m}
	result := l.Run(context.Background())
	for _, f := range result.Findings {
		if f.Severity == lint.SeverityError {
			t.Errorf("unexpected error: %s", f.Message)
		}
	}
}

func TestBatchIdempotency_Missing(t *testing.T) {
	m := &config.MigrationFile{
		Name:     "V1",
		Database: "mysql",
		Phases: []config.Phase{{
			Name: "backfill",
			Batch: &config.BatchConfig{
				Query:    "UPDATE t SET col = 1 LIMIT {batch_size}",
				Size:     100,
				DoneWhen: "SELECT COUNT(*) FROM t WHERE col IS NULL",
			},
		}},
	}
	l := &lint.Linter{Migration: m}
	result := l.Run(context.Background())
	if !result.HasError {
		t.Fatal("expected ERROR for backfill WHERE clause missing idempotency condition")
	}
}

func TestNoUserLimitInBatch(t *testing.T) {
	m := &config.MigrationFile{
		Name:     "V1",
		Database: "mysql",
		Phases: []config.Phase{{
			Name: "backfill",
			Batch: &config.BatchConfig{
				Query:    "UPDATE t SET col = 1 WHERE col IS NULL LIMIT 100",
				Size:     100,
				DoneWhen: "SELECT COUNT(*) FROM t WHERE col IS NULL",
			},
		}},
	}
	l := &lint.Linter{Migration: m}
	result := l.Run(context.Background())
	if !result.HasError {
		t.Fatal("expected ERROR for literal LIMIT in batch query")
	}
}
