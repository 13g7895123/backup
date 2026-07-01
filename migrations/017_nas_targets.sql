CREATE TABLE IF NOT EXISTS nas_targets (
    id              SERIAL PRIMARY KEY,
    code            VARCHAR(50) UNIQUE NOT NULL,
    name            VARCHAR(100) NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    mount_type      VARCHAR(20) NOT NULL DEFAULT 'nfs',
    remote_path     TEXT NOT NULL DEFAULT '',
    default_subpath TEXT NOT NULL DEFAULT '',
    enabled         BOOLEAN NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
