package store_test

import (
	"testing"

	"github.com/ddevilz/phasedb/internal/store"
)

func TestEventTypeIsTerminal(t *testing.T) {
	tests := []struct {
		name     string
		event    store.EventType
		terminal bool
	}{
		{"Started", store.EventStarted, false},
		{"Completed", store.EventCompleted, true},
		{"Failed", store.EventFailed, true},
		{"TimedOut", store.EventTimedOut, true},
		{"Skipped", store.EventSkipped, false},
		{"RolledBack", store.EventRolledBack, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.event.IsTerminal()
			if got != tt.terminal {
				t.Errorf("EventType(%q).IsTerminal() = %v, want %v", tt.event, got, tt.terminal)
			}
		})
	}
}

func TestEventTypeConstants(t *testing.T) {
	tests := []struct {
		name     string
		event    store.EventType
		expected string
	}{
		{"EventStarted", store.EventStarted, "PHASE_STARTED"},
		{"EventCompleted", store.EventCompleted, "PHASE_COMPLETED"},
		{"EventFailed", store.EventFailed, "PHASE_FAILED"},
		{"EventTimedOut", store.EventTimedOut, "PHASE_TIMED_OUT"},
		{"EventSkipped", store.EventSkipped, "PHASE_SKIPPED"},
		{"EventRolledBack", store.EventRolledBack, "PHASE_ROLLED_BACK"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.event) != tt.expected {
				t.Errorf("EventType constant %s = %q, want %q", tt.name, string(tt.event), tt.expected)
			}
		})
	}
}

func TestErrLockHeld(t *testing.T) {
	if store.ErrLockHeld == nil {
		t.Fatal("ErrLockHeld must not be nil")
	}
	if store.ErrLockHeld.Error() == "" {
		t.Fatal("ErrLockHeld.Error() must not be empty string")
	}
	const want = "migration lock held by another process"
	if store.ErrLockHeld.Error() != want {
		t.Errorf("ErrLockHeld.Error() = %q, want %q", store.ErrLockHeld.Error(), want)
	}
}

func TestNonTerminalEventTypes(t *testing.T) {
	nonTerminal := []store.EventType{
		store.EventStarted,
		store.EventSkipped,
	}
	for _, e := range nonTerminal {
		if e.IsTerminal() {
			t.Errorf("expected %q to be non-terminal", e)
		}
	}
}

func TestTerminalEventTypes(t *testing.T) {
	terminal := []store.EventType{
		store.EventCompleted,
		store.EventFailed,
		store.EventTimedOut,
		store.EventRolledBack,
	}
	for _, e := range terminal {
		if !e.IsTerminal() {
			t.Errorf("expected %q to be terminal", e)
		}
	}
}
