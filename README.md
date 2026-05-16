# phasedb

Safe, phased database migrations for MySQL 8.0.13+. Implements the **expand-contract pattern** as a first-class concept with batched backfill, distributed locking, resume, and lint.

---

## Why

Flyway and Liquibase treat every migration as an atomic SQL script. When tables hit millions of rows, a single `ALTER TABLE` inside startup either:

- Locks the table for hours
- Gets killed mid-execution leaving `flyway_schema_history` in `FAILED` state
- Triggers a metadata lock timeout and corrupts the migration sequence

phasedb splits dangerous migrations into **four safe phases** deployed separately, with none of these problems.

---

## The Expand-Contract Pattern

```
Deploy 1                    Deploy 2                    Deploy 3
────────────────────────    ────────────────────────    ────────────────────────
EXPAND                      BACKFILL → GATE             CONTRACT
Add column as nullable   →  Fill data in batches     →  Make column NOT NULL
(instant, no lock)          (throttled, resumable)       Add index
                            Gate waits until done        (safe — all rows filled)
```

No full-table locks. No downtime. Resumable at any point.

---

## Architecture

```mermaid
graph TD
    CLI["CLI (cobra)\ncmd/phasedb/main.go"]
    Runner["Runner\ninternal/runner"]
    Phase["Phase Executors\ninternal/phase"]
    Store["Store\ninternal/store"]
    DB["DB Adapter\ninternal/db/mysql"]
    MySQL[("MySQL 8.0.13+")]
    Lint["Lint Engine\ninternal/lint"]
    Output["Output\ninternal/output"]

    CLI -->|"run / resume / rollback"| Runner
    CLI -->|"lint / --estimate"| Lint
    CLI -->|"status"| Output
    Runner -->|"Execute / Rollback"| Phase
    Runner -->|"InsertEvent / AcquireLock / InsertHeartbeat"| Store
    Phase -->|"ExecDDL / ExecBatch / QueryScalar"| DB
    Phase -->|"InsertCheckpoint / LatestCheckpoint"| Store
    Lint -->|"ColumnExists / RunEXPLAIN / GetTableRowCount"| DB
    Output -->|"LatestEvent / GetLock / LatestCheckpoint"| Store
    Store -->|SQL| MySQL
    DB -->|SQL| MySQL
```

### Component Responsibilities

| Component | Responsibility |
|---|---|
| **CLI** | Parse flags, wire deps, set up signal context, call runner |
| **Runner** | Phase loop, distributed lock, heartbeat, terminal event INSERT |
| **Phase Executors** | Phase logic only — return nil/error, never insert terminal events |
| **Store** | Append-only audit tables + distributed lock table |
| **DB Adapter** | DDL execution, batch DML, schema inspection, replication lag |
| **Lint** | Static analysis + dry-run estimates before execution |
| **Output** | Status resolution, progress bar, structured logging |

---

## Phase Executors

```mermaid
classDiagram
    class PhaseExecutor {
        <<interface>>
        +Type() PhaseType
        +Execute(ctx, db, store) error
        +Rollback(ctx, db, store) error
    }

    class ExpandExecutor {
        +ExecDDL with idempotency check
        +Rollback runs rollback_sql
    }

    class BackfillExecutor {
        +Batch loop with lag throttling
        +Checkpoints every batch
        +Resume from last checkpoint
        +Rollback runs rollback_sql
    }

    class GateExecutor {
        +Poll until predicate satisfied
        +Returns ErrGateTimeout on timeout
        +Rollback is no-op
    }

    class ContractExecutor {
        +Per-statement decomposition
        +Per-statement idempotency check
        +Per-statement checkpoint
        +Resume from statement_index
    }

    PhaseExecutor <|-- ExpandExecutor
    PhaseExecutor <|-- BackfillExecutor
    PhaseExecutor <|-- GateExecutor
    PhaseExecutor <|-- ContractExecutor
```

**Key invariant**: executors return `nil` or an `error`. They **never** insert `PHASE_COMPLETED`, `PHASE_FAILED`, or `PHASE_TIMED_OUT` — the runner owns all terminal event inserts.

---

## Runner State Machine

```mermaid
stateDiagram-v2
    [*] --> PENDING : no history rows

    PENDING --> RUNNING : AcquireLock + INSERT PHASE_STARTED

    RUNNING --> COMPLETED : Execute returns nil\nRunner inserts PHASE_COMPLETED
    RUNNING --> FAILED : Execute returns error\nRunner inserts PHASE_FAILED
    RUNNING --> TIMED_OUT : Execute returns ErrGateTimeout\nRunner inserts PHASE_TIMED_OUT
    RUNNING --> ROLLED_BACK : on_failure=rollback\nRunner inserts PHASE_ROLLED_BACK

    FAILED --> RUNNING : phasedb resume\n(steal expired lock)
    TIMED_OUT --> RUNNING : phasedb resume
    COMPLETED --> COMPLETED : already done — skip
```

### Runner Loop (per phase)

```mermaid
flowchart TD
    A[Query latest event for migration+phase] --> B{State?}
    B -->|COMPLETED| C[Skip — advance to next phase]
    B -->|RUNNING lock alive| D[ErrAlreadyRunning exit 3]
    B -->|RUNNING lock dead| E{resumeMode?}
    B -->|FAILED / TIMED_OUT| E
    B -->|PENDING| F[AcquireLock]
    E -->|false| G[ErrRequiresResume exit 4]
    E -->|true| F
    F -->|lock held by live process| D
    F -->|lock acquired| H[Compute attempt = MAX+1]
    H --> I[INSERT PHASE_STARTED]
    I --> J[Start heartbeat goroutine\nevery 30s: INSERT heartbeat\n+ refresh lock TTL]
    J --> K[executor.Execute ctx db store]
    K -->|nil| L[INSERT PHASE_COMPLETED]
    K -->|ErrGateTimeout| M[INSERT PHASE_TIMED_OUT]
    K -->|error| N[INSERT PHASE_FAILED]
    N --> O{on_failure=rollback?}
    O -->|yes| P[executor.Rollback\nINSERT PHASE_ROLLED_BACK]
    O -->|no| Q[Return error]
    P --> Q
    L --> R[DELETE lock\nReturn nil]
```

---

## Database Schema

Four tables: three append-only (history, checkpoints, heartbeats) + one mutable lock table.

```mermaid
erDiagram
    phasedb_history {
        BIGINT id PK
        VARCHAR migration_name
        VARCHAR phase_name
        INT attempt_number
        ENUM event_type
        ENUM phase_type
        TEXT phase_config_json
        BIGINT rows_affected
        TEXT error_message
        VARCHAR installed_by
        VARCHAR phasedb_version
        DATETIME3 created_at
    }

    phasedb_checkpoints {
        BIGINT id PK
        VARCHAR migration_name
        VARCHAR phase_name
        INT attempt_number
        INT statement_index
        TEXT checkpoint_json
        DATETIME3 created_at
    }

    phasedb_heartbeats {
        BIGINT id PK
        VARCHAR migration_name
        VARCHAR phase_name
        INT attempt_number
        VARCHAR process_id
        DATETIME3 created_at
    }

    phasedb_locks {
        VARCHAR migration_name PK
        VARCHAR process_id
        DATETIME3 acquired_at
        DATETIME3 expires_at
    }

    phasedb_history ||--o{ phasedb_checkpoints : "migration+phase+attempt"
    phasedb_history ||--o{ phasedb_heartbeats : "migration+phase+attempt"
    phasedb_history ||--o| phasedb_locks : "migration_name"
```

### Timing Constants

| Constant | Value | Role |
|---|---|---|
| `HEARTBEAT_INTERVAL` | 30s | How often heartbeat goroutine fires |
| `LOCK_TTL` | 90s | `expires_at = NOW() + 90s` |
| `DEAD_THRESHOLD` | 90s | `heartbeat_at < NOW() - 90s` = process dead |

These three are coupled. `LOCK_TTL` must equal `DEAD_THRESHOLD` must be ≥ `3 × HEARTBEAT_INTERVAL`.

---

## Backfill: Batch Loop

```mermaid
flowchart TD
    A[Capture null_rows_at_start\nmax_pk_at_start if pk_column set] --> B[Check replica lag]
    B -->|lag > threshold| C[Exponential backoff\nmax 60s]
    C --> B
    B -->|lag ok| D[ExecBatch\nWHERE col IS NULL LIMIT batchSize]
    D -->|rowsAffected > 0| E[INSERT checkpoint\nSleep delay_ms]
    E --> B
    D -->|rowsAffected == 0| F[Run done_when query]
    F -->|result == done_expected| G[Return nil]
    F -->|result != done_expected| B
```

**Resume**: restart loop. `WHERE col IS NULL` naturally skips already-processed rows. Idempotent by design.

---

## Gate: Poll Loop

```mermaid
flowchart TD
    A[Capture null_rows_at_start\nSet deadline = NOW + timeout_minutes] --> B{NOW > deadline?}
    B -->|yes| C[Return ErrGateTimeout]
    B -->|no| D[QueryScalar wait_until.query]
    D -->|fatal error| E[Return error]
    D -->|transient error| F{consecutiveErrors >= 5?}
    F -->|yes| E
    F -->|no| G[Backoff sleep + retry]
    G --> B
    D -->|result == expected| H[Return nil]
    D -->|result != expected| I[INSERT checkpoint\nSleep poll_interval_ms]
    I --> B
```

---

## Contract: Per-Statement Execution

```mermaid
flowchart TD
    A[Split SQL into statements] --> B[Load last statement_index\nfrom checkpoint]
    B --> C{More statements?}
    C -->|no| D[Return nil]
    C -->|yes| E{index < checkpointed?}
    E -->|yes skip| C
    E -->|no| F[IdempotencyFn\ncheck schema]
    F -->|already applied| G[Log skip + advance index]
    G --> C
    F -->|not applied| H[ExecDDL with lock timeout]
    H -->|success| I[INSERT checkpoint\nwith statement_index]
    I --> C
    H -->|lock timeout DDLError| J{retries < 3?}
    J -->|yes IdempotencyFn first| H
    J -->|no| K[Return DDLError]
```

---

## Distributed Lock

```mermaid
sequenceDiagram
    participant P1 as Process 1
    participant P2 as Process 2
    participant DB as phasedb_locks

    P1->>DB: INSERT lock (process_id=P1, expires_at=NOW+90s)
    DB-->>P1: OK — lock acquired

    P2->>DB: INSERT lock (process_id=P2)
    DB-->>P2: ERROR 1062 duplicate key

    P2->>DB: UPDATE SET process_id=P2 WHERE migration=X AND expires_at < NOW()
    DB-->>P2: rows_affected=0 — P1 alive, return ErrLockHeld

    Note over P1,DB: Every 30s — heartbeat refreshes expires_at

    P1->>DB: UPDATE expires_at = NOW+90s

    Note over P1,DB: P1 crashes — heartbeat stops — lock expires after 90s

    P2->>DB: UPDATE SET process_id=P2 WHERE migration=X AND expires_at < NOW()
    DB-->>P2: rows_affected=1 — lock stolen atomically
```

---

## SIGTERM Handling

```mermaid
flowchart LR
    S[SIGTERM / SIGINT] --> RC[rootCtx cancelled\nsignal.NotifyContext]
    RC --> HB[hbCtx cancelled\nheartbeat stops]
    HB --> LE[Lock expires after LOCK_TTL 90s]
    LE --> NR[Next runner steals lock\nand resumes]
    RC --> EX[executor ctx cancelled\ncurrent operation stops cleanly]
```

Lock is **not** explicitly released on SIGTERM — process may be mid-DDL. Next `phasedb resume` steals the expired lock after 90s.

---

## Installation

### Homebrew
```bash
brew install phasedb/tap/phasedb
```

### Docker
```bash
docker pull ghcr.io/ddevilz/phasedb:latest
```

### Binary (GitHub Releases)
```bash
curl -sSL https://github.com/ddevilz/phasedb/releases/latest/download/phasedb_linux_amd64.tar.gz | tar xz
```

### From source
```bash
go install github.com/ddevilz/phasedb/cmd/phasedb@latest
```

---

## Quick Start

### 1. Write a migration YAML

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

### 2. Lint before running

```bash
phasedb lint --migration add_checksum_column.yaml --estimate --db "mysql://user:pass@host/db"
```

### 3. Run

```bash
export DATABASE_URL="mysql://user:pass@host/db"
phasedb run --migration add_checksum_column.yaml
```

### 4. Monitor

```bash
phasedb status --migration add_checksum_column
phasedb status --migration add_checksum_column --format json
```

### 5. Resume if interrupted

```bash
phasedb resume --migration add_checksum_column.yaml
```

---

## CLI Reference

```
phasedb run      --migration FILE  [--db URL] [--dry-run] [--on-failure rollback]
phasedb status   --migration NAME  [--format json]
phasedb resume   --migration FILE  [--db URL]
phasedb rollback --migration NAME  --to expand
phasedb lint     --migration FILE  [--estimate] [--db URL] [--post-gate]
phasedb gc
```

### Config Resolution (first wins)

1. `--db` flag
2. `DATABASE_URL` environment variable
3. `database_url` in `phasedb.yaml`

### Exit Codes

| Code | Meaning |
|---|---|
| `0` | Success |
| `1` | General failure (phase failed, invalid YAML, DB error) |
| `2` | Gate timeout |
| `3` | Already running (lock held by live process) |
| `4` | Requires resume (STARTED or FAILED state, `--resume` not set) |

---

## Lint Rules

**Errors (block execution)**

| Rule | Description |
|---|---|
| Gate missing `wait_until` | Gate phase must have explicit poll condition |
| `rollback_sql` absent | Required when `on_failure: rollback` is set |
| `{batch_size}` missing | Backfill query must use placeholder, not hardcoded LIMIT |
| Backfill not idempotent | `SET col = X` must have `WHERE col IS NULL` or `WHERE col = sentinel` |
| User `LIMIT` / `ORDER BY` | Not allowed in backfill query body |

**Warnings (proceed with caution)**

| Rule | Description |
|---|---|
| `ADD COLUMN NOT NULL` | Without DEFAULT on MySQL — may lock table |
| `DROP COLUMN` without `rollback_sql` | Irreversible without explicit rollback |
| Full scan | EXPLAIN returns `access_type = ALL` on batch query |
| Gate timeout too short | `timeout_minutes < estimate * 1.5` |

---

## Database Privileges Required

```sql
GRANT SELECT, INSERT ON phasedb_history      TO 'phasedb'@'%';
GRANT SELECT, INSERT ON phasedb_checkpoints  TO 'phasedb'@'%';
GRANT SELECT, INSERT, DELETE ON phasedb_heartbeats TO 'phasedb'@'%';
GRANT SELECT, INSERT, UPDATE, DELETE ON phasedb_locks TO 'phasedb'@'%';
GRANT CREATE ON <your_db>.* TO 'phasedb'@'%';
```

---

## Requirements

- MySQL **8.0.13+** (descending composite index keys required)
- Go 1.22+ (to build from source)

---

## Deferred (v1.1+)

- PostgreSQL 14+ adapter
- Server mode (REST API + webhooks + dashboard)
