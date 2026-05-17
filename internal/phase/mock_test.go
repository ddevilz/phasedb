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
	batchResults         []int64
	batchIdx             int
	batchErr             error
	capturedBatchQueries []string // every query string passed to ExecBatch

	// QueryScalar
	scalarResults []int64
	scalarIdx     int
	scalarErr     error
	scalarErrs    []error // per-call error override; indexed by call order (nil = no error)

	// GetReplicaLag
	lagValue     int64
	lagErr       error
	lagCallCount int // counts invocations
}

func (m *mockAdapterFull) ExecDDL(_ context.Context, _ string, _ time.Duration) (*db.DDLResult, error) {
	m.ddlCalled++
	if m.ddlErr != nil {
		return nil, m.ddlErr
	}
	return &db.DDLResult{}, nil
}

func (m *mockAdapterFull) ExecBatch(_ context.Context, q string, _ int) (int64, error) {
	m.capturedBatchQueries = append(m.capturedBatchQueries, q)
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
	idx := m.scalarIdx
	m.scalarIdx++
	// Per-call error takes priority over global scalarErr
	if idx < len(m.scalarErrs) && m.scalarErrs[idx] != nil {
		return 0, m.scalarErrs[idx]
	}
	if m.scalarErr != nil {
		return 0, m.scalarErr
	}
	if idx >= len(m.scalarResults) {
		return 0, nil
	}
	return m.scalarResults[idx], nil
}

func (m *mockAdapterFull) GetReplicaLag(_ context.Context) (int64, error) {
	m.lagCallCount++
	return m.lagValue, m.lagErr
}

// mockStore is a minimal in-memory store for tests.
type mockStore struct {
	store.Store // embed for any methods not overridden
	checkpoints        []store.CheckpointRow
	checkpointToReturn *store.CheckpointRow // returned by LatestCheckpoint; nil = no prior checkpoint
}

func (s *mockStore) InsertCheckpoint(_ context.Context, c store.CheckpointRow) error {
	s.checkpoints = append(s.checkpoints, c)
	return nil
}

func (s *mockStore) LatestCheckpoint(_ context.Context, _, _ string, _ int) (*store.CheckpointRow, error) {
	return s.checkpointToReturn, nil
}
