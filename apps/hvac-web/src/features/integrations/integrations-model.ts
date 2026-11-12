import {
  PlatformApiError,
  type Asset,
  type Device,
  type EnergySeriesResponse,
  type Sensor,
  type SiteAssetModel,
  type TelemetryPoint,
} from '../../api/generated/platformGateway.gen.ts';
import type { WorkOrder } from '../../api/work-orders.ts';
import { formatTelemetryUnit } from '../../domain/centralPlantTelemetry.ts';
import {
  buildAssetsRows,
  type AssetsBindingState,
  type AssetsDeviceRow,
  type AssetsPointView,
  type AssetsSnapshotResult,
} from '../assets/model.ts';
import { buildTrendPointRef } from '../operations/trends/trends-model.ts';

const HOUR_MS = 3_600_000;

export type IntegrationsTabView = 'gateways' | 'meters' | 'sensors';

export type ReconciliationHourStatus = 'VALID' | 'SUSPECT' | 'MISSING' | 'IN_PROGRESS';

export type GovernanceBadgeTone = 'danger' | 'warning' | 'neutral' | 'success';
export type GovernanceShape = '◆' | '▲' | '■' | '○';

export interface GovernanceStatusPresentation {
  readonly level: 'CRITICAL' | 'WARNING' | 'NOTICE' | 'NORMAL';
  readonly shape: GovernanceShape;
  readonly tone: GovernanceBadgeTone;
  readonly label: string;
}

export interface HourlyReconciliationBucket {
  readonly index: number;
  readonly periodStart: string;
  readonly periodEnd: string;
  readonly hourLabel: string;
  readonly validKwh: number | null;
  readonly suspectKwh: number;
  readonly status: ReconciliationHourStatus;
}

export interface MeterTrendJumpSearch {
  readonly site: string;
  readonly points?: string;
  readonly window: 'custom';
  readonly from: string;
  readonly to: string;
  readonly granularity: 'FIVE_MINUTE';
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
  /** Own counter history for physical check meters on synthesized-total sites (#798). */
  readonly checkMeterHourlyByDeviceId?: ReadonlyMap<string, CheckMeterHourlySeries>;
  readonly workOrders?: readonly WorkOrder[];
  readonly window: { readonly fromMs: number; readonly toMs: number };
  readonly now?: Date;
}

export const QUALITY_REASON_EXPLANATIONS: Readonly<Record<string, string>> = Object.freeze({
  OUT_OF_RANGE: '读数超出物理量程范围',
  SPIKE: '读数变化超出单步允许跳变阈值',
  SENTINEL: '命中仪表断线或故障哨兵值',
  FLATLINE: '读数长时间恒定未变化，疑似卡值',
  DERIVED_INPUT: '上游输入读数存在异常',
  LOW_CONFIDENCE: '小温差时测量不确定度偏高',
  PHYSICS_WET_BULB_DRY: '湿球温度高于干球温度，违背热力学关系',
  PHYSICS_WET_BULB_CONDENSER: '冷凝器出水温度低于室外湿球温度，违背物理极限',
  PHYSICS_POWER: '实测功率超出设备铭牌额定功率合理范围',
  PHYSICS_COP: '实测能效比超出卡诺极限或合理区间',
  PHYSICS_CHW_DIRECTION: '冷冻水供回水温差方向异常（供水高于回水）',
  PHYSICS_CW_DIRECTION: '冷却水进出水温差方向异常（出水低于进水）',
  PHYSICS_HEAT_BALANCE: '冷机热平衡偏差超出允许范围',
  PROBE_OFFSET: '仅水泵运行稳态下检测到供回水探头零点偏差',
});

export function explainQualityReason(code: string): string {
  return QUALITY_REASON_EXPLANATIONS[code] ?? '读数存在质量异常，建议现场核查';
}

export function cleanEndpointDisplayName(displayName: string): string {
  return displayName.replace(/\s*通信端点$/u, '').trim() || displayName;
}

function primaryBoundAsset(binding: AssetsBindingState): Asset | null {
  if (binding.state === 'bound') return binding.asset;
  if (binding.state === 'multi-bound') return binding.bindings[0]?.asset ?? null;
  return null;
}

export function formatHourSlotLabel(ms: number, timezone: string): string {
  try {
    return new Intl.DateTimeFormat('zh-CN', {
      timeZone: timezone,
      hour: '2-digit',
      minute: '2-digit',
      hour12: false,
    }).format(new Date(ms));
  } catch {
    return new Date(ms).toISOString().slice(11, 16);
  }
}

export function formatSlotDateTime(msOrIso: number | string, timezone: string): string {
  const ms = typeof msOrIso === 'number' ? msOrIso : Date.parse(msOrIso);
  if (!Number.isFinite(ms)) return '—';
  try {
    return new Intl.DateTimeFormat('zh-CN', {
      timeZone: timezone,
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      hour12: false,
    }).format(new Date(ms));
  } catch {
    return new Date(ms).toISOString().slice(5, 16).replace('T', ' ');
  }
}

const PROTOCOL_LABELS: Readonly<Record<string, string>> = Object.freeze({
  MODBUS_TCP: 'Modbus TCP',
  MODBUS_RTU: 'Modbus RTU',
  MQTT: 'MQTT',
  BACNET_IP: 'BACnet/IP',
  HTTP: 'HTTP',
  COPIES: '共享通道',
  PROVISIONAL: '临时换算',
});

function formatProtocolLabel(raw: string): string {
  return PROTOCOL_LABELS[raw.toUpperCase()] ?? raw;
}

export function formatSourceChannel(metadata: Readonly<Record<string, unknown>>): string {
  const rawProtocol =
    typeof metadata.protocol === 'string' && metadata.protocol.trim()
      ? metadata.protocol.trim()
      : null;
  const protocol = rawProtocol ? formatProtocolLabel(rawProtocol) : null;
  const addressCandidate =
    (typeof metadata.address === 'string' && metadata.address.trim()) ||
    (typeof metadata.channel === 'string' && metadata.channel.trim()) ||
    (typeof metadata.register === 'string' && metadata.register.trim()) ||
    (typeof metadata.register === 'number' ? `寄存器 ${metadata.register}` : null);

  if (protocol && addressCandidate) return `${protocol} · ${addressCandidate}`;
  if (addressCandidate) return addressCandidate;
  if (protocol) return protocol;
  return '未登记通道';
}

export function formatSampleInterval(sampleIntervalMs: number): string {
  if (sampleIntervalMs % 60_000 === 0) {
    return `每 ${sampleIntervalMs / 60_000} 分钟`;
  }
  if (sampleIntervalMs >= 1000) {
    return `每 ${Math.round(sampleIntervalMs / 1000)} 秒`;
  }
  return `${sampleIntervalMs} ms`;
}

export function formatCounterPolicy(point: TelemetryPoint | null): string {
  if (!point || point.pointType !== 'COUNTER') {
    return '瞬时量监测（无累计脉冲）';
  }
  if (point.counterDecreaseMode === 'ROLLOVER') {
    const modulus = point.counterRolloverModulus
      ? `（模数 ${new Intl.NumberFormat('en-US').format(point.counterRolloverModulus)}）`
      : '';
    return `满量程回零${modulus}`;
  }
  if (point.counterDecreaseMode === 'RESET_TO_ZERO') {
    return '清零重置后继续累计';
  }
  if (point.counterDecreaseMode === 'INVALID') {
    return '读数回退判为无效';
  }
  return '连续单调累计';
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

const OPEN_WORK_ORDER_STATUSES = new Set<WorkOrder['status']>([
  'DRAFT',
  'OPEN',
  'IN_PROGRESS',
  'BLOCKED',
]);

export function findOpenExternalWorkOrder(
  workOrders: readonly WorkOrder[] | undefined,
  resourceId: string,
): WorkOrder | null {
  if (!workOrders) return null;
  for (const order of workOrders) {
    if (!OPEN_WORK_ORDER_STATUSES.has(order.status)) continue;
    if (
      order.sourceReferences.some(
        (ref) => ref.domain === 'EXTERNAL' && ref.resourceId === resourceId,
      )
    ) {
      return order;
    }
  }
  return null;
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

function collectPointReasonCodes(pointView: AssetsPointView | null): string[] {
  if (!pointView) return [];
  const merged = new Set<string>();
  for (const code of pointView.qualityReasons) {
    merged.add(code);
  }
  for (const code of pointView.health?.reasons ?? []) {
    merged.add(code);
  }
  return [...merged];
}

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
      const leftRank = PREFERRED_METER_TREND_KEYS.indexOf(
        left.pointCode as (typeof PREFERRED_METER_TREND_KEYS)[number],
      );
      const rightRank = PREFERRED_METER_TREND_KEYS.indexOf(
        right.pointCode as (typeof PREFERRED_METER_TREND_KEYS)[number],
      );
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

export function describeIntegrationsError(error: unknown, fallback = '数据暂时无法读取，请稍后重试'): string {
  if (error instanceof PlatformApiError) {
    if (error.problem.status === 403) {
      return '当前账号没有读取该站点接入或能耗统计数据的权限';
    }
    if (error.problem.status === 404) {
      return '未找到所选站点的资产或接入登记数据';
    }
    return '平台服务暂时无法返回接入核对数据，请稍后重试';
  }
  return fallback;
}

const DAY_MS = 86_400_000;

const PHYSICS_AND_CONFIDENCE_REASONS = new Set<string>([
  'LOW_CONFIDENCE',
  'PHYSICS_WET_BULB_DRY',
  'PHYSICS_WET_BULB_CONDENSER',
  'PHYSICS_POWER',
  'PHYSICS_COP',
  'PHYSICS_CHW_DIRECTION',
  'PHYSICS_CW_DIRECTION',
  'PHYSICS_HEAT_BALANCE',
]);

export type SensorCalibrationFilter = 'ALL' | 'ATTENTION' | 'PROBE_OFFSET' | 'CALIBRATION_DUE';

export type SensorCalibrationDueStatus = 'UNREGISTERED' | 'VALID' | 'EXPIRING_SOON' | 'OVERDUE';

export interface SensorCalibrationInfo {
  readonly status: SensorCalibrationDueStatus;
  readonly dueAt: string | null;
  readonly daysRemaining: number | null;
  readonly label: string;
}

export interface ProbeOffsetViewItem {
  readonly pairCode: string;
  readonly pairLabel: string;
  readonly offsetK: number;
  readonly stddevK: number;
  readonly sampleCount: number;
  readonly windowStartedAt: string;
  readonly windowEndedAt: string;
  readonly suggested: boolean;
  readonly applied: boolean;
  readonly alarm?: string;
  readonly summaryLabel: string;
}

export interface SensorHardwareMeta {
  readonly sensorId: string;
  readonly code: string;
  readonly displayName: string;
  readonly sensorType: string;
  readonly manufacturer: string | null;
  readonly model: string | null;
  readonly serialNumber: string | null;
}

export interface BoundAssetNameplateSummary {
  readonly assetId: string;
  readonly displayName: string;
  readonly assetType: string;
  readonly ratedPowerKw: number | null;
  readonly ratedCapacityKw: number | null;
}

export interface SensorCalibrationRow {
  readonly inspectKey: string;
  readonly pointId: string;
  readonly pointCode: string;
  readonly pointLabel: string;
  readonly deviceId: string;
  readonly deviceCode: string;
  readonly deviceDisplayName: string;
  readonly spaceName: string | null;
  readonly channelLabel: string;
  readonly sampleIntervalLabel: string;
  readonly currentValue: string;
  readonly unit: string | null;
  readonly sampledAt: string | null;
  readonly healthScore: number | null;
  readonly healthStatus: 'HEALTHY' | 'SUSPECT' | 'BAD' | 'STALE' | null;
  readonly primaryProbeOffset: ProbeOffsetViewItem | null;
  readonly probeOffsets: readonly ProbeOffsetViewItem[];
  readonly measurement: AssetsPointView['measurement'];
  readonly measurementSummaryLabel: string | null;
  readonly hasPhysicsOrLowConfidence: boolean;
  readonly diagnosticNotes: readonly string[];
  readonly calibration: SensorCalibrationInfo;
  readonly sensorMeta: SensorHardwareMeta | null;
  readonly boundAsset: BoundAssetNameplateSummary | null;
  readonly status: GovernanceStatusPresentation;
  readonly trendsSearch: MeterTrendJumpSearch;
  readonly workOrderSourceRef: string;
  readonly openWorkOrder: WorkOrder | null;
  readonly suggestedWorkOrderTitle: string;
  readonly suggestedWorkOrderDescription: string;
}

export interface SensorCalibrationSummary {
  readonly totalPoints: number;
  readonly attentionPoints: number;
  readonly probeOffsetCount: number;
  readonly physicsOrLowConfidenceCount: number;
  readonly calibrationConfiguredCount: number;
  readonly calibrationDueOrExpiringCount: number;
  readonly calibrationDueDisplay: string;
}

export interface SensorCalibrationModel {
  readonly summary: SensorCalibrationSummary;
  readonly rows: readonly SensorCalibrationRow[];
}

export interface BuildSensorCalibrationInput {
  readonly siteId: string;
  readonly timezone: string;
  readonly assetModel: SiteAssetModel;
  readonly snapshots?: ReadonlyMap<string, AssetsSnapshotResult>;
  readonly workOrders?: readonly WorkOrder[];
  readonly window: { readonly fromMs: number; readonly toMs: number };
  readonly now?: Date;
}

const PROBE_PAIR_LABELS: Readonly<Record<string, string>> = Object.freeze({
  'header-chw': '冷冻水总管供回水',
  plant_chw_delta_t: '冷冻水总管供回水',
  chw_main: '冷冻水总管供回水',
  'chw-main': '冷冻水总管供回水',
  'header-cw': '冷却水总管进出水',
  plant_cw_delta_t: '冷却水总管进出水',
  cw_main: '冷却水总管进出水',
  'cw-main': '冷却水总管进出水',
});

export function formatProbePairLabel(pairCode: string): string {
  const normalized = pairCode.trim().toLowerCase();
  if (PROBE_PAIR_LABELS[normalized]) {
    return PROBE_PAIR_LABELS[normalized];
  }
  const chillerChwMatch = /^chiller[-_]([a-z0-9]+)[-_]chw(?:[-_]delta[-_]t)?$/i.exec(normalized);
  if (chillerChwMatch?.[1]) {
    const unitTag = chillerChwMatch[1];
    const prefix = /^\d+$/.test(unitTag)
      ? `${Number(unitTag)} 号机组`
      : `${unitTag.toUpperCase()} 机组`;
    return `${prefix}冷冻水供回水`;
  }
  const chillerCwMatch = /^chiller[-_]([a-z0-9]+)[-_]cw(?:[-_]delta[-_]t)?$/i.exec(normalized);
  if (chillerCwMatch?.[1]) {
    const unitTag = chillerCwMatch[1];
    const prefix = /^\d+$/.test(unitTag)
      ? `${Number(unitTag)} 号机组`
      : `${unitTag.toUpperCase()} 机组`;
    return `${prefix}冷却水进出水`;
  }
  return '供回水温度探头对';
}

function formatProbeOffsetItem(
  raw: AssetsPointView['probeOffsets'][number],
): ProbeOffsetViewItem {
  const verb = raw.offsetK >= 0 ? '减去' : '加上';
  const magnitude = Math.abs(raw.offsetK).toFixed(2);
  return {
    pairCode: raw.pairCode,
    pairLabel: formatProbePairLabel(raw.pairCode),
    offsetK: raw.offsetK,
    stddevK: raw.stddevK,
    sampleCount: raw.sampleCount,
    windowStartedAt: raw.windowStartedAt,
    windowEndedAt: raw.windowEndedAt,
    suggested: raw.suggested,
    applied: raw.applied,
    alarm: raw.alarm,
    summaryLabel: `建议${verb} ${magnitude} K · 未采用`,
  };
}

function formatMeasurementIntervalLabel(
  measurement: NonNullable<AssetsPointView['measurement']>,
  unit: string | null,
): string {
  const pct =
    measurement.relative >= 0.1
      ? Math.round(measurement.relative * 100).toString()
      : (measurement.relative * 100).toFixed(1);
  const lowStr = measurement.low.toFixed(1);
  const highStr = measurement.high.toFixed(1);
  const unitSuffix = unit ? ` ${unit}` : '';
  const prefix = measurement.lowConfidence ? '低可信 · ' : '';
  return `${prefix}±${pct}%（${lowStr}–${highStr}${unitSuffix}）`;
}

function evaluateCalibrationDue(sensor: Sensor | null, nowMs: number): SensorCalibrationInfo {
  if (!sensor || !sensor.calibrationDueAt) {
    return {
      status: 'UNREGISTERED',
      dueAt: null,
      daysRemaining: null,
      label: '未登记',
    };
  }
  const dueMs = Date.parse(sensor.calibrationDueAt);
  if (!Number.isFinite(dueMs)) {
    return {
      status: 'UNREGISTERED',
      dueAt: null,
      daysRemaining: null,
      label: '未登记',
    };
  }
  const daysRemaining = Math.ceil((dueMs - nowMs) / DAY_MS);
  if (daysRemaining < 0) {
    return {
      status: 'OVERDUE',
      dueAt: sensor.calibrationDueAt,
      daysRemaining,
      label: `已超期 ${Math.abs(daysRemaining)} 天`,
    };
  }
  if (daysRemaining <= 30) {
    return {
      status: 'EXPIRING_SOON',
      dueAt: sensor.calibrationDueAt,
      daysRemaining,
      label: `剩余 ${daysRemaining} 天`,
    };
  }
  return {
    status: 'VALID',
    dueAt: sensor.calibrationDueAt,
    daysRemaining,
    label: `剩余 ${daysRemaining} 天`,
  };
}

const STATUS_LEVEL_PRIORITY: Readonly<Record<GovernanceStatusPresentation['level'], number>> = {
  CRITICAL: 1,
  WARNING: 2,
  NOTICE: 3,
  NORMAL: 4,
};

function resolveSensorRowStatus(input: {
  readonly connectionState: AssetsDeviceRow['operational']['connection']['state'];
  readonly pointView: AssetsPointView | null;
  readonly primaryProbeOffset: ProbeOffsetViewItem | null;
  readonly hasPhysicsOrLowConfidence: boolean;
  readonly calibration: SensorCalibrationInfo;
}): GovernanceStatusPresentation {
  if (input.connectionState === 'OFFLINE') {
    return { level: 'CRITICAL', shape: '◆', tone: 'danger', label: '通信中断' };
  }
  if (input.pointView?.health?.status === 'BAD' || input.pointView?.quality === 'INVALID') {
    return { level: 'CRITICAL', shape: '◆', tone: 'danger', label: '读数无效' };
  }
  if (input.calibration.status === 'OVERDUE') {
    return { level: 'CRITICAL', shape: '◆', tone: 'danger', label: '定检超期' };
  }
  if (input.primaryProbeOffset) {
    return { level: 'WARNING', shape: '▲', tone: 'warning', label: '零点待校核' };
  }
  if (input.hasPhysicsOrLowConfidence) {
    const allReasons = collectPointReasonCodes(input.pointView);
    const isOnlyLowConfidence =
      Boolean(input.pointView?.measurement?.lowConfidence) &&
      allReasons.every((code) => code === 'LOW_CONFIDENCE');
    return {
      level: 'WARNING',
      shape: '▲',
      tone: 'warning',
      label: isOnlyLowConfidence ? '低可信提醒' : '物理异常提醒',
    };
  }
  if (input.calibration.status === 'EXPIRING_SOON') {
    return { level: 'WARNING', shape: '▲', tone: 'warning', label: '定检临期' };
  }
  if (
    input.pointView?.health?.status === 'SUSPECT' ||
    input.pointView?.health?.status === 'STALE' ||
    input.pointView?.freshness === 'STALE'
  ) {
    return { level: 'WARNING', shape: '▲', tone: 'warning', label: '待现场确认' };
  }
  if (!input.pointView || input.pointView.state !== 'PRESENT') {
    return { level: 'NOTICE', shape: '■', tone: 'neutral', label: '等待读数' };
  }
  return { level: 'NORMAL', shape: '○', tone: 'success', label: '读数正常' };
}

export function buildSensorCalibrationModel(
  input: BuildSensorCalibrationInput,
): SensorCalibrationModel {
  const now = input.now ?? new Date();
  const nowMs = now.getTime();
  const sensorById = new Map(
    input.assetModel.sensors
      .filter((sensor) => sensor.status === 'ACTIVE')
      .map((sensor) => [sensor.id, sensor] as const),
  );
  const deviceRows = buildAssetsRows({
    assetModel: input.assetModel,
    snapshots: input.snapshots,
    now,
  });

  const rows: SensorCalibrationRow[] = [];

  for (const deviceRow of deviceRows) {
    if (deviceRow.device.status !== 'ACTIVE' || deviceRow.device.deviceType === 'GATEWAY') {
      continue;
    }
    const boundAsset = primaryBoundAsset(deviceRow.binding);
    const deviceDisplayName = boundAsset
      ? boundAsset.displayName
      : cleanEndpointDisplayName(deviceRow.device.displayName);
    const spaceName = deviceRow.space.state === 'bound' ? deviceRow.space.space.displayName : null;

    for (const point of deviceRow.telemetryPoints) {
      if (
        point.status !== 'ACTIVE' ||
        point.pointType !== 'TELEMETRY' ||
        point.valueType !== 'NUMBER'
      ) {
        continue;
      }

      const pointView =
        deviceRow.operational.points.find((candidate) => candidate.pointId === point.id) ?? null;
      const sensor = point.sensorId ? (sensorById.get(point.sensorId) ?? null) : null;
      const calibration = evaluateCalibrationDue(sensor, nowMs);

      const probeOffsets = (pointView?.probeOffsets ?? []).map(formatProbeOffsetItem);
      const primaryProbeOffset =
        probeOffsets.find((item) => item.suggested && !item.applied) ?? null;

      const resolvedUnit =
        pointView?.state === 'PRESENT' ? pointView.unit : formatTelemetryUnit(point.unit);
      const measurement = pointView?.measurement ?? null;
      const measurementSummaryLabel = measurement
        ? formatMeasurementIntervalLabel(measurement, resolvedUnit)
        : null;

      const pointReasons = collectPointReasonCodes(pointView);
      const hasPhysicsOrLowConfidence = Boolean(
        measurement?.lowConfidence ||
          pointReasons.some((code) => PHYSICS_AND_CONFIDENCE_REASONS.has(code)),
      );

      const diagnosticNotes = pointReasons.map(explainQualityReason);
      if (deviceRow.operational.connection.state === 'OFFLINE') {
        diagnosticNotes.unshift('设备当前通信中断，无法采集最新传感器读数');
      } else if (pointView?.freshness === 'STALE') {
        diagnosticNotes.unshift('点位读数已超过允许刷新间隔');
      }
      if (calibration.status === 'OVERDUE') {
        diagnosticNotes.push(`传感器定检${calibration.label}，请安排检定或校准`);
      } else if (calibration.status === 'EXPIRING_SOON') {
        diagnosticNotes.push(`传感器定检将于 ${calibration.daysRemaining} 天后到期`);
      }

      const status = resolveSensorRowStatus({
        connectionState: deviceRow.operational.connection.state,
        pointView,
        primaryProbeOffset,
        hasPhysicsOrLowConfidence,
        calibration,
      });

      const inspectKey = `${deviceRow.device.code}:${point.pointCode}`;
      const workOrderSourceRef = `integrations-sensor:${input.siteId}:${deviceRow.device.code}:${point.pointCode}`;
      const openWorkOrder = findOpenExternalWorkOrder(input.workOrders, workOrderSourceRef);
      const pointLabel = pointView?.label ?? point.displayName;

      rows.push({
        inspectKey,
        pointId: point.id,
        pointCode: point.pointCode,
        pointLabel,
        deviceId: deviceRow.device.id,
        deviceCode: deviceRow.device.code,
        deviceDisplayName,
        spaceName,
        channelLabel: formatSourceChannel(point.sourceMetadata),
        sampleIntervalLabel: formatSampleInterval(point.sampleIntervalMs),
        currentValue: pointView?.state === 'PRESENT' ? pointView.displayValue : '—',
        unit: resolvedUnit,
        sampledAt: pointView?.sampledAt ?? null,
        healthScore: pointView?.health?.score ?? null,
        healthStatus: pointView?.health?.status ?? null,
        primaryProbeOffset,
        probeOffsets,
        measurement,
        measurementSummaryLabel,
        hasPhysicsOrLowConfidence,
        diagnosticNotes,
        calibration,
        sensorMeta: sensor
          ? {
              sensorId: sensor.id,
              code: sensor.code,
              displayName: sensor.displayName,
              sensorType: sensor.sensorType,
              manufacturer: sensor.manufacturer,
              model: sensor.model,
              serialNumber: sensor.serialNumber,
            }
          : null,
        boundAsset: boundAsset
          ? {
              assetId: boundAsset.id,
              displayName: boundAsset.displayName,
              assetType: boundAsset.assetType,
              ratedPowerKw: boundAsset.ratedPower ?? null,
              ratedCapacityKw: boundAsset.ratedCapacity ?? null,
            }
          : null,
        status,
        trendsSearch: {
          site: input.siteId,
          points: buildTrendPointRef(deviceRow.device.id, point.pointCode),
          window: 'custom',
          from: new Date(input.window.fromMs).toISOString(),
          to: new Date(input.window.toMs).toISOString(),
          granularity: 'FIVE_MINUTE',
        },
        workOrderSourceRef,
        openWorkOrder,
        suggestedWorkOrderTitle: `${deviceDisplayName} · ${pointLabel}：现场校准核查`,
        suggestedWorkOrderDescription: primaryProbeOffset
          ? `${primaryProbeOffset.pairLabel}在仅水泵运行稳态窗口测得零点偏差 ${primaryProbeOffset.offsetK >= 0 ? '+' : ''}${primaryProbeOffset.offsetK.toFixed(2)} K（${primaryProbeOffset.summaryLabel}）。请现场比对供回水温度探头插入深度、导热硅脂与变送器零点。`
          : diagnosticNotes.length > 0
            ? `原因：${diagnosticNotes.join('；')}。请现场核对传感器接线、量程与检定状态。`
            : `请现场核查 ${deviceDisplayName} · ${pointLabel}（${inspectKey}）读数与校准状态。`,
      });
    }
  }

  rows.sort(
    (left, right) =>
      STATUS_LEVEL_PRIORITY[left.status.level] - STATUS_LEVEL_PRIORITY[right.status.level] ||
      left.deviceDisplayName.localeCompare(right.deviceDisplayName, 'zh-CN', { numeric: true }) ||
      left.pointLabel.localeCompare(right.pointLabel, 'zh-CN', { numeric: true }),
  );

  const probeOffsetCount = rows.filter((row) => row.primaryProbeOffset !== null).length;
  const physicsOrLowConfidenceCount = rows.filter((row) => row.hasPhysicsOrLowConfidence).length;
  const calibrationConfiguredCount = rows.filter(
    (row) => row.calibration.status !== 'UNREGISTERED',
  ).length;
  const calibrationDueOrExpiringCount = rows.filter(
    (row) =>
      row.calibration.status === 'OVERDUE' || row.calibration.status === 'EXPIRING_SOON',
  ).length;
  const attentionPoints = rows.filter(
    (row) => row.status.level === 'CRITICAL' || row.status.level === 'WARNING',
  ).length;

  return {
    summary: {
      totalPoints: rows.length,
      attentionPoints,
      probeOffsetCount,
      physicsOrLowConfidenceCount,
      calibrationConfiguredCount,
      calibrationDueOrExpiringCount,
      calibrationDueDisplay:
        calibrationConfiguredCount === 0 ? '未登记' : String(calibrationDueOrExpiringCount),
    },
    rows,
  };
}

export function filterSensorCalibrationRows(
  rows: readonly SensorCalibrationRow[],
  filter: SensorCalibrationFilter,
): readonly SensorCalibrationRow[] {
  if (filter === 'ATTENTION') {
    return rows.filter((row) => row.status.level === 'CRITICAL' || row.status.level === 'WARNING');
  }
  if (filter === 'PROBE_OFFSET') {
    return rows.filter((row) => row.primaryProbeOffset !== null);
  }
  if (filter === 'CALIBRATION_DUE') {
    return rows.filter(
      (row) =>
        row.calibration.status === 'OVERDUE' || row.calibration.status === 'EXPIRING_SOON',
    );
  }
  return rows;
}
