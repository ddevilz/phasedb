# Migration YAML Reference

## Structure

```yaml
migration: <name>        # unique identifier — used as primary key in all tables
database: mysql          # only "mysql" supported in v1.0
phases:
  - name: expand
    ...
  - name: backfill
    ...
  - name: gate
    ...
  - name: contract
    ...
```

Phases execute in order. Each phase is independent — a phase that already completed is skipped automatically on re-run.

---

## Expand Phase

```yaml
- name: expand
  sql: |
    ALTER TABLE events ADD COLUMN checksum VARCHAR(64) NULL;
  rollback_sql: |              # required if on_failure: rollback
    ALTER TABLE events DROP COLUMN checksum;
```

| Field | Required | Description |
|---|---|---|
| `sql` | Yes | DDL to execute. Must be additive (nullable column, new table, etc.) |
| `rollback_sql` | Conditional | Required when `on_failure: rollback` is set |

---

## Backfill Phase

```yaml
- name: backfill
  on_failure: rollback         # optional — auto-rollback this phase on failure
  batch:
    query: |
      UPDATE events
      SET checksum = SHA2(CONCAT(user_id, payload), 256)
      WHERE checksum IS NULL
      LIMIT {batch_size}
    size: 1000
    delay_ms: 10
    lag_threshold_ms: 500
    pk_column: id              # optional — integer PK upper bound
    done_when: "SELECT COUNT(*) FROM events WHERE checksum IS NULL"
    done_expected: 0
  rollback_sql: |
    UPDATE events SET checksum = NULL
```

| Field | Required | Description |
|---|---|---|
| `batch.query` | Yes | Must contain `{batch_size}` placeholder. No user-written `LIMIT` or `ORDER BY` |
| `batch.size` | Yes | Rows per batch |
| `batch.delay_ms` | No | Sleep between batches in ms (default 0) |
| `batch.lag_threshold_ms` | No | Pause batching if replica lag exceeds this value |
| `batch.pk_column` | No | Single-column integer PK. Enables `max_pk_at_start` upper bound. Prevents chasing concurrent inserts |
| `batch.done_when` | Yes | Scalar SQL query — checked when `rowsAffected = 0` |
| `batch.done_expected` | Yes | Expected value of `done_when` result to declare completion |
| `on_failure` | No | `rollback` or `none`. Overridable with `--on-failure` CLI flag |
| `rollback_sql` | Conditional | Required when `on_failure: rollback` |

**Constraints:**
- `{batch_size}` placeholder is required — lint errors if absent
- No user-written `LIMIT` or `ORDER BY` in batch query body
- WHERE clause must exclude already-processed rows: `col IS NULL` or `col = <unprocessed_sentinel>`
- `pk_column` must be a single-column integer PK (validated via `information_schema`)

---

## Gate Phase

```yaml
- name: gate
  wait_until:
    query: "SELECT COUNT(*) FROM events WHERE checksum IS NULL"
    expected: 0
    poll_interval_ms: 5000
    timeout_minutes: 120
```

| Field | Required | Description |
|---|---|---|
| `wait_until.query` | Yes | Scalar SQL query polled repeatedly |
| `wait_until.expected` | Yes | Target value — gate passes when result equals this |
| `wait_until.poll_interval_ms` | Yes | Sleep between polls |
| `wait_until.timeout_minutes` | Yes | Maximum wait time — returns exit code 2 on timeout |

**Gate must always have its own explicit `wait_until` block.** No inheritance from backfill's `done_when`.

---

## Contract Phase

```yaml
- name: contract
  sql: |
    ALTER TABLE events MODIFY COLUMN checksum VARCHAR(64) NOT NULL;
    ALTER TABLE events ADD INDEX idx_checksum (checksum);
  rollback_sql: |
    ALTER TABLE events DROP INDEX idx_checksum;
    ALTER TABLE events MODIFY COLUMN checksum VARCHAR(64) NULL;
```

| Field | Required | Description |
|---|---|---|
| `sql` | Yes | One or more DDL statements separated by `;` — each executed independently |
| `rollback_sql` | Recommended | Multi-statement rollback in reverse order |

Multiple statements are decomposed and checkpointed individually. Resume skips already-applied statements using `statement_index`.

---

## on_failure Behaviour

`on_failure` applies to **the single failing phase only** — it does not cascade to prior completed phases.

| Source | Precedence |
|---|---|
| `--on-failure` CLI flag | Highest — overrides YAML |
| `on_failure:` YAML field | Per-phase |
| Neither set | No auto-rollback |

---

## Full Example

```yaml
migration: add_checksum_column
database: mysql
phases:
  - name: expand
    sql: |
      ALTER TABLE events
        ADD COLUMN checksum VARCHAR(64) NULL;
    rollback_sql: |
      ALTER TABLE events DROP COLUMN checksum;

  - name: backfill
    on_failure: rollback
    batch:
      query: |
        UPDATE events
        SET checksum = SHA2(CONCAT(user_id, payload), 256)
        WHERE checksum IS NULL
        LIMIT {batch_size}
      size: 1000
      delay_ms: 10
      lag_threshold_ms: 500
      done_when: "SELECT COUNT(*) FROM events WHERE checksum IS NULL"
      done_expected: 0
    rollback_sql: |
      UPDATE events SET checksum = NULL

  - name: gate
    wait_until:
      query: "SELECT COUNT(*) FROM events WHERE checksum IS NULL"
      expected: 0
      poll_interval_ms: 5000
      timeout_minutes: 120

  - name: contract
    sql: |
      ALTER TABLE events
        MODIFY COLUMN checksum VARCHAR(64) NOT NULL;
      ALTER TABLE events
        ADD INDEX idx_checksum (checksum);
    rollback_sql: |
      ALTER TABLE events DROP INDEX idx_checksum;
      ALTER TABLE events MODIFY COLUMN checksum VARCHAR(64) NULL;
```
