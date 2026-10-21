-- How a sign-in waiting for its one-time code was started (#664, docs/specs/miniprogram-wechat.md):
-- a password ('pwd') or WeChat one-tap sign-in in the mini program ('wechat'). The code issued
-- after the one-time code names both, so a WeChat sign-in is never recorded as a password one.
BEGIN;

SET LOCAL ROLE identity_migrator;

ALTER TABLE identity.authorization_requests
  ADD COLUMN first_factor text;

-- Sign-ins already waiting for their one-time code were all started with a password.
UPDATE identity.authorization_requests SET first_factor = 'pwd' WHERE user_id IS NOT NULL;

ALTER TABLE identity.authorization_requests
  DROP CONSTRAINT authorization_requests_first_factor_check;
ALTER TABLE identity.authorization_requests
  ADD CONSTRAINT authorization_requests_first_factor_check
  CHECK ((user_id IS NULL AND first_factor_at IS NULL AND first_factor IS NULL)
      OR (user_id IS NOT NULL AND first_factor_at IS NOT NULL AND first_factor IN ('pwd', 'wechat')));

RESET ROLE;

COMMIT;
