BEGIN;

ALTER TABLE command_runtime.command_intents
  ADD COLUMN IF NOT EXISTS precondition_mode text NOT NULL DEFAULT 'REQUIRE_CURRENT_STATE'
    CHECK (precondition_mode IN ('REQUIRE_CURRENT_STATE', 'EMERGENCY_BYPASS')),
  ADD COLUMN IF NOT EXISTS bypass_reason text NOT NULL DEFAULT '';

ALTER TABLE command_runtime.command_intents
  DROP CONSTRAINT IF EXISTS command_intents_snapshot_revision_check;

ALTER TABLE command_runtime.command_intents
  DROP CONSTRAINT IF EXISTS command_intents_precondition_revision_check;

ALTER TABLE command_runtime.command_intents
  ADD CONSTRAINT command_intents_precondition_revision_check
    CHECK (
      (precondition_mode = 'REQUIRE_CURRENT_STATE' AND snapshot_revision > 0)
      OR (precondition_mode = 'EMERGENCY_BYPASS' AND snapshot_revision >= 0 AND btrim(bypass_reason) <> '')
    );

COMMENT ON COLUMN command_runtime.command_intents.precondition_mode IS
  '命令前置状态校验模式：REQUIRE_CURRENT_STATE 要求提交前绑定正向当前状态快照版本，EMERGENCY_BYPASS 允许应急停机类能力在无实时遥测或当前状态降级时按明确事由旁路';

COMMENT ON COLUMN command_runtime.command_intents.bypass_reason IS
  '采用 EMERGENCY_BYPASS 模式时的操作员或系统旁路原因说明，非旁路模式为空字符串';

COMMIT;
