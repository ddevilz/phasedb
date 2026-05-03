# phasedb Design Spec

**Date:** 2026-05-03  
**Status:** Approved  
**Scope:** v1.0 — CLI binary, MySQL 8.0.13+, all four phases (expand/backfill/gate/contract), lint, distribution

---

## 1. Problem Statement

Mid-size companies running MySQL with Flyway/Liquibase hit a hard wall when tables grow to millions of rows. A single `ALTER TABLE` inside Flyway startup either locks the table for hours, gets killed by job/pod timeout mid-execution leaving `flyway_schema_history` in FAILED state, or corrupts the migration sequence via metadata lock timeout.

**Root cause:** Flyway and Liquibase treat every migration as an atomic SQL script. No concept of phases, batched backfill, or conditional gating.

**phasedb fills the gap** by implementing the expand-contract pattern — splitting dangerous migrations into safe phases deployed separately. No existing off-the-shelf tool has all five of these simultaneously:

1. Multi-phase orchestration as a first-class concept
2. Batched, throttled, resumable, idempotent backfill
3. Programmable SQL-condition phase gate (poll until predicate satisfied)
4. Language-agnostic standalone binary (not tied to JVM, Python, Ruby)
5. Decoupled from application deploy lifecycle

---

## 2. Scope

### In scope (v1.0)
- CLI binary (single static Go binary)
- MySQL **8.0.13+** (minimum patch version — required for descending composite index keys)
- All four phases: EXPAND, BACKFILL, GATE, CONTRACT
- Lint mode with SQL AST parsing (vitess)
- Commands: `run`, `status`, `resume`, `rollback`, `lint`, `gc`
- Distribution: GitHub Releases + Homebrew + Docker image + apt/yum packages
- Integration tests via docker-compose (MySQL 8.0)

### Deferred (v1.1+)
- PostgreSQL 14+ support (including CONCURRENTLY DDL detection — see Section 15)
- Server mode (REST API + webhooks + dashboard)

### Explicitly out of scope (forever)
- Replacing Flyway/Liquibase for schema versioning
- Managing app deployment
- Online table rebuilds (gh-ost/Spirit territory)
- Cross-database migrations or heterogeneous engines
- Application dual-write logic

---

## 3. YAML Migration Format

```yaml
migration: V23_add_extrinsics_hash
database: mysql
phases:
  - name: expand
    sql: |
      ALTER TABLE PRODUCT_EXTRINSIC
        ADD COLUMN EXTRINSICS_HASH varchar(64) NULL;
    rollback_sql: |
      ALTER TABLE PRODUCT_EXTRINSIC DROP COLUMN EXTRINSICS_HASH;

  - name: backfill
    on_failure: rollback          # optional: auto-rollback on failure (single-phase only)
    batch:
      query: |
        UPDATE PRODUCT_EXTRINSIC
        SET EXTRINSICS_HASH = SHA2(CONCAT(PRODUCT_ID, EXTRINSIC_KEY, EXTRINSIC_VALUE), 256)
        WHERE EXTRINSICS_HASH IS NULL
        LIMIT {batch_size}
      size: 1000
      delay_ms: 10
      lag_threshold_ms: 500
      pk_column: PRODUCT_EXTRINSIC_ID   # optional: integer PK column for max_pk_at_start boundary
      done_when: "SELECT COUNT(*) FROM PRODUCT_EXTRINSIC WHERE EXTRINSICS_HASH IS NULL"
      done_expected: 0

  - name: gate
    wait_until:
      query: "SELECT COUNT(*) FROM PRODUCT_EXTRINSIC WHERE EXTRINSICS_HASH IS NULL"
      expected: 0
      poll_interval_ms: 5000
      timeout_minutes: 120

  - name: contract
    sql: |
      ALTER TABLE PRODUCT_EXTRINSIC
        MODIFY COLUMN EXTRINSICS_HASH varchar(64) NOT NULL;
      ALTER TABLE PRODUCT_EXTRINSIC
        ADD INDEX idx_extrinsics_hash (EXTRINSICS_HASH);
    rollback_sql: |
      ALTER TABLE PRODUCT_EXTRINSIC DROP INDEX idx_extrinsics_hash;
      ALTER TABLE PRODUCT_EXTRINSIC
        MODIFY COLUMN EXTRINSICS_HASH varchar(64) NULL;
```

**Constraints:**
- Gate must always have its own explicit `wait_until` block. Lint errors if absent. No inheritance from backfill's `done_when`.
- `rollback_sql` is required for any phase with `on_failure: rollback`. Absent → `ErrRollbackSQLMissing` at start, before any execution.
- Backfill `query` must contain `{batch_size}` placeholder. No user-written `LIMIT` or `ORDER BY`.
- `on_failure: rollback` applies to the **single failing phase only** — it does not cascade to prior phases.
- `pk_column` is optional. If provided, must be a single-column integer PK. Enables `max_pk_at_start` upper bound. If absent or if PK is composite/UUID, the `max_pk_at_start` boundary is skipped and the backfill runs until `done_when` is satisfied.

**`on_failure` precedence:** CLI `--on-failure` flag overrides YAML `on_failure`. Both absent = no auto-rollback.

---

## 4. CLI Commands

```bash
phasedb run      --migration V23.yaml [--db $URL] [--dry-run] [--on-failure rollback]
phasedb status   --migration V23      [--format json]
phasedb resume   --migration V23      [--db $URL]
phasedb rollback --migration V23      --to expand
phasedb lint     --migration V23.yaml [--post-gate] [--estimate]
phasedb gc                            # prunes phasedb_heartbeats for terminal migrations
```

**`phasedb resume` vs `phasedb run`:** `phasedb resume` sets `resumeMode = true` internally before invoking the runner. This bypasses the guards in runner steps 3 and 4 that would otherwise require explicit resume. `phasedb run` never sets `resumeMode`. Both share the same runner logic — the only difference is this boolean. If the lock is actively held by a live process at step 6 (`rows_affected = 0` from the steal UPDATE), `phasedb resume` still returns `ErrAlreadyRunning` (exit code 3) — `resumeMode` does not bypass a live lock.

**Config loading order** (first non-empty wins):
1. `--db` flag
2. `DATABASE_URL` environment variable
3. `database_url` field in `phasedb.yaml` config file

Error if none found.

**Output modes:**
- Default: human-readable progress lines with ETA
- `--log-format json`: structured JSON per event (for CI/machines)

**Exit codes:**

| Code | Meaning | When |
|---|---|---|
| `0` | Success | All phases completed |
| `1` | General failure | Phase FAILED, invalid YAML, DB error |
| `2` | Gate timeout | Gate phase timed out |
| `3` | Already running | Lock held by live process |
| `4` | Requires resume | STARTED or FAILED state detected, `--resume` not set |

---

## 5. Database Schema — Four Append-Only Tables

All tables use `DATETIME(3)` with UTC enforced at connection time via DSN (`loc=UTC` for MySQL). The phasedb DB user requires `INSERT + SELECT` on all tables; `UPDATE` on `phasedb_locks` only; `DELETE` on `phasedb_heartbeats` only (via `phasedb gc`); `CREATE TABLE` on first run.

**Timing constants (hardcoded, not configurable in v1.0):**
- `HEARTBEAT_INTERVAL = 30s` — how often heartbeatLoop INSERTs into `phasedb_heartbeats` and refreshes `phasedb_locks.expires_at`
- `LOCK_TTL = 90s` — `expires_at = NOW() + 90s` (3× heartbeat interval; process must miss 3 consecutive heartbeats before declared dead)
- `DEAD_THRESHOLD = 90s` — used in status/resume logic to determine if a process is dead (`heartbeat_at < NOW() - 90s`)

These three are coupled. `LOCK_TTL` must equal `DEAD_THRESHOLD` must be ≥ `3 × HEARTBEAT_INTERVAL`. Changing one requires changing all three.

### 5.1 `phasedb_history` — phase state transitions, immutable

```sql
CREATE TABLE IF NOT EXISTS phasedb_history (
    id                BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    migration_name    VARCHAR(256) NOT NULL,
    phase_name        VARCHAR(64)  NOT NULL,
    attempt_number    INT UNSIGNED NOT NULL,
    event_type        ENUM('PHASE_STARTED','PHASE_COMPLETED','PHASE_FAILED',
                           'PHASE_TIMED_OUT','PHASE_SKIPPED','PHASE_ROLLED_BACK') NOT NULL,
    phase_type        ENUM('EXPAND','BACKFILL','GATE','CONTRACT') NOT NULL,
    phase_config_json TEXT NOT NULL,   -- snapshot of YAML phase block, immutable audit record
    rows_affected     BIGINT NULL,
    error_message     TEXT NULL,       -- truncated to 4096 chars
    installed_by      VARCHAR(256) NOT NULL,
    phasedb_version   VARCHAR(32)  NOT NULL,
    created_at        DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    UNIQUE KEY uq_attempt_event (migration_name, phase_name, attempt_number, event_type),
    KEY idx_latest (migration_name, phase_name, id DESC)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

Note: `KEY idx_latest (..., id DESC)` requires MySQL 8.0.13+. This is why the minimum version in Section 2 is 8.0.13, not 8.0.0.

**Current state** = most recent row for `(migration_name, phase_name)` by `id DESC`.

### 5.2 `phasedb_checkpoints` — backfill/gate cursors, immutable

```sql
CREATE TABLE IF NOT EXISTS phasedb_checkpoints (
    id               BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    migration_name   VARCHAR(256) NOT NULL,
    phase_name       VARCHAR(64)  NOT NULL,
    attempt_number   INT UNSIGNED NOT NULL,
    statement_index  INT UNSIGNED NOT NULL DEFAULT 0, -- for CONTRACT multi-stmt resume
    checkpoint_json  TEXT NOT NULL,
    created_at       DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    KEY idx_resume (migration_name, phase_name, attempt_number, id DESC)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

**Resume**: query most recent checkpoint row for `(migration, phase, attempt_number)`. For CONTRACT, also use `statement_index` to skip already-applied statements.

### 5.3 `phasedb_heartbeats` — process liveness, prunable via `phasedb gc`

```sql
CREATE TABLE IF NOT EXISTS phasedb_heartbeats (
    id               BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    migration_name   VARCHAR(256) NOT NULL,
    phase_name       VARCHAR(64)  NOT NULL,
    attempt_number   INT UNSIGNED NOT NULL,
    process_id       VARCHAR(512) NOT NULL,  -- hostname:pid
    created_at       DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    KEY idx_liveness (migration_name, phase_name, attempt_number, created_at DESC)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

**Dead process detection**: latest heartbeat row older than `DEAD_THRESHOLD` (90s) = process dead. `phasedb gc` is the only operation allowed to DELETE from this table. `phasedb_history` and `phasedb_checkpoints` are immutable forever.

**`phasedb gc` retention criterion**: deletes all `phasedb_heartbeats` rows for migrations where the latest `phasedb_history` event is a terminal state (`PHASE_COMPLETED`, `PHASE_FAILED`, `PHASE_TIMED_OUT`, or `PHASE_ROLLED_BACK`) across all phases. Heartbeats for in-progress migrations (latest event is `PHASE_STARTED`) are never deleted by gc.

### 5.4 `phasedb_locks` — distributed mutex, mutable

```sql
CREATE TABLE IF NOT EXISTS phasedb_locks (
    migration_name   VARCHAR(256) PRIMARY KEY,
    process_id       VARCHAR(512) NOT NULL,
    acquired_at      DATETIME(3)  NOT NULL,
    expires_at       DATETIME(3)  NOT NULL  -- refreshed every HEARTBEAT_INTERVAL (30s); LOCK_TTL = 90s
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

**Lock acquire**: INSERT on first run. On duplicate key:
```sql
-- Atomic steal if prior process is dead:
UPDATE phasedb_locks
SET process_id  = ?,
    acquired_at = NOW(3),
    expires_at  = DATE_ADD(NOW(3), INTERVAL 90 SECOND)
WHERE migration_name = ?
  AND expires_at < NOW(3);
-- rows_affected = 1 → steal succeeded atomically, no TOCTOU race
-- rows_affected = 0 → live process holds lock, exit code 3
```

**Lock release**: DELETE on clean exit only. On SIGTERM: the root context is cancelled via `signal.NotifyContext` (see Section 6). `defer cancelHeartbeat()` fires, heartbeat stops, `expires_at` is not refreshed, lock expires after `LOCK_TTL` (90s). Lock is NOT explicitly released on SIGTERM — process may be mid-DDL.

### 5.5 Privilege Matrix

| Table | SELECT | INSERT | UPDATE | DELETE |
|---|---|---|---|---|
| `phasedb_history` | ✓ | ✓ | ✗ | ✗ |
| `phasedb_checkpoints` | ✓ | ✓ | ✗ | ✗ |
| `phasedb_heartbeats` | ✓ | ✓ | ✗ | `phasedb gc` only |
| `phasedb_locks` | ✓ | ✓ | ✓ (`process_id`, `acquired_at`, `expires_at`) | ✓ (on release) |

---

## 6. State Machine

### Phase States
```
PENDING → RUNNING → COMPLETED
                  → FAILED
                  → TIMED_OUT  (gate only)
                  → ROLLED_BACK
SKIPPED (phase already COMPLETED in prior run)
```

### SIGTERM Wiring
The root `context.Context` in `main.go` is created with `signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)`. This context is passed through all runner and executor calls. On SIGTERM, this context is cancelled, which propagates to `hbCtx` (via the heartbeat's `context.WithCancel`), stopping the heartbeat goroutine. No explicit lock release occurs on SIGTERM.

### Runner Loop (per phase)

```
0.  (main.go) rootCtx = signal.NotifyContext(background, SIGTERM, SIGINT)
    Pass rootCtx into MigrationRunner.Run(rootCtx)

1.  Query latest event for (migration, phase) → determine state

2.  If COMPLETED → log "already completed, skipping", advance to next phase. EXIT CODE 0.

3.  If PHASE_STARTED (no terminal event):
      lock exists + expires_at > NOW()  → return ErrAlreadyRunning (exit code 3)
      lock exists + expires_at < NOW()  → dead process detected
                                          if resumeMode = false → return ErrRequiresResume (exit code 4)
                                          if resumeMode = true  → proceed to step 6
      no lock                           → orphaned STARTED
                                          if resumeMode = false → return ErrRequiresResume (exit code 4)
                                          if resumeMode = true  → proceed to step 6

4.  If FAILED/TIMED_OUT:
      if resumeMode = false → return ErrRequiresResume (exit code 4)
      if resumeMode = true  → proceed to step 6

5.  If PENDING → proceed to step 6

6.  Acquire phasedb_locks:
      Try INSERT. On duplicate key → try conditional UPDATE steal.
      If UPDATE rows_affected = 0 → return ErrAlreadyRunning (exit code 3)

7.  hbCtx, cancelHeartbeat := context.WithCancel(rootCtx)
    defer cancelHeartbeat()   // unconditional: fires on return, error, panic, SIGTERM propagation
    go heartbeatLoop(hbCtx) → every 30s: INSERT phasedb_heartbeats + UPDATE phasedb_locks.expires_at

8.  Compute attempt_number:
      attempt_number = MAX(attempt_number FROM phasedb_history WHERE migration=? AND phase=?) + 1
      Try INSERT PHASE_STARTED with this attempt_number.
      On duplicate key (dup event for same attempt): release lock (DELETE), return ErrAlreadyRunning (exit code 3)
      Note: dup key here means another process won the race in steps 6-8; this process loses and exits.

9.  result = executor.Execute(ctx, db, store)
      Executor returns nil (success) or error. Executor does NOT insert terminal events.

10. Terminal guard: query latest event for (migration, phase, attempt_number)
      If already terminal (COMPLETED/FAILED/TIMED_OUT) → return ErrAlreadyTerminated
      (This guards against concurrent runners both reaching this point)
    
    If result == nil  → INSERT PHASE_COMPLETED (rows_affected from executor result)
    If result != nil  → INSERT PHASE_FAILED (error_message = result.Error()[:4096])
                        check on_failure config → if rollback: call executor.Rollback()
                          INSERT PHASE_ROLLED_BACK for this phase only
                          (rollback is single-phase only, does not cascade to prior phases)

11. If success: DELETE phasedb_locks
```

**Rollback scope clarification:** `on_failure: rollback` (YAML) or `--on-failure rollback` (CLI) calls `Rollback()` on the **single failing phase only**. It does not cascade to prior already-completed phases. To roll back multiple phases, use `phasedb rollback --to <phase>` which explicitly runs rollback in reverse order from the current phase down to the target phase.

### PhaseExecutor Interface
```go
type PhaseExecutor interface {
    Type()     PhaseType
    // Execute runs the phase logic. Returns nil on success, error on failure.
    // Must NOT insert terminal events (PHASE_COMPLETED/FAILED/TIMED_OUT) — runner does that.
    Execute(ctx context.Context, db Adapter, store Store) error
    // Rollback undoes the phase's changes. Called by runner when on_failure: rollback.
    // GATE rollback is a no-op (no side effects). EXPAND rollback runs rollback_sql.
    // BACKFILL rollback runs rollback_sql if provided; otherwise no-op (backfill is not reversed by default).
    Rollback(ctx context.Context, db Adapter, store Store) error
}
```

### Adapter Interface
```go
// internal/db/adapter.go — all methods required by phase executors and lint

type Adapter interface {
    // DDL — used by ExpandExecutor and ContractExecutor
    ExecDDL(ctx context.Context, sql string, lockTimeout time.Duration) (*DDLResult, error)

    // DML — used by BackfillExecutor
    ExecBatch(ctx context.Context, query string, batchSize int) (rowsAffected int64, err error)

    // Scalar query — used by GateExecutor and BackfillExecutor (done_when check)
    QueryScalar(ctx context.Context, query string, args ...any) (int64, error)

    // Schema inspection — used by expand/contract idempotency checks and lint
    ColumnExists(ctx context.Context, table, column string) (bool, error)
    GetColumnDefinition(ctx context.Context, table, column string) (*ColumnDef, error)
    IndexExists(ctx context.Context, table, index string) (bool, error)
    GetTableRowCount(ctx context.Context, table string) (int64, error)
    GetServerVersion(ctx context.Context) (string, error)

    // Replication — used by BackfillExecutor for lag throttling
    GetReplicaLag(ctx context.Context) (int64, error) // ms; -1 if not replicating; 0 if privilege error

    // Lint — used by lint rules
    RunEXPLAIN(ctx context.Context, query string) (*EXPLAINResult, error)

    // Lifecycle
    Ping(ctx context.Context) error
    Close() error
}

type DDLResult struct {
    IsAlreadyApplied bool // schema inspection confirmed effect already present
}

type ColumnDef struct {
    Name       string
    DataType   string
    IsNullable bool
    Default    *string
}

type EXPLAINResult struct {
    AccessType string // "ALL" = full scan, "ref", "range", etc.
}
```

---

## 7. Backfill Executor

### Batch Loop

```
1. Capture once at phase start (before first batch):
   - null_rows_at_start = run done_when query
   - if pk_column is set in YAML:
       max_pk_at_start = SELECT MAX({pk_column}) FROM {table}
       (only valid for single-column integer PK; enforced by lint)
   - Store in first checkpoint INSERT

2. Loop:
   a. Check replica lag via db.GetReplicaLag():
      - Returns 0 if privilege error (log warning once, not per-batch)
      - If lag > lag_threshold_ms:
          consecutiveLagEvents++
          consecutiveGoodReadings = 0          // reset good-reading counter on any lag event
          sleep(min(delay_ms * 2^consecutiveLagEvents, 60000ms))
      - If lag <= lag_threshold_ms:
          consecutiveGoodReadings++
          if consecutiveGoodReadings >= 3:
              consecutiveLagEvents = 0
              consecutiveGoodReadings = 0

   b. Build batch query:
      - if max_pk_at_start is set: append AND {pk_column} <= max_pk_at_start to WHERE clause
      - else: use query as-is
      - Execute via db.ExecBatch(ctx, query, batchSize)

   c. If rowsAffected == 0:
      - Run db.QueryScalar(done_when)
      - If result == done_expected → return nil (success — runner inserts PHASE_COMPLETED)
      - Else → continue (concurrent writes added new rows within max_pk_at_start bound)

   d. INSERT checkpoint row (migration, phase, attempt, checkpoint_json)
   e. Sleep(delay_ms)
```

**Important**: the executor returns `nil` on success. It does **not** insert `PHASE_COMPLETED` — the runner does that in step 10.

### Checkpoint JSON Schema
```json
{
  "rows_processed_total": 4200000,
  "batch_number": 4200,
  "null_rows_at_start": 8400000,
  "max_pk_at_start": 16800000,  // only present if pk_column is configured in YAML
  "last_batch_affected": 1000,
  "checkpointed_at": "2026-05-03T05:32:00Z"
}
```

`max_pk_at_start` is omitted from the JSON when `pk_column` is not configured. Implementers must treat its absence as "no upper bound active."

### Resume
Restart batch loop. `WHERE col IS NULL [AND {pk} <= max_pk_at_start]` naturally skips already-processed rows. "Unwritten batch" scenario (batch committed, checkpoint not written): re-executes that batch, `rowsAffected = 0` due to idempotent WHERE, moves on.

### Progress Estimation
- **Every batch** (cheap): `progress = rows_processed_total / null_rows_at_start`
- **Every 5 min** (expensive): actual `SELECT COUNT(*) WHERE col IS NULL` updates denominator
- **ETA**: rolling 10-minute throughput window → `remaining_rows / rows_per_sec`
- Status JSON marks `progress_confidence: "estimated" | "measured"`

---

## 8. Gate Executor

### Poll Loop

```
capture null_rows_at_start = db.QueryScalar(wait_until.query) on first successful poll
deadline = NOW() + timeout_minutes

loop:
  if NOW() > deadline:
    return ErrGateTimeout  // runner inserts PHASE_TIMED_OUT (exit code 2)

  result, err = db.QueryScalar(wait_until.query)

  if err != nil:
    if transient (see error codes below):
      consecutiveErrors++
      if consecutiveErrors >= 5:
        return fmt.Errorf("gate: 5 consecutive transient errors: %w", err)
        // runner inserts PHASE_FAILED
      sleep(min(poll_interval * 2^consecutiveErrors, 60s))
      continue
    else (fatal):
      return fmt.Errorf("gate: fatal query error: %w", err)
      // runner inserts PHASE_FAILED

  consecutiveErrors = 0

  if result == expected:
    return nil  // runner inserts PHASE_COMPLETED

  INSERT checkpoint row (current result, progress %, ETA)
  sleep(poll_interval_ms)
```

**Important**: the executor returns `nil` on success, `ErrGateTimeout` on timeout, or an error on failure. It does **not** insert `PHASE_COMPLETED`, `PHASE_FAILED`, or `PHASE_TIMED_OUT` — the runner inserts the appropriate terminal event based on the return value.

### Error Classification (MySQL)

**Fatal** (no retry): 1045 (access denied), 1142 (command denied), 1146 (table doesn't exist), 1054 (unknown column), 1064 (syntax error), 1217/1451 (FK constraint), 1292/1366/1406 (data truncation / dirty rows — error message must include: `"gate passed but dirty rows detected — run phasedb lint --post-gate"`)

**Transient** (retry with backoff): 1040 (too many connections), 1205 (lock wait timeout), 2003 (can't connect), 2006 (server gone away)

### Progress
`progress = (null_rows_at_start - current) / null_rows_at_start`
ETA via linear extrapolation from first poll. Stored in each checkpoint row. `null_rows_at_start = 0` → `progress_confidence = "unavailable"` (first poll failed).

### Gate Checkpoint JSON Schema
```json
{
  "current_result": 4200,
  "progress_pct": 0.61,
  "null_rows_at_start": 10800,
  "estimated_completion": "2026-05-03T06:45:00Z",
  "checked_at": "2026-05-03T05:32:00Z"
}
```

`null_rows_at_start` is captured on the first successful poll. `progress_pct` is derived as `(null_rows_at_start - current_result) / null_rows_at_start`. `estimated_completion` is a linear extrapolation; omitted if only one data point exists yet.

---

## 9. Contract Executor

### Multi-Statement Decomposition
CONTRACT SQL is split into individual statements at **execution time** (not lint time only). Each statement has its own idempotency check and checkpoint:

```go
type ContractStatement struct {
    SQL            string
    StatementIndex int
    IdempotencyFn  func(ctx context.Context, db Adapter) (bool, error) // true = already applied
}
```

Resume reads last `statement_index` from `phasedb_checkpoints`. Statements with `index < last_checkpointed` are skipped.

### Per-Statement Execution
For each statement:
1. Call `IdempotencyFn` — e.g., `GetColumnDefinition` for MODIFY COLUMN, `IndexExists` for ADD INDEX
2. If already applied → skip, log "statement N already applied", advance `statement_index`
3. Else → `db.ExecDDL(ctx, sql, lockTimeout)` → if `DDLResult.IsAlreadyApplied` → treat as success
4. INSERT checkpoint with `statement_index`

### DDL Error Type
```go
type DDLError struct {
    Err         error
    IsRetryable bool  // lock timeout → retry up to ddl_lock_retries (default 3)
}
```

Lock timeout retry: before each retry, call the statement's `IdempotencyFn` to check if DDL was applied despite the error. If applied → `IsAlreadyApplied = true` → advance without re-executing.

**The executor returns `nil` on success. It does not insert `PHASE_COMPLETED` — the runner does.**

---

## 10. Lint Mode

### Rules Enforced

**Errors (block execution):**
- Gate missing `wait_until` block
- `rollback_sql` absent when `on_failure: rollback` is set
- `{batch_size}` placeholder missing from backfill query
- User-written `LIMIT` or `ORDER BY` in backfill query
- `ADD COLUMN NOT NULL` without `DEFAULT` on MySQL ≤ 5.7
- Backfill WHERE clause idempotency: the column named in `SET col = ...` must appear in the WHERE clause with a condition that excludes already-processed rows. Accepted patterns: `col IS NULL`, `col = <unprocessed_sentinel>` (e.g., `col = 0` for a boolean flag being set to 1). If neither pattern is found, lint errors with: `"backfill WHERE clause must exclude already-processed rows: col IS NULL or col = <unprocessed_sentinel>"`
- `pk_column` set to a non-integer or composite column (validated via `information_schema`)

**Warnings (proceed with caution):**
- EXPLAIN on batch query returns `access_type = ALL` (full scan)
- `timeout_minutes < estimated_backfill_duration * 1.5`
- `DROP COLUMN` in CONTRACT without `rollback_sql`
- `ADD COLUMN NOT NULL` without DEFAULT on MySQL 8.0 (version-specific risk)

### `--post-gate` Mode
Re-checks `done_when` condition against live DB. Errors if result ≠ `done_expected`. Intended to run between gate completion and contract execution as a human-triggered safety check.

### SQL Parsing
- Regex for structural pre-screening (fast, no dependency)
- `vitess.io/vitess/go/vt/sqlparser` for MySQL DDL AST (accurate column definition extraction, type change detection, idempotency clause analysis)

### Dry-Run Estimates (with `--estimate` flag)
1. Row count via `SELECT COUNT(*) FROM {table}`
2. Estimated backfill duration: `rows / (batch_size / (delay_ms / 1000.0))`
3. Gate timeout adequacy: warns if `timeout_minutes < estimate * 1.5`
4. Estimated ALTER TABLE duration based on table size from `information_schema.TABLES`

---

## 11. Go Project Structure

```
phasedb/
├── cmd/phasedb/
│   └── main.go                    # cobra root, signal.NotifyContext, version via ldflags
│
├── internal/
│   ├── cli/
│   │   ├── root.go                # global flags: --db, --migration, --config, --log-format
│   │   ├── run.go                 # resumeMode = false
│   │   ├── status.go
│   │   ├── resume.go              # resumeMode = true
│   │   ├── rollback.go
│   │   ├── lint.go
│   │   └── gc.go
│   │
│   ├── config/
│   │   ├── migration.go           # MigrationFile YAML struct + validation
│   │   ├── phase.go               # Phase, BatchConfig (pk_column), GateConfig structs
│   │   └── loader.go              # --db > DATABASE_URL env > phasedb.yaml; on_failure merge
│   │
│   ├── db/
│   │   ├── adapter.go             # Adapter interface + DDLError + DDLResult + ColumnDef + EXPLAINResult
│   │   ├── factory.go             # NewAdapter(dsn) — enforces UTC at connection time (loc=UTC)
│   │   └── mysql/
│   │       ├── adapter.go
│   │       ├── ddl.go             # ExecDDL: schema check + lock timeout retry
│   │       ├── replication.go     # GetReplicaLag: SHOW REPLICA STATUS + privilege fallback
│   │       └── schema.go          # ColumnExists, GetColumnDefinition, IndexExists, RunEXPLAIN
│   │
│   ├── store/
│   │   ├── store.go               # Store interface (all four tables)
│   │   ├── schema.go              # EnsureSchema: CREATE TABLE IF NOT EXISTS
│   │   ├── mysql_store.go
│   │   └── models.go              # PhaseEvent, CheckpointJSON, LockRow structs
│   │
│   ├── runner/
│   │   ├── runner.go              # MigrationRunner.Run(): phase loop, lock acquire, terminal INSERT
│   │   ├── heartbeat.go           # heartbeatLoop: INSERT heartbeats + UPDATE lock expires_at
│   │   └── transitions.go         # state resolution, terminal guard, resumeMode checks, exit codes
│   │
│   ├── phase/
│   │   ├── executor.go            # PhaseExecutor interface
│   │   ├── expand.go              # returns nil/error; no terminal event INSERT
│   │   ├── backfill.go            # returns nil/error; no terminal event INSERT
│   │   ├── gate.go                # returns nil/ErrGateTimeout/error; no terminal event INSERT
│   │   ├── contract.go            # ContractStatement decomposition, per-stmt checkpoint
│   │   └── rollback.go            # reverse-order rollback for phasedb rollback --to
│   │
│   ├── lint/
│   │   ├── linter.go
│   │   ├── rules.go               # LintRule interface + registry
│   │   ├── rules_mysql.go         # NOT NULL without DEFAULT, EXPLAIN check, pk_column validation
│   │   ├── rules_common.go        # gate missing wait_until, idempotency clause check, done_when
│   │   ├── parser_mysql.go        # vitess AST helpers
│   │   └── estimates.go
│   │
│   └── output/
│       ├── status.go              # StatusJSON struct + resolution logic (see Section 12)
│       ├── progress.go            # human-readable progress, ETA
│       └── logger.go              # slog wrapper: text|json
│
├── migrations/                    # example YAML files
├── docker-compose.yml             # MySQL 8.0 for local integration tests
├── tests/integration/
│   ├── expand_test.go
│   ├── backfill_test.go
│   ├── gate_test.go
│   └── contract_test.go
├── go.mod
├── Makefile
└── .goreleaser.yml                # GitHub Releases + Homebrew + Docker + apt/yum
```

**Dependency flow**: `cli → runner → phase/executor + store + db/adapter`. `lint → db/adapter` (read-only). No circular deps. All deps injected via constructors, no globals.

---

## 12. Status JSON Output

```json
{
  "migration": "V23_add_extrinsics_hash",
  "current_phase": "backfill",
  "phase_status": "RUNNING",
  "backfill_progress": 0.61,
  "progress_confidence": "estimated",
  "null_rows_remaining": 1638000,
  "rows_processed_total": 4200000,
  "estimated_completion": "2026-05-03T06:45:00Z",
  "gate_status": "pending",
  "attempt_number": 1,
  "running_on": "pod-abc123:42",
  "phasedb_version": "1.0.0"
}
```

### Status Resolution Logic

`phasedb status` joins across three tables. Resolution rules in order:

1. Query `phasedb_history` for all events for this migration, ordered by `id DESC`. Derive `current_phase` and `phase_status` from most recent event per phase.
2. Query `phasedb_locks` for this migration. If a lock row exists with `expires_at > NOW()`: `running_on = lock.process_id`. If lock row missing or expired: `running_on = null`.
3. Query `phasedb_checkpoints` for most recent checkpoint row for `(migration, current_phase, attempt_number)`. Extract `backfill_progress`, `null_rows_remaining`, `rows_processed_total`, `estimated_completion`.
4. **Contradiction handling**: if `phase_status = "RUNNING"` (latest event is PHASE_STARTED, no terminal event) but `running_on = null` (lock missing or expired): emit `phase_status = "RUNNING"` with `"warning": "process may be dead — no active lock found. Run phasedb resume or phasedb status --check-liveness"`.
5. If migration name not found in `phasedb_history`: return `{"migration": "...", "phase_status": "NOT_STARTED"}`.

---

## 13. Distribution

| Channel | Mechanism |
|---|---|
| GitHub Releases | `.goreleaser.yml` — linux/darwin/windows, amd64/arm64 |
| Homebrew | Custom tap `phasedb/tap` |
| Docker | `ghcr.io/phasedb/phasedb:latest` + version tags |
| apt/yum | `.goreleaser.yml` nfpm configuration |

---

## 14. Key Design Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Core architecture | PhaseExecutor interface | Clean phase isolation, easy unit testing, extensible |
| History storage | Append-only (no UPDATE on history/checkpoints) | Immutable audit trail, minimal DB privileges |
| Terminal event ownership | Runner owns all PHASE_COMPLETED/FAILED/TIMED_OUT INSERTs | Prevents executor/runner double-insert conflict |
| Concurrency control | `phasedb_locks` table + heartbeat | No Redis dependency, works in all deployment models |
| Lock steal | Single conditional UPDATE | Atomic, no TOCTOU race, no DELETE needed |
| Backfill termination | `done_when` query (explicit predicate) | Handles non-nullable and non-NULL-sentinel columns |
| Backfill boundary | Optional `pk_column` for `max_pk_at_start` upper bound | Opt-in; restricted to single-column integer PKs; prevents chasing concurrent inserts |
| Backfill cursor (correctness) | WHERE idempotency clause, not PK cursor | Handles composite PKs, UUIDs, non-monotonic PKs |
| Gate independence | Always explicit `wait_until`, no inheritance | No hidden coupling between phases |
| CONTRACT resume | Per-statement checkpointing | Handles partial execution of multi-DDL CONTRACT |
| Rollback scope | Single-phase only for `on_failure: rollback` | Prevents silent cascade; use `phasedb rollback --to` for multi-phase |
| UTC enforcement | DSN-level (`loc=UTC`) | Prevents heartbeat timestamp comparison failures across timezones |
| Rollback SQL | Explicit `rollback_sql` only | Never infer inverse DDL — too risky for CONTRACT |
| Timing constants | Hardcoded (HEARTBEAT=30s, LOCK_TTL=90s, DEAD_THRESHOLD=90s) | Coupling is intentional; misconfiguring one without others breaks liveness detection |
| SIGTERM handling | `signal.NotifyContext` on root ctx → heartbeat stops → lock expires naturally | Safe for mid-DDL kills; next runner steals after TTL |
| Minimum MySQL version | 8.0.13+ | Required for descending composite index keys (`id DESC`) |

---

## 15. Future Work (v1.1+)

### PostgreSQL Support
All adapter interface methods will have a `postgres/` implementation. Key differences from MySQL:
- DDL is transactional — wrap in `BEGIN`/`COMMIT`, `SET LOCAL lock_timeout`
- Replica lag: `pg_last_xact_replay_timestamp()` on replica, `pg_stat_replication` on primary
- Advisory lock: `pg_try_advisory_lock(hashtext('phasedb:{migration}'))`

### CONCURRENTLY DDL Detection (PostgreSQL only)
`CREATE INDEX CONCURRENTLY` cannot run inside a transaction. Lint will detect `CONCURRENTLY` in contract SQL and force `IdempotencyFn` check before every retry (no blind retry). This is a PostgreSQL-only concern and does not affect MySQL v1.0 behaviour.
