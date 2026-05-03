package phase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ddevilz/phasedb/internal/config"
	"github.com/ddevilz/phasedb/internal/db"
	"github.com/ddevilz/phasedb/internal/phase"
)

type mockAdapter struct {
	db.Adapter
	ddlCalled int
	ddlErr    error
	ddlResult *db.DDLResult
}

func (m *mockAdapter) ExecDDL(_ context.Context, _ string, _ time.Duration) (*db.DDLResult, error) {
	m.ddlCalled++
	if m.ddlResult != nil {
		return m.ddlResult, nil
	}
	if m.ddlErr != nil {
		return nil, m.ddlErr
	}
	return &db.DDLResult{}, nil
}

func TestExpandExecutor_CallsDDL(t *testing.T) {
	mock := &mockAdapter{}
	ex := &phase.ExpandExecutor{
		Phase:     config.Phase{Name: "expand", SQL: "ALTER TABLE t ADD COLUMN c INT NULL"},
		Migration: "V1",
	}
	if err := ex.Execute(context.Background(), mock, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.ddlCalled != 1 {
		t.Errorf("ExecDDL called %d times, want 1", mock.ddlCalled)
	}
}

func TestExpandExecutor_DDLError(t *testing.T) {
	mock := &mockAdapter{ddlErr: errors.New("lock timeout")}
	ex := &phase.ExpandExecutor{
		Phase:     config.Phase{Name: "expand", SQL: "ALTER TABLE t ADD COLUMN c INT NULL"},
		Migration: "V1",
	}
	if err := ex.Execute(context.Background(), mock, nil); err == nil {
		t.Fatal("expected error from DDL failure")
	}
}

func TestExpandExecutor_EmptySQL(t *testing.T) {
	mock := &mockAdapter{}
	ex := &phase.ExpandExecutor{
		Phase:     config.Phase{Name: "expand", SQL: ""},
		Migration: "V1",
	}
	if err := ex.Execute(context.Background(), mock, nil); err == nil {
		t.Fatal("expected error for empty SQL")
	}
	if mock.ddlCalled != 0 {
		t.Error("ExecDDL should not be called with empty SQL")
	}
}
