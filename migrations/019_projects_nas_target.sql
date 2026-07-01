ALTER TABLE projects
    ADD COLUMN IF NOT EXISTS nas_target_id INTEGER REFERENCES nas_targets(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS nas_subpath TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_projects_nas_target_id ON projects(nas_target_id);
