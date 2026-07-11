CREATE TABLE IF NOT EXISTS agent_commands (
    id BIGSERIAL PRIMARY KEY,
    agent_id INTEGER NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    project_id INTEGER NULL REFERENCES projects(id) ON DELETE SET NULL,
    schedule_id INTEGER NULL REFERENCES schedules(id) ON DELETE SET NULL,
    restore_record_id BIGINT NULL REFERENCES restore_records(id) ON DELETE SET NULL,
    type VARCHAR(50) NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    result JSONB NOT NULL DEFAULT '{}'::jsonb,
    log_output TEXT NOT NULL DEFAULT '',
    log_ref TEXT NOT NULL DEFAULT '',
    error_msg TEXT NOT NULL DEFAULT '',
    attempt_count INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ NULL,
    finished_at TIMESTAMPTZ NULL
);

CREATE INDEX IF NOT EXISTS idx_agent_commands_agent_status_created
    ON agent_commands(agent_id, status, created_at);

CREATE INDEX IF NOT EXISTS idx_agent_commands_project_created
    ON agent_commands(project_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_agent_commands_restore_record
    ON agent_commands(restore_record_id);
