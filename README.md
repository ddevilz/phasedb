# phasedb

Safe, phased database migrations for MySQL 8.0.13+.

Implements the **expand-contract pattern** — splitting dangerous `ALTER TABLE` operations into four safe phases deployed independently. No full-table locks, no downtime, resumable at any point.

---

## The Problem

Flyway and Liquibase treat every migration as an atomic SQL script. On tables with millions of rows, a single `ALTER TABLE` inside startup either:

- Locks the table for hours
- Gets killed mid-execution leaving metadata in a `FAILED` state
- Triggers lock timeout and corrupts the migration sequence

## vs. Flyway / Liquibase

| Feature | phasedb | Flyway | Liquibase |
|---|---|---|---|
| Zero-downtime on large tables | ✅ | ❌ | ❌ |
| Resumable migrations | ✅ | ❌ | ❌ |
| Backfill throttling (lag-aware) | ✅ | ❌ | ❌ |
| No table locks during migration | ✅ | ❌ | ❌ |
| Lint + time estimate before run | ✅ | ❌ | ❌ |
| Postgres support | Roadmap | ✅ | ✅ |
| Spring Boot / framework integration | ❌ | ✅ | ✅ |

**Use phasedb when**: your table has >1M rows and you can't afford downtime.  
**Use Flyway/Liquibase when**: you need framework integration or Postgres and your tables are small enough that `ALTER TABLE` is instant.

---

## The Pattern

```
Deploy 1                    Deploy 2                    Deploy 3
────────────────────────    ────────────────────────    ────────────────────────
EXPAND                      BACKFILL → GATE             CONTRACT
Add column as nullable   →  Fill data in batches     →  Make column NOT NULL
(instant, no lock)          (throttled, resumable)       Add index
                            Gate waits until done        (safe — all rows filled)
```

---

## Install

### Homebrew
```bash
brew install ddevilz/phasedb/phasedb
```

### Docker
```bash
docker pull ghcr.io/ddevilz/phasedb:latest
```

### Binary (GitHub Releases)
```bash
# Linux
curl -sSL https://github.com/ddevilz/phasedb/releases/latest/download/phasedb_0.1.1_linux_amd64.tar.gz | tar xz
# macOS (Apple Silicon)
curl -sSL https://github.com/ddevilz/phasedb/releases/latest/download/phasedb_0.1.1_darwin_arm64.tar.gz | tar xz
```

### From source
```bash
go install github.com/ddevilz/phasedb/cmd/phasedb@latest
```

---

## Quick Start

**1. Lint your migration**
```bash
phasedb lint --migration add_checksum_column.yaml --estimate --db "mysql://user:pass@host/db"
```

**2. Run**
```bash
export DATABASE_URL="mysql://user:pass@host/db"
phasedb run --migration add_checksum_column.yaml
```

**3. Monitor**
```bash
phasedb status --migration add_checksum_column
phasedb status --migration add_checksum_column --format json
```

**4. Resume if interrupted**
```bash
phasedb resume --migration add_checksum_column.yaml
```

---

## Benchmarks

**1,000,000 row table on MySQL 8.0 (MacBook M-series, Docker):**

| Metric | Raw ALTER TABLE (Flyway) | phasedb |
|---|---|---|
| Total duration | 36s | **35s** |
| Table locked / unavailable | **36s** | **0s** |
| Failed availability checks | 0 / 63 | 0 / 62 |

phasedb is faster than raw ALTER TABLE and holds zero table locks. Raw ALTER locks the table for the entire duration — at 10M rows that's ~6 minutes of downtime. phasedb's PK cursor backfill scans each row exactly once (O(n)), releasing the lock between every batch.

Run it yourself: `bash benchmarks/run_benchmark.sh --rows 1000000`

Full results: [benchmarks/RESULTS.md](benchmarks/RESULTS.md)

---

## Requirements

- MySQL **8.0.13+**
- Go 1.22+ (to build from source)

---

## Documentation

| Document | Contents |
|---|---|
| [docs/architecture.md](docs/architecture.md) | Component graph, phase executor interface, project structure |
| [docs/phases.md](docs/phases.md) | Expand, backfill, gate, contract — deep dives with diagrams |
| [docs/runner-internals.md](docs/runner-internals.md) | State machine, runner loop, distributed lock, SIGTERM handling |
| [docs/database-schema.md](docs/database-schema.md) | ER diagram, table DDL, timing constants, privilege matrix |
| [docs/migration-yaml.md](docs/migration-yaml.md) | Full YAML format reference with all fields and constraints |
| [docs/cli-reference.md](docs/cli-reference.md) | All commands, flags, exit codes, config resolution |
| [docs/lint.md](docs/lint.md) | All lint rules, `--estimate`, `--post-gate` |

---

## Example Migration

```yaml
migration: add_checksum_column
database: mysql
phases:
  - name: expand
    sql: |
      ALTER TABLE EVENTS ADD COLUMN CHECKSUM VARCHAR(64) NULL;
    rollback_sql: |
      ALTER TABLE EVENTS DROP COLUMN CHECKSUM;

  - name: backfill
    on_failure: rollback
    batch:
      query: |
        UPDATE EVENTS
        SET CHECKSUM = SHA2(CONCAT(USER_ID, PAYLOAD), 256)
        WHERE CHECKSUM IS NULL
        LIMIT {batch_size}
      size: 1000
      delay_ms: 10
      lag_threshold_ms: 500
      done_when: "SELECT COUNT(*) FROM EVENTS WHERE CHECKSUM IS NULL"
      done_expected: 0

  - name: gate
    wait_until:
      query: "SELECT COUNT(*) FROM EVENTS WHERE CHECKSUM IS NULL"
      expected: 0
      poll_interval_ms: 5000
      timeout_minutes: 120

  - name: contract
    sql: |
      ALTER TABLE EVENTS MODIFY COLUMN CHECKSUM VARCHAR(64) NOT NULL;
      ALTER TABLE EVENTS ADD INDEX IDX_CHECKSUM (CHECKSUM);
    rollback_sql: |
      ALTER TABLE EVENTS DROP INDEX IDX_CHECKSUM;
      ALTER TABLE EVENTS MODIFY COLUMN CHECKSUM VARCHAR(64) NULL;
```
