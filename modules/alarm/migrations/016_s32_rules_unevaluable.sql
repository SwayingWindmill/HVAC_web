BEGIN;
SET LOCAL ROLE s4_alarm_migrator;

-- A rule that cannot be judged for a long time is itself a fault nobody hears of (#749): its
-- inputs are missing, stale, invalid or untrusted, so it neither raises nor clears. The state
-- remembers since when it has been unjudgeable; the scheduled pass raises one Site Alarm
-- "rules-unevaluable" listing such rules, and recovers it once they are judged again.
-- Outside its schedule a rule is not supposed to be judged, so that does not count.
ALTER TABLE alarm_runtime.alarm_evaluation_state
  ADD COLUMN indeterminate_since timestamptz;

CREATE FUNCTION alarm_runtime.track_indeterminate_since()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, alarm_runtime
AS $$
BEGIN
  IF NEW.status = 'INDETERMINATE' AND COALESCE(NEW.quality_blocker, '') <> 'OUTSIDE_SCHEDULE' THEN
    IF TG_OP = 'UPDATE' AND OLD.indeterminate_since IS NOT NULL
       AND OLD.status = 'INDETERMINATE' AND COALESCE(OLD.quality_blocker, '') <> 'OUTSIDE_SCHEDULE' THEN
      NEW.indeterminate_since := OLD.indeterminate_since;
    ELSE
      NEW.indeterminate_since := NEW.last_evaluated_at;
    END IF;
  ELSE
    NEW.indeterminate_since := NULL;
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER alarm_evaluation_state_indeterminate_since
  BEFORE INSERT OR UPDATE ON alarm_runtime.alarm_evaluation_state
  FOR EACH ROW EXECUTE FUNCTION alarm_runtime.track_indeterminate_since();

-- Rules already unjudgeable count from their last evaluation: at worst the first Alarm comes
-- one threshold after this migration.
UPDATE alarm_runtime.alarm_evaluation_state
SET indeterminate_since = last_evaluated_at
WHERE status = 'INDETERMINATE' AND COALESCE(quality_blocker, '') <> 'OUTSIDE_SCHEDULE';

CREATE INDEX alarm_evaluation_state_indeterminate_idx
  ON alarm_runtime.alarm_evaluation_state (tenant_id, indeterminate_since)
  WHERE indeterminate_since IS NOT NULL;

-- The Tenants the scheduled pass looks at: those with a rule unjudgeable since before the
-- threshold, and those with a "rules-unevaluable" Alarm still active, which may recover.
CREATE FUNCTION alarm_runtime.tenants_with_unevaluable_rules(since timestamptz)
RETURNS SETOF uuid
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, alarm_runtime
AS $$
  SELECT tenant_id FROM alarm_runtime.alarm_evaluation_state
  WHERE indeterminate_since IS NOT NULL AND indeterminate_since <= since
  UNION
  SELECT tenant_id FROM alarm_runtime.alarm_current
  WHERE alarm_type = 'rules-unevaluable' AND condition = 'ACTIVE'
$$;

REVOKE ALL ON FUNCTION alarm_runtime.tenants_with_unevaluable_rules(timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION alarm_runtime.tenants_with_unevaluable_rules(timestamptz) TO s4_alarm_runtime;

COMMIT;
