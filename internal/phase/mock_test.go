package phase_test

import (
	"context"
	"time"

	"github.com/ddevilz/phasedb/internal/db"
	"github.com/ddevilz/phasedb/internal/store"
)

// mockAdapterFull is a flexible mock that covers all Adapter methods needed by
// backfill and contract executors.
type mockAdapterFull struct {
	db.Adapter // embed for any methods not overridden

	// ExecDDL
	ddlCalled int
	ddlErr    error

	// ExecBatch
	batchResults []int64
	batchIdx     int
	batchErr     error

	// QueryScalar
	scalarResults []int64
	scalarIdx     int
	scalarErr     error

	// GetReplicaLag
	lagValue int64
	lagErr   error
}

func (m *mockAdapterFull) ExecDDL(_ context.Context, _ string, _ time.Duration) (*db.DDLResult, error) {
	m.ddlCalled++
	if m.ddlErr != nil {
		return nil, m.ddlErr
	}
	return &db.DDLResult{}, nil
}

func (m *mockAdapterFull) ExecBatch(_ context.Context, _ string, _ int) (int64, error) {
	if m.batchErr != nil {
		return 0, m.batchErr
	}
	if m.batchIdx >= len(m.batchResults) {
		return 0, nil
	}
	v := m.batchResults[m.batchIdx]
	m.batchIdx++
	return v, nil
}

func (m *mockAdapterFull) QueryScalar(_ context.Context, _ string, _ ...any) (int64, error) {
	if m.scalarErr != nil {
		return 0, m.scalarErr
	}
	if m.scalarIdx >= len(m.scalarResults) {
		return 0, nil
	}
	v := m.scalarResults[m.scalarIdx]
	m.scalarIdx++
	return v, nil
}

func (m *mockAdapterFull) GetReplicaLag(_ context.Context) (int64, error) {
	return m.lagValue, m.lagErr
}

// mockStore is a minimal in-memory store for tests.
type mockStore struct {
	store.Store // embed for any methods not overridden
	checkpoints []store.CheckpointRow
}

func (s *mockStore) InsertCheckpoint(_ context.Context, c store.CheckpointRow) error {
	s.checkpoints = append(s.checkpoints, c)
	return nil
}

func (s *mockStore) LatestCheckpoint(_ context.Context, _, _ string, _ int) (*store.CheckpointRow, error) {
	return nil, nil
}
