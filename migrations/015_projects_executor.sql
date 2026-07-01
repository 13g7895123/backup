ALTER TABLE projects
    ADD COLUMN IF NOT EXISTS executor_type VARCHAR(20) NOT NULL DEFAULT 'local',
    ADD COLUMN IF NOT EXISTS executor_agent_id INTEGER REFERENCES agents(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_projects_executor_agent_id ON projects(executor_agent_id);
