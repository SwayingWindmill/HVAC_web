export const PRESENCE_ACCEPTED_SIGNAL_TYPES =
  "ARRAY['SOURCE_ACTIVITY', 'EXPLICIT_CONNECT', 'EXPLICIT_DISCONNECT']::text[]";

export function buildOnboardingRevisionCheckSql(
  siteIdLiteral: string,
  expectedSiteRevision?: number | null,
): string {
  if (expectedSiteRevision === undefined) {
    return '';
  }
  if (
    expectedSiteRevision !== null &&
    (!Number.isInteger(expectedSiteRevision) || expectedSiteRevision < 1)
  ) {
    throw new Error(
      `expectedSiteRevision must be null or a positive integer, got ${expectedSiteRevision}`,
    );
  }
  if (expectedSiteRevision === null) {
    return `DO $$
DECLARE current_rev bigint;
BEGIN
  SELECT revision INTO current_rev FROM core_registry.sites WHERE id = ${siteIdLiteral} FOR UPDATE;
  IF current_rev IS NOT NULL THEN
    RAISE EXCEPTION 'site revision conflict: expected uncreated site, got revision %', current_rev USING ERRCODE = '40001';
  END IF;
END $$;

`;
  }
  return `DO $$
DECLARE current_rev bigint;
BEGIN
  SELECT revision INTO current_rev FROM core_registry.sites WHERE id = ${siteIdLiteral} FOR UPDATE;
  IF current_rev IS DISTINCT FROM ${expectedSiteRevision}::bigint THEN
    RAISE EXCEPTION 'site revision conflict: expected %, found %', ${expectedSiteRevision}, current_rev USING ERRCODE = '40001';
  END IF;
END $$;

`;
}

export function buildOnboardingAuditBeforeSql(
  tenantLiteral: string,
  siteIdLiteral: string,
): string {
  return `CREATE TEMP TABLE _onboarding_before (
  resource_type text NOT NULL,
  resource_id uuid NOT NULL,
  before_revision bigint NOT NULL,
  PRIMARY KEY (resource_type, resource_id)
) ON COMMIT DROP;

INSERT INTO _onboarding_before (resource_type, resource_id, before_revision)
SELECT 'SITE', id, revision FROM core_registry.sites WHERE tenant_id = ${tenantLiteral} AND id = ${siteIdLiteral}
UNION ALL
SELECT 'SPACE', id, revision FROM core_registry.spaces WHERE tenant_id = ${tenantLiteral} AND site_id = ${siteIdLiteral}
UNION ALL
SELECT 'ASSET', id, revision FROM core_registry.assets WHERE tenant_id = ${tenantLiteral} AND site_id = ${siteIdLiteral}
UNION ALL
SELECT 'DEVICE', id, revision FROM core_registry.devices WHERE tenant_id = ${tenantLiteral} AND site_id = ${siteIdLiteral}
UNION ALL
SELECT 'POINT', id, revision FROM core_registry.telemetry_points WHERE tenant_id = ${tenantLiteral} AND site_id = ${siteIdLiteral};`;
}

const onboardingAuditUuidExpr = `(
    substr(s.ms_hex, 1, 8) || '-' ||
    substr(s.ms_hex, 9, 4) || '-7' ||
    substr(s.rnd, 14, 3) || '-' ||
    substr('89ab', 1 + (get_byte(decode(substr(s.rnd, 17, 2), 'hex'), 0) & 3), 1) ||
    substr(s.rnd, 18, 3) || '-' ||
    substr(s.rnd, 21, 12)
  )::uuid`;

export function buildOnboardingAuditOutboxEmitSql(
  tenantLiteral: string,
  siteIdLiteral: string,
  principalLiteral: string,
): string {
  return `CREATE TEMP TABLE _onboarding_changes ON COMMIT DROP AS
WITH current_rows AS (
  SELECT 'SITE'::text AS resource_type, id AS resource_id, revision AS after_revision FROM core_registry.sites WHERE tenant_id = ${tenantLiteral} AND id = ${siteIdLiteral}
  UNION ALL
  SELECT 'SPACE'::text, id, revision FROM core_registry.spaces WHERE tenant_id = ${tenantLiteral} AND site_id = ${siteIdLiteral}
  UNION ALL
  SELECT 'ASSET'::text, id, revision FROM core_registry.assets WHERE tenant_id = ${tenantLiteral} AND site_id = ${siteIdLiteral}
  UNION ALL
  SELECT 'DEVICE'::text, id, revision FROM core_registry.devices WHERE tenant_id = ${tenantLiteral} AND site_id = ${siteIdLiteral}
  UNION ALL
  SELECT 'POINT'::text, id, revision FROM core_registry.telemetry_points WHERE tenant_id = ${tenantLiteral} AND site_id = ${siteIdLiteral}
)
SELECT
  cur.resource_type,
  cur.resource_id,
  prev.before_revision,
  cur.after_revision,
  CASE cur.resource_type
    WHEN 'SITE' THEN CASE WHEN prev.before_revision IS NULL THEN 'registry.site.created' ELSE 'registry.site.updated' END
    WHEN 'SPACE' THEN CASE WHEN prev.before_revision IS NULL THEN 'registry.space.created' ELSE 'registry.space.updated' END
    WHEN 'ASSET' THEN CASE WHEN prev.before_revision IS NULL THEN 'registry.asset.created' ELSE 'registry.asset.updated' END
    WHEN 'DEVICE' THEN CASE WHEN prev.before_revision IS NULL THEN 'registry.device.created' ELSE 'registry.device.updated' END
    WHEN 'POINT' THEN CASE WHEN prev.before_revision IS NULL THEN 'registry.point.created' ELSE 'registry.point.updated' END
  END AS event_type,
  CASE cur.resource_type
    WHEN 'SITE' THEN 'registry.site.write'
    WHEN 'SPACE' THEN 'registry.space.write'
    WHEN 'ASSET' THEN 'registry.asset.write'
    WHEN 'DEVICE' THEN 'registry.device.write'
    WHEN 'POINT' THEN 'registry.point.write'
  END AS action
FROM current_rows cur
LEFT JOIN _onboarding_before prev
  ON prev.resource_type = cur.resource_type AND prev.resource_id = cur.resource_id
WHERE prev.before_revision IS NULL OR cur.after_revision > prev.before_revision;

INSERT INTO core_registry.registry_audit_facts (
  event_id, tenant_id, site_id, principal_id, action, resource_type, resource_id,
  before_revision, after_revision, outcome, reason, occurred_at
)
SELECT
  ${onboardingAuditUuidExpr},
  ${tenantLiteral}::uuid, ${siteIdLiteral}::uuid, ${principalLiteral}::uuid, s.action, s.resource_type, s.resource_id,
  s.before_revision, s.after_revision, 'COMMITTED', 'site-onboarding sync', clock_timestamp()
FROM (
  SELECT
    c.*,
    lpad(to_hex((extract(epoch FROM clock_timestamp()) * 1000)::bigint), 12, '0') AS ms_hex,
    replace(gen_random_uuid()::text, '-', '') AS rnd
  FROM _onboarding_changes c
) s;

INSERT INTO core_registry.domain_outbox_events (
  id, tenant_id, site_id, event_type, schema_version, subject_type, subject_id,
  aggregate_type, aggregate_id, aggregate_version, occurred_at, payload, created_at
)
SELECT
  ${onboardingAuditUuidExpr},
  ${tenantLiteral}::uuid,
  CASE WHEN s.resource_type = 'SITE' THEN NULL::uuid ELSE ${siteIdLiteral}::uuid END,
  s.event_type, 1, s.resource_type, s.resource_id,
  s.resource_type, s.resource_id, s.after_revision, clock_timestamp(),
  jsonb_build_object(
    'siteId', ${siteIdLiteral}::text,
    'resourceType', s.resource_type,
    'resourceId', s.resource_id::text,
    'revision', s.after_revision,
    'source', 'site-onboarding'
  ),
  clock_timestamp()
FROM (
  SELECT
    c.*,
    lpad(to_hex((extract(epoch FROM clock_timestamp()) * 1000)::bigint), 12, '0') AS ms_hex,
    replace(gen_random_uuid()::text, '-', '') AS rnd
  FROM _onboarding_changes c
) s
ON CONFLICT (tenant_id, aggregate_type, aggregate_id, aggregate_version, event_type) DO NOTHING;`;
}

export function isSynthesizedTotalMeter(totalMeter: unknown): boolean {
  return Boolean(
    totalMeter &&
      typeof totalMeter === 'object' &&
      !Array.isArray(totalMeter) &&
      (totalMeter as Record<string, unknown>).kind === 'sum-of-sub-meters',
  );
}

export function canonical(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(canonical);
  if (value && typeof value === 'object') {
    const record = value as Record<string, unknown>;
    return Object.fromEntries(
      Object.keys(record)
        .sort()
        .map((key) => [key, canonical(record[key])]),
    );
  }
  return value;
}

export function sameJson(left: unknown, right: unknown): boolean {
  return JSON.stringify(canonical(left)) === JSON.stringify(canonical(right));
}

export function differences(want: unknown, have: unknown, path = ''): string[] {
  if (want && have && typeof want === 'object' && typeof have === 'object' && !Array.isArray(want)) {
    const left = want as Record<string, unknown>;
    const right = have as Record<string, unknown>;
    return [...new Set([...Object.keys(left), ...Object.keys(right)])].flatMap((key) =>
      differences(left[key], right[key], path ? `${path}.${key}` : key),
    );
  }
  return sameJson(want, have) ? [] : [`${path}: 清单 ${JSON.stringify(want)}，现在 ${JSON.stringify(have)}`];
}

export function scheduleCodes(sql: string): Array<{ code: string; jobType: string }> {
  return [...sql.matchAll(/INSERT INTO core_registry\.schedule_definitions[\s\S]*?ON CONFLICT/g)].flatMap(([block]) =>
    [...block.matchAll(/^\s*(?:SELECT |\()[^,]+, '([a-z0-9-]+)', '[^']*', [^,]+, [^,]+, '([A-Z_]+)'/gm)].map(
      ([, code, jobType]) => ({ code, jobType }),
    ),
  );
}
