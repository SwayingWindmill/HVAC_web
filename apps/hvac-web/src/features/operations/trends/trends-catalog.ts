import type { Asset, Device, SiteAssetModel, TelemetryPoint } from '../../../api/generated/platformGateway.gen.ts';
import { formatTelemetryUnit, telemetryPointDefinition } from '../../../domain/centralPlantTelemetry.ts';

export const MAX_TREND_POINTS = 8;

export type TrendPresetId =
  | 'plant-overview'
  | 'chiller-comparison'
  | 'pump-hydraulics'
  | 'outdoor-load';

export interface TrendPointItem {
  readonly ref: string;
  readonly pointId: string;
  readonly deviceId: string;
  readonly deviceName: string;
  readonly assetId: string | null;
  readonly assetType: string | null;
  readonly pointCode: string;
  readonly sourceKey: string;
  readonly pointLabel: string;
  readonly seriesLabel: string;
  readonly unit: string;
  readonly precision: number;
  readonly kind: 'gauge' | 'state';
  readonly pointType: 'TELEMETRY' | 'SETTING' | 'STATE';
  readonly isSetpoint: boolean;
}

export interface TrendCatalog {
  readonly points: readonly TrendPointItem[];
  readonly byRef: ReadonlyMap<string, TrendPointItem>;
  readonly byDeviceId: ReadonlyMap<string, readonly TrendPointItem[]>;
}

export interface TrendPreset {
  readonly id: TrendPresetId;
  readonly title: string;
  readonly pointRefs: readonly string[];
  readonly unconnectedLabels: readonly string[];
}

const EXTRA_POINT_LABELS: Readonly<Record<string, string>> = {
  'chiller.leaving_cooling_water_temperature': '冷却水出水温度',
};

const PLANT_LEVEL_PREFIXES = ['hvac_meter.', 'btu_meter.', 'weather.'] as const;

function normalizeTrendUnit(rawUnit: string | null | undefined, fallbackUnit?: string): string {
  const candidate = rawUnit ?? fallbackUnit ?? null;
  if (!candidate) return '';
  if (candidate === 'K') return '°C';
  return formatTelemetryUnit(candidate) ?? '';
}

function resolvePointLabel(point: TelemetryPoint): { label: string; defaultUnit?: string; precision: number } {
  const domain = telemetryPointDefinition(point.sourceKey);
  if (domain.label !== point.sourceKey) {
    return { label: domain.label, defaultUnit: domain.defaultUnit, precision: domain.precision ?? 1 };
  }
  const extra = EXTRA_POINT_LABELS[point.sourceKey];
  if (extra) {
    return { label: extra, defaultUnit: 'Cel', precision: 1 };
  }
  const cleanedDisplay = point.displayName.trim();
  return {
    label: cleanedDisplay || point.pointCode,
    defaultUnit: point.unit ?? undefined,
    precision: point.unit === 'm3/h' || point.unit === '%RH' ? 0 : 1,
  };
}

function cleanDeviceDisplayName(name: string): string {
  return name.replace(/\s*通信端点$/u, '').trim() || name;
}

export function buildTrendPointRef(deviceId: string, pointCode: string): string {
  return `${deviceId}:${pointCode}`;
}

export function buildTrendCatalog(model: Pick<SiteAssetModel, 'assets' | 'devices' | 'telemetryPoints' | 'relationships'>): TrendCatalog {
  const assetsById = new Map<string, Asset>(
    model.assets.filter((asset) => asset.status === 'ACTIVE').map((asset) => [asset.id, asset]),
  );
  const devicesById = new Map<string, Device>(
    model.devices.filter((device) => device.status === 'ACTIVE').map((device) => [device.id, device]),
  );
  const boundAssetByDevice = new Map<string, Asset>();
  for (const rel of model.relationships) {
    if (rel.status === 'ACTIVE' && rel.fromType === 'DEVICE' && rel.toType === 'ASSET') {
      const asset = assetsById.get(rel.toId);
      if (asset && !boundAssetByDevice.has(rel.fromId)) {
        boundAssetByDevice.set(rel.fromId, asset);
      }
    }
  }

  const items: TrendPointItem[] = [];
  for (const point of model.telemetryPoints) {
    if (point.status !== 'ACTIVE') continue;
    const device = devicesById.get(point.reportingDeviceId);
    if (!device || device.deviceType === 'GATEWAY') continue;

    const pointType = point.pointType;
    if (pointType !== 'TELEMETRY' && pointType !== 'SETTING' && pointType !== 'STATE') continue;

    const isGauge =
      (pointType === 'TELEMETRY' || pointType === 'SETTING') &&
      point.valueType === 'NUMBER';
    const isState =
      pointType === 'STATE' ||
      ((pointType === 'TELEMETRY' || pointType === 'SETTING') &&
        (point.valueType === 'STRING' || point.valueType === 'BOOLEAN'));

    if (!isGauge && !isState) continue;
    if (isState && point.sourceKey.endsWith('.fault_code')) continue;

    const boundAsset = boundAssetByDevice.get(device.id) ?? null;
    const deviceName = boundAsset ? boundAsset.displayName : cleanDeviceDisplayName(device.displayName);
    const resolved = resolvePointLabel(point);
    const unit = isGauge ? normalizeTrendUnit(point.unit, resolved.defaultUnit) : '';
    const isPlantWide = PLANT_LEVEL_PREFIXES.some((prefix) => point.sourceKey.startsWith(prefix));
    const seriesLabel = isPlantWide ? resolved.label : `${deviceName} · ${resolved.label}`;

    items.push({
      ref: buildTrendPointRef(device.id, point.pointCode),
      pointId: point.id,
      deviceId: device.id,
      deviceName,
      assetId: boundAsset?.id ?? null,
      assetType: boundAsset?.assetType ?? null,
      pointCode: point.pointCode,
      sourceKey: point.sourceKey,
      pointLabel: resolved.label,
      seriesLabel,
      unit,
      precision: resolved.precision,
      kind: isGauge ? 'gauge' : 'state',
      pointType,
      isSetpoint: pointType === 'SETTING',
    });
  }

  items.sort((left, right) => {
    const devCmp = left.deviceName.localeCompare(right.deviceName, 'zh-CN');
    if (devCmp !== 0) return devCmp;
    if (left.kind !== right.kind) return left.kind === 'gauge' ? -1 : 1;
    return left.pointLabel.localeCompare(right.pointLabel, 'zh-CN');
  });

  const byRef = new Map<string, TrendPointItem>();
  const byDeviceId = new Map<string, TrendPointItem[]>();
  for (const item of items) {
    byRef.set(item.ref, item);
    const list = byDeviceId.get(item.deviceId) ?? [];
    list.push(item);
    byDeviceId.set(item.deviceId, list);
  }

  return { points: items, byRef, byDeviceId };
}

export function findFirstBySourceKey(items: readonly TrendPointItem[], sourceKey: string): TrendPointItem | undefined {
  return items.find((item) => item.sourceKey === sourceKey);
}

export function findAllBySourceKey(items: readonly TrendPointItem[], sourceKey: string): readonly TrendPointItem[] {
  return items.filter((item) => item.sourceKey === sourceKey);
}

export function deriveBuiltInPresets(catalog: TrendCatalog): readonly TrendPreset[] {
  const { points } = catalog;
  const presets: TrendPreset[] = [];

  // 1. 冷站功率与供回水 (default)
  {
    const refs: string[] = [];
    const unconnected: string[] = [];
    const totalPower = findFirstBySourceKey(points, 'hvac_meter.active_power');
    const supplyTemp =
      findFirstBySourceKey(points, 'btu_meter.supply_water_temperature') ??
      findFirstBySourceKey(points, 'chiller.leaving_chilled_water_temperature');
    const returnTemp =
      findFirstBySourceKey(points, 'btu_meter.return_water_temperature') ??
      findFirstBySourceKey(points, 'chiller.entering_chilled_water_temperature');
    const setpoint = findFirstBySourceKey(points, 'chiller.chilled_water_temperature_setpoint');

    if (totalPower) refs.push(totalPower.ref);
    else unconnected.push('中央空调总功率');

    if (supplyTemp) refs.push(supplyTemp.ref);
    else unconnected.push('供水温度');

    if (returnTemp && !refs.includes(returnTemp.ref)) refs.push(returnTemp.ref);
    else if (!returnTemp) unconnected.push('回水温度');

    if (setpoint && refs.length < MAX_TREND_POINTS) refs.push(setpoint.ref);

    const gaugeCount = refs.length;
    for (const stateItem of findAllBySourceKey(points, 'chiller.run_state')) {
      if (refs.length >= MAX_TREND_POINTS) break;
      refs.push(stateItem.ref);
    }

    if (gaugeCount > 0) {
      presets.push({
        id: 'plant-overview',
        title: '冷站功率与供回水',
        pointRefs: refs.slice(0, MAX_TREND_POINTS),
        unconnectedLabels: unconnected,
      });
    }
  }

  // 2. 各冷机运行对比
  {
    const refs: string[] = [];
    const unconnected: string[] = [];
    const leavingTemps = findAllBySourceKey(points, 'chiller.leaving_chilled_water_temperature');
    const powers = findAllBySourceKey(points, 'chiller.power');
    const runStates = findAllBySourceKey(points, 'chiller.run_state');

    for (const item of leavingTemps.slice(0, 4)) {
      if (refs.length < MAX_TREND_POINTS) refs.push(item.ref);
    }
    if (leavingTemps.length === 0) unconnected.push('冷冻水出水温度');

    for (const item of powers.slice(0, 4)) {
      if (refs.length < MAX_TREND_POINTS) refs.push(item.ref);
    }
    if (powers.length === 0) unconnected.push('主机功率');

    if (leavingTemps.length === 1) {
      const setpoint = findFirstBySourceKey(points, 'chiller.chilled_water_temperature_setpoint');
      if (setpoint && refs.length < MAX_TREND_POINTS) refs.push(setpoint.ref);
    }
    for (const stateItem of runStates) {
      if (refs.length >= MAX_TREND_POINTS) break;
      refs.push(stateItem.ref);
    }

    const hasChillerGauge = leavingTemps.length > 0 || powers.length > 0;
    if (hasChillerGauge) {
      presets.push({
        id: 'chiller-comparison',
        title: '各冷机运行对比',
        pointRefs: refs.slice(0, MAX_TREND_POINTS),
        unconnectedLabels: unconnected,
      });
    }
  }

  // 3. 水泵流量与温差
  {
    const refs: string[] = [];
    const unconnected: string[] = [];
    const flow =
      findFirstBySourceKey(points, 'btu_meter.flow_rate') ??
      findFirstBySourceKey(points, 'chwp.flow_rate');
    const deltaT = findFirstBySourceKey(points, 'btu_meter.temperature_difference');
    const chwpFreq = findAllBySourceKey(points, 'chwp.frequency');
    const cwpFreq = findAllBySourceKey(points, 'cwp.frequency');
    const pumpRunStates = [
      ...findAllBySourceKey(points, 'chwp.run_state'),
      ...findAllBySourceKey(points, 'cwp.run_state'),
    ];

    if (flow) refs.push(flow.ref);
    else unconnected.push('冷冻水流量');

    if (deltaT) refs.push(deltaT.ref);
    else unconnected.push('供回水温差');

    const freqs = [...chwpFreq, ...cwpFreq];
    for (const item of freqs) {
      if (refs.length < MAX_TREND_POINTS) refs.push(item.ref);
    }
    if (freqs.length === 0) unconnected.push('水泵频率');

    const gaugeCount = refs.length;
    for (const stateItem of pumpRunStates) {
      if (refs.length >= MAX_TREND_POINTS) break;
      refs.push(stateItem.ref);
    }

    if (gaugeCount > 0) {
      presets.push({
        id: 'pump-hydraulics',
        title: '水泵流量与温差',
        pointRefs: refs.slice(0, MAX_TREND_POINTS),
        unconnectedLabels: unconnected,
      });
    }
  }

  // 4. 室外环境与供冷负荷
  {
    const refs: string[] = [];
    const unconnected: string[] = [];
    const dryBulb = findFirstBySourceKey(points, 'weather.ambient_dry_bulb_temperature');
    const wetBulb = findFirstBySourceKey(points, 'weather.ambient_wet_bulb_temperature');
    const coolingCap = findFirstBySourceKey(points, 'btu_meter.instant_cooling_capacity');
    const activePower = findFirstBySourceKey(points, 'hvac_meter.active_power');

    if (dryBulb) refs.push(dryBulb.ref);
    else unconnected.push('室外干球温度');

    if (wetBulb) refs.push(wetBulb.ref);
    else unconnected.push('室外湿球温度');

    if (coolingCap) refs.push(coolingCap.ref);
    else unconnected.push('瞬时制冷量');

    if (activePower && refs.length < MAX_TREND_POINTS) refs.push(activePower.ref);

    if (dryBulb || wetBulb) {
      presets.push({
        id: 'outdoor-load',
        title: '室外环境与供冷负荷',
        pointRefs: refs.slice(0, MAX_TREND_POINTS),
        unconnectedLabels: unconnected,
      });
    }
  }

  return presets;
}

export function groupTrendDeviceRequests(
  points: readonly TrendPointItem[],
): readonly { readonly deviceId: string; readonly keys: readonly string[] }[] {
  const capped = points.slice(0, MAX_TREND_POINTS);
  const byDevice = new Map<string, Set<string>>();
  for (const point of capped) {
    const set = byDevice.get(point.deviceId) ?? new Set<string>();
    set.add(point.pointCode);
    byDevice.set(point.deviceId, set);
  }
  return [...byDevice.entries()].map(([deviceId, keys]) => ({
    deviceId,
    keys: [...keys],
  }));
}
