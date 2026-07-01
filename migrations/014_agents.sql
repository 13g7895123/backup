CREATE TABLE IF NOT EXISTS agents (
    id            SERIAL PRIMARY KEY,
    code          VARCHAR(50) UNIQUE NOT NULL,
    name          VARCHAR(100) NOT NULL,
    base_url      TEXT NOT NULL,
    token_hash    TEXT NOT NULL,
    enabled       BOOLEAN NOT NULL DEFAULT true,
    status        VARCHAR(20) NOT NULL DEFAULT 'offline',
    host_name     TEXT NOT NULL DEFAULT '',
    ip_address    TEXT NOT NULL DEFAULT '',
    version       TEXT NOT NULL DEFAULT '',
    last_seen_at  TIMESTAMPTZ,
    last_error    TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_agents_status ON agents(status);
