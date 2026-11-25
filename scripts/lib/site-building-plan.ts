export const BUILDING_ROOM_KINDS = [
  'hall',
  'private',
  'smallPrivate',
  'kitchen',
  'plantRoom',
  'service',
  'corridor',
  'core',
  'shaft',
] as const;

const BUILDING_ROOM_KIND_SET = new Set<string>(BUILDING_ROOM_KINDS);

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value);
}

function isFiniteRange(value: unknown, limit: number): value is [number, number] {
  return (
    Array.isArray(value)
    && value.length === 2
    && typeof value[0] === 'number'
    && typeof value[1] === 'number'
    && Number.isFinite(value[0])
    && Number.isFinite(value[1])
    && value[0] >= 0
    && value[0] < value[1]
    && value[1] <= limit
  );
}

/** Validate an optional site.buildingPlan manifest block against platform-gateway bounds. */
export function validateSiteBuildingPlan(plan: unknown, problems: string[]): void {
  if (plan == null) return;
  if (!isPlainObject(plan)) {
    problems.push('site.buildingPlan must be an object or null');
    return;
  }
  const { name, width, depth, floors } = plan;
  if (typeof name !== 'string' || !name.trim() || name.length > 128) {
    problems.push('site.buildingPlan.name must be 1..128 characters');
  }
  if (typeof width !== 'number' || !Number.isFinite(width) || width <= 0 || width > 1000) {
    problems.push('site.buildingPlan.width must be in (0, 1000]');
  }
  if (typeof depth !== 'number' || !Number.isFinite(depth) || depth <= 0 || depth > 1000) {
    problems.push('site.buildingPlan.depth must be in (0, 1000]');
  }
  if (!Array.isArray(floors) || floors.length < 1 || floors.length > 100) {
    problems.push('site.buildingPlan.floors must have 1..100 floors');
    return;
  }

  const maxW = typeof width === 'number' && Number.isFinite(width) ? width : 0;
  const maxD = typeof depth === 'number' && Number.isFinite(depth) ? depth : 0;
  const seenLevels = new Set<number>();

  for (const [floorIndex, floor] of floors.entries()) {
    if (!isPlainObject(floor)) {
      problems.push(`site.buildingPlan.floors[${floorIndex}] must be an object`);
      continue;
    }
    const { level, label, height, rooms } = floor;
    if (typeof level !== 'number' || !Number.isInteger(level) || level < 1 || level > 200) {
      problems.push(`site.buildingPlan.floors[${floorIndex}].level must be an integer 1..200`);
    } else if (seenLevels.has(level)) {
      problems.push(`site.buildingPlan: duplicate floor level ${String(level)}`);
    } else {
      seenLevels.add(level);
    }
    if (typeof label !== 'string' || !label.trim() || label.length > 128) {
      problems.push(`site.buildingPlan.floors[${floorIndex}].label must be 1..128 characters`);
    }
    if (typeof height !== 'number' || !Number.isFinite(height) || height <= 0 || height > 30) {
      problems.push(`site.buildingPlan.floors[${floorIndex}].height must be in (0, 30]`);
    }
    if (!Array.isArray(rooms) || rooms.length < 1 || rooms.length > 200) {
      problems.push(`site.buildingPlan.floors[${floorIndex}].rooms must have 1..200 rooms`);
      continue;
    }
    const seenRooms = new Set<string>();
    for (const [roomIndex, room] of rooms.entries()) {
      const prefix = `site.buildingPlan.floors[${floorIndex}].rooms[${roomIndex}]`;
      if (!isPlainObject(room)) {
        problems.push(`${prefix} must be an object`);
        continue;
      }
      if (typeof room.id !== 'string' || !room.id.trim() || room.id.length > 64) {
        problems.push(`${prefix}.id must be 1..64 characters`);
      } else if (seenRooms.has(room.id)) {
        problems.push(`site.buildingPlan.floors[${floorIndex}]: duplicate room id ${room.id}`);
      } else {
        seenRooms.add(room.id);
      }
      if (typeof room.name !== 'string' || !room.name.trim() || room.name.length > 128) {
        problems.push(`${prefix}.name must be 1..128 characters`);
      }
      if (typeof room.kind !== 'string' || !BUILDING_ROOM_KIND_SET.has(room.kind)) {
        problems.push(`${prefix}.kind ${String(room.kind)} is unsupported`);
      }
      if (!isFiniteRange(room.x, maxW)) {
        problems.push(`${prefix}.x must satisfy 0 <= x[0] < x[1] <= width`);
      }
      if (!isFiniteRange(room.z, maxD)) {
        problems.push(`${prefix}.z must satisfy 0 <= z[0] < z[1] <= depth`);
      }
    }
  }
}

export function buildSiteRowSql(
  site: { code: string; name: string; timezone: string; buildingPlan?: unknown },
  tenantSql: string,
  siteIdSql: string,
  helpers: {
    sqlLiteral: (v: unknown) => string;
    sqlJson: (v: unknown) => string;
    fromFile: (...cols: string[]) => ReadonlyArray<readonly [string, string]>;
    ACTIVE: ReadonlyArray<readonly [string, string]>;
    updateWhenChanged: (table: string, assignments: ReadonlyArray<readonly [string, string]>) => string;
  },
): string {
  const hasBuildingPlan = Object.hasOwn(site, 'buildingPlan');
  const buildingPlanValue = hasBuildingPlan && site.buildingPlan != null ? helpers.sqlJson(site.buildingPlan) : 'NULL';
  const siteAssignments = [...helpers.fromFile('code', 'display_name', 'timezone'), ...helpers.ACTIVE, ...(hasBuildingPlan ? helpers.fromFile('building_plan') : [])];
  return `INSERT INTO core_registry.sites (id, code, display_name, timezone, status, revision, created_at, updated_at, tenant_id, floor_area_m2, climate_zone, building_plan)
VALUES (${siteIdSql}, ${helpers.sqlLiteral(site.code)}, ${helpers.sqlLiteral(site.name)}, ${helpers.sqlLiteral(site.timezone)}, 'ACTIVE', 1, clock_timestamp(), clock_timestamp(), ${tenantSql}, NULL, NULL, ${buildingPlanValue})
ON CONFLICT (id) ${helpers.updateWhenChanged('sites', siteAssignments)};`;
}

export function buildingPlanStateSqlField(siteSql: string): string {
  return `  'siteBuildingPlan', (SELECT building_plan FROM core_registry.sites WHERE id = ${siteSql}),`;
}

export function planSiteBuildingPlan(
  profile: { site: { buildingPlan?: unknown } },
  state: { siteBuildingPlan?: unknown },
  sameJsonFn: (left: unknown, right: unknown) => boolean,
  add: (area: string, status: string, item: string, detail?: string) => void,
): void {
  if (!Object.hasOwn(profile.site, 'buildingPlan')) return;
  const want = profile.site.buildingPlan ?? null;
  const have = state.siteBuildingPlan ?? null;
  if (want === null) {
    if (have === null) add('站点和设备', 'ok', '数字孪生建筑平面');
    else add('站点和设备', 'differs', '数字孪生建筑平面', '清单设为 null，库里有旧平面');
    return;
  }
  if (have === null) add('站点和设备', 'missing', '数字孪生建筑平面');
  else if (!sameJsonFn(want, have)) add('站点和设备', 'differs', '数字孪生建筑平面', '楼层或房间布局和清单不同');
  else add('站点和设备', 'ok', '数字孪生建筑平面');
}
