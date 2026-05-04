package lint

import (
	"fmt"

	"vitess.io/vitess/go/vt/sqlparser"
)

// ParseDDL parses a single DDL statement using vitess and returns the AST.
func ParseDDL(sql string) (sqlparser.Statement, error) {
	parser, err := sqlparser.New(sqlparser.Options{})
	if err != nil {
		return nil, fmt.Errorf("create parser: %w", err)
	}
	stmt, err := parser.Parse(sql)
	if err != nil {
		return nil, fmt.Errorf("parse DDL: %w", err)
	}
	return stmt, nil
}

// IsAddColumnNotNull returns true if the DDL is an ALTER TABLE ADD COLUMN
// where the column is NOT NULL and has no DEFAULT.
func IsAddColumnNotNull(sql string) bool {
	stmt, err := ParseDDL(sql)
	if err != nil {
		return false
	}
	alter, ok := stmt.(*sqlparser.AlterTable)
	if !ok {
		return false
	}
	for _, opt := range alter.AlterOptions {
		addCol, ok := opt.(*sqlparser.AddColumns)
		if !ok {
			continue
		}
		for _, col := range addCol.Columns {
			if col.Type.Options == nil {
				continue
			}
			notNull := col.Type.Options.Null != nil && !*col.Type.Options.Null
			hasDefault := col.Type.Options.Default != nil
			if notNull && !hasDefault {
				return true
			}
		}
	}
	return false
}
