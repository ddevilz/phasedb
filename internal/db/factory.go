package db

import (
	"fmt"
	"strings"

	"github.com/ddevilz/phasedb/internal/db/mysql"
)

// NewAdapter returns an Adapter for the given DSN.
// Supported schemes: mysql://
func NewAdapter(dsn string) (Adapter, error) {
	switch {
	case strings.HasPrefix(dsn, "mysql://"):
		return mysql.New(dsn)
	default:
		return nil, fmt.Errorf("unsupported database scheme in DSN %q (supported: mysql://)", dsn)
	}
}
