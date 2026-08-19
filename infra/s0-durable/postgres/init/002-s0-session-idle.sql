BEGIN;
SET LOCAL ROLE s0_migrator;

ALTER TABLE gateway.sessions
  ADD COLUMN IF NOT EXISTS last_activity_at timestamptz;

UPDATE gateway.sessions
SET last_activity_at = created_at
WHERE last_activity_at IS NULL;

ALTER TABLE gateway.sessions
  ALTER COLUMN last_activity_at SET NOT NULL;

CREATE INDEX IF NOT EXISTS sessions_last_activity_idx
  ON gateway.sessions (last_activity_at)
  WHERE revoked_at IS NULL;

COMMIT;
