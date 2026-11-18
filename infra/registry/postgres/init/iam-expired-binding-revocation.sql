\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s1_iam_migrator;

CREATE INDEX IF NOT EXISTS telemetry_revocation_facts_expiry_lookup_idx
  ON iam.telemetry_revocation_facts (tenant_id, source_type, source_id, reason_code, occurred_at);

-- 自然到期的临时角色绑定与租户成员资格不会触发行更新，由轮询在事务内为已过有效期且尚未登记吊销事实的记录补发事件。
CREATE OR REPLACE FUNCTION iam.sweep_expired_telemetry_revocations(
  tenant_value uuid,
  at_value timestamptz DEFAULT clock_timestamp()
)
RETURNS integer
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, iam
AS $sweep$
DECLARE
  emitted_roles integer := 0;
  emitted_memberships integer := 0;
  policy_rev text;
BEGIN
  IF tenant_value IS NULL THEN
    RETURN 0;
  END IF;
  PERFORM pg_advisory_xact_lock(hashtext('iam.expire_telemetry'), hashtext(tenant_value::text));
  policy_rev := COALESCE(iam.active_telemetry_policy_revision(tenant_value), 'telemetry-access:unavailable');

  INSERT INTO iam.telemetry_revocation_facts
    (tenant_id, principal_id, source_type, source_id, action, policy_revision, reason_code, occurred_at)
  SELECT
    binding.tenant_id,
    binding.principal_id,
    'ROLE_BINDING',
    binding.id,
    'telemetry.*',
    policy_rev,
    'ROLE_BINDING_EXPIRED',
    at_value
  FROM iam.role_bindings binding
  WHERE binding.tenant_id = tenant_value
    AND binding.status = 'ACTIVE'
    AND binding.valid_to IS NOT NULL
    AND binding.valid_to <= at_value
    AND NOT EXISTS (
      SELECT 1
      FROM iam.telemetry_revocation_facts fact
      WHERE fact.tenant_id = binding.tenant_id
        AND fact.source_type = 'ROLE_BINDING'
        AND fact.source_id = binding.id
        AND fact.reason_code = 'ROLE_BINDING_EXPIRED'
        AND fact.occurred_at >= binding.valid_to
    );
  GET DIAGNOSTICS emitted_roles = ROW_COUNT;

  INSERT INTO iam.telemetry_revocation_facts
    (tenant_id, principal_id, source_type, source_id, policy_revision, reason_code, occurred_at)
  SELECT
    membership.tenant_id,
    membership.principal_id,
    'MEMBERSHIP',
    membership.id,
    policy_rev,
    'MEMBERSHIP_EXPIRED',
    at_value
  FROM iam.tenant_memberships membership
  WHERE membership.tenant_id = tenant_value
    AND membership.status = 'ACTIVE'
    AND membership.valid_to IS NOT NULL
    AND membership.valid_to <= at_value
    AND NOT EXISTS (
      SELECT 1
      FROM iam.telemetry_revocation_facts fact
      WHERE fact.tenant_id = membership.tenant_id
        AND fact.source_type = 'MEMBERSHIP'
        AND fact.source_id = membership.id
        AND fact.reason_code = 'MEMBERSHIP_EXPIRED'
        AND fact.occurred_at >= membership.valid_to
    );
  GET DIAGNOSTICS emitted_memberships = ROW_COUNT;

  RETURN emitted_roles + emitted_memberships;
END
$sweep$;

REVOKE ALL ON FUNCTION iam.sweep_expired_telemetry_revocations(uuid, timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION iam.sweep_expired_telemetry_revocations(uuid, timestamptz) TO s2_iam_grant_runtime;

RESET ROLE;

COMMIT;
