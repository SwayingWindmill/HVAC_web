import type { SiteAssetModel } from '../../api/generated/platformGateway.gen.ts';
import type { DeviceHistoryAggregateBucket } from '../../api/generated/s2Telemetry.gen.ts';
import type { CreateWorkOrderInput, WorkOrder, WorkOrderSourceReference } from '../../api/work-orders.ts';
import type { TrendsSearch } from '../operations/trends/trends-search.ts';
import {
  DAY_OF_WEEK_LABELS,
  formatMinuteOfDay,
  type EffectiveScheduleDay,
  type EffectiveScheduleWeek,
} from './schedule-calendar-model.ts';

export {
  DAY_OF_WEEK_LABELS,
  EXCEPTION_DECISION_LABELS,
  EXCEPTION_KIND_LABELS,
  EXCEPTION_STATUS_LABELS,
  addCalendarDays,
  createClientUuidV7,
  findOverlappingMinuteSlot,
  formatMinuteOfDay,
  mergeWeeklyScheduleSpans,
  mondayOfWeek,
  nextDayOfWeek,
  parseTimeInputToMinute,
  resolveEffectiveWeek,
  siteLocalDateString,
  splitScheduleSlotDraft,
  validateCoolingSlotRowInput,
} from './schedule-calendar-model.ts';
export type {
  EditableScheduleSpan,
  EffectiveCoolingSlot,
  EffectiveScheduleDay,
  EffectiveScheduleWeek,
  ScheduleSlotDraft,
  ValidatedSlotRowResult,
} from './schedule-calendar-model.ts';

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
  readonly feederAuxiliaryPowerKw?: number | null;
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

function readPositiveNumberProperty(obj: object | undefined, key: string): number | null {
  if (!obj) return null;
  const descriptor = Object.getOwnPropertyDescriptor(obj, key);
  const value: unknown = descriptor?.value;
  return typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : null;
}

export function extractSchedulePlantMonitorTargets(assetModel: SiteAssetModel): SchedulePlantMonitorTarget[] {
  const activeAssets = assetModel.assets.filter((a) => a.status === 'ACTIVE');
  const assetById = new Map(activeAssets.map((a) => [a.id, a]));
  const assetByCode = new Map(activeAssets.map((a) => [a.code.toLowerCase(), a]));
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
    const asset = assetByDeviceId.get(device.id) ?? assetByCode.get(device.code.toLowerCase());
    const assetType = asset?.assetType ?? '';
    const isChiller = assetType === 'CHILLER' || device.deviceType === 'CHILLER_CONTROLLER';
    const isChilledWaterPump =
      assetType === 'CHILLED_WATER_PUMP' ||
      ((device.deviceType === 'PUMP_CONTROLLER' || device.deviceType === 'PROVISIONAL_READINGS') &&
        device.code.toLowerCase().startsWith('chwp'));

    if (!isChiller && !isChilledWaterPump) continue;

    const points = pointsByDevice.get(device.id) ?? [];
    const powerPoint =
      points.find(
        (p) =>
          p.pointCode === 'power_total' ||
          p.pointCode === 'power' ||
          p.pointCode === 'active_power',
      ) ?? null;
    const runStatePoint = points.find((p) => p.pointCode === 'run_state') ?? null;

    const keys: string[] = [];
    if (powerPoint) keys.push(powerPoint.pointCode);
    if (runStatePoint && !keys.includes(runStatePoint.pointCode)) keys.push(runStatePoint.pointCode);
    if (keys.length === 0) continue;

    const assetRatedPower =
      typeof asset?.ratedPower === 'number' && Number.isFinite(asset.ratedPower) && asset.ratedPower > 0
        ? asset.ratedPower
        : null;
    const ratedPowerKw = isChiller
      ? (assetRatedPower ??
        readNumericMetadata(powerPoint?.sourceMetadata, ['ratedPowerKw', 'ratedPower', 'nominalPowerKw']))
      : null;
    const feederAuxiliaryPowerKw = isChiller
      ? (readPositiveNumberProperty(asset, 'feederAuxiliaryPowerKw') ??
        readNumericMetadata(powerPoint?.sourceMetadata, ['feederAuxiliaryPowerKw']))
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
      ...(feederAuxiliaryPowerKw !== null ? { feederAuxiliaryPowerKw } : {}),
    });
  }

  return targets;
}

function isRunningStateValue(value: unknown): boolean {
  if (value === true || value === 1) return true;
  return typeof value === 'string' && value.toUpperCase() === 'RUNNING';
}

function isStateBucketRunning(bucket: DeviceHistoryAggregateBucket | undefined): boolean {
  if (!bucket?.state) return false;
  if (Array.isArray(bucket.state.stateCounts) && bucket.state.stateCounts.length > 0) {
    const runningCount = bucket.state.stateCounts
      .filter((entry) => isRunningStateValue(entry.value))
      .reduce((sum, entry) => sum + entry.count, 0);
    const totalCount = bucket.state.stateCounts.reduce((sum, entry) => sum + entry.count, 0);
    if (totalCount > 0) {
      return runningCount / totalCount >= 0.5;
    }
  }
  return isRunningStateValue(bucket.state.lastValue);
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
        const feederAuxKw =
          typeof target.feederAuxiliaryPowerKw === 'number' &&
          Number.isFinite(target.feederAuxiliaryPowerKw) &&
          target.feederAuxiliaryPowerKw > 0
            ? target.feederAuxiliaryPowerKw
            : 0;
        const minKw =
          feederAuxKw +
          (target.ratedPowerKw !== null && target.ratedPowerKw > 0
            ? target.ratedPowerKw * 0.1
            : FALLBACK_CHILLER_RUNNING_MIN_KW);
        if (runStateBucket?.state != null) {
          if (isStateBucketRunning(runStateBucket)) {
            running = true;
            if (typeof avgPower === 'number' && Number.isFinite(avgPower)) {
              chillerPowerSum += avgPower;
              hasChillerPower = true;
            }
          }
        } else if (typeof avgPower === 'number' && Number.isFinite(avgPower) && avgPower >= minKw) {
          running = true;
          chillerPowerSum += avgPower;
          hasChillerPower = true;
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
        '请现场核实是否为未登记的加班供冷、自控未按时停机或现场手动启机；若属临时加班供冷，可在「运行计划 → 运行例外」补登运行例外。',
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
