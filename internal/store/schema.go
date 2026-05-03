package store

const mysqlSchema = `
CREATE TABLE IF NOT EXISTS phasedb_history (
    id                BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    migration_name    VARCHAR(256) NOT NULL,
    phase_name        VARCHAR(64)  NOT NULL,
    attempt_number    INT UNSIGNED NOT NULL,
    event_type        ENUM('PHASE_STARTED','PHASE_COMPLETED','PHASE_FAILED',
                           'PHASE_TIMED_OUT','PHASE_SKIPPED','PHASE_ROLLED_BACK') NOT NULL,
    phase_type        ENUM('EXPAND','BACKFILL','GATE','CONTRACT') NOT NULL,
    phase_config_json TEXT NOT NULL,
    rows_affected     BIGINT NULL,
    error_message     TEXT NULL,
    installed_by      VARCHAR(256) NOT NULL,
    phasedb_version   VARCHAR(32)  NOT NULL,
    created_at        DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    UNIQUE KEY uq_attempt_event (migration_name, phase_name, attempt_number, event_type),
    KEY idx_latest (migration_name, phase_name, id DESC)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS phasedb_checkpoints (
    id               BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    migration_name   VARCHAR(256) NOT NULL,
    phase_name       VARCHAR(64)  NOT NULL,
    attempt_number   INT UNSIGNED NOT NULL,
    statement_index  INT UNSIGNED NOT NULL DEFAULT 0,
    checkpoint_json  TEXT NOT NULL,
    created_at       DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    KEY idx_resume (migration_name, phase_name, attempt_number, id DESC)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS phasedb_heartbeats (
    id               BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    migration_name   VARCHAR(256) NOT NULL,
    phase_name       VARCHAR(64)  NOT NULL,
    attempt_number   INT UNSIGNED NOT NULL,
    process_id       VARCHAR(512) NOT NULL,
    created_at       DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    KEY idx_liveness (migration_name, phase_name, attempt_number, created_at DESC)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS phasedb_locks (
    migration_name   VARCHAR(256) PRIMARY KEY,
    process_id       VARCHAR(512) NOT NULL,
    acquired_at      DATETIME(3)  NOT NULL,
    expires_at       DATETIME(3)  NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
`
