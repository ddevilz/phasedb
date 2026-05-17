# Runner Internals

## Phase State Machine

Each phase in a migration moves through these states, derived from the most recent row in `phasedb_history`.

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

## Runner Loop (per phase)

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

## Distributed Lock

phasedb uses a single row in `phasedb_locks` as a distributed mutex. No Redis, no ZooKeeper — just MySQL.

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

### Lock Steal SQL

```sql
-- Atomic steal if prior process is dead:
UPDATE phasedb_locks
SET process_id  = ?,
    acquired_at = NOW(3),
    expires_at  = DATE_ADD(NOW(3), INTERVAL 90 SECOND)
WHERE migration_name = ?
  AND expires_at < NOW(3);
-- rows_affected = 1 → steal succeeded — no TOCTOU race
-- rows_affected = 0 → live process holds lock → exit code 3
```

### Timing Constants

| Constant | Value | Role |
|---|---|---|
| `HEARTBEAT_INTERVAL` | 30s | How often heartbeat goroutine fires |
| `LOCK_TTL` | 90s | `expires_at = NOW() + 90s` on acquire/refresh |
| `DEAD_THRESHOLD` | 90s | `heartbeat_at < NOW() - 90s` = process dead |

These three are **coupled**. `LOCK_TTL` must equal `DEAD_THRESHOLD` must be ≥ `3 × HEARTBEAT_INTERVAL`. Changing one without the others breaks liveness detection.

## SIGTERM Handling

```mermaid
flowchart LR
    S[SIGTERM / SIGINT] --> RC[rootCtx cancelled\nsignal.NotifyContext]
    RC --> HB[hbCtx cancelled\nheartbeat stops]
    HB --> LE[Lock expires after LOCK_TTL 90s]
    LE --> NR[Next runner steals lock\nand resumes]
    RC --> EX[executor ctx cancelled\ncurrent operation stops cleanly]
```

The lock is **not** explicitly released on SIGTERM — the process may be mid-DDL. The next `phasedb resume` steals the expired lock after 90s.

## Rollback Scope

`on_failure: rollback` (YAML) or `--on-failure rollback` (CLI) calls `Rollback()` on the **single failing phase only**. It does not cascade to prior already-completed phases.

To roll back multiple phases in reverse order, use:

```bash
phasedb rollback --migration my_migration --to expand
```

This explicitly runs rollback from the current phase down to the target phase.

## Resume vs Run

| | `phasedb run` | `phasedb resume` |
|---|---|---|
| `resumeMode` | `false` | `true` |
| STARTED/FAILED state | returns exit 4 | proceeds to step 6 |
| Lock held by live process | returns exit 3 | returns exit 3 |
| Dead lock | returns exit 4 | steals lock, continues |

Both share the same runner — the only difference is the `resumeMode` boolean.
