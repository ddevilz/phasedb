package runner_test

import (
	"testing"
	"time"

	"github.com/ddevilz/phasedb/internal/runner"
	"github.com/ddevilz/phasedb/internal/store"
)

func TestDetermineState_Pending(t *testing.T) {
	state := runner.DetermineState(nil, nil)
	if state != runner.StatePending {
		t.Errorf("got %v, want StatePending", state)
	}
}

func TestDetermineState_Completed(t *testing.T) {
	ev := &store.PhaseEvent{EventType: store.EventCompleted}
	state := runner.DetermineState(ev, nil)
	if state != runner.StateCompleted {
		t.Errorf("got %v, want StateCompleted", state)
	}
}

func TestDetermineState_RunningWithLiveLock(t *testing.T) {
	ev := &store.PhaseEvent{EventType: store.EventStarted}
	lock := &store.LockRow{ExpiresAt: time.Now().UTC().Add(60 * time.Second)}
	state := runner.DetermineState(ev, lock)
	if state != runner.StateRunning {
		t.Errorf("got %v, want StateRunning", state)
	}
}

func TestDetermineState_OrphanedNoLock(t *testing.T) {
	ev := &store.PhaseEvent{EventType: store.EventStarted}
	state := runner.DetermineState(ev, nil)
	if state != runner.StateOrphaned {
		t.Errorf("got %v, want StateOrphaned", state)
	}
}

func TestDetermineState_OrphanedExpiredLock(t *testing.T) {
	ev := &store.PhaseEvent{EventType: store.EventStarted}
	lock := &store.LockRow{ExpiresAt: time.Now().UTC().Add(-5 * time.Second)}
	state := runner.DetermineState(ev, lock)
	if state != runner.StateOrphaned {
		t.Errorf("got %v, want StateOrphaned", state)
	}
}

func TestCheckStateGate_CompletedNoResume(t *testing.T) {
	if err := runner.CheckStateGate(runner.StateCompleted, false, nil); err != nil {
		t.Errorf("completed should not error: %v", err)
	}
}

func TestCheckStateGate_FailedRequiresResume(t *testing.T) {
	if err := runner.CheckStateGate(runner.StateFailed, false, nil); err == nil {
		t.Fatal("expected ErrRequiresResume")
	}
}

func TestCheckStateGate_FailedWithResumeMode(t *testing.T) {
	if err := runner.CheckStateGate(runner.StateFailed, true, nil); err != nil {
		t.Errorf("resume mode should allow failed phase: %v", err)
	}
}
