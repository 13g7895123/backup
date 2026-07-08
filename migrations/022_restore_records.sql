CREATE TABLE IF NOT EXISTS restore_records (
    id              BIGSERIAL PRIMARY KEY,
    backup_record_id BIGINT REFERENCES backup_records(id) ON DELETE SET NULL,
    project_id      INTEGER REFERENCES projects(id) ON DELETE SET NULL,
    project_name    VARCHAR(100) NOT NULL DEFAULT '',
    type            VARCHAR(20) NOT NULL DEFAULT '',
    strategy        VARCHAR(20) NOT NULL DEFAULT 'new',
    target          TEXT NOT NULL DEFAULT '',
    status          VARCHAR(20) NOT NULL DEFAULT 'running',
    snapshot_path   TEXT NOT NULL DEFAULT '',
    error_msg       TEXT NOT NULL DEFAULT '',
    agent_id        INTEGER REFERENCES agents(id) ON DELETE SET NULL,
    agent_name      VARCHAR(100) NOT NULL DEFAULT '',
    run_host        TEXT NOT NULL DEFAULT '',
    started_at      TIMESTAMPTZ DEFAULT NOW(),
    finished_at     TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_restore_records_backup_record_id ON restore_records(backup_record_id);
CREATE INDEX IF NOT EXISTS idx_restore_records_project_id ON restore_records(project_id);
CREATE INDEX IF NOT EXISTS idx_restore_records_status ON restore_records(status);
