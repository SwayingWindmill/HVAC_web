-- User administration from 系统管理 (spec docs/specs/iam-roles-and-users.md) records who was
-- enabled, disabled or renamed, and every change a user makes to their own password.
BEGIN;

SET LOCAL ROLE identity_migrator;

ALTER TABLE identity.security_audit_intents DROP CONSTRAINT security_audit_intents_event_type_check;
ALTER TABLE identity.security_audit_intents ADD CONSTRAINT security_audit_intents_event_type_check
  CHECK (event_type IN (
    'USER_CREATED', 'PASSWORD_RESET', 'LOGIN_FAILED', 'LOGIN_LOCKED', 'LOGIN_SUCCEEDED',
    'MFA_ENROLLMENT_STARTED', 'MFA_ENABLED', 'MFA_DISABLED', 'MFA_FAILED', 'MFA_LOCKED',
    'STEP_UP_SUCCEEDED', 'STEP_UP_FAILED',
    'USER_ENABLED', 'USER_DISABLED', 'PROFILE_UPDATED', 'PASSWORD_CHANGED'
  ));

RESET ROLE;

COMMIT;
