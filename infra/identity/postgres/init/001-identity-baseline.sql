BEGIN;

SET LOCAL ROLE identity_migrator;

CREATE OR REPLACE FUNCTION identity.is_uuid_v7(value uuid)
RETURNS boolean
LANGUAGE sql
IMMUTABLE
STRICT
AS $$
  SELECT (get_byte(uuid_send(value), 6) >> 4) = 7
     AND (get_byte(uuid_send(value), 8) >> 6) = 2
$$;

CREATE TABLE IF NOT EXISTS identity.users (
  id uuid PRIMARY KEY CHECK (identity.is_uuid_v7(id)),
  username text NOT NULL,
  username_normalized text NOT NULL UNIQUE,
  display_name text NOT NULL,
  email text NOT NULL,
  password_phc text NOT NULL CHECK (password_phc LIKE '$argon2id$%'),
  status text NOT NULL CHECK (status IN ('ACTIVE', 'DISABLED', 'RETIRED')),
  failed_attempts integer NOT NULL DEFAULT 0 CHECK (failed_attempts >= 0),
  locked_until timestamptz,
  last_login_at timestamptz,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS identity.authorization_requests (
  challenge_hash bytea PRIMARY KEY CHECK (octet_length(challenge_hash) = 32),
  client_id text NOT NULL,
  redirect_uri text NOT NULL,
  state text NOT NULL,
  nonce text NOT NULL,
  code_challenge text NOT NULL,
  scope text NOT NULL,
  attempt_count integer NOT NULL CHECK (attempt_count >= 0),
  expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL,
  CHECK (expires_at > created_at)
);

CREATE TABLE IF NOT EXISTS identity.authorization_codes (
  code_hash bytea PRIMARY KEY CHECK (octet_length(code_hash) = 32),
  user_id uuid NOT NULL REFERENCES identity.users(id),
  client_id text NOT NULL,
  redirect_uri text NOT NULL,
  nonce text NOT NULL,
  code_challenge text NOT NULL,
  scope text NOT NULL,
  expires_at timestamptz NOT NULL,
  consumed_at timestamptz,
  created_at timestamptz NOT NULL,
  CHECK (expires_at > created_at),
  CHECK (consumed_at IS NULL OR consumed_at >= created_at)
);

CREATE INDEX IF NOT EXISTS authorization_requests_expiry_idx
  ON identity.authorization_requests (expires_at);
CREATE INDEX IF NOT EXISTS authorization_codes_expiry_idx
  ON identity.authorization_codes (expires_at)
  WHERE consumed_at IS NULL;

REVOKE ALL ON ALL TABLES IN SCHEMA identity FROM PUBLIC;
REVOKE ALL ON ALL FUNCTIONS IN SCHEMA identity FROM PUBLIC;

GRANT SELECT ON identity.users TO identity_runtime, identity_directory_reader;
GRANT UPDATE ON identity.users TO identity_runtime;
GRANT SELECT, INSERT, UPDATE ON identity.users TO identity_admin;
GRANT SELECT, INSERT, UPDATE, DELETE ON identity.authorization_requests, identity.authorization_codes TO identity_runtime;

RESET ROLE;

COMMIT;
