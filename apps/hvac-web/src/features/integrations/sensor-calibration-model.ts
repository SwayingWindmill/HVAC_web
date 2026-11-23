import type {
  Sensor,
  SiteAssetModel,
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
  formatSampleInterval,
  formatSourceChannel,
  primaryBoundAsset,
  type GovernanceStatusPresentation,
  type MeterTrendJumpSearch,
} from './governance-shared-model.ts';

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
