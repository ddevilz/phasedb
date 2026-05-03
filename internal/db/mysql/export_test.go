package mysql_test

import (
	"github.com/ddevilz/phasedb/internal/db/mysql"
	dbpkg "github.com/ddevilz/phasedb/internal/db"
)

// Compile-time check: mysql.New return type satisfies db.Adapter.
var _ dbpkg.Adapter = mustAdapter()

func mustAdapter() dbpkg.Adapter {
	a, _ := mysql.New("mysql://u:p@localhost:3306/db")
	return a
}
