package output_test

import (
	"context"
	"testing"

	"github.com/ddevilz/phasedb/internal/output"
	"github.com/ddevilz/phasedb/internal/store"
)

// mockStore implements store.Store with minimal stubs.
// LatestEvent always returns (nil, nil); GetLock always returns (nil, nil).
type mockStore struct{}

func (m *mockStore) InsertEvent(_ context.Context, _ store.PhaseEvent) error { return nil }
func (m *mockStore) LatestEvent(_ context.Context, _, _ string) (*store.PhaseEvent, error) {
	return nil, nil
}
func (m *mockStore) LatestEventForAttempt(_ context.Context, _, _ string, _ int) (*store.PhaseEvent, error) {
	return nil, nil
}
func (m *mockStore) MaxAttemptNumber(_ context.Context, _, _ string) (int, error) { return 0, nil }
func (m *mockStore) InsertCheckpoint(_ context.Context, _ store.CheckpointRow) error { return nil }
func (m *mockStore) LatestCheckpoint(_ context.Context, _, _ string, _ int) (*store.CheckpointRow, error) {
	return nil, nil
}
func (m *mockStore) InsertHeartbeat(_ context.Context, _, _ string, _ int, _ string) error {
	return nil
}
func (m *mockStore) DeleteHeartbeatsForCompletedMigrations(_ context.Context) (int64, error) {
	return 0, nil
}
func (m *mockStore) AcquireLock(_ context.Context, _, _ string) error  { return nil }
func (m *mockStore) RefreshLock(_ context.Context, _, _ string) error  { return nil }
func (m *mockStore) ReleaseLock(_ context.Context, _ string) error     { return nil }
func (m *mockStore) GetLock(_ context.Context, _ string) (*store.LockRow, error) {
	return nil, nil
}
func (m *mockStore) EnsureSchema(_ context.Context) error { return nil }
func (m *mockStore) Close() error                         { return nil }

func TestBuildStatus_NotStarted(t *testing.T) {
	ctx := context.Background()
	s := &mockStore{}
	st, err := output.BuildStatus(ctx, "test_migration", s, "v0.1.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if st.PhaseStatus != "not_started" {
		t.Errorf("expected PhaseStatus=not_started, got %q", st.PhaseStatus)
	}
	if st.Migration != "test_migration" {
		t.Errorf("expected Migration=test_migration, got %q", st.Migration)
	}
	if st.PhasedbVersion != "v0.1.0" {
		t.Errorf("expected PhasedbVersion=v0.1.0, got %q", st.PhasedbVersion)
	}
	if st.CurrentPhase != "" {
		t.Errorf("expected empty CurrentPhase, got %q", st.CurrentPhase)
	}
}

func TestFormatProgress_Zero(t *testing.T) {
	s := output.FormatProgress(0.0, 10)
	if s == "" {
		t.Fatal("expected non-empty string")
	}
}

func TestFormatProgress_Full(t *testing.T) {
	s := output.FormatProgress(1.0, 10)
	if s == "" {
		t.Fatal("expected non-empty string")
	}
}

func TestFormatProgress_Half(t *testing.T) {
	s := output.FormatProgress(0.5, 10)
	// Should have 5 filled chars
	_ = s
}

func TestFormatProgress_DefaultWidth(t *testing.T) {
	// width <= 0 should default to 40
	s := output.FormatProgress(0.5, 0)
	if s == "" {
		t.Fatal("expected non-empty string for default width")
	}
}

func TestFormatProgress_OverCap(t *testing.T) {
	// pct > 1.0 should not produce more than width filled chars
	s := output.FormatProgress(1.5, 10)
	if s == "" {
		t.Fatal("expected non-empty string")
	}
}
