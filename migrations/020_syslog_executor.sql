ALTER TABLE syslog_configs
    ADD COLUMN IF NOT EXISTS executor_agent_id INTEGER REFERENCES agents(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_syslog_configs_executor_agent_id ON syslog_configs(executor_agent_id);
