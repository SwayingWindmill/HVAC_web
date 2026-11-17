BEGIN;

SET LOCAL ROLE s3_command_migrator;

ALTER TABLE command_runtime.command_intents
    ADD COLUMN IF NOT EXISTS priority_tier text NOT NULL DEFAULT 'MANUAL_OVERRIDE'
        CHECK (priority_tier IN ('SAFETY_INTERLOCK','MANUAL_OVERRIDE','PROTECTION_RULE','AI_OPTIMIZATION','SCHEDULE')),
    ADD COLUMN IF NOT EXISTS command_source text NOT NULL DEFAULT 'OPERATOR'
        CHECK (command_source IN ('SAFETY_INTERLOCK','OPERATOR','PROTECTION_RULE','OPTIMIZATION','SCHEDULE'));

COMMENT ON COLUMN command_runtime.command_intents.priority_tier IS '指令仲裁优先级层级（SAFETY_INTERLOCK 500、MANUAL_OVERRIDE 400、PROTECTION_RULE 300、AI_OPTIMIZATION 200、SCHEDULE 100）';
COMMENT ON COLUMN command_runtime.command_intents.command_source IS '指令发起来源分类（SAFETY_INTERLOCK、OPERATOR、PROTECTION_RULE、OPTIMIZATION、SCHEDULE）';

CREATE TABLE IF NOT EXISTS command_runtime.device_priority_locks (
    tenant_id uuid NOT NULL,
    site_id uuid NOT NULL,
    device_id uuid NOT NULL,
    locked_priority text NOT NULL
        CHECK (locked_priority IN ('SAFETY_INTERLOCK','MANUAL_OVERRIDE','PROTECTION_RULE','AI_OPTIMIZATION','SCHEDULE')),
    command_source text NOT NULL
        CHECK (command_source IN ('SAFETY_INTERLOCK','OPERATOR','PROTECTION_RULE','OPTIMIZATION','SCHEDULE')),
    locked_by text NOT NULL CHECK (btrim(locked_by) <> ''),
    locked_until timestamptz NOT NULL,
    reason text NOT NULL CHECK (btrim(reason) <> ''),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, device_id)
);

CREATE INDEX IF NOT EXISTS device_priority_locks_tenant_until_idx
    ON command_runtime.device_priority_locks (tenant_id, locked_until);

COMMENT ON TABLE command_runtime.device_priority_locks IS '设备控制优先级独占锁，在锁有效期内阻止低于锁定优先级的控制指令进入执行通道';
COMMENT ON COLUMN command_runtime.device_priority_locks.tenant_id IS '租户唯一标识';
COMMENT ON COLUMN command_runtime.device_priority_locks.site_id IS '冷站或站点唯一标识';
COMMENT ON COLUMN command_runtime.device_priority_locks.device_id IS '被锁定的目标设备唯一标识';
COMMENT ON COLUMN command_runtime.device_priority_locks.locked_priority IS '当前锁定的最低放行优先级层级';
COMMENT ON COLUMN command_runtime.device_priority_locks.command_source IS '施加该设备优先级锁的来源分类';
COMMENT ON COLUMN command_runtime.device_priority_locks.locked_by IS '施加或更新优先级锁的主体标识（操作员或安全规则标识）';
COMMENT ON COLUMN command_runtime.device_priority_locks.locked_until IS '优先级锁自动过期失效的截止时间';
COMMENT ON COLUMN command_runtime.device_priority_locks.reason IS '施加设备优先级锁的业务或安全原因说明';
COMMENT ON COLUMN command_runtime.device_priority_locks.created_at IS '优先级锁记录首次创建时间';
COMMENT ON COLUMN command_runtime.device_priority_locks.updated_at IS '优先级锁记录最近一次更新时间';

ALTER TABLE command_runtime.device_priority_locks ENABLE ROW LEVEL SECURITY;
ALTER TABLE command_runtime.device_priority_locks FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS device_priority_locks_migrator ON command_runtime.device_priority_locks;
CREATE POLICY device_priority_locks_migrator ON command_runtime.device_priority_locks
    FOR ALL TO s3_command_migrator
    USING (true)
    WITH CHECK (true);

DROP POLICY IF EXISTS device_priority_locks_tenant ON command_runtime.device_priority_locks;
CREATE POLICY device_priority_locks_tenant ON command_runtime.device_priority_locks
    FOR ALL TO s3_command_runtime
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

REVOKE ALL ON command_runtime.device_priority_locks FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON command_runtime.device_priority_locks TO s3_command_runtime;

RESET ROLE;

COMMIT;
