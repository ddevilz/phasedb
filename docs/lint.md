# Lint

phasedb lint validates migrations statically before execution. It uses regex for structural pre-screening and `vitess.io/vitess/go/vt/sqlparser` for MySQL DDL AST analysis.

```bash
phasedb lint --migration add_checksum_column.yaml
phasedb lint --migration add_checksum_column.yaml --estimate --db "mysql://user:pass@host/db"
```

---

## Rules

### Errors — block execution

| Rule | Message |
|---|---|
| Gate missing `wait_until` | `gate phase requires wait_until block` |
| `rollback_sql` absent with `on_failure: rollback` | `on_failure: rollback requires rollback_sql` |
| `{batch_size}` placeholder missing | `batch query must contain {batch_size} placeholder` |
| User `LIMIT` in backfill query | `batch query must not contain user-written LIMIT` |
| User `ORDER BY` in backfill query | `batch query must not contain ORDER BY` |
| Backfill not idempotent | `backfill WHERE clause must exclude already-processed rows: col IS NULL or col = <unprocessed_sentinel>` |
| `pk_column` not integer or composite | `pk_column must be a single-column integer primary key` |

**Idempotency rule**: the column named in `SET col = ...` must appear in the WHERE clause with `col IS NULL` or `col = <unprocessed_sentinel>` (e.g. `col = 0` for a boolean flag being set to 1).

### Warnings — proceed with caution

| Rule | Message |
|---|---|
| `ADD COLUMN NOT NULL` without `DEFAULT` | `ADD COLUMN NOT NULL without DEFAULT may cause issues on MySQL` |
| `DROP COLUMN` without `rollback_sql` | `DROP COLUMN in contract without rollback_sql is irreversible` |
| Full scan on batch query | `EXPLAIN on batch query shows full table scan (access_type=ALL)` |
| Gate timeout too short | `timeout_minutes < estimated_backfill_duration * 1.5` |

---

## --estimate Flag

Requires `--db` to connect to a live database. For each backfill phase:

1. `SELECT COUNT(*)` from the target table — row count
2. Estimated backfill duration: `rows / (batch_size / (delay_ms / 1000.0))`
3. Gate timeout adequacy check: warns if `timeout_minutes < estimate * 1.5`
4. Estimated ALTER TABLE duration based on `information_schema.TABLES` data size

Example output:

```
Table:            events
Row count:        8,400,000
Est. backfill:    23m 20s  (size=1000, delay=10ms)
Gate timeout:     120m     ✓  (adequate — 5.1x estimate)
```

---

## --post-gate Flag

Intended to run between gate completion and contract execution as a human-triggered safety check.

Re-runs the `done_when` query against the live database. Errors if result ≠ `done_expected`:

```
ERROR: post-gate check failed: done_when returned 142, expected 0
  Hint: dirty rows detected — investigate before running contract
```

---

## SQL Parsing

- **Regex**: fast structural pre-screening (placeholder presence, LIMIT, ORDER BY)
- **vitess AST**: accurate column definition extraction, type change detection, idempotency clause analysis

The vitess parser is only used for DDL statements (expand/contract SQL). Backfill queries are DML — validated via regex only.
