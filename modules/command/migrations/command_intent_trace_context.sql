BEGIN;

SET LOCAL ROLE s3_command_migrator;

ALTER TABLE command_runtime.command_intents
    ADD COLUMN IF NOT EXISTS trace_id text NOT NULL DEFAULT ''
    CHECK (trace_id = '' OR trace_id ~ '^[0-9a-f]{32}$');

COMMENT ON COLUMN command_runtime.command_intents.trace_id IS '命令提交时记录的 W3C 32 位十六进制分布式追踪编号（未携带追踪上下文时为空字符串）。';

RESET ROLE;

COMMIT;
