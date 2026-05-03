package mysql

import dbpkg "github.com/ddevilz/phasedb/internal/db"

// Compile-time check: mysqlAdapter implements db.Adapter
var _ dbpkg.Adapter = (*mysqlAdapter)(nil)
