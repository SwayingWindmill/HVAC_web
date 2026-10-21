-- WeChat bound to an account in the mini program (#664, docs/specs/miniprogram-wechat.md): the
-- person's OpenID in the platform's own mini program. One WeChat per account, one account per
-- WeChat. WeChat's session key is never kept.
BEGIN;

SET LOCAL ROLE identity_migrator;

CREATE TABLE identity.wechat_bindings (
  user_id uuid PRIMARY KEY REFERENCES identity.users(id),
  app_id text NOT NULL CHECK (app_id <> ''),
  open_id text NOT NULL CHECK (open_id <> ''),
  bound_at timestamptz NOT NULL,
  CONSTRAINT wechat_bindings_one_account_per_wechat UNIQUE (app_id, open_id)
);

REVOKE ALL ON identity.wechat_bindings FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON identity.wechat_bindings TO identity_runtime;

RESET ROLE;

COMMIT;
