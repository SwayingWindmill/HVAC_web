import {
  MAX_TREND_POINTS,
  findAllBySourceKey,
  findFirstBySourceKey,
  type ResolvedTrendWindow,
  type TrendCatalog,
  type TrendPointItem,
  type TrendPresetId,
} from './trends-model.ts';

export interface TrendOverlayFlags {
  readonly alarms: boolean;
  readonly commands: boolean;
}

export interface TrendOverlayAlarmInput {
  readonly alarmId: string;
  readonly siteId: string;
  readonly deviceId?: string;
  readonly alarmType: string;
  readonly title: string;
  readonly summary: string;
  readonly condition: 'ACTIVE' | 'CLEARED';
  readonly currentSeverity: 'CRITICAL' | 'MAJOR' | 'MINOR' | 'WARNING' | 'INFO';
  readonly peakSeverity: 'CRITICAL' | 'MAJOR' | 'MINOR' | 'WARNING' | 'INFO';
  readonly occurrenceCount: number;
  readonly firstOccurredAt: string;
  readonly lastOccurredAt: string;
  readonly clearedAt?: string;
  readonly timeline: readonly unknown[];
  readonly version: number;
}

export interface TrendOverlayCommandInput {
  readonly commandId: string;
  readonly siteId: string;
  readonly deviceId: string;
  readonly pointId: string;
  readonly capability: string;
  readonly status: string;
  readonly risk: string;
  readonly approvalPolicy: string;
  readonly approvalCount: number;
  readonly requiredApprovalCount: number;
  readonly parameters: Readonly<Record<string, number>>;
  readonly transitions: readonly {
    readonly toStatus: string;
    readonly occurredAt: string;
  }[];
  readonly createdAt: string;
  readonly updatedAt: string;
}

export interface TrendAlarmSpan {
  readonly id: string;
  readonly alarm: TrendOverlayAlarmInput;
  readonly deviceName: string;
  readonly rawStartMs: number;
  readonly rawEndMs: number;
  readonly clampedStartMs: number;
  readonly clampedEndMs: number;
  readonly anchorPercent: number;
  readonly spanPercent: number;
}

export interface TrendCommandMark {
  readonly id: string;
  readonly command: TrendOverlayCommandInput;
  readonly deviceName: string;
  readonly atMs: number;
  readonly anchorPercent: number;
  readonly summaryLabel: string;
  readonly statusLabel: string;
}

export type TrendRailEvent =
  | { readonly kind: 'alarm'; readonly atMs: number; readonly anchorPercent: number; readonly span: TrendAlarmSpan }
  | { readonly kind: 'command'; readonly atMs: number; readonly anchorPercent: number; readonly mark: TrendCommandMark };

export interface TrendRailCluster {
  readonly id: string;
  readonly anchorPercent: number;
  readonly atMs: number;
  readonly events: readonly TrendRailEvent[];
}

export interface TrendOverlaysModel {
  readonly alarmSpans: readonly TrendAlarmSpan[];
  readonly commandMarks: readonly TrendCommandMark[];
  readonly railItems: readonly TrendRailCluster[];
}

const COMMAND_STATUS_LABELS: Readonly<Record<string, string>> = {
  SUBMITTED: '已提交',
  VALIDATING: '校验中',
  AWAITING_APPROVAL: '待审批',
  APPROVED: '已批准',
  QUEUED: '排队中',
  DISPATCHING: '下发中',
  SUCCEEDED: '已执行',
  FAILED: '执行失败',
  REJECTED: '已驳回',
  CANCELLED: '已取消',
  EXPIRED: '已过期',
  OUTCOME_UNKNOWN: '待核实',
};

const COMMAND_CAPABILITY_LABELS: Readonly<Record<string, string>> = {
  SETPOINT: '设定值调整',
  START: '开机指令',
  STOP: '停机指令',
  MODE: '模式切换',
  RESET: '复位指令',
};

export function parseTrendOverlays(raw: string | undefined): TrendOverlayFlags {
  if (!raw) return { alarms: false, commands: false };
  const tokens = new Set(
    raw
      .split(',')
      .map((item) => item.trim().toLowerCase())
      .filter(Boolean),
  );
  return {
    alarms: tokens.has('alarms'),
    commands: tokens.has('commands'),
  };
}

export function formatTrendOverlays(flags: TrendOverlayFlags): string | undefined {
  const parts: string[] = [];
  if (flags.alarms) parts.push('alarms');
  if (flags.commands) parts.push('commands');
  return parts.length > 0 ? parts.join(',') : undefined;
}

function resolveCommandTimestampMs(command: TrendOverlayCommandInput): number {
  for (let idx = command.transitions.length - 1; idx >= 0; idx -= 1) {
    const tr = command.transitions[idx]!;
    if (tr.toStatus === 'SUCCEEDED' || tr.toStatus === 'DISPATCHING' || tr.toStatus === 'FAILED') {
      const parsed = Date.parse(tr.occurredAt);
      if (Number.isFinite(parsed)) return parsed;
    }
  }
  return Date.parse(command.createdAt);
}

export function buildTrendOverlays(input: {
  readonly window: ResolvedTrendWindow;
  readonly selectedPoints: readonly TrendPointItem[];
  readonly catalog: TrendCatalog | null;
  readonly showAlarms: boolean;
  readonly showCommands: boolean;
  readonly alarms?: readonly TrendOverlayAlarmInput[];
  readonly commands?: readonly TrendOverlayCommandInput[];
}): TrendOverlaysModel {
  const { window, selectedPoints, catalog } = input;
  const spanMs = Math.max(1, window.toMs - window.fromMs);
  const selectedDeviceIds = new Set(selectedPoints.map((point) => point.deviceId));
  const deviceNameById = new Map<string, string>();
  const pointById = new Map<string, TrendPointItem>();
  if (catalog) {
    for (const point of catalog.points) {
      if (!deviceNameById.has(point.deviceId)) {
        deviceNameById.set(point.deviceId, point.deviceName);
      }
      pointById.set(point.pointId, point);
    }
  }
  for (const point of selectedPoints) {
    deviceNameById.set(point.deviceId, point.deviceName);
    pointById.set(point.pointId, point);
  }

  const alarmSpans: TrendAlarmSpan[] = [];
  if (input.showAlarms && input.alarms) {
    for (const alarm of input.alarms) {
      if (alarm.deviceId && selectedDeviceIds.size > 0 && !selectedDeviceIds.has(alarm.deviceId)) {
        continue;
      }
      const rawStartMs = Date.parse(alarm.firstOccurredAt);
      const rawEndMs =
        alarm.condition === 'CLEARED' && alarm.clearedAt
          ? Date.parse(alarm.clearedAt)
          : window.toMs;
      if (!Number.isFinite(rawStartMs) || !Number.isFinite(rawEndMs)) continue;
      if (rawStartMs > window.toMs || rawEndMs < window.fromMs) continue;

      const clampedStartMs = Math.max(window.fromMs, Math.min(window.toMs, rawStartMs));
      const clampedEndMs = Math.max(clampedStartMs, Math.min(window.toMs, rawEndMs));
      const anchorPercent = ((clampedStartMs - window.fromMs) / spanMs) * 100;
      const spanPercent = Math.max(0.8, ((clampedEndMs - clampedStartMs) / spanMs) * 100);
      const deviceName = alarm.deviceId
        ? deviceNameById.get(alarm.deviceId) ?? '关联设备'
        : '冷站全局';

      alarmSpans.push({
        id: `alarm-${alarm.alarmId}`,
        alarm,
        deviceName,
        rawStartMs,
        rawEndMs,
        clampedStartMs,
        clampedEndMs,
        anchorPercent,
        spanPercent,
      });
    }
    alarmSpans.sort((left, right) => left.clampedStartMs - right.clampedStartMs);
  }

  const commandMarks: TrendCommandMark[] = [];
  if (input.showCommands && input.commands) {
    for (const command of input.commands) {
      if (selectedDeviceIds.size > 0 && !selectedDeviceIds.has(command.deviceId)) {
        continue;
      }
      const atMs = resolveCommandTimestampMs(command);
      if (!Number.isFinite(atMs) || atMs < window.fromMs || atMs > window.toMs) {
        continue;
      }
      const point = pointById.get(command.pointId);
      const deviceName = point?.deviceName ?? deviceNameById.get(command.deviceId) ?? '控制设备';
      const actionLabel = point?.pointLabel ?? COMMAND_CAPABILITY_LABELS[command.capability] ?? command.capability;
      const paramEntries = Object.entries(command.parameters ?? {});
      const paramText =
        paramEntries.length > 0
          ? ` → ${paramEntries.map(([, val]) => `${val}${point?.unit ? ` ${point.unit}` : ''}`).join(', ')}`
          : '';
      const statusLabel = COMMAND_STATUS_LABELS[command.status] ?? command.status;
      const anchorPercent = ((atMs - window.fromMs) / spanMs) * 100;

      commandMarks.push({
        id: `cmd-${command.commandId}`,
        command,
        deviceName,
        atMs,
        anchorPercent,
        summaryLabel: `${deviceName} · ${actionLabel}${paramText}`,
        statusLabel,
      });
    }
    commandMarks.sort((left, right) => left.atMs - right.atMs);
  }

  const allEvents: TrendRailEvent[] = [
    ...alarmSpans.map(
      (span): TrendRailEvent => ({
        kind: 'alarm',
        atMs: span.clampedStartMs,
        anchorPercent: span.anchorPercent,
        span,
      }),
    ),
    ...commandMarks.map(
      (mark): TrendRailEvent => ({
        kind: 'command',
        atMs: mark.atMs,
        anchorPercent: mark.anchorPercent,
        mark,
      }),
    ),
  ].sort((left, right) => left.atMs - right.atMs);

  const DENSE_THRESHOLD_PERCENT = 2.5;
  const railItems: TrendRailCluster[] = [];
  for (const event of allEvents) {
    const prev = railItems[railItems.length - 1];
    const lastEvent = prev?.events[prev.events.length - 1];
    if (prev && lastEvent && Math.abs(event.anchorPercent - lastEvent.anchorPercent) <= DENSE_THRESHOLD_PERCENT) {
      const mergedEvents = [...prev.events, event];
      railItems[railItems.length - 1] = {
        ...prev,
        events: mergedEvents,
      };
    } else {
      railItems.push({
        id: event.kind === 'alarm' ? event.span.id : event.mark.id,
        anchorPercent: event.anchorPercent,
        atMs: event.atMs,
        events: [event],
      });
    }
  }

  return { alarmSpans, commandMarks, railItems };
}

export interface TrendDeepLinkSearch {
  readonly site: string;
  readonly window: 'custom';
  readonly from: string;
  readonly to: string;
  readonly points?: string;
  readonly preset?: TrendPresetId;
  readonly overlays: string;
  readonly source: 'alarm' | 'diagnostic';
  readonly sourceId: string;
  readonly sourceTitle: string;
}

const CONTEXT_PAD_MS = 30 * 60_000;
const MIN_WINDOW_SPAN_MS = 15 * 60_000;

function appendUniqueRefs(target: string[], candidates: readonly (TrendPointItem | undefined)[]): void {
  for (const item of candidates) {
    if (!item || target.length >= MAX_TREND_POINTS) continue;
    if (!target.includes(item.ref)) target.push(item.ref);
  }
}

export function buildAlarmTrendSearch(input: {
  readonly siteId: string;
  readonly alarm: {
    readonly alarmId: string;
    readonly title: string;
    readonly deviceId?: string;
    readonly condition: 'ACTIVE' | 'CLEARED';
    readonly firstOccurredAt: string;
    readonly clearedAt?: string;
  };
  readonly catalog?: TrendCatalog | null;
  readonly nowMs?: number;
}): TrendDeepLinkSearch {
  const nowMs = input.nowMs ?? Date.now();
  const startMs = Date.parse(input.alarm.firstOccurredAt);
  const rawEndMs =
    input.alarm.condition === 'CLEARED' && input.alarm.clearedAt
      ? Date.parse(input.alarm.clearedAt)
      : nowMs;
  const safeEndMs = Number.isFinite(rawEndMs) ? Math.min(nowMs, rawEndMs + CONTEXT_PAD_MS) : nowMs;
  const safeStartMs = Number.isFinite(startMs)
    ? Math.min(safeEndMs - MIN_WINDOW_SPAN_MS, startMs - CONTEXT_PAD_MS)
    : safeEndMs - 2 * CONTEXT_PAD_MS;

  const refs: string[] = [];
  if (input.catalog) {
    const { points, byDeviceId } = input.catalog;
    if (input.alarm.deviceId) {
      const devicePoints = byDeviceId.get(input.alarm.deviceId) ?? [];
      appendUniqueRefs(refs, devicePoints.filter((p) => p.kind === 'gauge').slice(0, 4));
      appendUniqueRefs(refs, devicePoints.filter((p) => p.kind === 'state').slice(0, 1));
    }
    appendUniqueRefs(refs, [
      findFirstBySourceKey(points, 'hvac_meter.active_power'),
      findFirstBySourceKey(points, 'btu_meter.supply_water_temperature') ??
        findFirstBySourceKey(points, 'chiller.leaving_chilled_water_temperature'),
      findFirstBySourceKey(points, 'btu_meter.return_water_temperature') ??
        findFirstBySourceKey(points, 'chiller.entering_chilled_water_temperature'),
      findFirstBySourceKey(points, 'chiller.chilled_water_temperature_setpoint'),
      ...findAllBySourceKey(points, 'chiller.run_state').slice(0, 2),
    ]);
  }

  return {
    site: input.siteId,
    window: 'custom',
    from: new Date(safeStartMs).toISOString(),
    to: new Date(safeEndMs).toISOString(),
    ...(refs.length > 0 ? { points: refs.join(',') } : { preset: 'plant-overview' }),
    overlays: 'alarms,commands',
    source: 'alarm',
    sourceId: input.alarm.alarmId,
    sourceTitle: input.alarm.title,
  };
}

export function buildFindingTrendSearch(input: {
  readonly siteId: string;
  readonly finding: {
    readonly id: string;
    readonly assetId: string;
    readonly findingType: string;
    readonly evaluationFrom: string;
    readonly evaluationTo: string;
  };
  readonly title: string;
  readonly catalog?: TrendCatalog | null;
  readonly nowMs?: number;
}): TrendDeepLinkSearch {
  const nowMs = input.nowMs ?? Date.now();
  const evalFromMs = Date.parse(input.finding.evaluationFrom);
  const evalToMs = Date.parse(input.finding.evaluationTo);
  const safeEndMs = Number.isFinite(evalToMs) ? Math.min(nowMs, evalToMs + CONTEXT_PAD_MS) : nowMs;
  const safeStartMs = Number.isFinite(evalFromMs)
    ? Math.min(safeEndMs - MIN_WINDOW_SPAN_MS, evalFromMs - CONTEXT_PAD_MS)
    : safeEndMs - 2 * CONTEXT_PAD_MS;

  const refs: string[] = [];
  if (input.catalog) {
    const { points } = input.catalog;
    appendUniqueRefs(refs, [
      findFirstBySourceKey(points, 'btu_meter.temperature_difference'),
      findFirstBySourceKey(points, 'btu_meter.supply_water_temperature'),
      findFirstBySourceKey(points, 'btu_meter.return_water_temperature'),
      findFirstBySourceKey(points, 'btu_meter.flow_rate'),
      ...findAllBySourceKey(points, 'chwp.frequency').slice(0, 2),
      findFirstBySourceKey(points, 'chiller.leaving_chilled_water_temperature'),
      ...findAllBySourceKey(points, 'chiller.run_state').slice(0, 1),
    ]);
    const assetPoints = points.filter((p) => p.assetId === input.finding.assetId);
    appendUniqueRefs(refs, assetPoints.slice(0, 3));
  }

  return {
    site: input.siteId,
    window: 'custom',
    from: new Date(safeStartMs).toISOString(),
    to: new Date(safeEndMs).toISOString(),
    ...(refs.length > 0 ? { points: refs.join(',') } : { preset: 'pump-hydraulics' }),
    overlays: 'alarms,commands',
    source: 'diagnostic',
    sourceId: input.finding.id,
    sourceTitle: input.title,
  };
}
