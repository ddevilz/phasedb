package phase_test

import (
	"context"
	"testing"

	"github.com/ddevilz/phasedb/internal/config"
	"github.com/ddevilz/phasedb/internal/db"
	"github.com/ddevilz/phasedb/internal/phase"
)

type mockAdapterGate struct {
	db.Adapter // embed for unimplemented methods
	queryResults []int64
	queryIdx     int
}

func (m *mockAdapterGate) QueryScalar(_ context.Context, _ string, _ ...any) (int64, error) {
	if m.queryIdx >= len(m.queryResults) {
		return 0, nil
	}
	v := m.queryResults[m.queryIdx]
	m.queryIdx++
	return v, nil
}

func TestGateExecutor_PassesWhenExpected(t *testing.T) {
	mock := &mockAdapterGate{
		queryResults: []int64{5, 3, 0}, // counts down to 0 (expected)
	}
	ex := &phase.GateExecutor{
		Phase: config.Phase{
			Name: "gate",
			WaitUntil: &config.GateConfig{
				Query:          "SELECT COUNT(*) FROM t WHERE c IS NULL",
				Expected:       0,
				PollIntervalMs: 1, // 1ms for fast test
				TimeoutMinutes: 1,
			},
		},
		Migration: "V1",
	}
	if err := ex.Execute(context.Background(), mock, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGateExecutor_Timeout(t *testing.T) {
	mock := &mockAdapterGate{
		queryResults: []int64{100, 100, 100, 100, 100, 100, 100, 100},
	}
	ex := &phase.GateExecutor{
		Phase: config.Phase{
			Name: "gate",
			WaitUntil: &config.GateConfig{
				Query:          "SELECT COUNT(*) FROM t WHERE c IS NULL",
				Expected:       0,
				PollIntervalMs: 1,
				TimeoutMinutes: 0, // will use default 120 min — too long for test
			},
		},
		Migration: "V1",
	}
	// Use a cancelled context to simulate timeout quickly
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	err := ex.Execute(ctx, mock, nil)
	if err == nil {
		t.Fatal("expected context error")
	}
}
