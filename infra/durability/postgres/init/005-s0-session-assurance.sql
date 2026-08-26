BEGIN;
SET LOCAL ROLE s0_migrator;

ALTER TABLE gateway.sessions
  ADD COLUMN IF NOT EXISTS authentication_acr text NOT NULL DEFAULT 'urn:hvac:loa:1',
  ADD COLUMN IF NOT EXISTS authentication_amr jsonb NOT NULL DEFAULT '["pwd"]'::jsonb,
  ADD COLUMN IF NOT EXISTS authentication_time timestamptz;

UPDATE gateway.sessions
SET authentication_time = created_at
WHERE authentication_time IS NULL;

ALTER TABLE gateway.sessions
  ALTER COLUMN authentication_time SET NOT NULL;

ALTER TABLE gateway.sessions
  DROP CONSTRAINT IF EXISTS sessions_authentication_acr_check;
ALTER TABLE gateway.sessions
  ADD CONSTRAINT sessions_authentication_acr_check
  CHECK (authentication_acr IN ('urn:hvac:loa:1', 'urn:hvac:loa:2'));

ALTER TABLE gateway.sessions
  DROP CONSTRAINT IF EXISTS sessions_authentication_amr_check;
ALTER TABLE gateway.sessions
  ADD CONSTRAINT sessions_authentication_amr_check
  CHECK (jsonb_typeof(authentication_amr) = 'array' AND jsonb_array_length(authentication_amr) > 0);

ALTER TABLE gateway.sessions
  DROP CONSTRAINT IF EXISTS sessions_authentication_time_check;
ALTER TABLE gateway.sessions
  ADD CONSTRAINT sessions_authentication_time_check
  CHECK (authentication_time <= created_at + interval '1 second');

RESET ROLE;
COMMIT;
