BEGIN;

SET LOCAL ROLE identity_migrator;

ALTER TABLE identity.authorization_requests
  ADD COLUMN IF NOT EXISTS required_acr text NOT NULL DEFAULT 'urn:hvac:loa:1',
  ADD COLUMN IF NOT EXISTS user_id uuid REFERENCES identity.users(id),
  ADD COLUMN IF NOT EXISTS first_factor_at timestamptz;

ALTER TABLE identity.authorization_requests
  DROP CONSTRAINT IF EXISTS authorization_requests_required_acr_check;
ALTER TABLE identity.authorization_requests
  ADD CONSTRAINT authorization_requests_required_acr_check
  CHECK (required_acr IN ('urn:hvac:loa:1', 'urn:hvac:loa:2'));
ALTER TABLE identity.authorization_requests
  DROP CONSTRAINT IF EXISTS authorization_requests_first_factor_check;
ALTER TABLE identity.authorization_requests
  ADD CONSTRAINT authorization_requests_first_factor_check
  CHECK ((user_id IS NULL AND first_factor_at IS NULL) OR (user_id IS NOT NULL AND first_factor_at IS NOT NULL));

ALTER TABLE identity.authorization_codes
  ADD COLUMN IF NOT EXISTS acr text NOT NULL DEFAULT 'urn:hvac:loa:1',
  ADD COLUMN IF NOT EXISTS amr text[] NOT NULL DEFAULT ARRAY['pwd']::text[],
  ADD COLUMN IF NOT EXISTS auth_time timestamptz;

UPDATE identity.authorization_codes
SET auth_time = created_at
WHERE auth_time IS NULL;

ALTER TABLE identity.authorization_codes
  ALTER COLUMN auth_time SET NOT NULL;
ALTER TABLE identity.authorization_codes
  DROP CONSTRAINT IF EXISTS authorization_codes_acr_check;
ALTER TABLE identity.authorization_codes
  ADD CONSTRAINT authorization_codes_acr_check
  CHECK (acr IN ('urn:hvac:loa:1', 'urn:hvac:loa:2'));
ALTER TABLE identity.authorization_codes
  DROP CONSTRAINT IF EXISTS authorization_codes_amr_check;
ALTER TABLE identity.authorization_codes
  ADD CONSTRAINT authorization_codes_amr_check
  CHECK (cardinality(amr) > 0);

CREATE TABLE IF NOT EXISTS identity.user_mfa (
  user_id uuid PRIMARY KEY REFERENCES identity.users(id),
  method text NOT NULL CHECK (method = 'TOTP'),
  key_id text NOT NULL,
  cipher_nonce bytea NOT NULL CHECK (octet_length(cipher_nonce) = 12),
  ciphertext bytea NOT NULL CHECK (octet_length(ciphertext) > 16),
  status text NOT NULL CHECK (status IN ('PENDING', 'ACTIVE')),
  failed_attempts integer NOT NULL DEFAULT 0 CHECK (failed_attempts >= 0),
  locked_until timestamptz,
  last_used_counter bigint NOT NULL DEFAULT -1 CHECK (last_used_counter >= -1),
  activated_at timestamptz,
  last_verified_at timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  CHECK (updated_at >= created_at),
  CHECK ((status = 'PENDING' AND activated_at IS NULL) OR (status = 'ACTIVE' AND activated_at IS NOT NULL))
);

CREATE TABLE IF NOT EXISTS identity.security_audit_intents (
  event_id uuid PRIMARY KEY CHECK (identity.is_uuid_v7(event_id)),
  event_type text NOT NULL CHECK (event_type IN (
    'USER_CREATED', 'PASSWORD_RESET', 'LOGIN_FAILED', 'LOGIN_LOCKED', 'LOGIN_SUCCEEDED',
    'MFA_ENROLLMENT_STARTED', 'MFA_ENABLED', 'MFA_DISABLED', 'MFA_FAILED', 'MFA_LOCKED',
    'STEP_UP_SUCCEEDED', 'STEP_UP_FAILED'
  )),
  user_id uuid REFERENCES identity.users(id),
  subject_ref text NOT NULL CHECK (subject_ref ~ '^[0-9a-f]{64}$'),
  outcome text NOT NULL CHECK (outcome IN ('SUCCEEDED', 'FAILED')),
  reason_code text NOT NULL,
  occurred_at timestamptz NOT NULL,
  details jsonb NOT NULL CHECK (jsonb_typeof(details) = 'object')
);

CREATE TABLE IF NOT EXISTS identity.security_outbox (
  event_id uuid PRIMARY KEY REFERENCES identity.security_audit_intents(event_id) ON DELETE RESTRICT,
  payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
  status text NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'PUBLISHED', 'FAILED')),
  attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
  available_at timestamptz NOT NULL,
  published_at timestamptz,
  last_error_code text,
  created_at timestamptz NOT NULL,
  CHECK (published_at IS NULL OR published_at >= created_at)
);

CREATE INDEX IF NOT EXISTS security_outbox_pending_idx
  ON identity.security_outbox (available_at, created_at)
  WHERE status = 'PENDING';

REVOKE ALL ON identity.user_mfa, identity.security_audit_intents, identity.security_outbox FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON identity.user_mfa TO identity_runtime, identity_admin;
GRANT SELECT, INSERT ON identity.security_audit_intents TO identity_runtime, identity_admin;
GRANT SELECT, INSERT, UPDATE ON identity.security_outbox TO identity_runtime, identity_admin;

RESET ROLE;

COMMIT;
