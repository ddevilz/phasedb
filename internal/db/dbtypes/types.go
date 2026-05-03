// Package dbtypes contains shared types used by both the db adapter interface
// and concrete driver implementations, avoiding import cycles.
package dbtypes

type DDLResult struct {
	IsAlreadyApplied bool
}

type ColumnDef struct {
	Name       string
	DataType   string
	IsNullable bool
	Default    *string
}

type EXPLAINResult struct {
	AccessType string // "ALL" = full scan
}

type DDLError struct {
	Err         error
	IsRetryable bool
}

func (e *DDLError) Error() string { return e.Err.Error() }
func (e *DDLError) Unwrap() error { return e.Err }
