CREATE TABLE IF NOT EXISTS agent_nas_targets (
    agent_id        INTEGER NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    nas_target_id   INTEGER NOT NULL REFERENCES nas_targets(id) ON DELETE CASCADE,
    mount_base      TEXT NOT NULL,
    writable        BOOLEAN NOT NULL DEFAULT true,
    last_checked_at TIMESTAMPTZ,
    PRIMARY KEY (agent_id, nas_target_id)
);
