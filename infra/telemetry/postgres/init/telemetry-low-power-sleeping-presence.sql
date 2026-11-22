\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s2_telemetry_migrator;

ALTER TABLE telemetry_runtime.presence_policies
  ADD COLUMN IF NOT EXISTS sleep_cycle_seconds integer;

ALTER TABLE telemetry_runtime.presence_policies
  DROP CONSTRAINT IF EXISTS presence_policies_sleep_cycle_check;
ALTER TABLE telemetry_runtime.presence_policies
  ADD CONSTRAINT presence_policies_sleep_cycle_check
  CHECK (
    sleep_cycle_seconds IS NULL
    OR (
      sleep_cycle_seconds BETWEEN 1 AND 604800
      AND sleep_cycle_seconds >= online_within_seconds
      AND offline_after_seconds > sleep_cycle_seconds
    )
  );

COMMENT ON COLUMN telemetry_runtime.presence_policies.sleep_cycle_seconds IS '低功耗休眠设备的期望唤醒上报周期（秒）；为空表示常在线设备，非空且介于在线判定窗口与离线阈值之间时在唤醒间隙判定为 SLEEPING。';

ALTER TABLE telemetry_runtime.presence_policies
  DROP CONSTRAINT IF EXISTS presence_policies_accepted_signal_types_check;
ALTER TABLE telemetry_runtime.presence_policies
  ADD CONSTRAINT presence_policies_accepted_signal_types_check
  CHECK (
    cardinality(accepted_signal_types) BETWEEN 1 AND 4
    AND accepted_signal_types <@ ARRAY['SOURCE_ACTIVITY', 'EXPLICIT_CONNECT', 'EXPLICIT_DISCONNECT', 'EXPLICIT_SLEEP']::text[]
  );

DO $update_signal_type_check$
DECLARE
  constraint_name text;
BEGIN
  FOR constraint_name IN
    SELECT conname
    FROM pg_constraint
    WHERE conrelid = 'telemetry_runtime.presence_signals'::regclass
      AND contype = 'c'
      AND pg_get_constraintdef(oid) ILIKE '%signal_type%'
  LOOP
    EXECUTE format('ALTER TABLE telemetry_runtime.presence_signals DROP CONSTRAINT %I', constraint_name);
  END LOOP;
END $update_signal_type_check$;

ALTER TABLE telemetry_runtime.presence_signals
  ADD CONSTRAINT presence_signals_signal_type_check
  CHECK (signal_type IN ('SOURCE_ACTIVITY', 'EXPLICIT_CONNECT', 'EXPLICIT_DISCONNECT', 'EXPLICIT_SLEEP'));

DO $update_presence_state_check$
DECLARE
  constraint_name text;
BEGIN
  FOR constraint_name IN
    SELECT conname
    FROM pg_constraint
    WHERE conrelid = 'telemetry_runtime.device_presence'::regclass
      AND contype = 'c'
      AND pg_get_constraintdef(oid) LIKE '%current_state%'
      AND pg_get_constraintdef(oid) LIKE '%ONLINE%'
  LOOP
    EXECUTE format('ALTER TABLE telemetry_runtime.device_presence DROP CONSTRAINT %I', constraint_name);
  END LOOP;
END $update_presence_state_check$;

ALTER TABLE telemetry_runtime.device_presence
  ADD CONSTRAINT device_presence_current_state_check
  CHECK (current_state IS NULL OR current_state IN ('ONLINE', 'SLEEPING', 'OFFLINE', 'UNKNOWN'));

DO $update_display_state_check$
DECLARE
  constraint_name text;
BEGIN
  FOR constraint_name IN
    SELECT conname
    FROM pg_constraint
    WHERE conrelid = 'telemetry_runtime.device_observation_snapshots'::regclass
      AND contype = 'c'
      AND pg_get_constraintdef(oid) LIKE '%display_state%'
      AND pg_get_constraintdef(oid) LIKE '%STALE%'
  LOOP
    EXECUTE format('ALTER TABLE telemetry_runtime.device_observation_snapshots DROP CONSTRAINT %I', constraint_name);
  END LOOP;
END $update_display_state_check$;

ALTER TABLE telemetry_runtime.device_observation_snapshots
  ADD CONSTRAINT device_observation_snapshots_display_state_check
  CHECK (display_state IS NULL OR display_state IN ('ONLINE', 'SLEEPING', 'OFFLINE', 'STALE', 'UNKNOWN', 'UNAVAILABLE'));

RESET ROLE;

COMMIT;
