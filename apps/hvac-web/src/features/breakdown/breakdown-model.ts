// The plant's electricity split by machine. Each machine with a sub-meter reports the energy it
// drew; the HVAC total meter is the measured whole, and the difference between the two is shown
// as unallocated, negative when the sub-meters add up past the total. A machine without a
// sub-meter is listed as unmetered rather than estimated.

export const EQUIPMENT_ORDER = ['CHILLER', 'CHILLED_WATER_PUMP', 'COOLING_WATER_PUMP', 'COOLING_TOWER'] as const;
export type EquipmentCategory = (typeof EQUIPMENT_ORDER)[number];

export interface Machine {
  readonly deviceId: string;
  readonly name: string;
  readonly category: EquipmentCategory;
}

export interface EnergyPoint {
  readonly periodStart: string;
  readonly energyKWh: number;
}

export interface DeviceEnergy {
  readonly deviceId: string;
  readonly points: readonly EnergyPoint[];
}

export interface BreakdownItem extends Machine {
  readonly energyKWh: number;
  /** Of the total meter, or of the sub-meters' sum without a total reading. */
  readonly share: number;
}

export type BreakdownRow = { readonly key: string } & Readonly<Record<EquipmentCategory, number>>;

export interface Breakdown {
  /** The HVAC total meter's energy; null without a reading. */
  readonly totalKWh: number | null;
  readonly subMeteredKWh: number;
  /** Total minus the sub-meters; negative when they add up past it, null without a total. */
  readonly unallocatedKWh: number | null;
  /** Sub-metered machines, most energy first. */
  readonly items: readonly BreakdownItem[];
  /** Machines without a sub-meter, in plant order. */
  readonly unmetered: readonly Machine[];
  /** Categories with a machine, in plant order. */
  readonly categories: readonly {
    readonly category: EquipmentCategory;
    readonly energyKWh: number;
    readonly share: number;
    readonly metered: number;
    readonly unmetered: number;
  }[];
  /** Sub-metered energy per chart bucket and category, in time order. */
  readonly rows: readonly BreakdownRow[];
}

const sum = (points: readonly EnergyPoint[]) => points.reduce((total, point) => total + point.energyKWh, 0);

export function energyBreakdown(
  machines: readonly Machine[],
  total: readonly EnergyPoint[],
  devices: readonly DeviceEnergy[],
  bucketKey: (periodStart: string) => string = (periodStart) => periodStart,
): Breakdown {
  const pointsByDevice = new Map(devices.map((device) => [device.deviceId, device.points]));
  const metered = machines.filter((machine) => pointsByDevice.has(machine.deviceId));
  const totalKWh = total.length === 0 ? null : sum(total);
  const energies = new Map(metered.map((machine) => [machine.deviceId, sum(pointsByDevice.get(machine.deviceId)!)]));
  const subMeteredKWh = [...energies.values()].reduce((left, right) => left + right, 0);
  const whole = totalKWh ?? subMeteredKWh;
  const share = (energy: number) => (whole > 0 ? energy / whole : 0);

  const items = metered
    .map((machine): BreakdownItem => ({ ...machine, energyKWh: energies.get(machine.deviceId)!, share: share(energies.get(machine.deviceId)!) }))
    .sort((left, right) => right.energyKWh - left.energyKWh);

  const categories = EQUIPMENT_ORDER
    .filter((category) => machines.some((machine) => machine.category === category))
    .map((category) => {
      const energyKWh = items.filter((item) => item.category === category).reduce((total, item) => total + item.energyKWh, 0);
      return {
        category,
        energyKWh,
        share: share(energyKWh),
        metered: metered.filter((machine) => machine.category === category).length,
        unmetered: machines.filter((machine) => machine.category === category && !pointsByDevice.has(machine.deviceId)).length,
      };
    });

  const rows = new Map<string, Record<EquipmentCategory, number>>();
  for (const machine of metered) {
    for (const point of pointsByDevice.get(machine.deviceId)!) {
      const key = bucketKey(point.periodStart);
      const row = rows.get(key) ?? { CHILLER: 0, CHILLED_WATER_PUMP: 0, COOLING_WATER_PUMP: 0, COOLING_TOWER: 0 };
      row[machine.category] += point.energyKWh;
      rows.set(key, row);
    }
  }

  return {
    totalKWh,
    subMeteredKWh,
    unallocatedKWh: totalKWh === null ? null : totalKWh - subMeteredKWh,
    items,
    unmetered: machines.filter((machine) => !pointsByDevice.has(machine.deviceId)),
    categories,
    rows: [...rows.entries()].sort(([left], [right]) => left.localeCompare(right)).map(([key, row]) => ({ key, ...row })),
  };
}
