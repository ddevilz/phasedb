package phase_test

import (
	"context"
	"testing"

	"github.com/ddevilz/phasedb/internal/config"
	"github.com/ddevilz/phasedb/internal/phase"
)

// TestContractExecutor_ExecutesAllStatements verifies that ExecDDL is called once
// per non-empty SQL statement separated by semicolons.
func TestContractExecutor_ExecutesAllStatements(t *testing.T) {
	adapter := &mockAdapterFull{}
	s := &mockStore{}

	ex := &phase.ContractExecutor{
		Phase: config.Phase{
			Name: "contract",
			SQL:  "ALTER TABLE t DROP COLUMN a;ALTER TABLE t DROP COLUMN b",
		},
		Migration:     "V1",
		AttemptNumber: 1,
	}

	if err := ex.Execute(context.Background(), adapter, s); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if adapter.ddlCalled != 2 {
		t.Errorf("ExecDDL called %d times, want 2", adapter.ddlCalled)
	}
}

// TestContractExecutor_SkipsEmptyStatements verifies that trailing semicolons
// (which produce empty strings after splitting) do not result in extra ExecDDL
// calls and do not corrupt the statement index.
func TestContractExecutor_SkipsEmptyStatements(t *testing.T) {
	adapter := &mockAdapterFull{}
	s := &mockStore{}

	// Trailing semicolon produces ["ALTER TABLE t DROP COLUMN a", ""] after split
	ex := &phase.ContractExecutor{
		Phase: config.Phase{
			Name: "contract",
			SQL:  "ALTER TABLE t DROP COLUMN a;",
		},
		Migration:     "V1",
		AttemptNumber: 1,
	}

	if err := ex.Execute(context.Background(), adapter, s); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if adapter.ddlCalled != 1 {
		t.Errorf("ExecDDL called %d times, want 1 (empty statement from trailing semicolon must be skipped)", adapter.ddlCalled)
	}
}

// TestContractExecutor_CheckpointIndexMatchesRealIdx verifies that the checkpoint
// StatementIndex uses the realIdx counter (not the raw slice index), so that
// empty statements from trailing semicolons do not shift the index.
func TestContractExecutor_CheckpointIndexMatchesRealIdx(t *testing.T) {
	adapter := &mockAdapterFull{}
	s := &mockStore{}

	// Two real statements plus a trailing semicolon
	ex := &phase.ContractExecutor{
		Phase: config.Phase{
			Name: "contract",
			SQL:  "ALTER TABLE t DROP COLUMN a;ALTER TABLE t DROP COLUMN b;",
		},
		Migration:     "V1",
		AttemptNumber: 1,
	}

	if err := ex.Execute(context.Background(), adapter, s); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if adapter.ddlCalled != 2 {
		t.Errorf("ExecDDL called %d times, want 2", adapter.ddlCalled)
	}
	if len(s.checkpoints) != 2 {
		t.Fatalf("got %d checkpoints, want 2", len(s.checkpoints))
	}
	// First checkpoint should have StatementIndex 0, second should have 1
	if s.checkpoints[0].StatementIndex != 0 {
		t.Errorf("checkpoint[0].StatementIndex = %d, want 0", s.checkpoints[0].StatementIndex)
	}
	if s.checkpoints[1].StatementIndex != 1 {
		t.Errorf("checkpoint[1].StatementIndex = %d, want 1", s.checkpoints[1].StatementIndex)
	}
}
