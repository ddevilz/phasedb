# CLI Reference

## Commands

```
phasedb run      --migration FILE  [--db URL] [--dry-run] [--on-failure rollback]
phasedb resume   --migration FILE  [--db URL]
phasedb status   --migration NAME  [--format json]
phasedb rollback --migration NAME  --to PHASE
phasedb lint     --migration FILE  [--estimate] [--db URL] [--post-gate]
phasedb gc
```

---

## phasedb run

Runs all phases of a migration in order. Skips phases already in `COMPLETED` state.

```bash
phasedb run --migration add_checksum_column.yaml
phasedb run --migration add_checksum_column.yaml --on-failure rollback
phasedb run --migration add_checksum_column.yaml --dry-run
```

| Flag | Description |
|---|---|
| `--migration FILE` | Path to migration YAML |
| `--db URL` | Database connection URL (overrides env/config) |
| `--on-failure rollback\|none` | Override per-phase `on_failure` in YAML |
| `--dry-run` | Parse and validate without executing |

Returns exit code 4 if migration is in `STARTED` or `FAILED` state — use `phasedb resume` instead.

---

## phasedb resume

Resumes a migration that was interrupted, failed, or timed out. Sets `resumeMode = true` — bypasses the exit 4 guard and steals an expired lock.

```bash
phasedb resume --migration add_checksum_column.yaml
```

| Flag | Description |
|---|---|
| `--migration FILE` | Path to migration YAML |
| `--db URL` | Database connection URL |

Returns exit code 3 if a live process currently holds the lock.

---

## phasedb status

Shows the current state of a migration across all phases.

```bash
phasedb status --migration add_checksum_column
phasedb status --migration add_checksum_column --format json
```

| Flag | Description |
|---|---|
| `--migration NAME` | Migration name (not file path) |
| `--format json` | Emit structured JSON instead of human-readable text |

### JSON Output

```json
{
  "migration": "add_checksum_column",
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

If `phase_status = "RUNNING"` but no active lock is found, a warning is emitted:

```
warning: process may be dead — no active lock found. Run phasedb resume or phasedb status --check-liveness
```

---

## phasedb rollback

Rolls back phases in reverse order from the current phase down to the target phase.

```bash
phasedb rollback --migration add_checksum_column --to expand
```

| Flag | Description |
|---|---|
| `--migration NAME` | Migration name |
| `--to PHASE` | Target phase to roll back to (inclusive) |

This runs `Rollback()` on each phase from current → target, inserting `PHASE_ROLLED_BACK` for each.

---

## phasedb lint

Validates a migration YAML statically and optionally against a live database.

```bash
phasedb lint --migration add_checksum_column.yaml
phasedb lint --migration add_checksum_column.yaml --estimate --db "mysql://user:pass@host/db"
phasedb lint --migration add_checksum_column.yaml --post-gate --db "mysql://user:pass@host/db"
```

| Flag | Description |
|---|---|
| `--migration FILE` | Path to migration YAML |
| `--db URL` | Required for `--estimate` and `--post-gate` |
| `--estimate` | Fetch row counts and estimate backfill duration + gate timeout adequacy |
| `--post-gate` | Re-check `done_when` condition against live DB. Errors if result ≠ `done_expected` |

See [lint.md](lint.md) for all rules.

---

## phasedb gc

Prunes `phasedb_heartbeats` rows for migrations that have reached a terminal state. Safe to run on a cron.

```bash
phasedb gc
```

Deletes heartbeats for migrations where the latest `phasedb_history` event is `PHASE_COMPLETED`, `PHASE_FAILED`, `PHASE_TIMED_OUT`, or `PHASE_ROLLED_BACK` across all phases. In-progress migrations (latest event is `PHASE_STARTED`) are never touched.

---

## Global Flags

| Flag | Description |
|---|---|
| `--db URL` | Database URL. See config resolution below |
| `--config FILE` | Path to `phasedb.yaml` config file |
| `--log-format text\|json` | Output format (default: `text`) |

---

## Config Resolution

Database URL resolved in this order (first non-empty wins):

1. `--db` flag
2. `DATABASE_URL` environment variable
3. `database_url` field in `phasedb.yaml`

Error if none found.

---

## Exit Codes

| Code | Meaning | When |
|---|---|---|
| `0` | Success | All phases completed |
| `1` | General failure | Phase failed, invalid YAML, DB error |
| `2` | Gate timeout | Gate phase timed out |
| `3` | Already running | Lock held by live process |
| `4` | Requires resume | STARTED or FAILED state, `--resume` not set |
