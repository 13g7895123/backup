ALTER TABLE projects
    ADD COLUMN IF NOT EXISTS transfer_mode VARCHAR(20) NOT NULL DEFAULT 'direct';

UPDATE projects
SET transfer_mode = 'direct'
WHERE transfer_mode IS NULL OR transfer_mode = '';
