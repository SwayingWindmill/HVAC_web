BEGIN;
SET LOCAL ROLE s4_alarm_migrator;

-- Evaluation evidence stays 30 days (#651). Rows inside the window still cannot be changed or
-- deleted; an older row can only be deleted.
CREATE FUNCTION alarm_runtime.reject_alarm_evaluation_event_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' AND OLD.evaluated_at < now() - interval '30 days' THEN
    RETURN OLD;
  END IF;
  RAISE EXCEPTION 'alarm evaluation evidence is immutable for 30 days';
END $$;

DROP TRIGGER alarm_evaluation_event_immutable ON alarm_runtime.alarm_evaluation_event;
CREATE TRIGGER alarm_evaluation_event_immutable
BEFORE UPDATE OR DELETE ON alarm_runtime.alarm_evaluation_event
FOR EACH ROW EXECUTE FUNCTION alarm_runtime.reject_alarm_evaluation_event_mutation();

-- The runtime has no DELETE on the table; this deletes up to batch_size expired rows across
-- Tenants and returns how many it deleted.
CREATE FUNCTION alarm_runtime.prune_expired_alarm_evaluation_events(batch_size integer)
RETURNS bigint
LANGUAGE sql
VOLATILE
SECURITY DEFINER
SET search_path = pg_catalog, alarm_runtime
AS $$
  WITH expired AS (
    DELETE FROM alarm_runtime.alarm_evaluation_event
    WHERE (tenant_id, site_id, assignment_id, state_version) IN (
      SELECT tenant_id, site_id, assignment_id, state_version
      FROM alarm_runtime.alarm_evaluation_event
      WHERE evaluated_at < now() - interval '30 days'
      LIMIT batch_size
    )
    RETURNING 1
  )
  SELECT count(*) FROM expired
$$;

REVOKE ALL ON FUNCTION alarm_runtime.prune_expired_alarm_evaluation_events(integer) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION alarm_runtime.prune_expired_alarm_evaluation_events(integer) TO s4_alarm_runtime;

RESET ROLE;
COMMIT;
