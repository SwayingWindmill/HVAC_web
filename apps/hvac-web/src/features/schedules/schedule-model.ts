import type {
  OperatingException,
  OperatingExceptionDecision,
  OperatingExceptionKind,
  OperatingExceptionSlot,
  OperatingExceptionStatus,
  OperatingSchedule,
  OperatingScheduleSlot,
  OperatingScheduleSlotInput,
  SiteAssetModel,
} from '../../api/generated/platformGateway.gen.ts';
import type { DeviceHistoryAggregateBucket } from '../../api/generated/s2Telemetry.gen.ts';
import type { CreateWorkOrderInput, WorkOrder, WorkOrderSourceReference } from '../../api/work-orders.ts';
import type { TrendsSearch } from '../../routes/_app._site.operations.trends.tsx';
import { siteMidnight, wallTime } from '../energy-analysis/consumption/model.ts';

export const DAY_OF_WEEK_LABELS: Readonly<Record<number, string>> = {
  1: '周一',
  2: '周二',
  3: '周三',
  4: '周四',
  5: '周五',
  6: '周六',
  7: '周日',
};

export const EXCEPTION_KIND_LABELS: Readonly<Record<OperatingExceptionKind, string>> = {
  HOLIDAY_SHUTDOWN: '节假日停机',
  OVERTIME_COOLING: '加班供冷',
  MAINTENANCE_WINDOW: '维保停机',
};

export const EXCEPTION_STATUS_LABELS: Readonly<Record<OperatingExceptionStatus, string>> = {
  PENDING_APPROVAL: '待审批',
  APPROVED: '已生效',
  REJECTED: '已驳回',
  CANCELLED: '已撤回',
};

export const EXCEPTION_DECISION_LABELS: Readonly<Record<OperatingExceptionDecision, string>> = {
  APPROVE: '审批通过',
  REJECT: '驳回申请',
  CANCEL: '撤回例外',
};

export interface ScheduleSlotDraft {
  readonly dayOfWeek: number;
  readonly startMinute: number;
  readonly endMinute: number;
  readonly crossesMidnight: boolean;
  readonly targetSupplyTempC?: number;
  readonly spanGroupId?: string;
}

export interface EditableScheduleSpan {
  readonly key: string;
  readonly dayOfWeek: number;
  readonly startMinute: number;
  readonly endMinute: number;
  readonly crossesMidnight: boolean;
  readonly targetSupplyTempC?: number;
  readonly spanGroupId?: string;
}

export interface EffectiveCoolingSlot {
  readonly id: string;
  readonly source: 'WEEKLY' | 'EXCEPTION';
  readonly dayOfWeek: number;
  readonly date: string;
  readonly startMinute: number;
  readonly endMinute: number;
  readonly startMs: number;
  readonly endMs: number;
  readonly targetSupplyTempC: number | null;
  readonly spanGroupId: string | null;
  readonly exceptionId: string | null;
  readonly exceptionTitle: string | null;
}

export interface EffectiveScheduleDay {
  readonly dayOfWeek: number;
  readonly label: string;
  readonly date: string;
  readonly startMs: number;
  readonly endMs: number;
  readonly approvedException: OperatingException | null;
  readonly effectiveSlots: readonly EffectiveCoolingSlot[];
}

export interface EffectiveScheduleWeek {
  readonly mondayDate: string;
  readonly sundayDate: string;
  readonly startMs: number;
  readonly endMs: number;
  readonly timezone: string;
  readonly preCoolLeadMinutes: number;
  readonly coastDownLagMinutes: number;
  readonly days: readonly EffectiveScheduleDay[];
}

export interface SchedulePlantMonitorTarget {
  readonly deviceId: string;
  readonly deviceName: string;
  readonly assetId: string | null;
  readonly assetName: string;
  readonly role: 'CHILLER' | 'CHILLED_WATER_PUMP';
  readonly keys: readonly string[];
  readonly powerKey: string | null;
  readonly runStateKey: string | null;
  readonly ratedPowerKw: number | null;
}

export interface ScheduleDeviation {
  readonly id: string;
  readonly kind: 'UNSCHEDULED_RUNNING';
  readonly date: string;
  readonly dayOfWeek: number;
  readonly startMs: number;
  readonly endMs: number;
  readonly startMinute: number;
  readonly endMinute: number;
  readonly durationMinutes: number;
  readonly runningDeviceIds: readonly string[];
  readonly runningDeviceNames: readonly string[];
  readonly averagePowerKw: number | null;
  readonly maxPowerKw: number | null;
}

function pad2(value: number): string {
  return String(value).padStart(2, '0');
}

export function formatMinuteOfDay(minute: number): string {
  const clamped = Math.max(0, Math.min(1440, Math.round(minute)));
  if (clamped === 1440) return '24:00';
  const h = Math.floor(clamped / 60);
  const m = clamped % 60;
  return `${pad2(h)}:${pad2(m)}`;
}

export function parseTimeInputToMinute(value: string, allow24 = false): number | null {
  const trimmed = value.trim();
  if (allow24 && trimmed === '24:00') return 1440;
  const match = /^(\d{1,2}):(\d{2})$/.exec(trimmed);
  if (!match) return null;
  const h = Number(match[1]);
  const m = Number(match[2]);
  if (!Number.isInteger(h) || !Number.isInteger(m) || m < 0 || m > 59) return null;
  if (h === 24 && m === 0 && allow24) return 1440;
  if (h < 0 || h > 23) return null;
  return h * 60 + m;
}

/** Generates an RFC 9562 UUIDv7 using Web Crypto for cross-midnight spanGroupId grouping. */
export function createClientUuidV7(nowMs = Date.now()): string {
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  const ts = BigInt(Math.max(0, Math.trunc(nowMs)));
  bytes[0] = Number((ts >> 40n) & 0xffn);
  bytes[1] = Number((ts >> 32n) & 0xffn);
  bytes[2] = Number((ts >> 24n) & 0xffn);
  bytes[3] = Number((ts >> 16n) & 0xffn);
  bytes[4] = Number((ts >> 8n) & 0xffn);
  bytes[5] = Number(ts & 0xffn);
  bytes[6] = (bytes[6] & 0x0f) | 0x70;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  const hex = Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('');
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20, 32)}`;
}

export function nextDayOfWeek(dayOfWeek: number): number {
  return (dayOfWeek % 7) + 1;
}

export function splitScheduleSlotDraft(draft: ScheduleSlotDraft): OperatingScheduleSlotInput[] {
  const tempField =
    draft.targetSupplyTempC !== undefined && Number.isFinite(draft.targetSupplyTempC)
      ? { targetSupplyTempC: Number(draft.targetSupplyTempC.toFixed(1)) }
      : {};

  if (!draft.crossesMidnight) {
    return [
      {
        dayOfWeek: draft.dayOfWeek,
        startMinute: draft.startMinute,
        endMinute: draft.endMinute,
        ...tempField,
      },
    ];
  }

  const spanGroupId = draft.spanGroupId ?? createClientUuidV7();
  const firstPart: OperatingScheduleSlotInput = {
    dayOfWeek: draft.dayOfWeek,
    startMinute: draft.startMinute,
    endMinute: 1440,
    spanGroupId,
    ...tempField,
  };
  if (draft.endMinute <= 0) {
    return [firstPart];
  }
  const secondPart: OperatingScheduleSlotInput = {
    dayOfWeek: nextDayOfWeek(draft.dayOfWeek),
    startMinute: 0,
    endMinute: draft.endMinute,
    spanGroupId,
    ...tempField,
  };
  return [firstPart, secondPart];
}

export function mergeWeeklyScheduleSpans(
  slots: readonly (OperatingScheduleSlot | OperatingScheduleSlotInput)[],
): EditableScheduleSpan[] {
  const bySpan = new Map<string, (OperatingScheduleSlot | OperatingScheduleSlotInput)[]>();
  const standalone: (OperatingScheduleSlot | OperatingScheduleSlotInput)[] = [];

  for (const slot of slots) {
    if (slot.spanGroupId) {
      const group = bySpan.get(slot.spanGroupId) ?? [];
      group.push(slot);
      bySpan.set(slot.spanGroupId, group);
    } else {
      standalone.push(slot);
    }
  }

  const result: EditableScheduleSpan[] = [];
  for (const slot of standalone) {
    result.push({
      key: `${slot.dayOfWeek}-${slot.startMinute}-${slot.endMinute}`,
      dayOfWeek: slot.dayOfWeek,
      startMinute: slot.startMinute,
      endMinute: slot.endMinute,
      crossesMidnight: false,
      ...(slot.targetSupplyTempC !== undefined ? { targetSupplyTempC: slot.targetSupplyTempC } : {}),
    });
  }

  for (const [spanGroupId, group] of bySpan.entries()) {
    if (group.length === 2) {
      const tail = group.find((s) => s.endMinute === 1440);
      const head = group.find((s) => s.startMinute === 0);
      if (tail && head && nextDayOfWeek(tail.dayOfWeek) === head.dayOfWeek) {
        result.push({
          key: spanGroupId,
          dayOfWeek: tail.dayOfWeek,
          startMinute: tail.startMinute,
          endMinute: head.endMinute,
          crossesMidnight: true,
          spanGroupId,
          ...(tail.targetSupplyTempC !== undefined ? { targetSupplyTempC: tail.targetSupplyTempC } : {}),
        });
        continue;
      }
    }
    for (const slot of group) {
      result.push({
        key: `${spanGroupId}:${slot.dayOfWeek}:${slot.startMinute}`,
        dayOfWeek: slot.dayOfWeek,
        startMinute: slot.startMinute,
        endMinute: slot.endMinute,
        crossesMidnight: false,
        spanGroupId,
        ...(slot.targetSupplyTempC !== undefined ? { targetSupplyTempC: slot.targetSupplyTempC } : {}),
      });
    }
  }

  return result.sort((a, b) => (a.dayOfWeek !== b.dayOfWeek ? a.dayOfWeek - b.dayOfWeek : a.startMinute - b.startMinute));
}

function parseIsoCalendarDate(isoDate: string): { year: number; month: number; day: number } {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(isoDate.trim());
  if (!match) throw new Error(`Invalid ISO date: ${isoDate}`);
  return { year: Number(match[1]), month: Number(match[2]), day: Number(match[3]) };
}

function formatIsoCalendarDate(year: number, month: number, day: number): string {
  const utc = new Date(Date.UTC(year, month - 1, day));
  return `${utc.getUTCFullYear()}-${pad2(utc.getUTCMonth() + 1)}-${pad2(utc.getUTCDate())}`;
}

export function addCalendarDays(isoDate: string, deltaDays: number): string {
  const { year, month, day } = parseIsoCalendarDate(isoDate);
  return formatIsoCalendarDate(year, month, day + deltaDays);
}

export function siteLocalDateString(instant: Date, timezone: string): string {
  const local = wallTime(instant, timezone);
  return formatIsoCalendarDate(local.year, local.month, local.day);
}

export function mondayOfWeek(isoDate: string): string {
  const { year, month, day } = parseIsoCalendarDate(isoDate);
  const utcDay = new Date(Date.UTC(year, month - 1, day)).getUTCDay(); // 0=Sun..6=Sat
  const isoDow = utcDay === 0 ? 7 : utcDay; // 1=Mon..7=Sun
  return formatIsoCalendarDate(year, month, day - (isoDow - 1));
}

function slotStartEndMs(
  year: number,
  month: number,
  day: number,
  startMinute: number,
  endMinute: number,
  timezone: string,
): { startMs: number; endMs: number } {
  const dayStart = siteMidnight(year, month, day, timezone).getTime();
  const nextDayStart = siteMidnight(year, month, day + 1, timezone).getTime();
  const startMs = dayStart + startMinute * 60_000;
  const endMs = endMinute >= 1440 ? nextDayStart : Math.min(nextDayStart, dayStart + endMinute * 60_000);
  return { startMs, endMs };
}

export function resolveEffectiveWeek(input: {
  readonly schedule: OperatingSchedule;
  readonly exceptions: readonly OperatingException[];
  readonly anchorDate: string;
  readonly timezone?: string;
}): EffectiveScheduleWeek {
  const timezone = input.timezone ?? input.schedule.timezone;
  const mondayDate = mondayOfWeek(input.anchorDate);
  const sundayDate = addCalendarDays(mondayDate, 6);
  const approvedExceptions = input.exceptions.filter((item) => item.status === 'APPROVED');

  const days: EffectiveScheduleDay[] = [];
  for (let offset = 0; offset < 7; offset += 1) {
    const dayOfWeek = offset + 1;
    const date = addCalendarDays(mondayDate, offset);
    const { year, month, day } = parseIsoCalendarDate(date);
    const startMs = siteMidnight(year, month, day, timezone).getTime();
    const endMs = siteMidnight(year, month, day + 1, timezone).getTime();

    const approvedException =
      approvedExceptions.find((item) => item.startDate <= date && date <= item.endDate) ?? null;

    let effectiveSlots: EffectiveCoolingSlot[];
    if (approvedException) {
      // Full-day 00:00-24:00 override: replaces ALL weekly slots on this calendar date (including cross-midnight spillovers).
      const replacementSlots: readonly OperatingExceptionSlot[] =
        approvedException.kind === 'OVERTIME_COOLING' ? approvedException.replacementSlots : [];
      effectiveSlots = [...replacementSlots]
        .sort((a, b) => a.startMinute - b.startMinute)
        .map((slot, idx) => {
          const bounds = slotStartEndMs(year, month, day, slot.startMinute, slot.endMinute, timezone);
          return {
            id: `exception:${approvedException.id}:${date}:${idx}`,
            source: 'EXCEPTION' as const,
            dayOfWeek,
            date,
            startMinute: slot.startMinute,
            endMinute: slot.endMinute,
            startMs: bounds.startMs,
            endMs: bounds.endMs,
            targetSupplyTempC: slot.targetSupplyTempC ?? null,
            spanGroupId: null,
            exceptionId: approvedException.id,
            exceptionTitle: approvedException.title,
          };
        });
    } else {
      effectiveSlots = input.schedule.slots
        .filter((slot) => slot.dayOfWeek === dayOfWeek)
        .sort((a, b) => a.startMinute - b.startMinute)
        .map((slot, idx) => {
          const bounds = slotStartEndMs(year, month, day, slot.startMinute, slot.endMinute, timezone);
          return {
            id: slot.id ? `weekly:${slot.id}:${date}` : `weekly:${dayOfWeek}:${slot.startMinute}:${idx}`,
            source: 'WEEKLY' as const,
            dayOfWeek,
            date,
            startMinute: slot.startMinute,
            endMinute: slot.endMinute,
            startMs: bounds.startMs,
            endMs: bounds.endMs,
            targetSupplyTempC: slot.targetSupplyTempC ?? null,
            spanGroupId: slot.spanGroupId ?? null,
            exceptionId: null,
            exceptionTitle: null,
          };
        });
    }

    days.push({
      dayOfWeek,
      label: DAY_OF_WEEK_LABELS[dayOfWeek] ?? `周${dayOfWeek}`,
      date,
      startMs,
      endMs,
      approvedException,
      effectiveSlots,
    });
  }

  return {
    mondayDate,
    sundayDate,
    startMs: days[0].startMs,
    endMs: days[6].endMs,
    timezone,
    preCoolLeadMinutes: input.schedule.preCoolLeadMinutes,
    coastDownLagMinutes: input.schedule.coastDownLagMinutes,
    days,
  };
}

function readNumericMetadata(meta: Record<string, unknown> | undefined, keys: readonly string[]): number | null {
  if (!meta) return null;
  for (const key of keys) {
    const candidate = meta[key];
    if (typeof candidate === 'number' && Number.isFinite(candidate) && candidate > 0) {
      return candidate;
    }
  }
  return null;
}

export function extractSchedulePlantMonitorTargets(assetModel: SiteAssetModel): SchedulePlantMonitorTarget[] {
  const assetById = new Map(assetModel.assets.filter((a) => a.status === 'ACTIVE').map((a) => [a.id, a]));
  const assetByDeviceId = new Map<string, (typeof assetModel.assets)[number]>();

  for (const rel of assetModel.relationships) {
    if (rel.status !== 'ACTIVE' || rel.validTo !== null) continue;
    if (rel.fromType === 'DEVICE' && rel.toType === 'ASSET') {
      const asset = assetById.get(rel.toId);
      if (asset) assetByDeviceId.set(rel.fromId, asset);
    } else if (rel.fromType === 'ASSET' && rel.toType === 'DEVICE') {
      const asset = assetById.get(rel.fromId);
      if (asset) assetByDeviceId.set(rel.toId, asset);
    }
  }

  const pointsByDevice = new Map<string, (typeof assetModel.telemetryPoints)[number][]>();
  for (const point of assetModel.telemetryPoints) {
    if (point.status !== 'ACTIVE') continue;
    const list = pointsByDevice.get(point.reportingDeviceId) ?? [];
    list.push(point);
    pointsByDevice.set(point.reportingDeviceId, list);
  }

  const targets: SchedulePlantMonitorTarget[] = [];
  for (const device of assetModel.devices) {
    if (device.status !== 'ACTIVE') continue;
    const asset = assetByDeviceId.get(device.id);
    const assetType = asset?.assetType ?? '';
    const isChiller = assetType === 'CHILLER' || device.deviceType === 'CHILLER_CONTROLLER';
    const isChilledWaterPump =
      assetType === 'CHILLED_WATER_PUMP' ||
      (device.deviceType === 'PUMP_CONTROLLER' && device.code.toLowerCase().startsWith('chwp'));

    if (!isChiller && !isChilledWaterPump) continue;

    const points = pointsByDevice.get(device.id) ?? [];
    const powerPoint =
      points.find((p) => p.pointCode === 'power' || p.pointCode === 'active_power') ?? null;
    const runStatePoint = points.find((p) => p.pointCode === 'run_state') ?? null;

    const keys: string[] = [];
    if (powerPoint) keys.push(powerPoint.pointCode);
    if (runStatePoint && !keys.includes(runStatePoint.pointCode)) keys.push(runStatePoint.pointCode);
    if (keys.length === 0) continue;

    const ratedPowerKw = isChiller
      ? readNumericMetadata(powerPoint?.sourceMetadata, ['ratedPowerKw', 'ratedPower', 'nominalPowerKw'])
      : null;

    targets.push({
      deviceId: device.id,
      deviceName: device.displayName,
      assetId: asset?.id ?? null,
      assetName: asset?.displayName ?? device.displayName.replace(/\s*通信端点$/, ''),
      role: isChiller ? 'CHILLER' : 'CHILLED_WATER_PUMP',
      keys,
      powerKey: powerPoint?.pointCode ?? null,
      runStateKey: runStatePoint?.pointCode ?? null,
      ratedPowerKw,
    });
  }

  return targets;
}

function isStateBucketRunning(bucket: DeviceHistoryAggregateBucket | undefined): boolean {
  if (!bucket?.state) return false;
  if (Array.isArray(bucket.state.stateCounts) && bucket.state.stateCounts.length > 0) {
    const runningCount = bucket.state.stateCounts
      .filter((entry) => entry.value === 'RUNNING' || entry.value === true || entry.value === 1)
      .reduce((sum, entry) => sum + entry.count, 0);
    const totalCount = bucket.state.stateCounts.reduce((sum, entry) => sum + entry.count, 0);
    if (totalCount > 0) {
      return runningCount / totalCount >= 0.5;
    }
  }
  return bucket.state.lastValue === 'RUNNING' || bucket.state.lastValue === true;
}

const QUARTER_HOUR_MS = 15 * 60_000;
const FALLBACK_CHILLER_RUNNING_MIN_KW = 10;

export function detectUnscheduledRunningDeviations(input: {
  readonly siteId: string;
  readonly week: EffectiveScheduleWeek;
  readonly targets: readonly SchedulePlantMonitorTarget[];
  readonly bucketsByDevice: ReadonlyMap<string, readonly DeviceHistoryAggregateBucket[]>;
  readonly nowMs: number;
}): ScheduleDeviation[] {
  if (input.targets.length === 0) return [];

  // Build permitted intervals across the week: [startMs - preCoolLead, endMs + coastDownLag].
  const preCoolMs = input.week.preCoolLeadMinutes * 60_000;
  const coastDownMs = input.week.coastDownLagMinutes * 60_000;
  const permittedWindows = input.week.days.flatMap((day) =>
    day.effectiveSlots.map((slot) => ({
      startMs: slot.startMs - preCoolMs,
      endMs: slot.endMs + coastDownMs,
    })),
  );

  const isBucketPermitted = (bucketStartMs: number, bucketEndMs: number): boolean =>
    permittedWindows.some((win) => bucketStartMs < win.endMs && bucketEndMs > win.startMs);

  // Index aggregate buckets by `${deviceId}:${telemetryKey}:${bucketStartMs}`
  const indexed = new Map<string, DeviceHistoryAggregateBucket>();
  const candidateStarts = new Set<number>();

  for (const target of input.targets) {
    const buckets = input.bucketsByDevice.get(target.deviceId) ?? [];
    for (const bucket of buckets) {
      const startMs = Date.parse(bucket.periodStart);
      const endMs = Date.parse(bucket.periodEnd);
      if (!Number.isFinite(startMs) || !Number.isFinite(endMs)) continue;
      if (startMs < input.week.startMs || endMs > input.week.endMs || endMs > input.nowMs) continue;
      indexed.set(`${target.deviceId}:${bucket.telemetryKey}:${startMs}`, bucket);
      candidateStarts.add(startMs);
    }
  }

  interface FlaggedBucket {
    readonly startMs: number;
    readonly endMs: number;
    readonly day: EffectiveScheduleDay;
    readonly runningDeviceIds: readonly string[];
    readonly runningDeviceNames: readonly string[];
    readonly totalChillerPowerKw: number | null;
  }

  const flagged: FlaggedBucket[] = [];
  const sortedStarts = [...candidateStarts].sort((a, b) => a - b);

  for (const startMs of sortedStarts) {
    const endMs = startMs + QUARTER_HOUR_MS;
    if (endMs > input.nowMs) continue;
    if (isBucketPermitted(startMs, endMs)) continue;

    const day = input.week.days.find((d) => startMs >= d.startMs && startMs < d.endMs);
    if (!day) continue;

    const runningIds: string[] = [];
    const runningNames: string[] = [];
    let chillerPowerSum = 0;
    let hasChillerPower = false;

    for (const target of input.targets) {
      const powerBucket = target.powerKey
        ? indexed.get(`${target.deviceId}:${target.powerKey}:${startMs}`)
        : undefined;
      const runStateBucket = target.runStateKey
        ? indexed.get(`${target.deviceId}:${target.runStateKey}:${startMs}`)
        : undefined;

      let running = false;
      const avgPower = powerBucket?.gauge?.average;
      if (target.role === 'CHILLER') {
        const minKw =
          target.ratedPowerKw !== null && target.ratedPowerKw > 0
            ? target.ratedPowerKw * 0.1
            : FALLBACK_CHILLER_RUNNING_MIN_KW;
        if (typeof avgPower === 'number' && Number.isFinite(avgPower) && avgPower >= minKw) {
          running = true;
          chillerPowerSum += avgPower;
          hasChillerPower = true;
        } else if (isStateBucketRunning(runStateBucket)) {
          running = true;
          if (typeof avgPower === 'number' && Number.isFinite(avgPower)) {
            chillerPowerSum += avgPower;
            hasChillerPower = true;
          }
        }
      } else if (isStateBucketRunning(runStateBucket)) {
        running = true;
      }

      if (running) {
        runningIds.push(target.deviceId);
        runningNames.push(target.assetName);
      }
    }

    if (runningIds.length > 0) {
      flagged.push({
        startMs,
        endMs,
        day,
        runningDeviceIds: runningIds,
        runningDeviceNames: runningNames,
        totalChillerPowerKw: hasChillerPower ? chillerPowerSum : null,
      });
    }
  }

  // Merge contiguous 15m flagged buckets within the same calendar day into a single deviation episode.
  const deviations: ScheduleDeviation[] = [];
  let index = 0;
  while (index < flagged.length) {
    const first = flagged[index];
    let last = first;
    const deviceIds = new Set<string>(first.runningDeviceIds);
    const deviceNames = new Set<string>(first.runningDeviceNames);
    const powers: number[] =
      first.totalChillerPowerKw !== null ? [first.totalChillerPowerKw] : [];

    while (
      index + 1 < flagged.length &&
      flagged[index + 1].day.date === first.day.date &&
      flagged[index + 1].startMs === last.endMs
    ) {
      index += 1;
      last = flagged[index];
      for (const id of last.runningDeviceIds) deviceIds.add(id);
      for (const name of last.runningDeviceNames) deviceNames.add(name);
      if (last.totalChillerPowerKw !== null) powers.push(last.totalChillerPowerKw);
    }

    const startMinute = Math.max(0, Math.round((first.startMs - first.day.startMs) / 60_000));
    const endMinute = Math.min(1440, Math.round((last.endMs - first.day.startMs) / 60_000));
    const avgPower =
      powers.length > 0
        ? Number((powers.reduce((sum, p) => sum + p, 0) / powers.length).toFixed(1))
        : null;
    const maxPower = powers.length > 0 ? Number(Math.max(...powers).toFixed(1)) : null;

    deviations.push({
      id: `deviation:${new Date(first.startMs).toISOString()}`,
      kind: 'UNSCHEDULED_RUNNING',
      date: first.day.date,
      dayOfWeek: first.day.dayOfWeek,
      startMs: first.startMs,
      endMs: last.endMs,
      startMinute,
      endMinute,
      durationMinutes: Math.max(15, endMinute - startMinute),
      runningDeviceIds: [...deviceIds],
      runningDeviceNames: [...deviceNames],
      averagePowerKw: avgPower,
      maxPowerKw: maxPower,
    });
    index += 1;
  }

  return deviations;
}

export function buildDeviationTrendsSearch(
  deviation: ScheduleDeviation,
  targets: readonly SchedulePlantMonitorTarget[],
): TrendsSearch {
  const relevant = targets.filter((t) => deviation.runningDeviceIds.includes(t.deviceId));
  const selectedTargets = relevant.length > 0 ? relevant : targets;
  const refs: string[] = [];
  for (const target of selectedTargets) {
    for (const key of target.keys) {
      const ref = `${target.deviceId}:${key}`;
      if (!refs.includes(ref) && refs.length < 8) {
        refs.push(ref);
      }
    }
  }
  const padMs = 30 * 60_000;
  return {
    window: 'custom',
    from: new Date(deviation.startMs - padMs).toISOString(),
    to: new Date(deviation.endMs + padMs).toISOString(),
    granularity: 'QUARTER_HOUR',
    ...(refs.length > 0 ? { points: refs.join(',') } : {}),
    overlays: 'alarms,commands',
  };
}

export function deviationWorkOrderResourceId(siteId: string, deviation: ScheduleDeviation): string {
  return `operating-schedule-deviation:${siteId}:${new Date(deviation.startMs).toISOString()}`;
}

export function buildDeviationWorkOrderDraft(
  siteId: string,
  deviation: ScheduleDeviation,
): {
  readonly initial: Pick<CreateWorkOrderInput, 'title' | 'description' | 'priority'>;
  readonly sourceReferences: WorkOrderSourceReference[];
} {
  const dayLabel = DAY_OF_WEEK_LABELS[deviation.dayOfWeek] ?? '';
  const windowLabel = `${deviation.date}（${dayLabel}）${formatMinuteOfDay(deviation.startMinute)}–${formatMinuteOfDay(deviation.endMinute)}`;
  const devicesText = deviation.runningDeviceNames.join('、') || '冷站主机/冷冻水泵';
  const powerSentence =
    deviation.averagePowerKw !== null
      ? `期间冷水机 15 分钟平均有功功率约 ${deviation.averagePowerKw} kW（峰值 ${deviation.maxPowerKw ?? deviation.averagePowerKw} kW）。`
      : '期间运行状态遥测显示设备持续运行。';

  return {
    initial: {
      title: `计划外开机核查 · ${deviation.date} ${formatMinuteOfDay(deviation.startMinute)}–${formatMinuteOfDay(deviation.endMinute)}`,
      description: [
        `核查时段：${windowLabel}（共 ${deviation.durationMinutes} 分钟，已排除预冷提前窗与停机余冷滞后窗）。`,
        `运行设备：${devicesText}。`,
        powerSentence,
        '请现场核实是否为未登记的加班供冷、自控未按时停机或现场手动启机；若属临时加班供冷，可在「运行计划 → 例外日程」补登运行例外。',
      ].join('\n'),
      priority: 'MEDIUM',
    },
    sourceReferences: [
      {
        domain: 'EXTERNAL',
        resourceId: deviationWorkOrderResourceId(siteId, deviation),
        relationship: 'ORIGIN',
      },
    ],
  };
}

export function findActiveDeviationWorkOrder<
  T extends Pick<WorkOrder, 'workOrderId' | 'status' | 'createdAt' | 'sourceReferences'>,
>(workOrders: readonly T[], siteId: string, deviation: ScheduleDeviation): T | null {
  const expectedRef = deviationWorkOrderResourceId(siteId, deviation);
  const matching = workOrders
    .filter(
      (wo) =>
        wo.status !== 'COMPLETED' &&
        wo.status !== 'CANCELLED' &&
        wo.sourceReferences.some(
          (ref) => ref.domain === 'EXTERNAL' && ref.resourceId === expectedRef,
        ),
    )
    .sort((a, b) => b.createdAt.localeCompare(a.createdAt));
  return matching[0] ?? null;
}
