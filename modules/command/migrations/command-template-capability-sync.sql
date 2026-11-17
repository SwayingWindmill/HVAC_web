BEGIN;
SET LOCAL ROLE s3_command_migrator;

ALTER TABLE command_runtime.capability_profiles
  ADD COLUMN IF NOT EXISTS owner_tenant_id uuid,
  ADD COLUMN IF NOT EXISTS source_synced_at timestamptz;

COMMENT ON COLUMN command_runtime.capability_profiles.owner_tenant_id IS '模板能力档位所属租户标识；平台内置能力档位为空，仅允许同租户更新。';
COMMENT ON COLUMN command_runtime.capability_profiles.source_synced_at IS '最近一次从模板出站事件同步该能力档位的时间戳，用于防止乱序重试覆盖。';

GRANT SELECT, INSERT, UPDATE ON command_runtime.capability_profiles TO s3_command_runtime;

COMMIT;
