import type {
  OperatingException,
  OperatingExceptionDecision,
  OperatingExceptionKind,
  OperatingExceptionSlot,
  OperatingExceptionStatus,
  OperatingSchedule,
  OperatingScheduleSlot,
  OperatingScheduleSlotInput,
} from '../../api/generated/platformGateway.gen.ts';
import { siteMidnight, wallTime } from '../../lib/site-time.ts';

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

export type ValidatedSlotRowResult =
  | {
      readonly ok: true;
      readonly startMinute: number;
      readonly endMinute: number;
      readonly targetSupplyTempC: number | undefined;
    }
  | {
      readonly ok: false;
      readonly message: string;
    };

export function validateCoolingSlotRowInput(input: {
  readonly rowLabel: string;
  readonly startTime: string;
  readonly endTime: string;
  readonly crossesMidnight: boolean;
  readonly allowCrossMidnight: boolean;
  readonly targetSupplyTempInput: string;
}): ValidatedSlotRowResult {
  const startMinute = parseTimeInputToMinute(input.startTime, false);
  const endMinute = parseTimeInputToMinute(input.endTime, !input.crossesMidnight);
  if (startMinute === null || endMinute === null) {
    return {
      ok: false,
      message: `${input.rowLabel} 的起止时间格式无效，请使用 HH:MM（结束时间可填 24:00）`,
    };
  }
  if (!input.crossesMidnight && startMinute >= endMinute) {
    return {
      ok: false,
      message: input.allowCrossMidnight
        ? `${input.rowLabel} 的结束时间须晚于开始时间；若需跨午夜运行至次日凌晨，请勾选「跨日」`
        : `${input.rowLabel} 的结束时间须晚于开始时间（可填 24:00）`,
    };
  }
  if (input.crossesMidnight && (endMinute <= 0 || endMinute >= 1440)) {
    return {
      ok: false,
      message: `${input.rowLabel} 勾选跨日后，次日结束时间须在 00:01 至 23:59 之间`,
    };
  }

  let targetSupplyTempC: number | undefined;
  const trimmedTemp = input.targetSupplyTempInput.trim();
  if (trimmedTemp !== '') {
    const parsedTemp = Number(trimmedTemp);
    if (!Number.isFinite(parsedTemp) || parsedTemp < 4 || parsedTemp > 20) {
      return {
        ok: false,
        message: `${input.rowLabel} 的交接参考供水温度须在 4.0–20.0 °C 之间`,
      };
    }
    targetSupplyTempC = Number(parsedTemp.toFixed(1));
  }

  return {
    ok: true,
    startMinute,
    endMinute,
    targetSupplyTempC,
  };
}

export function findOverlappingMinuteSlot<T extends { readonly startMinute: number; readonly endMinute: number }>(
  slots: readonly T[],
): readonly [T, T] | null {
  const sorted = [...slots].sort((a, b) => a.startMinute - b.startMinute);
  for (let i = 1; i < sorted.length; i += 1) {
    if (sorted[i].startMinute < sorted[i - 1].endMinute) {
      return [sorted[i - 1], sorted[i]];
    }
  }
  return null;
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
