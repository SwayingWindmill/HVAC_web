BEGIN;
SET LOCAL ROLE s0_migrator;

-- Which client a signed-in Session belongs to (#664): the browser carries it in its cookie, the
-- WeChat mini program in the Authorization header. Each is accepted only the way it was issued.
ALTER TABLE gateway.sessions
  ADD COLUMN IF NOT EXISTS client text NOT NULL DEFAULT 'BROWSER';

ALTER TABLE gateway.sessions
  DROP CONSTRAINT IF EXISTS sessions_client_check;
ALTER TABLE gateway.sessions
  ADD CONSTRAINT sessions_client_check
  CHECK (client IN ('BROWSER', 'MINI_PROGRAM'));

RESET ROLE;
COMMIT;
