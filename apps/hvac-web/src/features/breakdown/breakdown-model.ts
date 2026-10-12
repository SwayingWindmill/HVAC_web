import type { DeviceHistoryAggregateBucket } from '../../api/generated/s2Telemetry.gen';

// The plant's electricity split by machine. Machines report power, not energy, so each one's
// energy is its average power over each hour; the HVAC meter's energy is the measured whole,
// and what the machines do not account for is shown as unallocated rather than spread.

export const EQUIPMENT_ORDER = ['CHILLER', 'CHILLED_WATER_PUMP', 'COOLING_WATER_PUMP', 'COOLING_TOWER'] as const;
export type EquipmentCategory = (typeof EQUIPMENT_ORDER)[number];

export interface PowerBucket {
  readonly periodStart: string;
  readonly periodEnd: string;
  readonly averageKW: number;
  readonly peakKW: number;
}

export interface EquipmentSeries {
  readonly deviceId: string;
  readonly name: string;
  readonly category: EquipmentCategory;
  readonly buckets: readonly PowerBucket[];
}

export function powerBuckets(buckets: readonly Pick<DeviceHistoryAggregateBucket, 'periodStart' | 'periodEnd' | 'gauge'>[]): PowerBucket[] {
  return buckets.flatMap((bucket) => (bucket.gauge
    ? [{ periodStart: bucket.periodStart, periodEnd: bucket.periodEnd, averageKW: bucket.gauge.average, peakKW: bucket.gauge.maximum }]
    : []));
}

const HOUR_MS = 3_600_000;
const bucketEnergy = (bucket: PowerBucket) => bucket.averageKW * ((Date.parse(bucket.periodEnd) - Date.parse(bucket.periodStart)) / HOUR_MS);

export interface BreakdownItem {
  readonly deviceId: string;
  readonly name: string;
  readonly category: EquipmentCategory;
  readonly energyKWh: number;
  /** Of the metered whole, or of the machines' sum when they add up past it. */
  readonly share: number;
  readonly averageKW: number;
  readonly peakKW: number;
}

export type BreakdownRow = { readonly key: string } & Readonly<Record<EquipmentCategory, number>>;

export interface Breakdown {
  readonly meteredKWh: number | null;
  /** The meter's energy the machines do not account for; null without a meter reading. */
  readonly unallocatedKWh: number | null;
  /** Machines, most energy first. */
  readonly items: readonly BreakdownItem[];
  /** Categories with a machine, in plant order. */
  readonly categories: readonly { readonly category: EquipmentCategory; readonly energyKWh: number; readonly share: number }[];
  /** Energy per chart bucket and category, in time order. */
  readonly rows: readonly BreakdownRow[];
}

export function energyBreakdown(
  equipment: readonly EquipmentSeries[],
  metered: readonly { periodStart: string; energyKWh: number }[],
  bucketKey: (periodStart: string) => string = (periodStart) => periodStart,
): Breakdown {
  const meteredKWh = metered.length === 0 ? null : metered.reduce((sum, point) => sum + point.energyKWh, 0);
  const energies = equipment.map((machine) => machine.buckets.reduce((sum, bucket) => sum + bucketEnergy(bucket), 0));
  const allocated = energies.reduce((sum, energy) => sum + energy, 0);
  const whole = Math.max(meteredKWh ?? 0, allocated);
  const share = (energy: number) => (whole > 0 ? energy / whole : 0);

  const items = equipment.map((machine, index): BreakdownItem => {
    const hours = machine.buckets.reduce((sum, bucket) => sum + (Date.parse(bucket.periodEnd) - Date.parse(bucket.periodStart)) / HOUR_MS, 0);
    return {
      deviceId: machine.deviceId,
      name: machine.name,
      category: machine.category,
      energyKWh: energies[index],
      share: share(energies[index]),
      averageKW: hours > 0 ? energies[index] / hours : 0,
      peakKW: machine.buckets.reduce((peak, bucket) => Math.max(peak, bucket.peakKW), 0),
    };
  }).sort((left, right) => right.energyKWh - left.energyKWh);

  const categories = EQUIPMENT_ORDER
    .filter((category) => equipment.some((machine) => machine.category === category))
    .map((category) => {
      const energyKWh = items.filter((item) => item.category === category).reduce((sum, item) => sum + item.energyKWh, 0);
      return { category, energyKWh, share: share(energyKWh) };
    });

  const rows = new Map<string, Record<EquipmentCategory, number>>();
  for (const machine of equipment) {
    for (const bucket of machine.buckets) {
      const key = bucketKey(bucket.periodStart);
      const row = rows.get(key) ?? { CHILLER: 0, CHILLED_WATER_PUMP: 0, COOLING_WATER_PUMP: 0, COOLING_TOWER: 0 };
      row[machine.category] += bucketEnergy(bucket);
      rows.set(key, row);
    }
  }

  return {
    meteredKWh,
    unallocatedKWh: meteredKWh === null ? null : Math.max(0, meteredKWh - allocated),
    items,
    categories,
    rows: [...rows.entries()].sort(([left], [right]) => left.localeCompare(right)).map(([key, row]) => ({ key, ...row })),
  };
}
