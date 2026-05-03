package config_test

import (
	"strings"
	"testing"

	"github.com/ddevilz/phasedb/internal/config"
)

func TestParseMigration_Valid(t *testing.T) {
	yaml := `
migration: V23_test
database: mydb
phases:
  - name: expand
    sql: ALTER TABLE users ADD COLUMN new_col INT;
`
	m, err := config.ParseMigration([]byte(yaml))
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if m.Name != "V23_test" {
		t.Errorf("expected Name=%q, got %q", "V23_test", m.Name)
	}
	if len(m.Phases) != 1 {
		t.Errorf("expected 1 phase, got %d", len(m.Phases))
	}
	if m.Phases[0].Name != "expand" {
		t.Errorf("expected phase name=%q, got %q", "expand", m.Phases[0].Name)
	}
}

func TestParseMigration_MissingName(t *testing.T) {
	yaml := `
database: mydb
phases:
  - name: expand
    sql: ALTER TABLE users ADD COLUMN new_col INT;
`
	_, err := config.ParseMigration([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for missing migration name, got nil")
	}
	if !strings.Contains(err.Error(), "migration name") {
		t.Errorf("expected error about migration name, got: %v", err)
	}
}

func TestParseMigration_MissingDatabase(t *testing.T) {
	yaml := `
migration: V23_test
phases:
  - name: expand
    sql: ALTER TABLE users ADD COLUMN new_col INT;
`
	_, err := config.ParseMigration([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for missing database field, got nil")
	}
	if !strings.Contains(err.Error(), "database") {
		t.Errorf("expected error about database field, got: %v", err)
	}
}

func TestParseMigration_GateMissingWaitUntil(t *testing.T) {
	yaml := `
migration: V23_gate_test
database: mydb
phases:
  - name: gate
    sql: SELECT 1;
`
	_, err := config.ParseMigration([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for gate phase missing wait_until, got nil")
	}
	if !strings.Contains(err.Error(), "wait_until") {
		t.Errorf("expected error about wait_until, got: %v", err)
	}
}

func TestParseMigration_BackfillMissingBatchSize(t *testing.T) {
	yaml := `
migration: V23_backfill_test
database: mydb
phases:
  - name: backfill
    batch:
      query: "UPDATE users SET col = 1 WHERE id > 0 LIMIT {batch_size}"
      size: 0
`
	_, err := config.ParseMigration([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for backfill with batch.size == 0, got nil")
	}
	if !strings.Contains(err.Error(), "batch.size") {
		t.Errorf("expected error about batch.size, got: %v", err)
	}
}

func TestParseMigration_BackfillMissingBatchPlaceholder(t *testing.T) {
	yaml := `
migration: V23_backfill_placeholder_test
database: mydb
phases:
  - name: backfill
    batch:
      query: "UPDATE users SET col = 1 WHERE id > 0 LIMIT 100"
      size: 500
`
	_, err := config.ParseMigration([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for backfill batch query missing {batch_size}, got nil")
	}
	if !strings.Contains(err.Error(), "{batch_size}") {
		t.Errorf("expected error about {batch_size} placeholder, got: %v", err)
	}
}

func TestParseMigration_BackfillMissingBatchBlock(t *testing.T) {
	yaml := `
migration: V23_backfill_nobatch
database: mydb
phases:
  - name: backfill
    sql: UPDATE users SET col = 1;
`
	_, err := config.ParseMigration([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for backfill with no batch block, got nil")
	}
	if !strings.Contains(err.Error(), "batch") {
		t.Errorf("expected error about batch block, got: %v", err)
	}
}

func TestParseMigration_OnFailureRollbackRequiresRollbackSQL(t *testing.T) {
	yaml := `
migration: V23_rollback_test
database: mydb
phases:
  - name: expand
    sql: ALTER TABLE users ADD COLUMN new_col INT;
    on_failure: rollback
`
	_, err := config.ParseMigration([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for on_failure: rollback without rollback_sql, got nil")
	}
	if !strings.Contains(err.Error(), "rollback_sql") {
		t.Errorf("expected error about rollback_sql, got: %v", err)
	}
}

func TestOnFailurePrecedence_CLIOverridesYAML(t *testing.T) {
	yaml := `
migration: V23_override_test
database: mydb
phases:
  - name: backfill
    on_failure: rollback
    rollback_sql: "ALTER TABLE users DROP COLUMN new_col;"
    batch:
      query: "UPDATE users SET col = 1 WHERE id > 0 LIMIT {batch_size}"
      size: 500
      delay_ms: 100
`
	m, err := config.ParseMigration([]byte(yaml))
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if m.Phases[0].OnFailure != "rollback" {
		t.Errorf("expected on_failure=%q before override, got %q", "rollback", m.Phases[0].OnFailure)
	}

	m.ApplyCLIOverrides(config.CLIOverrides{OnFailure: "none"})

	if m.Phases[0].OnFailure != "none" {
		t.Errorf("expected on_failure=%q after override, got %q", "none", m.Phases[0].OnFailure)
	}
}

func TestOnFailurePrecedence_EmptyCLIDoesNotOverride(t *testing.T) {
	yaml := `
migration: V23_no_override_test
database: mydb
phases:
  - name: backfill
    on_failure: rollback
    rollback_sql: "ALTER TABLE users DROP COLUMN new_col;"
    batch:
      query: "UPDATE users SET col = 1 WHERE id > 0 LIMIT {batch_size}"
      size: 500
`
	m, err := config.ParseMigration([]byte(yaml))
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	m.ApplyCLIOverrides(config.CLIOverrides{OnFailure: ""})

	if m.Phases[0].OnFailure != "rollback" {
		t.Errorf("expected on_failure to remain %q after empty override, got %q", "rollback", m.Phases[0].OnFailure)
	}
}

func TestParseMigration_InvalidYAML(t *testing.T) {
	bad := `not: [valid: yaml`
	_, err := config.ParseMigration([]byte(bad))
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}
