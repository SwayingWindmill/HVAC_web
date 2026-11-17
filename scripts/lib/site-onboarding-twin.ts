const sqlLiteral = (value: string): string => `'${String(value).replaceAll("'", "''")}'`;

export const SEMANTIC_ROLES = [
  'ACTIVE_POWER',
  'RUN_STATE',
  'CHW_LEAVING_TEMP',
  'CHW_ENTERING_TEMP',
  'CW_LEAVING_TEMP',
  'CW_ENTERING_TEMP',
  'SUPPLY_WATER_TEMP',
  'RETURN_WATER_TEMP',
  'COOLING_LOAD',
  'WATER_FLOW',
  'ZONE_TEMP',
] as const;

const SEMANTIC_ROLE_SET = new Set<string>(SEMANTIC_ROLES);

export const ASSET_RELATIONSHIP_ROLES = ['SERVES'] as const;
const ASSET_RELATIONSHIP_ROLE_SET = new Set<string>(ASSET_RELATIONSHIP_ROLES);

// How a rerun moves an existing point_subject_bindings row to the point's status (EXCLUDED.status).
export const SUBJECT_BINDING_LIFECYCLE: ReadonlyArray<readonly [string, string]> = [
  ['status', 'EXCLUDED.status'],
  ['valid_from', `CASE WHEN EXCLUDED.status = 'ACTIVE' AND (point_subject_bindings.status <> 'ACTIVE' OR point_subject_bindings.valid_to IS NOT NULL) THEN clock_timestamp() ELSE point_subject_bindings.valid_from END`],
  ['valid_to', `CASE WHEN EXCLUDED.status = 'ACTIVE' THEN NULL WHEN point_subject_bindings.status = 'ACTIVE' AND point_subject_bindings.valid_to IS NULL THEN GREATEST(clock_timestamp(), point_subject_bindings.valid_from + interval '1 millisecond') ELSE point_subject_bindings.valid_to END`],
];

const ASSET_RELATIONSHIP_LIFECYCLE: ReadonlyArray<readonly [string, string]> = [
  ['status', `'ACTIVE'`],
  ['valid_from', `CASE WHEN asset_relationships.status <> 'ACTIVE' OR asset_relationships.valid_to IS NOT NULL THEN clock_timestamp() ELSE asset_relationships.valid_from END`],
  ['valid_to', 'NULL'],
];

const SIMULATOR_SEMANTIC_ROLE_BY_TELEMETRY_KEY = new Map<string, string>([
  ['chiller.power', 'ACTIVE_POWER'],
  ['chwp.power', 'ACTIVE_POWER'],
  ['cwp.power', 'ACTIVE_POWER'],
  ['cooling_tower.power', 'ACTIVE_POWER'],
  ['hvac_meter.active_power', 'ACTIVE_POWER'],
  ['chiller.run_state', 'RUN_STATE'],
  ['chwp.run_state', 'RUN_STATE'],
  ['cwp.run_state', 'RUN_STATE'],
  ['cooling_tower.run_state', 'RUN_STATE'],
  ['chiller.leaving_chilled_water_temperature', 'CHW_LEAVING_TEMP'],
  ['chiller.entering_chilled_water_temperature', 'CHW_ENTERING_TEMP'],
  ['chiller.entering_cooling_water_temperature', 'CW_ENTERING_TEMP'],
  ['cooling_tower.entering_water_temperature', 'CW_ENTERING_TEMP'],
  ['cooling_tower.leaving_water_temperature', 'CW_LEAVING_TEMP'],
  ['btu_meter.supply_water_temperature', 'SUPPLY_WATER_TEMP'],
  ['btu_meter.return_water_temperature', 'RETURN_WATER_TEMP'],
  ['btu_meter.instant_cooling_capacity', 'COOLING_LOAD'],
  ['btu_meter.flow_rate', 'WATER_FLOW'],
  ['chwp.flow_rate', 'WATER_FLOW'],
  ['cwp.flow_rate', 'WATER_FLOW'],
  ['zone.temperature', 'ZONE_TEMP'],
]);

const SIMULATOR_HYDRAULIC_EDGES: ReadonlyArray<readonly [string, string, string]> = [
  ['equipment-chwp-01', 'equipment-chiller-01', 'SERVES'],
  ['equipment-cwp-01', 'equipment-chiller-01', 'SERVES'],
  ['equipment-ct-01', 'equipment-chiller-01', 'SERVES'],
];

interface TwinAssetLike {
  id: string;
  code: string;
}

interface TwinRelationshipInput {
  from?: string;
  to?: string;
  role?: string;
}

interface TwinPointLike {
  id?: string;
  code: string;
  pointIndex?: number;
  pointType?: string;
  status?: string;
  asset?: string;
  semanticRole?: string;
  device?: {
    code: string;
    asset?: string;
    deviceIndex?: number;
  };
}

interface TwinDeviceLike {
  code: string;
  points: TwinPointLike[];
}

interface TwinProfileLike {
  tenantId: string;
  site: { id: string };
  assets?: TwinAssetLike[];
  assetRelationships?: TwinRelationshipInput[];
}

interface TwinStateLike {
  pointSemanticRoles?: Record<string, string>;
  assetRelationships?: Array<{ fromAssetId: string; toAssetId: string; role: string }>;
}

function resolveAssetRef(assets: readonly TwinAssetLike[], ref: string | undefined): TwinAssetLike | null {
  if (!ref) return null;
  return assets.find((asset) => asset.id === ref || asset.code === ref) ?? null;
}

function resolvedAssetRelationships(
  profile: TwinProfileLike,
  rowId: (siteId: string, kind: string, index: number) => string,
): Array<{ id: string; fromAssetId: string; toAssetId: string; role: string }> {
  const assets = profile.assets ?? [];
  return (profile.assetRelationships ?? []).map((rel, index) => {
    const fromAsset = resolveAssetRef(assets, rel?.from);
    const toAsset = resolveAssetRef(assets, rel?.to);
    if (!fromAsset) throw new Error(`assetRelationships[${index}]: unknown from asset ${rel?.from}`);
    if (!toAsset) throw new Error(`assetRelationships[${index}]: unknown to asset ${rel?.to}`);
    if (!rel?.role || !ASSET_RELATIONSHIP_ROLE_SET.has(rel.role)) {
      throw new Error(`assetRelationships[${index}]: unsupported role ${rel?.role}`);
    }
    if (fromAsset.id === toAsset.id) {
      throw new Error(`assetRelationships[${index}]: asset ${fromAsset.code} cannot serve itself`);
    }
    return {
      id: rowId(profile.site.id, '6600', index + 1),
      fromAssetId: fromAsset.id,
      toAssetId: toAsset.id,
      role: rel.role,
    };
  });
}

export function validatePlantTwinFields(
  profile: TwinProfileLike,
  devices: readonly TwinDeviceLike[],
  problems: string[],
): void {
  for (const device of devices) {
    for (const point of device.points) {
      if (point.semanticRole != null) {
        if (typeof point.semanticRole !== 'string' || !SEMANTIC_ROLE_SET.has(point.semanticRole)) {
          problems.push(`${device.code}/${point.code}: unknown semanticRole ${String(point.semanticRole)}`);
        } else if (point.pointType === 'COMMAND') {
          problems.push(`${device.code}/${point.code}: COMMAND point cannot declare semanticRole`);
        }
      }
    }
  }

  if (profile.assetRelationships == null) return;
  if (!Array.isArray(profile.assetRelationships)) {
    problems.push('assetRelationships must be an array');
    return;
  }

  const assets = profile.assets ?? [];
  const seenEdges = new Set<string>();
  const adjacency = new Map<string, string[]>();
  for (const [index, rel] of profile.assetRelationships.entries()) {
    const label = `assetRelationships[${index}]`;
    const fromAsset = resolveAssetRef(assets, rel?.from);
    const toAsset = resolveAssetRef(assets, rel?.to);
    if (!fromAsset) problems.push(`${label}: unknown from asset ${String(rel?.from)}`);
    if (!toAsset) problems.push(`${label}: unknown to asset ${String(rel?.to)}`);
    if (!rel?.role || !ASSET_RELATIONSHIP_ROLE_SET.has(rel.role)) {
      problems.push(`${label}: unsupported role ${String(rel?.role)} (expected ${ASSET_RELATIONSHIP_ROLES.join(', ')})`);
    }
    if (fromAsset && toAsset) {
      if (fromAsset.id === toAsset.id) {
        problems.push(`${label}: asset ${fromAsset.code} cannot serve itself`);
      } else if (rel?.role && ASSET_RELATIONSHIP_ROLE_SET.has(rel.role)) {
        const key = `${fromAsset.id}|${toAsset.id}|${rel.role}`;
        if (seenEdges.has(key)) problems.push(`${label}: duplicate edge ${fromAsset.code} -> ${toAsset.code} (${rel.role})`);
        seenEdges.add(key);
        const targets = adjacency.get(fromAsset.id) ?? [];
        targets.push(toAsset.id);
        adjacency.set(fromAsset.id, targets);
      }
    }
  }

  const visitState = new Map<string, number>();
  const hasCycle = (nodeId: string): boolean => {
    const state = visitState.get(nodeId) ?? 0;
    if (state === 1) return true;
    if (state === 2) return false;
    visitState.set(nodeId, 1);
    for (const nextId of adjacency.get(nodeId) ?? []) {
      if (hasCycle(nextId)) return true;
    }
    visitState.set(nodeId, 2);
    return false;
  };
  for (const nodeId of adjacency.keys()) {
    if (hasCycle(nodeId)) {
      problems.push('assetRelationships: hydraulic SERVES graph contains a cycle');
      break;
    }
  }
}

export function buildAssetRelationshipsSql(
  profile: TwinProfileLike,
  helpers: {
    rowId: (siteId: string, kind: string, index: number) => string;
    updateWhenChanged: (table: string, assignments: ReadonlyArray<readonly [string, string]>) => string;
  },
): string {
  if (!Object.hasOwn(profile, 'assetRelationships')) {
    return '';
  }
  const tenant = sqlLiteral(profile.tenantId);
  const siteId = sqlLiteral(profile.site.id);
  const fromColumns = (...columns: string[]): Array<readonly [string, string]> =>
    columns.map((column) => [column, `EXCLUDED.${column}`]);
  const rels = resolvedAssetRelationships(profile, helpers.rowId);
  const activeTupleFilter = rels.length
    ? `AND (from_asset_id, to_asset_id, relationship_role) NOT IN (\n${rels.map((rel) => `    (${sqlLiteral(rel.fromAssetId)}::uuid, ${sqlLiteral(rel.toAssetId)}::uuid, ${sqlLiteral(rel.role)}::text)`).join(',\n')}\n  )`
    : '';
  const retireSql = `UPDATE core_registry.asset_relationships
SET status = 'RETIRED',
    valid_to = GREATEST(clock_timestamp(), valid_from + interval '1 millisecond'),
    revision = revision + 1,
    updated_at = clock_timestamp()
WHERE tenant_id = ${tenant}
  AND site_id = ${siteId}
  AND status = 'ACTIVE'
  AND valid_to IS NULL${activeTupleFilter ? `\n  ${activeTupleFilter}` : ''};`;

  if (rels.length === 0) {
    return `${retireSql}\n`;
  }

  const relRows = rels.map((rel) =>
    `  (${sqlLiteral(rel.id)}, ${tenant}, ${siteId}, ${sqlLiteral(rel.fromAssetId)}, ${sqlLiteral(rel.toAssetId)}, ${sqlLiteral(rel.role)}, 'ACTIVE', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp())`
  ).join(',\n');
  const upsertSql = `INSERT INTO core_registry.asset_relationships (id, tenant_id, site_id, from_asset_id, to_asset_id, relationship_role, status, valid_from, valid_to, revision, created_at, updated_at)
SELECT COALESCE((SELECT existing.id FROM core_registry.asset_relationships existing WHERE existing.tenant_id = v.tenant_id::uuid AND existing.site_id = v.site_id::uuid AND existing.from_asset_id = v.from_asset_id::uuid AND existing.to_asset_id = v.to_asset_id::uuid AND existing.relationship_role = v.relationship_role ORDER BY existing.created_at LIMIT 1), v.id::uuid),
  v.tenant_id::uuid, v.site_id::uuid, v.from_asset_id::uuid, v.to_asset_id::uuid, v.relationship_role, v.status, v.valid_from, v.valid_to::timestamptz, v.revision::bigint, v.created_at, v.updated_at
FROM (VALUES
${relRows}
) AS v(id, tenant_id, site_id, from_asset_id, to_asset_id, relationship_role, status, valid_from, valid_to, revision, created_at, updated_at)
ON CONFLICT (id) ${helpers.updateWhenChanged('asset_relationships', [...fromColumns('from_asset_id', 'to_asset_id', 'relationship_role'), ...ASSET_RELATIONSHIP_LIFECYCLE])};`;

  return `${retireSql}\n${upsertSql}\n`;
}

export function buildPointSubjectBindingsSql(
  profile: TwinProfileLike,
  points: readonly TwinPointLike[],
  helpers: {
    rowId: (siteId: string, kind: string, index: number) => string;
    pointStatus: (point: TwinPointLike) => string;
    updateWhenChanged: (table: string, assignments: ReadonlyArray<readonly [string, string]>) => string;
  },
): string {
  const tenant = sqlLiteral(profile.tenantId);
  const siteId = sqlLiteral(profile.site.id);
  const fromColumns = (...columns: string[]): Array<readonly [string, string]> =>
    columns.map((column) => [column, `EXCLUDED.${column}`]);

  const subjectRows = points.map((point) => {
    const device = point.device;
    if (!device || device.deviceIndex == null || point.pointIndex == null || !point.id) {
      throw new Error(`point ${point.code} is missing resolved identity`);
    }
    const asset = point.asset ?? device.asset;
    const fallbackId = helpers.rowId(profile.site.id, '6300', device.deviceIndex * 0x10000 + point.pointIndex + 1);
    const bindingRole = point.pointType === 'COMMAND' ? "'CONTROLS'" : "'DESCRIBES'";
    if (point.semanticRole != null) {
      if (typeof point.semanticRole !== 'string' || !SEMANTIC_ROLE_SET.has(point.semanticRole)) {
        throw new Error(`${device.code}/${point.code}: unknown semanticRole ${String(point.semanticRole)}`);
      }
      if (point.pointType === 'COMMAND') {
        throw new Error(`${device.code}/${point.code}: COMMAND point cannot declare semanticRole`);
      }
    }
    const semanticRole = point.semanticRole ? sqlLiteral(point.semanticRole) : 'NULL';
    return `  (${sqlLiteral(fallbackId)}, ${tenant}, ${siteId}, ${sqlLiteral(point.id)}, ${asset ? "'ASSET'" : "'SITE'"}, NULL, ${asset ? sqlLiteral(asset) : 'NULL'}, ${bindingRole}, ${sqlLiteral(helpers.pointStatus(point))}, clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp(), ${semanticRole})`;
  }).join(',\n');

  return `INSERT INTO core_registry.point_subject_bindings (id, tenant_id, site_id, point_id, subject_type, space_id, asset_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at, semantic_role)
SELECT COALESCE((SELECT existing.id FROM core_registry.point_subject_bindings existing WHERE existing.tenant_id = v.tenant_id::uuid AND existing.site_id = v.site_id::uuid AND existing.point_id = v.point_id::uuid AND existing.binding_role = v.binding_role ORDER BY existing.created_at LIMIT 1), v.id::uuid),
  v.tenant_id::uuid, v.site_id::uuid, v.point_id::uuid, v.subject_type, v.space_id::uuid, v.asset_id::uuid, v.binding_role, v.status, v.valid_from, v.valid_to::timestamptz, v.revision::bigint, v.created_at, v.updated_at, v.semantic_role::text
FROM (VALUES
${subjectRows}
) AS v(id, tenant_id, site_id, point_id, subject_type, space_id, asset_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at, semantic_role)
ON CONFLICT (id) ${helpers.updateWhenChanged('point_subject_bindings', [...fromColumns('point_id', 'subject_type', 'space_id', 'asset_id', 'semantic_role'), ...SUBJECT_BINDING_LIFECYCLE])};`;
}

export function plantTwinStateSqlFields(tenantSql: string, siteSql: string): string {
  return `  'pointSemanticRoles', (SELECT coalesce(json_object_agg(point_id, semantic_role), '{}') FROM core_registry.point_subject_bindings WHERE tenant_id = ${tenantSql} AND site_id = ${siteSql} AND status = 'ACTIVE' AND valid_to IS NULL AND semantic_role IS NOT NULL),
  'assetRelationships', (SELECT coalesce(json_agg(json_build_object('fromAssetId', from_asset_id, 'toAssetId', to_asset_id, 'role', relationship_role) ORDER BY relationship_role, from_asset_id, to_asset_id), '[]'::json) FROM core_registry.asset_relationships WHERE tenant_id = ${tenantSql} AND site_id = ${siteSql} AND status = 'ACTIVE' AND valid_to IS NULL),`;
}

export function planPlantTwinOnboarding(
  profile: TwinProfileLike,
  points: readonly TwinPointLike[],
  state: TwinStateLike,
  rowId: (siteId: string, kind: string, index: number) => string,
  pointStatus: (point: TwinPointLike) => string,
  add: (area: string, status: string, item: string, detail?: string) => void,
): { assetRelationshipItems: boolean; semanticRoleItems: boolean } {
  let assetRelationshipItems = false;
  if (Object.hasOwn(profile, 'assetRelationships')) {
    assetRelationshipItems = true;
    const wantEdges = resolvedAssetRelationships(profile, rowId).map(({ fromAssetId, toAssetId, role }) => ({ fromAssetId, toAssetId, role }));
    const haveEdges = Array.isArray(state.assetRelationships) ? state.assetRelationships : null;
    const edgeKey = (edge: { fromAssetId: string; toAssetId: string; role: string }) => `${edge.fromAssetId}|${edge.toAssetId}|${edge.role}`;
    const wantSet = new Set(wantEdges.map(edgeKey));
    const haveSet = new Set((haveEdges ?? []).map(edgeKey));
    const missingEdges = wantEdges.filter((edge) => !haveSet.has(edgeKey(edge)));
    const extraEdges = (haveEdges ?? []).filter((edge) => !wantSet.has(edgeKey(edge)));
    if (haveEdges === null) add('站点和设备', missingEdges.length ? 'missing' : 'ok', `资产水力关系 ${wantEdges.length} 条`);
    else if (extraEdges.length > 0) add('站点和设备', 'differs', `资产水力关系 ${wantEdges.length} 条`, `清单 ${wantEdges.length} 条，库里多 ${extraEdges.length} 条、缺 ${missingEdges.length} 条`);
    else if (missingEdges.length > 0) add('站点和设备', 'missing', `资产水力关系 ${wantEdges.length} 条`, `缺 ${missingEdges.length} 条`);
    else add('站点和设备', 'ok', `资产水力关系 ${wantEdges.length} 条`);
  }

  let semanticRoleItems = false;
  const semanticPoints = points.filter((point) => pointStatus(point) === 'ACTIVE' && point.semanticRole);
  if (semanticPoints.length || state.pointSemanticRoles) {
    semanticRoleItems = true;
    const haveRoles = state.pointSemanticRoles ?? {};
    const missingRoles = semanticPoints.filter((point) => !point.id || !haveRoles[point.id]);
    const changedRoles = semanticPoints.filter((point) => point.id && haveRoles[point.id] && haveRoles[point.id] !== point.semanticRole);
    const activeSemanticIds = new Set(semanticPoints.map((point) => point.id).filter((id): id is string => Boolean(id)));
    const clearedRoles = Object.keys(haveRoles).filter((id) => !activeSemanticIds.has(id));
    const details = [
      ...missingRoles.map((point) => `缺 ${point.device?.code ?? '?'}/${point.code}`),
      ...changedRoles.map((point) => `${point.device?.code ?? '?'}/${point.code} 应为 ${point.semanticRole ?? ''}`),
      ...clearedRoles.map((id) => `${id} 应清除 semantic_role`),
    ];
    const status = changedRoles.length || clearedRoles.length ? 'differs' : missingRoles.length ? 'missing' : 'ok';
    add('点位', status, `语义角色 ${semanticPoints.length} 个`, details.join('；'));
  }

  return { assetRelationshipItems, semanticRoleItems };
}

export function buildSimulatorPointSubjectsAndEdgesSql(
  points: ReadonlyArray<{
    deviceId: string;
    telemetryKey: string;
    pointType: string;
    subjectType: string;
    subjectId: string;
  }>,
  ids: {
    nextID: () => string;
    pointIdByRef: Map<string, string>;
    assetIdByKey: Map<string, string>;
    spaceIdByKey: Map<string, string>;
  },
  tenantId: string,
  siteId: string,
  siteUUIDFn: (site: unknown, offset: number) => string,
  site: unknown,
): string {
  const pointSubjects = points.map((point) => {
    const pointID = ids.pointIdByRef.get(`${point.deviceId}/${point.telemetryKey}`);
    if (!pointID) throw new Error(`missing simulator point id for ${point.deviceId}/${point.telemetryKey}`);
    const bindingRole = point.pointType === 'COMMAND' ? 'CONTROLS' : 'DESCRIBES';
    const semanticRole = point.pointType === 'COMMAND'
      ? null
      : (SIMULATOR_SEMANTIC_ROLE_BY_TELEMETRY_KEY.get(point.telemetryKey) ?? null);
    const semanticRoleSql = semanticRole ? sqlLiteral(semanticRole) : 'NULL';
    if (point.subjectType === 'SITE') {
      return `(${sqlLiteral(ids.nextID())}, ${sqlLiteral(tenantId)}, ${sqlLiteral(siteId)}, ${sqlLiteral(pointID)}, 'SITE', NULL, NULL, ${sqlLiteral(bindingRole)}, 'ACTIVE', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp(), ${semanticRoleSql})`;
    }
    if (point.subjectType === 'EQUIPMENT') {
      const assetID = ids.assetIdByKey.get(point.subjectId);
      if (!assetID) throw new Error(`missing simulator asset id for ${point.subjectId}`);
      return `(${sqlLiteral(ids.nextID())}, ${sqlLiteral(tenantId)}, ${sqlLiteral(siteId)}, ${sqlLiteral(pointID)}, 'ASSET', NULL, ${sqlLiteral(assetID)}, ${sqlLiteral(bindingRole)}, 'ACTIVE', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp(), ${semanticRoleSql})`;
    }
    if (point.subjectType === 'AREA') {
      const spaceID = ids.spaceIdByKey.get(point.subjectId);
      if (!spaceID) throw new Error(`missing simulator space id for ${point.subjectId}`);
      return `(${sqlLiteral(ids.nextID())}, ${sqlLiteral(tenantId)}, ${sqlLiteral(siteId)}, ${sqlLiteral(pointID)}, 'SPACE', ${sqlLiteral(spaceID)}, NULL, ${sqlLiteral(bindingRole)}, 'ACTIVE', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp(), ${semanticRoleSql})`;
    }
    throw new Error(`unsupported point subject ${point.subjectType}`);
  }).join(',\n');

  const assetRelationships = SIMULATOR_HYDRAULIC_EDGES.map(([fromKey, toKey, role], index) => {
    const fromID = ids.assetIdByKey.get(fromKey);
    const toID = ids.assetIdByKey.get(toKey);
    if (!fromID || !toID) throw new Error(`missing simulator hydraulic asset ids for ${fromKey} -> ${toKey}`);
    return `(
    ${sqlLiteral(siteUUIDFn(site, 0x850000000001 + index))}, ${sqlLiteral(tenantId)}, ${sqlLiteral(siteId)},
    ${sqlLiteral(fromID)}, ${sqlLiteral(toID)},
    ${sqlLiteral(role)}, 'ACTIVE', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp()
  )`;
  }).join(',\n');

  return `INSERT INTO core_registry.point_subject_bindings (id, tenant_id, site_id, point_id, subject_type, space_id, asset_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at, semantic_role) VALUES
${pointSubjects}
ON CONFLICT (id) DO UPDATE SET point_id=EXCLUDED.point_id, subject_type=EXCLUDED.subject_type, space_id=EXCLUDED.space_id, asset_id=EXCLUDED.asset_id, binding_role=EXCLUDED.binding_role, semantic_role=EXCLUDED.semantic_role, status='ACTIVE', valid_to=NULL, updated_at=clock_timestamp();

INSERT INTO core_registry.asset_relationships (id, tenant_id, site_id, from_asset_id, to_asset_id, relationship_role, status, valid_from, valid_to, revision, created_at, updated_at) VALUES
${assetRelationships}
ON CONFLICT (id) DO UPDATE SET from_asset_id=EXCLUDED.from_asset_id, to_asset_id=EXCLUDED.to_asset_id, relationship_role=EXCLUDED.relationship_role, status='ACTIVE', valid_to=NULL, updated_at=clock_timestamp();`;
}
