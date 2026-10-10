BEGIN;
SET LOCAL ROLE s3_command_migrator;

-- Approval is authorized by the approver's COMMAND_APPROVE grant, recorded in the snapshot's
-- authorization columns. The approver role authorized nothing and recorded an arbitrary role
-- (#448).
ALTER TABLE command_runtime.command_approval_snapshots DROP COLUMN approver_role;

RESET ROLE;
COMMIT;
