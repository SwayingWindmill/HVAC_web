import {
  type Asset,
  type Device,
  type EnergySeriesResponse,
  type SiteAssetModel,
  type TelemetryPoint,
} from '../../api/generated/platformGateway.gen.ts';
import type { WorkOrder } from '../../api/work-orders.ts';
import { formatTelemetryUnit } from '../../domain/centralPlantTelemetry.ts';
import {
  buildAssetsRows,
  type AssetsDeviceRow,
  type AssetsPointView,
  type AssetsSnapshotResult,
} from '../assets/model.ts';
import { buildTrendPointRef } from '../operations/trends/trends-model.ts';
import {
  cleanEndpointDisplayName,
  collectPointReasonCodes,
  explainQualityReason,
  findOpenExternalWorkOrder,
  formatCounterPolicy,
  formatHourSlotLabel,
  formatSampleInterval,
  formatSourceChannel,
  primaryBoundAsset,
  type GovernanceStatusPresentation,
  type MeterTrendJumpSearch,
} from './governance-shared-model.ts';

const HOUR_MS = 3_600_000;

export type ReconciliationHourStatus = 'VALID' | 'SUSPECT' | 'MISSING' | 'IN_PROGRESS';

export interface HourlyReconciliationBucket {
  readonly index: number;
  readonly periodStart: string;
  readonly periodEnd: string;
  readonly hourLabel: string;
  readonly validKwh: number | null;
  readonly suspectKwh: number;
  readonly status: ReconciliationHourStatus;
}

export type MeterRole = 'MAIN_ELECTRICITY' | 'THERMAL_METER' | 'FLOW_METER' | 'SUB_ELECTRICITY';

export interface MeterReconciliationRow {
  readonly deviceId: string;
  readonly deviceCode: string;
  readonly displayName: string;
  readonly boundAssetName: string | null;
  readonly boundAssetType: string | null;
  readonly spaceName: string | null;
  readonly meterRole: MeterRole;
  readonly meterRoleLabel: '总电表' | '总冷量表' | '流量计' | '设备电表';
  readonly primaryCounterPoint: TelemetryPoint | null;
  readonly primaryReadingLabel: string;
  readonly primaryReadingValue: string;
  readonly primaryReadingUnit: string | null;
  readonly sampledAt: string | null;
  readonly channelLabel: string;
  readonly sampleIntervalLabel: string;
  readonly counterPolicyLabel: string;
  readonly supportsHourlyEnergy: boolean;
  readonly hourlyStrip: readonly HourlyReconciliationBucket[];
  readonly hasHourlySeries: boolean;
  readonly validEnergyKwh: number | null;
  readonly suspectEnergyKwh: number;
  readonly missingCompletedHours: number;
  readonly diagnosticNotes: readonly string[];
  readonly status: GovernanceStatusPresentation;
  readonly trendsSearch: MeterTrendJumpSearch;
  readonly workOrderSourceRef: string;
  readonly openWorkOrder: WorkOrder | null;
  readonly suggestedWorkOrderTitle: string;
  readonly suggestedWorkOrderDescription: string;
  readonly historyKeys: readonly string[];
}

export interface MeterReconciliationSummary {
  readonly totalMeters: number;
  readonly completedHours: number;
  readonly validHours: number;
  readonly suspectHours: number;
  readonly missingHours: number;
  readonly attentionMeters: number;
  readonly totalValidElectricityKwh: number | null;
  readonly totalSuspectElectricityKwh: number;
}

export interface MeterReconciliationModel {
  readonly summary: MeterReconciliationSummary;
  readonly hourlyBuckets: readonly HourlyReconciliationBucket[];
  readonly rows: readonly MeterReconciliationRow[];
}

export interface CheckMeterHourlySeries {
  readonly validPoints: EnergySeriesResponse['points'];
  readonly allPoints: EnergySeriesResponse['points'];
}

export interface BuildMeterReconciliationInput {
  readonly siteId: string;
  readonly timezone: string;
  readonly assetModel: SiteAssetModel;
  readonly snapshots?: ReadonlyMap<string, AssetsSnapshotResult>;
  readonly electricityValid: EnergySeriesResponse | null;
  readonly electricityWithSuspect: EnergySeriesResponse | null;
  readonly coolingValid: EnergySeriesResponse | null;
  readonly coolingWithSuspect: EnergySeriesResponse | null;
  /** Own counter history for physical check meters on synthesized-total sites. */
  readonly checkMeterHourlyByDeviceId?: ReadonlyMap<string, CheckMeterHourlySeries>;
  readonly workOrders?: readonly WorkOrder[];
  readonly window: { readonly fromMs: number; readonly toMs: number };
  readonly now?: Date;
}

function buildHourlyBucketsFromPoints(
  validPoints: EnergySeriesResponse['points'] | undefined,
  allPoints: EnergySeriesResponse['points'] | undefined,
  window: { readonly fromMs: number; readonly toMs: number },
  nowMs: number,
  timezone: string,
): readonly HourlyReconciliationBucket[] {
  const validByStart = new Map<number, number>();
  const allByStart = new Map<number, number>();
  for (const pt of validPoints ?? []) {
    validByStart.set(Date.parse(pt.periodStart), pt.energyKWh);
  }
  for (const pt of allPoints ?? []) {
    allByStart.set(Date.parse(pt.periodStart), pt.energyKWh);
  }

  const buckets: HourlyReconciliationBucket[] = [];
  const slotCount = Math.max(0, Math.round((window.toMs - window.fromMs) / HOUR_MS));
  for (let index = 0; index < slotCount; index += 1) {
    const startMs = window.fromMs + index * HOUR_MS;
    const endMs = startMs + HOUR_MS;
    const hasValid = validByStart.has(startMs);
    const hasAll = allByStart.has(startMs);
    const validKwh = hasValid ? Number((validByStart.get(startMs) ?? 0).toFixed(3)) : null;
    const totalKwh = hasAll ? (allByStart.get(startMs) ?? 0) : validKwh;
    const suspectKwh =
      validKwh !== null && totalKwh !== null
        ? Math.max(0, Number((totalKwh - validKwh).toFixed(3)))
        : hasAll && !hasValid
          ? Math.max(0, Number((totalKwh ?? 0).toFixed(3)))
          : 0;

    let status: ReconciliationHourStatus;
    if (endMs > nowMs) {
      status = 'IN_PROGRESS';
    } else if (!hasValid && !hasAll) {
      status = 'MISSING';
    } else if (suspectKwh > 0.001) {
      status = 'SUSPECT';
    } else {
      status = 'VALID';
    }

    buckets.push({
      index,
      periodStart: new Date(startMs).toISOString(),
      periodEnd: new Date(endMs).toISOString(),
      hourLabel: formatHourSlotLabel(startMs, timezone),
      validKwh,
      suspectKwh,
      status,
    });
  }
  return buckets;
}

function classifyMeterRole(device: Device, boundAsset: Asset | null, points: readonly TelemetryPoint[]): MeterRole | null {
  if (device.status !== 'ACTIVE' || device.deviceType === 'GATEWAY') return null;
  const deviceType = device.deviceType.toUpperCase();
  const assetType = boundAsset?.assetType.toUpperCase() ?? '';
  const activeCounters = points.filter((pt) => pt.status === 'ACTIVE' && pt.pointType === 'COUNTER');

  if (
    deviceType === 'BTU_METER' ||
    assetType === 'BTU_METER' ||
    activeCounters.some((pt) => pt.sourceKey.startsWith('btu_meter.') || pt.pointCode.includes('cooling'))
  ) {
    return 'THERMAL_METER';
  }
  if (deviceType === 'FLOW_METER') {
    return 'FLOW_METER';
  }
  // Equipment sub-meters on Songyaxuan have deviceType = HVAC_POWER_METER bound to CHILLER / PUMP / TOWER assets.
  if (
    boundAsset &&
    assetType !== 'HVAC_POWER_METER' &&
    assetType !== 'POWER_METER'
  ) {
    return activeCounters.length > 0 || deviceType === 'POWER_METER' || deviceType === 'HVAC_POWER_METER'
      ? 'SUB_ELECTRICITY'
      : null;
  }
  if (deviceType === 'POWER_METER' || deviceType === 'HVAC_POWER_METER' || assetType === 'HVAC_POWER_METER') {
    return 'MAIN_ELECTRICITY';
  }
  if (activeCounters.length > 0) {
    return 'SUB_ELECTRICITY';
  }
  return null;
}

const METER_ROLE_ORDER: Readonly<Record<MeterRole, number>> = {
  MAIN_ELECTRICITY: 1,
  THERMAL_METER: 2,
  FLOW_METER: 3,
  SUB_ELECTRICITY: 4,
};

const METER_ROLE_LABELS: Readonly<Record<MeterRole, MeterReconciliationRow['meterRoleLabel']>> = {
  MAIN_ELECTRICITY: '总电表',
  THERMAL_METER: '总冷量表',
  FLOW_METER: '流量计',
  SUB_ELECTRICITY: '设备电表',
};

const PREFERRED_METER_TREND_KEYS = [
  'active_power',
  'power',
  'instant_cooling_capacity',
  'flow_rate',
  'supply_water_temperature',
  'return_water_temperature',
] as const;

function buildMeterTrendJump(
  siteId: string,
  deviceRow: AssetsDeviceRow,
  window: { readonly fromMs: number; readonly toMs: number },
): MeterTrendJumpSearch {
  const gaugeCandidates = deviceRow.telemetryPoints
    .filter(
      (pt) =>
        pt.status === 'ACTIVE' &&
        (pt.pointType === 'TELEMETRY' || pt.pointType === 'SETTING') &&
        pt.valueType === 'NUMBER',
    )
    .sort((left, right) => {
      const leftRank = PREFERRED_METER_TREND_KEYS.findIndex((key) => key === left.pointCode);
      const rightRank = PREFERRED_METER_TREND_KEYS.findIndex((key) => key === right.pointCode);
      const normLeft = leftRank === -1 ? 99 : leftRank;
      const normRight = rightRank === -1 ? 99 : rightRank;
      return normLeft - normRight || left.pointCode.localeCompare(right.pointCode);
    })
    .slice(0, 4);

  const refs = gaugeCandidates.map((pt) => buildTrendPointRef(deviceRow.device.id, pt.pointCode));
  return {
    site: siteId,
    ...(refs.length > 0 ? { points: refs.join(',') } : {}),
    window: 'custom',
    from: new Date(window.fromMs).toISOString(),
    to: new Date(window.toMs).toISOString(),
    granularity: 'FIVE_MINUTE',
  };
}

function resolveMeterStatus(input: {
  readonly deviceRow: AssetsDeviceRow;
  readonly primaryView: AssetsPointView | null;
  readonly suspectEnergyKwh: number;
  readonly missingCompletedHours: number;
  readonly hasHourlySeries: boolean;
}): GovernanceStatusPresentation {
  const conn = input.deviceRow.operational.connection.state;
  const pv = input.primaryView;
  if (conn === 'OFFLINE') {
    return { level: 'CRITICAL', shape: '◆', tone: 'danger', label: '通信中断' };
  }
  if (pv?.health?.status === 'BAD' || pv?.quality === 'INVALID') {
    return { level: 'CRITICAL', shape: '◆', tone: 'danger', label: '读数无效' };
  }
  if (
    pv?.health?.status === 'SUSPECT' ||
    pv?.health?.status === 'STALE' ||
    pv?.freshness === 'STALE' ||
    input.suspectEnergyKwh > 0.001 ||
    (input.hasHourlySeries && input.missingCompletedHours > 0)
  ) {
    return { level: 'WARNING', shape: '▲', tone: 'warning', label: '待现场确认' };
  }
  if (!pv || pv.state !== 'PRESENT') {
    return { level: 'NOTICE', shape: '■', tone: 'neutral', label: '等待读数' };
  }
  return { level: 'NORMAL', shape: '○', tone: 'success', label: '计量正常' };
}

export function findMainElectricityCounterDevices(
  assetModel: SiteAssetModel,
): readonly { readonly deviceId: string; readonly pointCode: string }[] {
  const deviceRows = buildAssetsRows({ assetModel });
  const result: { deviceId: string; pointCode: string }[] = [];
  for (const deviceRow of deviceRows) {
    const boundAsset = primaryBoundAsset(deviceRow.binding);
    const role = classifyMeterRole(deviceRow.device, boundAsset, deviceRow.telemetryPoints);
    if (role !== 'MAIN_ELECTRICITY') continue;
    const primaryCounter = deviceRow.telemetryPoints.find(
      (pt) => pt.status === 'ACTIVE' && pt.pointType === 'COUNTER',
    );
    if (primaryCounter) {
      result.push({ deviceId: deviceRow.device.id, pointCode: primaryCounter.pointCode });
    }
  }
  return result;
}

export function buildMeterReconciliationModel(
  input: BuildMeterReconciliationInput,
): MeterReconciliationModel {
  const now = input.now ?? new Date();
  const nowMs = now.getTime();
  const deviceRows = buildAssetsRows({
    assetModel: input.assetModel,
    snapshots: input.snapshots,
    now,
  });

  const hourlyBuckets = buildHourlyBucketsFromPoints(
    input.electricityValid?.points,
    input.electricityWithSuspect?.points,
    input.window,
    nowMs,
    input.timezone,
  );
  const isSynthesized = Boolean(
    input.electricityValid?.metadata.synthesized ||
      input.electricityWithSuspect?.metadata.synthesized,
  );

  const meterRows: MeterReconciliationRow[] = [];

  for (const deviceRow of deviceRows) {
    const boundAsset = primaryBoundAsset(deviceRow.binding);
    const role = classifyMeterRole(deviceRow.device, boundAsset, deviceRow.telemetryPoints);
    if (!role) continue;

    const activeCounters = deviceRow.telemetryPoints.filter(
      (pt) => pt.status === 'ACTIVE' && pt.pointType === 'COUNTER',
    );
    const primaryCounterPoint = activeCounters[0] ?? null;
    const fallbackPoint =
      primaryCounterPoint ??
      deviceRow.telemetryPoints.find(
        (pt) => pt.status === 'ACTIVE' && pt.pointType === 'TELEMETRY' && pt.valueType === 'NUMBER',
      ) ??
      null;

    const primaryView = fallbackPoint
      ? (deviceRow.operational.points.find((pt) => pt.pointId === fallbackPoint.id) ?? null)
      : null;

    let validSeriesPoints: EnergySeriesResponse['points'] | undefined;
    let allSeriesPoints: EnergySeriesResponse['points'] | undefined;
    let hasHourlySeries = false;

    if (role === 'MAIN_ELECTRICITY' && (input.electricityValid || input.electricityWithSuspect)) {
      const devValid = input.electricityValid?.devices?.find((d) => d.deviceId === deviceRow.device.id);
      const devAll = input.electricityWithSuspect?.devices?.find((d) => d.deviceId === deviceRow.device.id);
      if (isSynthesized) {
        const checkMeter = input.checkMeterHourlyByDeviceId?.get(deviceRow.device.id);
        validSeriesPoints = devValid?.points ?? checkMeter?.validPoints;
        allSeriesPoints = devAll?.points ?? checkMeter?.allPoints;
        hasHourlySeries = Boolean(devValid || devAll || checkMeter || primaryCounterPoint);
      } else {
        validSeriesPoints = devValid?.points ?? input.electricityValid?.points;
        allSeriesPoints = devAll?.points ?? input.electricityWithSuspect?.points;
        hasHourlySeries = true;
      }
    } else if (role === 'SUB_ELECTRICITY' && (input.electricityValid || input.electricityWithSuspect)) {
      const devValid = input.electricityValid?.devices?.find((d) => d.deviceId === deviceRow.device.id);
      const devAll = input.electricityWithSuspect?.devices?.find((d) => d.deviceId === deviceRow.device.id);
      if (devValid || devAll) {
        validSeriesPoints = devValid?.points;
        allSeriesPoints = devAll?.points;
        hasHourlySeries = true;
      }
    } else if (role === 'THERMAL_METER' && (input.coolingValid || input.coolingWithSuspect)) {
      const devValid = input.coolingValid?.devices?.find((d) => d.deviceId === deviceRow.device.id);
      const devAll = input.coolingWithSuspect?.devices?.find((d) => d.deviceId === deviceRow.device.id);
      validSeriesPoints = devValid?.points ?? input.coolingValid?.points;
      allSeriesPoints = devAll?.points ?? input.coolingWithSuspect?.points;
      hasHourlySeries = true;
    }

    const rowStrip = hasHourlySeries
      ? buildHourlyBucketsFromPoints(validSeriesPoints, allSeriesPoints, input.window, nowMs, input.timezone)
      : [];

    const validEnergyKwh = hasHourlySeries
      ? Number(
          rowStrip
            .reduce((sum, bucket) => sum + (bucket.validKwh ?? 0), 0)
            .toFixed(2),
        )
      : null;
    const suspectEnergyKwh = hasHourlySeries
      ? Number(rowStrip.reduce((sum, bucket) => sum + bucket.suspectKwh, 0).toFixed(2))
      : 0;
    const missingCompletedHours = hasHourlySeries
      ? rowStrip.filter((bucket) => bucket.status === 'MISSING').length
      : 0;

    const reasonCodes = new Set<string>();
    for (const pt of deviceRow.operational.points) {
      for (const reason of collectPointReasonCodes(pt)) {
        reasonCodes.add(reason);
      }
    }
    const diagnosticNotes = [...reasonCodes].map(explainQualityReason);
    if (deviceRow.operational.connection.state === 'OFFLINE') {
      diagnosticNotes.unshift('设备当前通信中断，最新累计读数未更新');
    } else if (primaryView?.freshness === 'STALE') {
      diagnosticNotes.unshift('最新累计读数已超过允许刷新间隔');
    }
    if (hasHourlySeries && missingCompletedHours > 0) {
      diagnosticNotes.push(`近 24 小时缺 ${missingCompletedHours} 段小时计量数据`);
    }

    const status = resolveMeterStatus({
      deviceRow,
      primaryView,
      suspectEnergyKwh,
      missingCompletedHours,
      hasHourlySeries,
    });

    const displayName = boundAsset
      ? boundAsset.displayName
      : cleanEndpointDisplayName(deviceRow.device.displayName);
    const workOrderSourceRef = `integrations-meter:${input.siteId}:${deviceRow.device.code}`;
    const openWorkOrder = findOpenExternalWorkOrder(input.workOrders, workOrderSourceRef);

    const historyKeys = fallbackPoint ? [fallbackPoint.pointCode] : [];
    const primaryReadingUnit =
      primaryView?.state === 'PRESENT'
        ? primaryView.unit
        : fallbackPoint
          ? formatTelemetryUnit(fallbackPoint.unit)
          : null;

    meterRows.push({
      deviceId: deviceRow.device.id,
      deviceCode: deviceRow.device.code,
      displayName,
      boundAssetName: boundAsset?.displayName ?? null,
      boundAssetType: boundAsset?.assetType ?? null,
      spaceName: deviceRow.space.state === 'bound' ? deviceRow.space.space.displayName : null,
      meterRole: role,
      meterRoleLabel: METER_ROLE_LABELS[role],
      primaryCounterPoint,
      primaryReadingLabel: primaryView?.label ?? fallbackPoint?.displayName ?? '累计读数',
      primaryReadingValue: primaryView?.state === 'PRESENT' ? primaryView.displayValue : '—',
      primaryReadingUnit,
      sampledAt: primaryView?.sampledAt ?? null,
      channelLabel: fallbackPoint ? formatSourceChannel(fallbackPoint.sourceMetadata) : '未登记通道',
      sampleIntervalLabel: fallbackPoint ? formatSampleInterval(fallbackPoint.sampleIntervalMs) : '—',
      counterPolicyLabel: formatCounterPolicy(primaryCounterPoint),
      supportsHourlyEnergy: role !== 'FLOW_METER' && primaryCounterPoint !== null,
      hourlyStrip: rowStrip,
      hasHourlySeries,
      validEnergyKwh,
      suspectEnergyKwh,
      missingCompletedHours,
      diagnosticNotes,
      status,
      trendsSearch: buildMeterTrendJump(input.siteId, deviceRow, input.window),
      workOrderSourceRef,
      openWorkOrder,
      suggestedWorkOrderTitle: `${displayName}：计量数据现场核查`,
      suggestedWorkOrderDescription:
        diagnosticNotes.length > 0
          ? `原因：${diagnosticNotes.join('；')}。请现场核对表计脉冲倍率、通信通道与累计示数。`
          : `请现场核对 ${displayName}（${deviceRow.device.code}）表计通信与累计示数。`,
      historyKeys,
    });
  }

  meterRows.sort(
    (left, right) =>
      METER_ROLE_ORDER[left.meterRole] - METER_ROLE_ORDER[right.meterRole] ||
      left.displayName.localeCompare(right.displayName, 'zh-CN', { numeric: true }),
  );

  const hasPlantElectricity = Boolean(input.electricityValid || input.electricityWithSuspect);
  const completedBuckets = hourlyBuckets.filter((b) => b.status !== 'IN_PROGRESS');
  const validHours = hasPlantElectricity
    ? completedBuckets.filter((b) => b.status === 'VALID').length
    : 0;
  const suspectHours = hasPlantElectricity
    ? completedBuckets.filter((b) => b.status === 'SUSPECT').length
    : 0;
  const missingHours = hasPlantElectricity
    ? completedBuckets.filter((b) => b.status === 'MISSING').length
    : 0;

  const totalValidElectricityKwh = hasPlantElectricity
    ? Number(hourlyBuckets.reduce((sum, b) => sum + (b.validKwh ?? 0), 0).toFixed(2))
    : null;
  const totalSuspectElectricityKwh = hasPlantElectricity
    ? Number(hourlyBuckets.reduce((sum, b) => sum + b.suspectKwh, 0).toFixed(2))
    : 0;

  return {
    summary: {
      totalMeters: meterRows.length,
      completedHours: hasPlantElectricity ? completedBuckets.length : 0,
      validHours,
      suspectHours,
      missingHours,
      attentionMeters: meterRows.filter((row) => row.status.level === 'CRITICAL' || row.status.level === 'WARNING').length,
      totalValidElectricityKwh,
      totalSuspectElectricityKwh,
    },
    hourlyBuckets: hasPlantElectricity ? hourlyBuckets : [],
    rows: meterRows,
  };
}
