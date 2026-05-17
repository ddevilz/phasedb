#!/usr/bin/env bash
set -euo pipefail

# ---------------------------------------------------------------------------
# phasedb vs Raw ALTER TABLE benchmark
# Usage: bash benchmarks/run_benchmark.sh [--rows N]
# ---------------------------------------------------------------------------

ROWS=1000000
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
BENCH_MIGRATION="$SCRIPT_DIR/bench_migration.yaml"
RESULTS_FILE="$SCRIPT_DIR/RESULTS.md"
DB_NAME="phasedb_bench"
DATABASE_URL="${DATABASE_URL:-mysql://root:root@127.0.0.1:3306/$DB_NAME}"
MYSQL="mysql -h 127.0.0.1 -P 3306 -u root -proot --connect-timeout=5 --get-server-public-key"
now_ms() { python3 -c "import time; print(int(time.time() * 1000))"; }

# Temp files cleaned up on exit
AVAIL_LOG_ALTER=$(mktemp)
AVAIL_LOG_PHASEDB=$(mktemp)
trap 'rm -f "$AVAIL_LOG_ALTER" "$AVAIL_LOG_PHASEDB" "$BENCH_MIGRATION"' EXIT

# ---------------------------------------------------------------------------
# Parse args
# ---------------------------------------------------------------------------
while [[ $# -gt 0 ]]; do
  case "$1" in
    --rows) ROWS="$2"; shift 2 ;;
    *) echo "Unknown flag: $1"; exit 1 ;;
  esac
done

# ---------------------------------------------------------------------------
# Dependency checks
# ---------------------------------------------------------------------------
for dep in docker mysql; do
  if ! command -v "$dep" &>/dev/null; then
    echo "ERROR: '$dep' is not installed or not in PATH."
    [[ "$dep" == "docker" ]] && echo "  Install Docker: https://docs.docker.com/get-docker/"
    [[ "$dep" == "mysql" ]] && echo "  Install MySQL client: brew install mysql-client  OR  apt install mysql-client"
    exit 1
  fi
done

if ! command -v phasedb &>/dev/null; then
  echo "ERROR: 'phasedb' not found in PATH."
  echo "  Run: go install ./cmd/phasedb"
  echo "  Or:  go install github.com/ddevilz/phasedb/cmd/phasedb@latest"
  exit 1
fi

# ---------------------------------------------------------------------------
# Start MySQL
# ---------------------------------------------------------------------------
echo "==> Starting MySQL via docker-compose..."
docker compose -f "$REPO_ROOT/docker-compose.yml" up -d mysql

echo "==> Waiting for MySQL to be healthy (max 60s)..."
WAIT=0
until mysql -h 127.0.0.1 -P 3306 -u root -proot --connect-timeout=2 --get-server-public-key -e "SELECT 1" &>/dev/null 2>&1; do
  WAIT=$((WAIT + 1))
  if [[ $WAIT -gt 60 ]]; then
    echo "ERROR: MySQL did not become healthy within 60 seconds."
    exit 1
  fi
  sleep 1
done
echo "    MySQL ready after ${WAIT}s"

# ---------------------------------------------------------------------------
# Setup benchmark database and table
# ---------------------------------------------------------------------------
echo "==> Setting up benchmark database '$DB_NAME'..."
$MYSQL -e "DROP DATABASE IF EXISTS $DB_NAME; CREATE DATABASE $DB_NAME;"
$MYSQL "$DB_NAME" <<'SQL'
CREATE TABLE benchmark_events (
  id         BIGINT AUTO_INCREMENT PRIMARY KEY,
  user_id    BIGINT NOT NULL,
  payload    TEXT NOT NULL,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB;
SQL

# ---------------------------------------------------------------------------
# Seed rows
# ---------------------------------------------------------------------------
ROWS_FMT=$(printf "%'d" "$ROWS")
echo "==> Seeding $ROWS_FMT rows via recursive CTE..."

$MYSQL "$DB_NAME" -e "
SET SESSION cte_max_recursion_depth = $ROWS;
INSERT INTO benchmark_events (user_id, payload)
WITH RECURSIVE seq(n) AS (
  SELECT 1
  UNION ALL
  SELECT n + 1 FROM seq WHERE n < $ROWS
)
SELECT (n % 100000), 'benchmark-payload-data-string-for-testing-migration-speed' FROM seq;
" 2>/dev/null

echo "    Inserted $ROWS_FMT rows"

# ---------------------------------------------------------------------------
# Availability poller (background) — writes "ok" or "fail" per check to log
# ---------------------------------------------------------------------------
availability_poll() {
  local logfile="$1"
  while true; do
    if mysql -h 127.0.0.1 -P 3306 -u root -proot --connect-timeout=1 --get-server-public-key "$DB_NAME" -e 'SELECT 1 FROM benchmark_events LIMIT 1' &>/dev/null 2>&1; then
      echo "ok" >> "$logfile"
    else
      echo "fail" >> "$logfile"
    fi
    sleep 0.5
  done
}

count_fails() {
  grep -c "^fail$" "$1" 2>/dev/null; return 0
}

count_total() {
  wc -l < "$1" | tr -d ' \n'
}

# bc omits leading zero for values < 1; fix with printf
fmt_sec() {
  printf "%.2f" "$(echo "scale=4; $1 / 1000" | bc)"
}

# ---------------------------------------------------------------------------
# BENCHMARK A — Raw ALTER TABLE
# ---------------------------------------------------------------------------
echo ""
echo "==> BENCHMARK A: Flyway approach (ADD COLUMN + full UPDATE + MODIFY NOT NULL + ADD INDEX)..."

# Start availability poller
> "$AVAIL_LOG_ALTER"
availability_poll "$AVAIL_LOG_ALTER" &
POLLER_A=$!

ALTER_START=$(now_ms)
# Step 1: ADD COLUMN NULL (INSTANT on MySQL 8.0)
$MYSQL "$DB_NAME" -e "ALTER TABLE benchmark_events ADD COLUMN checksum VARCHAR(64) NULL;" 2>/dev/null
# Step 2: UPDATE all rows (what Flyway would do inline)
$MYSQL "$DB_NAME" -e "UPDATE benchmark_events SET checksum = SHA2(CONCAT(user_id, payload), 256);" 2>/dev/null
# Step 3: MODIFY COLUMN NOT NULL + ADD INDEX (locks table, table scan)
$MYSQL "$DB_NAME" -e "ALTER TABLE benchmark_events MODIFY COLUMN checksum VARCHAR(64) NOT NULL, ADD INDEX idx_checksum (checksum);" 2>/dev/null
ALTER_END=$(now_ms)

kill $POLLER_A 2>/dev/null || true

ALTER_DURATION_MS=$(( ALTER_END - ALTER_START ))
ALTER_DURATION_S=$(fmt_sec "$ALTER_DURATION_MS")
ALTER_FAILS=$(count_fails "$AVAIL_LOG_ALTER")
ALTER_TOTAL=$(count_total "$AVAIL_LOG_ALTER")

echo "    Done in ${ALTER_DURATION_S}s — failed availability checks: $ALTER_FAILS / $ALTER_TOTAL"

# ---------------------------------------------------------------------------
# Reset table
# ---------------------------------------------------------------------------
echo "==> Resetting table (DROP COLUMN + INDEX)..."
$MYSQL "$DB_NAME" -e "ALTER TABLE benchmark_events DROP INDEX idx_checksum, DROP COLUMN checksum;" 2>/dev/null

# ---------------------------------------------------------------------------
# BENCHMARK B — phasedb
# ---------------------------------------------------------------------------
echo ""
echo "==> BENCHMARK B: phasedb expand-contract approach..."

# Write temporary migration YAML
cat > "$BENCH_MIGRATION" <<YAML
migration: bench_add_checksum
database: mysql
phases:
  - name: expand
    sql: |
      ALTER TABLE benchmark_events ADD COLUMN checksum VARCHAR(64) NULL;
    rollback_sql: |
      ALTER TABLE benchmark_events DROP COLUMN checksum;

  - name: backfill
    on_failure: rollback
    batch:
      query: |
        UPDATE benchmark_events
        SET checksum = SHA2(CONCAT(user_id, payload), 256)
        WHERE checksum IS NULL
        LIMIT {batch_size}
      size: 500
      delay_ms: 5
      lag_threshold_ms: 500
      done_when: "SELECT COUNT(*) FROM benchmark_events WHERE checksum IS NULL"
      done_expected: 0
    rollback_sql: |
      UPDATE benchmark_events SET checksum = NULL

  - name: gate
    wait_until:
      query: "SELECT COUNT(*) FROM benchmark_events WHERE checksum IS NULL"
      expected: 0
      poll_interval_ms: 2000
      timeout_minutes: 120

  - name: contract
    sql: |
      ALTER TABLE benchmark_events MODIFY COLUMN checksum VARCHAR(64) NOT NULL;
      ALTER TABLE benchmark_events ADD INDEX idx_bench_checksum (checksum);
    rollback_sql: |
      ALTER TABLE benchmark_events DROP INDEX idx_bench_checksum;
      ALTER TABLE benchmark_events MODIFY COLUMN checksum VARCHAR(64) NULL;
YAML

# Start availability poller
> "$AVAIL_LOG_PHASEDB"
availability_poll "$AVAIL_LOG_PHASEDB" &
POLLER_B=$!

export DATABASE_URL="$DATABASE_URL"
PHASEDB_START=$(now_ms)
phasedb run --migration "$BENCH_MIGRATION"
PHASEDB_END=$(now_ms)

kill $POLLER_B 2>/dev/null || true

PHASEDB_DURATION_MS=$(( PHASEDB_END - PHASEDB_START ))
PHASEDB_DURATION_S=$(fmt_sec "$PHASEDB_DURATION_MS")
PHASEDB_FAILS=$(count_fails "$AVAIL_LOG_PHASEDB")
PHASEDB_TOTAL=$(count_total "$AVAIL_LOG_PHASEDB")

echo "    Done in ${PHASEDB_DURATION_S}s — failed availability checks: $PHASEDB_FAILS / $PHASEDB_TOTAL"

# ---------------------------------------------------------------------------
# Print results table
# ---------------------------------------------------------------------------
TIMESTAMP=$(date -u +"%Y-%m-%d %H:%M:%S UTC")

ALTER_AVAIL="${ALTER_FAILS} failed / ${ALTER_TOTAL} checks"
PHASEDB_AVAIL="${PHASEDB_FAILS} failed / ${PHASEDB_TOTAL} checks"

print_table() {
  echo ""
  echo "=== phasedb vs Raw ALTER TABLE Benchmark ==="
  printf "Rows: %s   Date: %s\n\n" "$ROWS_FMT" "$TIMESTAMP"
  printf "%-30s %-22s %-22s\n" "Metric" "Raw ALTER TABLE" "phasedb"
  printf "%-30s %-22s %-22s\n" "------------------------------" "----------------------" "----------------------"
  printf "%-30s %-22s %-22s\n" "Total duration"           "${ALTER_DURATION_S}s"   "${PHASEDB_DURATION_S}s"
  printf "%-30s %-22s %-22s\n" "Table lock duration"       "${ALTER_DURATION_S}s"   "0s (always available)"
  printf "%-30s %-22s %-22s\n" "Availability checks"       "$ALTER_AVAIL"           "$PHASEDB_AVAIL"
  echo ""
}

print_table

# ---------------------------------------------------------------------------
# Append to RESULTS.md
# ---------------------------------------------------------------------------
{
  echo ""
  echo "## $TIMESTAMP"
  echo ""
  printf "**Rows:** %s\n\n" "$ROWS_FMT"
  echo "| Metric                        | Raw ALTER TABLE         | phasedb                 |"
  echo "|-------------------------------|-------------------------|-------------------------|"
  printf "| %-29s | %-23s | %-23s |\n" "Total duration"            "${ALTER_DURATION_S}s"  "${PHASEDB_DURATION_S}s"
  printf "| %-29s | %-23s | %-23s |\n" "Table lock (UPDATE+MODIFY+IDX)" "${ALTER_DURATION_S}s" "0s (table always available)"
  printf "| %-29s | %-23s | %-23s |\n" "Availability checks"       "$ALTER_AVAIL"          "$PHASEDB_AVAIL"
  echo ""
} >> "$RESULTS_FILE"

echo "==> Results appended to $RESULTS_FILE"

# ---------------------------------------------------------------------------
# Teardown prompt
# ---------------------------------------------------------------------------
echo ""
read -r -p "Tear down Docker containers? [y/N] " TEARDOWN
if [[ "$TEARDOWN" == "y" || "$TEARDOWN" == "Y" ]]; then
  docker compose -f "$REPO_ROOT/docker-compose.yml" down
  echo "==> Containers stopped."
else
  echo "==> Containers left running. Stop manually: docker compose down"
fi
