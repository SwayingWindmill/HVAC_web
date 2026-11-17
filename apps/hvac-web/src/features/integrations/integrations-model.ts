import {
  PlatformApiError,
  type Asset,
  type ConcentratorAuditItem,
  type ConcentratorChannelReading,
  type ConcentratorEidSlot,
  type ConcentratorParams,
  type ConcentratorParamsPatch,
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

export type IntegrationsTabView = 'gateways' | 'concentrators' | 'meters' | 'sensors';

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
  /** Own counter history for physical check meters on synthesized-total sites. */
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

export const PROTECTED_RS485_UIDS = [2, 3, 4, 5, 10, 31] as const;

const PROTECTED_RS485_UID_SET = new Set<number>(PROTECTED_RS485_UIDS);

export function isProtectedConcentratorUid(uid: number): boolean {
  return Number.isInteger(uid) && PROTECTED_RS485_UID_SET.has(uid);
}

export function describeProtectedConcentratorUid(uid: number): string {
  return `从站地址 ${uid} 为冷机 / 总电表 / 冷冻泵变频器等受保护 RS485 设备地址（受保护站号：2、3、4、5、10、31），已拦截集中器访问以防总线冲突。`;
}

export const CONCENTRATOR_BATTERY_CRITICAL_MV = 2450;
export const CONCENTRATOR_BATTERY_WARNING_MV = 2600;
export const MAX_CONCENTRATOR_EID = 4_294_967_295;

export interface ConcentratorSelectOption {
  readonly value: number;
  readonly label: string;
}

export const CONCENTRATOR_BAUD_RATE_OPTIONS: readonly ConcentratorSelectOption[] = [
  { value: 0, label: '1200 bps' },
  { value: 1, label: '2400 bps' },
  { value: 2, label: '4800 bps' },
  { value: 3, label: '9600 bps' },
  { value: 4, label: '19200 bps' },
  { value: 5, label: '38400 bps' },
] as const;

export const CONCENTRATOR_BANDWIDTH_OPTIONS: readonly ConcentratorSelectOption[] = [
  { value: 0, label: '125 kHz' },
  { value: 1, label: '250 kHz' },
  { value: 2, label: '500 kHz' },
] as const;

export const CONCENTRATOR_TIME_MARK_OPTIONS: readonly ConcentratorSelectOption[] = [
  { value: 0, label: '0 · 关闭时间戳' },
  { value: 1, label: '1 · 标准帧时间戳' },
  { value: 2, label: '2 · 扩展时间戳' },
  { value: 3, label: '3 · 调试模式时间戳' },
] as const;

export const CONCENTRATOR_RSSI_OUT_OPTIONS: readonly ConcentratorSelectOption[] = [
  { value: 0, label: '0 · 关闭随帧信号强度输出' },
  { value: 1, label: '1 · 开启随帧信号强度输出' },
] as const;

function lookupOptionLabel(
  options: readonly ConcentratorSelectOption[],
  value: number,
): string {
  return options.find((item) => item.value === value)?.label ?? String(value);
}

export function formatConcentratorBaudRate(code: number): string {
  return lookupOptionLabel(CONCENTRATOR_BAUD_RATE_OPTIONS, code);
}

export function formatConcentratorBandwidth(code: number): string {
  return lookupOptionLabel(CONCENTRATOR_BANDWIDTH_OPTIONS, code);
}

export function formatConcentratorTimeMark(code: number): string {
  return lookupOptionLabel(CONCENTRATOR_TIME_MARK_OPTIONS, code);
}

export function formatConcentratorRssiOut(code: number): string {
  return lookupOptionLabel(CONCENTRATOR_RSSI_OUT_OPTIONS, code);
}

export function formatConcentratorEidDisplay(eid: number): string {
  if (eid <= 0) return '未绑定 (0)';
  return `${eid} (0x${eid.toString(16).toUpperCase().padStart(8, '0')})`;
}

export type ConcentratorBatteryHealth =
  | 'NORMAL'
  | 'LOW_VOLTAGE'
  | 'CRITICAL_LOW'
  | 'UNKNOWN';

export interface ConcentratorBatteryClassification {
  readonly health: ConcentratorBatteryHealth;
  readonly status: GovernanceStatusPresentation;
}

export function classifyConcentratorBattery(
  voltageMv: number | null,
): ConcentratorBatteryClassification {
  if (voltageMv === null) {
    return {
      health: 'UNKNOWN',
      status: {
        level: 'NOTICE',
        shape: '■',
        tone: 'neutral',
        label: '无电压读数',
      },
    };
  }
  if (voltageMv < CONCENTRATOR_BATTERY_CRITICAL_MV) {
    return {
      health: 'CRITICAL_LOW',
      status: {
        level: 'CRITICAL',
        shape: '◆',
        tone: 'danger',
        label: '严重欠压 (<2450 mV)',
      },
    };
  }
  if (voltageMv < CONCENTRATOR_BATTERY_WARNING_MV) {
    return {
      health: 'LOW_VOLTAGE',
      status: {
        level: 'WARNING',
        shape: '▲',
        tone: 'warning',
        label: '低电压 (<2600 mV)',
      },
    };
  }
  return {
    health: 'NORMAL',
    status: {
      level: 'NORMAL',
      shape: '○',
      tone: 'success',
      label: '电量正常',
    },
  };
}

export interface ConcentratorChannelRow {
  readonly channel: number;
  readonly eid: number;
  readonly online: boolean;
  readonly sensorType: number;
  readonly sensorModel: string;
  readonly temperatureC: number | null;
  readonly voltageMv: number | null;
  readonly rssiDbm: number | null;
  readonly batteryHealth: ConcentratorBatteryHealth;
  readonly batteryStatus: GovernanceStatusPresentation;
  readonly linkStatus: GovernanceStatusPresentation;
  readonly overallStatus: GovernanceStatusPresentation;
}

export function buildConcentratorChannelRow(
  reading: ConcentratorChannelReading,
): ConcentratorChannelRow {
  const battery = classifyConcentratorBattery(reading.voltageMv);
  const linkStatus: GovernanceStatusPresentation = reading.online
    ? {
        level: 'NORMAL',
        shape: '○',
        tone: 'success',
        label: '在线',
      }
    : {
        level: 'CRITICAL',
        shape: '◆',
        tone: 'danger',
        label: '离线',
      };

  let overallStatus: GovernanceStatusPresentation = linkStatus;
  if (reading.online) {
    overallStatus =
      battery.health === 'CRITICAL_LOW' || battery.health === 'LOW_VOLTAGE'
        ? battery.status
        : linkStatus;
  }

  return {
    ...reading,
    batteryHealth: battery.health,
    batteryStatus: battery.status,
    linkStatus,
    overallStatus,
  };
}

export interface ConcentratorSummary {
  readonly totalChannels: number;
  readonly boundChannels: number;
  readonly onlineCount: number;
  readonly offlineCount: number;
  readonly lowBatteryCount: number;
  readonly criticalBatteryCount: number;
  readonly avgTemperatureC: number | null;
}

export function buildConcentratorSummary(
  rows: readonly ConcentratorChannelRow[],
): ConcentratorSummary {
  let boundChannels = 0;
  let onlineCount = 0;
  let offlineCount = 0;
  let lowBatteryCount = 0;
  let criticalBatteryCount = 0;
  let tempSum = 0;
  let tempCount = 0;

  for (const row of rows) {
    if (row.eid > 0) boundChannels += 1;
    if (row.online) {
      onlineCount += 1;
    } else {
      offlineCount += 1;
    }
    if (row.voltageMv !== null && row.voltageMv < CONCENTRATOR_BATTERY_WARNING_MV) {
      lowBatteryCount += 1;
      if (row.voltageMv < CONCENTRATOR_BATTERY_CRITICAL_MV) {
        criticalBatteryCount += 1;
      }
    }
    if (row.online && row.temperatureC !== null) {
      tempSum += row.temperatureC;
      tempCount += 1;
    }
  }

  return {
    totalChannels: rows.length,
    boundChannels,
    onlineCount,
    offlineCount,
    lowBatteryCount,
    criticalBatteryCount,
    avgTemperatureC: tempCount > 0 ? Number((tempSum / tempCount).toFixed(1)) : null,
  };
}

export type ConcentratorChannelFilter = 'ALL' | 'ONLINE' | 'OFFLINE' | 'LOW_BATTERY';

export function filterConcentratorChannels(
  rows: readonly ConcentratorChannelRow[],
  filter: ConcentratorChannelFilter,
  query: string,
): readonly ConcentratorChannelRow[] {
  const normalizedQuery = query.trim().toLowerCase();
  return rows.filter((row) => {
    if (filter === 'ONLINE' && !row.online) return false;
    if (filter === 'OFFLINE' && row.online) return false;
    if (
      filter === 'LOW_BATTERY' &&
      (row.voltageMv === null || row.voltageMv >= CONCENTRATOR_BATTERY_WARNING_MV)
    ) {
      return false;
    }
    if (!normalizedQuery) return true;
    const channelText = String(row.channel);
    const channelTag = `ch${row.channel}`;
    const eidDec = String(row.eid);
    const eidHex = row.eid.toString(16).toLowerCase();
    const eidPaddedHex = eidHex.padStart(8, '0');
    const model = row.sensorModel.toLowerCase();
    return (
      channelText.includes(normalizedQuery) ||
      channelTag.includes(normalizedQuery) ||
      eidDec.includes(normalizedQuery) ||
      eidHex.includes(normalizedQuery) ||
      eidPaddedHex.includes(normalizedQuery) ||
      `0x${eidHex}`.includes(normalizedQuery) ||
      `0x${eidPaddedHex}`.includes(normalizedQuery) ||
      model.includes(normalizedQuery)
    );
  });
}

export interface ConcentratorParamFieldSpec {
  readonly key: keyof ConcentratorParams;
  readonly label: string;
  readonly unit: string;
  readonly min: number;
  readonly max: number;
  readonly group: 'serial' | 'rf';
  readonly description: string;
  readonly options?: readonly ConcentratorSelectOption[];
}

export const CONCENTRATOR_PARAM_SPECS: readonly ConcentratorParamFieldSpec[] = [
  {
    key: 'modbusId',
    label: 'Modbus 从站地址',
    unit: '',
    min: 1,
    max: 247,
    group: 'serial',
    description: '避开 2/3/4/5/10/31 受保护 RS485 设备地址，默认使用 1',
  },
  {
    key: 'baudRateCode',
    label: '串口波特率代码',
    unit: '',
    min: 0,
    max: 5,
    group: 'serial',
    description: 'RS485 串口波特率挡位，须与现场采集总线一致',
    options: CONCENTRATOR_BAUD_RATE_OPTIONS,
  },
  {
    key: 'offLineTime',
    label: '探头离线判定时长',
    unit: '秒',
    min: 1,
    max: 65535,
    group: 'serial',
    description: '超过该时长未收到射频报文则判定对应探头离线',
  },
  {
    key: 'dataUplinkCycle',
    label: '数据主动上报周期',
    unit: '秒',
    min: 1,
    max: 65535,
    group: 'serial',
    description: '集中器周期采集并对外更新通道读数的基准间隔',
  },
  {
    key: 'localNet',
    label: '本地网络号',
    unit: '',
    min: 0,
    max: 65535,
    group: 'serial',
    description: '用于区分同一区域多台集中器的 LoRa 网络分组标识',
  },
  {
    key: 'timeMark',
    label: '时间戳标志',
    unit: '',
    min: 0,
    max: 3,
    group: 'serial',
    description: '控制通道报文是否附带帧时间戳标记',
    options: CONCENTRATOR_TIME_MARK_OPTIONS,
  },
  {
    key: 'rfTxPower',
    label: '射频发射功率',
    unit: 'dBm',
    min: 0,
    max: 30,
    group: 'rf',
    description: '无线下行发射功率（0–30 dBm），过低可能减小覆盖半径',
  },
  {
    key: 'rfFrequency',
    label: '中心频率',
    unit: 'MHz',
    min: 100,
    max: 1000,
    group: 'rf',
    description: '无线载波频率（常见 433 MHz / 470 MHz），须与探头一致',
  },
  {
    key: 'rfBandWidth',
    label: '射频带宽',
    unit: '',
    min: 0,
    max: 2,
    group: 'rf',
    description: '信道带宽越宽速率越高，但抗干扰与灵敏度相应下降',
    options: CONCENTRATOR_BANDWIDTH_OPTIONS,
  },
  {
    key: 'rfSf',
    label: '扩频因子 (SF)',
    unit: '',
    min: 5,
    max: 12,
    group: 'rf',
    description: 'LoRa 扩频因子（5–12），越大穿透越强但空中耗时越长',
  },
  {
    key: 'rfLeadCode',
    label: '前导码长度',
    unit: '',
    min: 1,
    max: 65535,
    group: 'rf',
    description: '无线帧前导同步长度',
  },
  {
    key: 'txTimeout',
    label: '发送超时',
    unit: '毫秒',
    min: 1,
    max: 65535,
    group: 'rf',
    description: '射频单帧发送超时保护阈值',
  },
  {
    key: 'rxTimeout',
    label: '接收超时',
    unit: '毫秒',
    min: 1,
    max: 65535,
    group: 'rf',
    description: '射频应答接收窗口超时阈值',
  },
  {
    key: 'rssiOut',
    label: '输出信号强度',
    unit: '',
    min: 0,
    max: 1,
    group: 'rf',
    description: '是否在传感器通道结构体内附带 RSSI 信号强度字段',
    options: CONCENTRATOR_RSSI_OUT_OPTIONS,
  },
] as const;

export function formatConcentratorParamValue(
  key: keyof ConcentratorParams,
  value: number,
): string {
  switch (key) {
    case 'baudRateCode':
      return formatConcentratorBaudRate(value);
    case 'rfBandWidth':
      return formatConcentratorBandwidth(value);
    case 'timeMark':
      return formatConcentratorTimeMark(value);
    case 'rssiOut':
      return formatConcentratorRssiOut(value);
    case 'rfTxPower':
      return `${value} dBm`;
    case 'rfFrequency':
      return `${value} MHz`;
    case 'offLineTime':
    case 'dataUplinkCycle':
      return `${value} 秒`;
    case 'txTimeout':
    case 'rxTimeout':
      return `${value} ms`;
    case 'rfSf':
      return `SF${value}`;
    default:
      return String(value);
  }
}

export interface ConcentratorParamsValidation {
  readonly valid: boolean;
  readonly protectedModbusId: boolean;
  readonly errors: Partial<Record<keyof ConcentratorParams, string>>;
}

export function validateConcentratorParamsDraft(
  draft: ConcentratorParams,
): ConcentratorParamsValidation {
  const errors: Partial<Record<keyof ConcentratorParams, string>> = {};
  let valid = true;

  for (const spec of CONCENTRATOR_PARAM_SPECS) {
    const val = draft[spec.key];
    if (!Number.isInteger(val) || val < spec.min || val > spec.max) {
      errors[spec.key] = `请输入 ${spec.min} 到 ${spec.max} 之间的整数`;
      valid = false;
    }
  }

  const protectedModbusId = isProtectedConcentratorUid(draft.modbusId);
  if (protectedModbusId) {
    errors.modbusId = describeProtectedConcentratorUid(draft.modbusId);
    valid = false;
  }

  return {
    valid,
    protectedModbusId,
    errors,
  };
}

export interface ConcentratorParamDiffItem {
  readonly key: keyof ConcentratorParams;
  readonly label: string;
  readonly before: number;
  readonly after: number;
  readonly beforeDisplay: string;
  readonly afterDisplay: string;
}

export interface ConcentratorParamsDiffResult {
  readonly hasChanges: boolean;
  readonly patch: ConcentratorParamsPatch | null;
  readonly changes: readonly ConcentratorParamDiffItem[];
}

export function buildConcentratorParamsDiff(
  original: ConcentratorParams,
  draft: ConcentratorParams,
): ConcentratorParamsDiffResult {
  const patch: Partial<Record<keyof ConcentratorParams, number>> = {};
  const changes: ConcentratorParamDiffItem[] = [];

  for (const spec of CONCENTRATOR_PARAM_SPECS) {
    const before = original[spec.key];
    const after = draft[spec.key];
    if (before === after) continue;
    patch[spec.key] = after;
    changes.push({
      key: spec.key,
      label: spec.label,
      before,
      after,
      beforeDisplay: formatConcentratorParamValue(spec.key, before),
      afterDisplay: formatConcentratorParamValue(spec.key, after),
    });
  }

  if (changes.length === 0) {
    return { hasChanges: false, patch: null, changes: [] };
  }

  return {
    hasChanges: true,
    patch,
    changes,
  };
}

export interface ConcentratorEidConflictResult {
  readonly hasConflicts: boolean;
  readonly duplicatesByEid: ReadonlyMap<number, readonly number[]>;
  readonly conflictingSlotSet: ReadonlySet<number>;
}

export function detectDuplicateNonZeroEids(
  slots: readonly ConcentratorEidSlot[],
): ConcentratorEidConflictResult {
  const byEid = new Map<number, number[]>();
  for (const entry of slots) {
    if (!Number.isInteger(entry.eid) || entry.eid <= 0) continue;
    const list = byEid.get(entry.eid);
    if (list) {
      list.push(entry.slot);
    } else {
      byEid.set(entry.eid, [entry.slot]);
    }
  }

  const duplicatesByEid = new Map<number, readonly number[]>();
  const conflictingSlotSet = new Set<number>();

  for (const [eid, slotNumbers] of byEid) {
    if (slotNumbers.length < 2) continue;
    const sorted = [...slotNumbers].sort((a, b) => a - b);
    duplicatesByEid.set(eid, sorted);
    for (const slotNum of sorted) {
      conflictingSlotSet.add(slotNum);
    }
  }

  return {
    hasConflicts: duplicatesByEid.size > 0,
    duplicatesByEid,
    conflictingSlotSet,
  };
}

export interface ConcentratorEidBatchFillInput {
  readonly startSlot: number;
  readonly count: number;
  readonly startEid: number;
  readonly step: number;
}

export function applyConcentratorEidBatchFill(
  slots: readonly ConcentratorEidSlot[],
  input: ConcentratorEidBatchFillInput,
): readonly ConcentratorEidSlot[] {
  const { startSlot, count, startEid, step } = input;
  if (
    !Number.isInteger(startSlot) ||
    startSlot < 1 ||
    startSlot > 200 ||
    !Number.isInteger(count) ||
    count < 1 ||
    !Number.isInteger(startEid) ||
    startEid < 0 ||
    startEid > MAX_CONCENTRATOR_EID ||
    !Number.isInteger(step) ||
    step < 0
  ) {
    return slots;
  }

  const endSlot = Math.min(200, startSlot + count - 1);
  return slots.map((item) => {
    if (item.slot < startSlot || item.slot > endSlot) return item;
    const offset = item.slot - startSlot;
    const nextEid =
      startEid === 0 ? 0 : Math.min(MAX_CONCENTRATOR_EID, startEid + offset * step);
    return {
      slot: item.slot,
      eid: nextEid,
    };
  });
}

export interface ConcentratorEidDiffItem {
  readonly slot: number;
  readonly beforeEid: number;
  readonly afterEid: number;
}

export function buildConcentratorEidDiff(
  original: readonly ConcentratorEidSlot[],
  draft: readonly ConcentratorEidSlot[],
): readonly ConcentratorEidDiffItem[] {
  const originalBySlot = new Map<number, number>();
  for (const item of original) {
    originalBySlot.set(item.slot, item.eid);
  }
  const diffs: ConcentratorEidDiffItem[] = [];
  for (const item of draft) {
    const beforeEid = originalBySlot.get(item.slot) ?? 0;
    if (beforeEid !== item.eid) {
      diffs.push({
        slot: item.slot,
        beforeEid,
        afterEid: item.eid,
      });
    }
  }
  return diffs.sort((a, b) => a.slot - b.slot);
}

export const CONCENTRATOR_AUDIT_OPERATION_LABELS: Readonly<
  Record<ConcentratorAuditItem['operation'], string>
> = {
  WRITE_PARAMS: '串口与射频参数写入',
  WRITE_EIDS: 'EID 槽位映射写入',
};

export const CONCENTRATOR_AUDIT_OUTCOME_PRESENTATION: Readonly<
  Record<ConcentratorAuditItem['outcome'], GovernanceStatusPresentation>
> = {
  APPLIED: {
    level: 'NORMAL',
    shape: '○',
    tone: 'success',
    label: '已写入 Flash',
  },
  REJECTED: {
    level: 'WARNING',
    shape: '▲',
    tone: 'warning',
    label: '校验拒绝',
  },
  FAILED: {
    level: 'CRITICAL',
    shape: '◆',
    tone: 'danger',
    label: '下发失败',
  },
  TIMEOUT: {
    level: 'CRITICAL',
    shape: '◆',
    tone: 'danger',
    label: '网关超时',
  },
  STAGED_SHADOW: {
    level: 'WARNING',
    shape: '▲',
    tone: 'warning',
    label: '已暂存待同步',
  },
};

export function mergeConcentratorAudits(
  ...groups: ReadonlyArray<readonly ConcentratorAuditItem[] | undefined>
): readonly ConcentratorAuditItem[] {
  const byId = new Map<string, ConcentratorAuditItem>();
  for (const group of groups) {
    if (!group) continue;
    for (const item of group) {
      if (!byId.has(item.id)) {
        byId.set(item.id, item);
      }
    }
  }
  return Array.from(byId.values())
    .sort((left, right) => Date.parse(right.occurredAt) - Date.parse(left.occurredAt))
    .slice(0, 20);
}

const CONCENTRATOR_UID_PATTERN = /(?:^|[^a-z0-9])uid[\s_-]*(\d{1,3})(?:[^0-9]|$)/i;

export function inferDefaultConcentratorUid(
  devices: readonly Pick<Device, 'deviceType' | 'code' | 'displayName'>[],
): number {
  for (const device of devices) {
    if (device.deviceType !== 'TEMPERATURE_MODULE') continue;
    for (const candidate of [device.code, device.displayName]) {
      const match = CONCENTRATOR_UID_PATTERN.exec(candidate);
      if (!match) continue;
      const uid = Number(match[1]);
      if (Number.isInteger(uid) && uid >= 1 && uid <= 247 && !isProtectedConcentratorUid(uid)) {
        return uid;
      }
    }
  }
  return 1;
}

export function describeConcentratorError(
  error: unknown,
  fallback = '集中器状态或配置暂时无法读取，请稍后重试',
): string {
  if (error instanceof PlatformApiError) {
    switch (error.problem.code) {
      case 'CONCENTRATOR_UID_PROTECTED':
        return '所选从站地址属于受保护的现场 RS485 设备（冷机 / 总电表 / 冷冻泵变频器），已拒绝本次读写';
      case 'CONCENTRATOR_EID_DUPLICATE':
        return '存在重复的非零探头 EID，请修正冲突槽位后再提交写入 Flash';
      case 'CONCENTRATOR_EIDS_INVALID':
        return '提交的 200 槽 EID 映射格式或数量无效，请核对后重试';
      case 'CONCENTRATOR_PARAMS_INVALID':
        return '提交的串口与射频参数超出有效范围，请核对后重试';
      case 'CONCENTRATOR_REASON_REQUIRED':
        return '请填写本次集中器变更原因后再确认下发';
      case 'CONCENTRATOR_REQUEST_INVALID':
        return '请求参数或 EID 槽位格式无效，请检查从站地址、数值范围与变更原因后重试';
      case 'CREDENTIAL_FORBIDDEN':
        return '当前账号缺少设备读取或集中器下发权限（需要 device.read / device.write）';
      case 'GATEWAY_NOT_FOUND':
        return '未找到所选现场网关，请确认网关登记状态';
      case 'GATEWAY_REVOKED':
        return '该现场网关接入凭据已停用，请先在「现场网关」重新签发接入码并激活';
      case 'CONCENTRATOR_RPC_TIMEOUT':
        return '无线集中器未及时应答或 Modbus 读取超时，请检查 RS485 接线与集中器供电';
      case 'CONCENTRATOR_TRANSPORT_UNAVAILABLE':
        return '现场网关远程连接通道暂未就绪，请先到「现场网关」确认网关状态';
      case 'CONCENTRATOR_EDGE_FAILED':
        return '现场网关转发集中器请求失败，请检查网关 Node-RED 运行状态与串口通信后重试';
      case 'CONCENTRATOR_SERVICE_UNAVAILABLE':
        return '平台集中器管理服务暂时不可用，请稍后重试';
      default:
        break;
    }
    if (error.problem.status === 403) {
      return '当前账号缺少设备读取或集中器下发权限（需要 device.read / device.write）';
    }
    if (error.problem.status === 404) {
      return '未找到所选现场网关，请确认网关登记状态';
    }
    if (error.problem.status === 409) {
      return '现场网关或集中器从站状态存在冲突，请刷新后重试';
    }
    if (error.problem.status === 422) {
      return '提交的参数、槽位映射或变更原因未通过校验，请核对后重试';
    }
    if (error.problem.status === 429) {
      return '集中器指令请求过于频繁，请稍候数秒再试';
    }
    if (error.problem.status === 502 || error.problem.status === 503 || error.problem.status === 504) {
      return '网关转发集中器请求失败或通道暂不可用，请稍后重试';
    }
  }
  return fallback;
}
