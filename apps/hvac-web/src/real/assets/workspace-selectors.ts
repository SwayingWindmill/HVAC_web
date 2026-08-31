import type { RealAssetsAssetRow, RealAssetsDeviceRow, RealAssetsHierarchyNode } from './model';

export type RealAssetsListMode = 'attention' | 'all';

function assetBindings(binding: RealAssetsDeviceRow['binding']) {
  if (binding.state === 'bound') return [binding];
  if (binding.state === 'multi-bound') return binding.bindings;
  return [];
}

function matchesDeviceSearch(row: RealAssetsDeviceRow, value: string): boolean {
  const query = value.trim().toLocaleLowerCase('zh-CN');
  if (!query) return true;
  const space = row.space.state === 'bound' ? row.space.space : undefined;
  const assets = assetBindings(row.binding).map((item) => item.asset);
  return [
    row.device.id,
    row.device.code,
    row.device.displayName,
    row.device.deviceType,
    space?.code,
    space?.displayName,
    ...assets.flatMap((item) => [item.code, item.displayName]),
  ].some((candidate) => candidate?.toLocaleLowerCase('zh-CN').includes(query));
}

export function indexRealAssetsHierarchy(root: RealAssetsHierarchyNode | null): ReadonlyMap<string, RealAssetsHierarchyNode> {
  const index = new Map<string, RealAssetsHierarchyNode>();
  const visit = (node: RealAssetsHierarchyNode) => {
    index.set(node.key, node);
    node.children.forEach(visit);
  };
  if (root) visit(root);
  return index;
}

export function filterRealAssetsDeviceRows(input: {
  readonly rows: readonly RealAssetsDeviceRow[];
  readonly search: string;
  readonly selectedDeviceIds?: ReadonlySet<string>;
  readonly listMode: RealAssetsListMode;
  readonly currentPending: boolean;
  readonly currentUnavailable: boolean;
}): RealAssetsDeviceRow[] {
  const attentionDeviceIds = new Set(input.rows
    .filter((row) => row.operational.needsAttention)
    .map((row) => row.device.id));
  return input.rows.filter((row) => (
    matchesDeviceSearch(row, input.search)
    && (!input.selectedDeviceIds || input.selectedDeviceIds.has(row.device.id))
    && (input.listMode === 'all' || input.currentPending || input.currentUnavailable || attentionDeviceIds.has(row.device.id))
  ));
}

export function filterRealAssetsAssetRows(input: {
  readonly rows: readonly RealAssetsAssetRow[];
  readonly search: string;
  readonly selectedHierarchy?: RealAssetsHierarchyNode;
  readonly selectedDeviceIds?: ReadonlySet<string>;
  readonly listMode: RealAssetsListMode;
  readonly currentPending: boolean;
  readonly currentUnavailable: boolean;
}): RealAssetsAssetRow[] {
  const selectedAssetId = input.selectedHierarchy?.kind === 'asset'
    ? input.selectedHierarchy.key.slice('asset:'.length)
    : null;
  const query = input.search.trim().toLocaleLowerCase('zh-CN');
  return input.rows.filter((row) => {
    const matchesQuery = !query || [
      row.asset.id,
      row.asset.code,
      row.asset.displayName,
      row.asset.assetType,
      row.space.state === 'bound' ? row.space.space.displayName : '',
      ...row.devices.flatMap((device) => [device.device.code, device.device.displayName]),
      ...row.sensors.flatMap((sensor) => [sensor.code, sensor.displayName]),
    ].some((value) => value.toLocaleLowerCase('zh-CN').includes(query));
    if (!matchesQuery) return false;
    if (input.listMode === 'attention' && !input.currentPending && !input.currentUnavailable && !row.needsAttention) return false;
    if (selectedAssetId) return row.asset.id === selectedAssetId;
    if (!input.selectedHierarchy || input.selectedHierarchy.kind === 'site') return true;
    return row.devices.some((device) => input.selectedDeviceIds?.has(device.device.id));
  });
}

export function summarizeRealAssetsDevices(
  rows: readonly RealAssetsDeviceRow[],
  currentPending: boolean,
  currentUnavailable: boolean,
) {
  const unavailable = currentPending || currentUnavailable;
  return {
    total: rows.length,
    attention: unavailable ? null : rows.filter((row) => row.operational.needsAttention).length,
    offline: unavailable ? null : rows.filter((row) => row.operational.connection.state === 'OFFLINE').length,
    healthyData: unavailable ? null : rows.filter((row) => (
      row.operational.telemetry.readiness === 'CURRENT'
      && row.operational.telemetry.freshness === 'FRESH'
      && row.operational.telemetry.quality === 'GOOD'
    )).length,
    connectionUnknown: unavailable ? null : rows.filter((row) => row.operational.connection.state === 'UNKNOWN').length,
  } as const;
}
