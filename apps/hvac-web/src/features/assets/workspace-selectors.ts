import type { AssetsAssetRow, AssetsDeviceRow, AssetsHierarchyNode } from './model';

export type AssetsListMode = 'all' | 'attention' | 'offline' | 'data-issue' | 'connection-unknown';
export type AssetsConnectionFilter = 'all' | 'ONLINE' | 'OFFLINE' | 'UNKNOWN';
export type AssetsDataFilter = 'all' | 'healthy' | 'issue';
export type AssetsRunningFilter = 'all' | 'RUNNING' | 'STOPPED' | 'STANDBY' | 'UNKNOWN';

function assetBindings(binding: AssetsDeviceRow['binding']) {
  if (binding.state === 'bound') return [binding];
  if (binding.state === 'multi-bound') return binding.bindings;
  return [];
}

function matchesDeviceSearch(row: AssetsDeviceRow, value: string): boolean {
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

export function indexAssetsHierarchy(root: AssetsHierarchyNode | null): ReadonlyMap<string, AssetsHierarchyNode> {
  const index = new Map<string, AssetsHierarchyNode>();
  const visit = (node: AssetsHierarchyNode) => {
    index.set(node.key, node);
    node.children.forEach(visit);
  };
  if (root) visit(root);
  return index;
}

function matchesDeviceListMode(row: AssetsDeviceRow, mode: AssetsListMode): boolean {
  if (mode === 'all') return true;
  if (mode === 'attention') return row.operational.needsAttention;
  if (mode === 'offline') return row.operational.connection.state === 'OFFLINE';
  if (mode === 'connection-unknown') return row.operational.connection.state === 'UNKNOWN';
  return row.operational.attentionReasons.some((reason) => reason !== 'PRESENCE_OFFLINE');
}

function hasHealthyData(row: AssetsDeviceRow): boolean {
  return row.operational.telemetry.readiness === 'CURRENT'
    && row.operational.telemetry.freshness === 'FRESH'
    && row.operational.telemetry.quality === 'GOOD';
}

function hasDataIssue(row: AssetsDeviceRow): boolean {
  return row.operational.attentionReasons.some((reason) => reason !== 'PRESENCE_OFFLINE');
}

function runningState(row: AssetsDeviceRow): Exclude<AssetsRunningFilter, 'all'> {
  const point = row.representativePoints.find((candidate) => candidate.state === 'PRESENT' && (
    candidate.displayValue === 'RUNNING'
    || candidate.displayValue === 'STOPPED'
    || candidate.displayValue === 'STANDBY'
    || candidate.key.endsWith('_state')
    || candidate.key.endsWith('.state')
  ));
  if (point?.displayValue === 'RUNNING' || point?.displayValue === 'STOPPED' || point?.displayValue === 'STANDBY') {
    return point.displayValue;
  }
  return 'UNKNOWN';
}

export function filterAssetsDeviceRows(input: {
  readonly rows: readonly AssetsDeviceRow[];
  readonly search: string;
  readonly selectedDeviceIds?: ReadonlySet<string>;
  readonly listMode: AssetsListMode;
  readonly deviceType?: string;
  readonly connectionFilter?: AssetsConnectionFilter;
  readonly runningFilter?: AssetsRunningFilter;
  readonly dataFilter?: AssetsDataFilter;
  readonly currentPending: boolean;
  readonly currentUnavailable: boolean;
}): AssetsDeviceRow[] {
  return input.rows.filter((row) => (
    matchesDeviceSearch(row, input.search)
    && (!input.selectedDeviceIds || input.selectedDeviceIds.has(row.device.id))
    && (!input.deviceType || row.device.deviceType === input.deviceType)
    && (!input.connectionFilter || input.connectionFilter === 'all' || row.operational.connection.state === input.connectionFilter)
    && (!input.runningFilter || input.runningFilter === 'all' || runningState(row) === input.runningFilter)
    && (!input.dataFilter || input.dataFilter === 'all' || (input.dataFilter === 'healthy' ? hasHealthyData(row) : hasDataIssue(row)))
    && (input.currentPending || input.currentUnavailable || matchesDeviceListMode(row, input.listMode))
  ));
}

export function filterAssetsAssetRows(input: {
  readonly rows: readonly AssetsAssetRow[];
  readonly search: string;
  readonly selectedHierarchy?: AssetsHierarchyNode;
  readonly selectedDeviceIds?: ReadonlySet<string>;
  readonly listMode: AssetsListMode;
  readonly deviceType?: string;
  readonly connectionFilter?: AssetsConnectionFilter;
  readonly runningFilter?: AssetsRunningFilter;
  readonly dataFilter?: AssetsDataFilter;
  readonly currentPending: boolean;
  readonly currentUnavailable: boolean;
}): AssetsAssetRow[] {
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
    const matchingDevices = row.devices.filter((device) => (
      (!input.deviceType || device.device.deviceType === input.deviceType)
      && (!input.connectionFilter || input.connectionFilter === 'all' || device.operational.connection.state === input.connectionFilter)
      && (!input.runningFilter || input.runningFilter === 'all' || runningState(device) === input.runningFilter)
      && (!input.dataFilter || input.dataFilter === 'all' || (input.dataFilter === 'healthy' ? hasHealthyData(device) : hasDataIssue(device)))
    ));
    if ((input.deviceType || (input.connectionFilter && input.connectionFilter !== 'all') || (input.runningFilter && input.runningFilter !== 'all') || (input.dataFilter && input.dataFilter !== 'all')) && matchingDevices.length === 0) return false;
    if (!input.currentPending && !input.currentUnavailable) {
      if (input.listMode === 'attention' && !row.needsAttention) return false;
      if (input.listMode === 'offline' && row.offlineDeviceCount === 0) return false;
      if (input.listMode === 'data-issue' && row.dataIssueDeviceCount === 0) return false;
      if (input.listMode === 'connection-unknown' && row.connectionUnknownDeviceCount === 0) return false;
    }
    if (selectedAssetId) return row.asset.id === selectedAssetId;
    if (!input.selectedHierarchy || input.selectedHierarchy.kind === 'site') return true;
    return row.devices.some((device) => input.selectedDeviceIds?.has(device.device.id));
  });
}

export function summarizeAssetsDevices(
  rows: readonly AssetsDeviceRow[],
  currentPending: boolean,
  currentUnavailable: boolean,
) {
  const unavailable = currentPending || currentUnavailable;
  return {
    total: rows.length,
    online: unavailable ? null : rows.filter((row) => row.operational.connection.state === 'ONLINE').length,
    attention: unavailable ? null : rows.filter((row) => row.operational.needsAttention).length,
    offline: unavailable ? null : rows.filter((row) => row.operational.connection.state === 'OFFLINE').length,
    dataIssue: unavailable ? null : rows.filter(hasDataIssue).length,
    healthyData: unavailable ? null : rows.filter(hasHealthyData).length,
    connectionUnknown: unavailable ? null : rows.filter((row) => row.operational.connection.state === 'UNKNOWN').length,
  } as const;
}
