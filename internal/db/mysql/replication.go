package mysql

import (
	"context"
	"database/sql"
	"fmt"
)

func (a *mysqlAdapter) GetReplicaLag(ctx context.Context) (int64, error) {
	for _, q := range []string{"SHOW REPLICA STATUS", "SHOW SLAVE STATUS"} {
		lag, err := a.parseReplicaLag(ctx, q)
		if err == nil {
			return lag, nil
		}
	}
	return 0, nil
}

func (a *mysqlAdapter) parseReplicaLag(ctx context.Context, query string) (int64, error) {
	rows, err := a.db.QueryContext(ctx, query)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	if !rows.Next() {
		return -1, nil // not a replica
	}
	cols, _ := rows.Columns()
	vals := make([]sql.RawBytes, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return 0, err
	}
	for i, c := range cols {
		if c == "Seconds_Behind_Master" || c == "Seconds_Behind_Source" {
			if vals[i] == nil {
				return -1, nil
			}
			var lag int64
			fmt.Sscanf(string(vals[i]), "%d", &lag)
			return lag * 1000, nil // ms
		}
	}
	return -1, nil
}
