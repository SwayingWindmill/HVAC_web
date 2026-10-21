// What the 运行 tab shows of the plant: whether it runs, its power, cooling and efficiency, the water
// loops and the air, as the web's 实时运行 page reads them (apps/hvac-web/src/features/operations/realtime).

import { grouped } from './format';
import { siteClock, type DayPoint } from './today';
import { CATEGORY_LABELS, EQUIPMENT_CATEGORIES, isRunning, plantReading, type Plant, type PlantCategory, type PlantDevice, type PlantReading } from './plant';

export type StatusTone = 'running' | 'stopped' | 'fault' | 'offline' | 'neutral';

export interface Metric {
  readonly label: string;
  readonly value: string;
  readonly unit: string;
  /** Not current (late or of doubtful quality): drawn dimmed, never as current. */
  readonly late: boolean;
}

/** A reading with its unit, as an operator reads it aloud: 7 °C, 72%, 132 m³/h. */
export function withUnit(reading: PlantReading): string {
  if (reading.unit === null) return reading.display;
  return reading.unit === '%' ? `${reading.display}%` : `${reading.display} ${reading.unit}`;
}

function equipment(plant: Plant) {
  const devices = plant.devices.filter((device) => EQUIPMENT_CATEGORIES.has(device.category));
  return {
    total: devices.length,
    running: devices.filter(isRunning).length,
    faults: devices.filter((device) => device.connection !== 'OFFLINE' && device.runState === 'FAULT').length,
  };
}

function deviceSummary(plant: Plant): string {
  const { total, running } = equipment(plant);
  return `${running} / ${total} 台运行 · ${plant.onlineCount} / ${plant.devices.length} 在线`;
}

function plantStatus(plant: Plant) {
  const { total, running, faults } = equipment(plant);
  if (total === 0) return null;
  const detail = `主要设备 ${running} / ${total} 台运行`;
  if (faults > 0) return { label: '有设备故障', tone: 'fault' as StatusTone, detail };
  return running > 0 ? { label: '运行中', tone: 'running' as StatusTone, detail } : { label: '已停机', tone: 'stopped' as StatusTone, detail };
}

/** Standby power over no cooling is not an efficiency: a stopped plant has none. */
const plantCop = (plant: Plant) => (equipment(plant).running === 0 ? null : plant.plantCop);

function copNote(plant: Plant): string {
  if (equipment(plant).running === 0) return '停机时不算能效。';
  if (plant.plantCop !== null) return '';
  if (plant.totalPower === null || plant.coolingCapacity === null) return '没有空调总电表或冷量计，算不出能效。';
  return '功率或制冷量读数不是最新的，暂不算能效。';
}

/** A reading as a figure; with `decimals`, written as the web's headline figures are (fixed decimals, thousands separators). */
function metric(label: string, reading: PlantReading | null, decimals?: number): Metric {
  if (reading === null || neverRead(reading)) return { label, value: '—', unit: reading?.unit ?? '', late: false };
  const value = decimals !== undefined && reading.numeric !== null ? grouped(reading.numeric, decimals) : reading.display;
  return { label, value, unit: reading.unit ?? '', late: !reading.current };
}

// The same loops the web's 水系统与室内外环境 table lists; a loop shows once its lead reading has come in.
const LOOPS: readonly {
  readonly name: string;
  readonly lead: string;
  readonly headline: readonly (readonly [string, string])[];
  readonly detail: readonly (readonly [string, string])[];
}[] = [
  {
    name: '冷冻水',
    lead: 'btu_meter.temperature_difference',
    headline: [['供', 'btu_meter.supply_water_temperature'], ['回', 'btu_meter.return_water_temperature']],
    detail: [['温差', 'btu_meter.temperature_difference'], ['流量', 'btu_meter.flow_rate']],
  },
  {
    name: '冷却水',
    lead: 'cooling_tower.approach_temperature',
    headline: [['出塔', 'cooling_tower.leaving_water_temperature'], ['进塔', 'cooling_tower.entering_water_temperature']],
    detail: [['与湿球差', 'cooling_tower.approach_temperature'], ['流量', 'cwp.flow_rate']],
  },
  { name: '室内', lead: 'zone.temperature', headline: [['', 'zone.temperature']], detail: [] },
  {
    name: '室外',
    lead: 'weather.ambient_dry_bulb_temperature',
    headline: [['', 'weather.ambient_dry_bulb_temperature']],
    detail: [['湿球', 'weather.ambient_wet_bulb_temperature'], ['相对湿度', 'weather.relative_humidity']],
  },
];

/** A reading that has come in at least once. */
const received = (plant: Plant, key: string) => {
  const reading = plantReading(plant, key);
  return reading === null || neverRead(reading) ? null : reading;
};

function loops(plant: Plant) {
  return LOOPS.filter((loop) => received(plant, loop.lead) !== null).map((loop) => {
    const readings = [...loop.headline, ...loop.detail].map(([, key]) => received(plant, key)).filter((reading) => reading !== null);
    const line = (keys: typeof loop.headline) => keys
      .map(([label, key]) => ({ label, reading: received(plant, key) }))
      .filter((entry) => entry.reading !== null)
      .map((entry) => `${entry.label ? `${entry.label} ` : ''}${withUnit(entry.reading!)}`)
      .join(' · ');
    return { name: loop.name, headline: line(loop.headline) || '—', detail: line(loop.detail), late: readings.some((reading) => !reading.current) };
  });
}

export function presentRealtime(plant: Plant) {
  return {
    status: plantStatus(plant),
    metrics: [
      // The web's 实时运行 writes power to 0.1 kW and cooling to whole kW.
      metric('冷站总功率', plant.totalPower, 1),
      metric('制冷量', plant.coolingCapacity, 0),
      { label: '能效', value: grouped(plantCop(plant), 2), unit: '', late: false },
    ],
    copNote: copNote(plant),
    loops: loops(plant),
    devices: deviceSummary(plant),
  };
}

/** The overview's 冷站现在 card; none for a Site without registered devices. */
export function presentPlantNow(plant: Plant) {
  if (plant.devices.length === 0) return null;
  return {
    status: plantStatus(plant),
    summary: deviceSummary(plant),
    metrics: [
      metric('功率', plant.totalPower, 1),
      metric('供水', plantReading(plant, 'btu_meter.supply_water_temperature')),
      metric('室内', plantReading(plant, 'zone.temperature')),
    ],
  };
}

const HOUR_MS = 3_600_000;

export type BarState = 'value' | 'idle' | 'missing' | 'later';

/** Each hour's energy, counted from the Site's midnight the Energy owner cut. */
const byHour = (points: readonly DayPoint[], from: number) =>
  new Map(points.map((point) => [Math.round((Date.parse(point.periodStart) - from) / HOUR_MS), point.energyKWh]));

/** A scale top that reads cleanly: 400, 1,500, 6. */
function scaleTop(max: number): number {
  if (max <= 0) return 1;
  const magnitude = 10 ** Math.floor(Math.log10(max));
  return (Math.ceil((max / magnitude) * 2) / 2) * magnitude;
}

/**
 * Today's 24 hours as bars: an hour with a value is drawn to scale, an hour with nothing to draw is
 * flat, an hour that ended without a reading is marked and named, and hours still to come stay empty.
 */
function hourBars(start: string, now: Date, valueAt: (hour: number) => number | 'idle' | undefined, top: (max: number) => number) {
  const from = Date.parse(start);
  const hours = Array.from({ length: 24 }, (_, hour) => {
    const value = valueAt(hour);
    if (value === 'idle') return { hour, state: 'idle' as BarState, value: null };
    if (value !== undefined) return { hour, state: 'value' as BarState, value };
    return { hour, state: (from + (hour + 1) * HOUR_MS <= now.getTime() ? 'missing' : 'later') as BarState, value: null };
  });
  const best = hours.reduce<(typeof hours)[number] | null>((most, entry) => (entry.value !== null && (most === null || entry.value > most.value!) ? entry : most), null);
  const scale = top(best?.value ?? 0);
  return {
    best,
    bars: hours.map((entry) => ({ hour: entry.hour, state: entry.state, height: entry.value === null ? 0 : Math.round((entry.value / scale) * 100) })),
    scale: [grouped(scale), grouped(scale / 2, scale % 2 === 0 ? 0 : 1), '0'],
    gaps: missingRanges(hours.filter((entry) => entry.state === 'missing').map((entry) => entry.hour)),
  };
}

/**
 * Today's efficiency hour by hour, over the same hourly electricity and cooling the overview sums:
 * cooling over electricity where both meters read. An hour that drew no cooling is drawn flat.
 */
export function hourlyCop(electricity: readonly DayPoint[], cooling: readonly DayPoint[], start: string, now: Date) {
  const electricityAt = byHour(electricity, Date.parse(start));
  const coolingAt = byHour(cooling, Date.parse(start));
  const { best, bars, scale, gaps } = hourBars(start, now, (hour) => {
    const used = electricityAt.get(hour);
    const cooled = coolingAt.get(hour);
    if (used === undefined || cooled === undefined) return undefined;
    return used > 0 && cooled > 0 ? cooled / used : 'idle';
  }, (max) => Math.max(1, Math.ceil(max)));
  return { bars, scale, gaps, peak: best === null ? '' : `今天最高 ${grouped(best.value, 2)}（${best.hour} 点）` };
}

/** The electricity a device's own meter recorded today, by the hour, and its share of the Site's. */
function deviceEnergy(today: DeviceToday) {
  if (today.device === null) return null;
  const sum = (points: readonly DayPoint[]) => points.reduce((total, point) => total + point.energyKWh, 0);
  const deviceAt = byHour(today.device, Date.parse(today.start));
  const { bars, scale, gaps } = hourBars(today.start, today.now, (hour) => deviceAt.get(hour), scaleTop);
  const site = sum(today.site);
  return {
    total: grouped(sum(today.device)),
    share: site > 0 ? `占全站 ${Math.round((sum(today.device) / site) * 100)}%` : '',
    bars,
    scale,
    gaps,
  };
}

/** Hours without readings as an operator says them: 12–14 点、20 点没有读数. */
function missingRanges(hours: readonly number[]): string {
  if (hours.length === 0) return '';
  const ranges: [number, number][] = [];
  for (const hour of hours) {
    const last = ranges[ranges.length - 1];
    if (last !== undefined && last[1] === hour - 1) last[1] = hour;
    else ranges.push([hour, hour]);
  }
  return `${ranges.map(([first, last]) => (first === last ? `${first}` : `${first}–${last}`)).join('、')} 点没有读数`;
}

/** A device's state in a word, as the web's device-presentation names it. */
export function deviceStatus(device: PlantDevice): { readonly label: string; readonly tone: StatusTone } {
  if (device.connection === 'OFFLINE') return { label: '离线', tone: 'offline' };
  if (device.runState === 'FAULT') return { label: device.faultCode ? `故障 ${device.faultCode}` : '故障', tone: 'fault' };
  if (device.runState === 'RUNNING') return { label: '运行', tone: 'running' };
  if (device.runState === 'STOPPED') return { label: '停机', tone: 'stopped' };
  if (device.connection === 'ONLINE') return { label: '在线', tone: 'neutral' };
  return { label: '状态未知', tone: 'neutral' };
}

/** Offline, faulty, or reporting late: what an operator should look at. */
export const needsAttention = (device: PlantDevice) => device.connection === 'OFFLINE' || device.runState === 'FAULT' || device.hasStaleData;

// The reading a row leads with: a machine's power (a pump without a power reading, its flow), else the first of its role's key readings (the web's KEY_READINGS).
const LEAD_KEYS: Readonly<Record<PlantCategory, readonly string[]>> = {
  CHILLER: ['chiller.power'],
  CHILLED_WATER_PUMP: ['chwp.power', 'chwp.flow_rate'],
  COOLING_WATER_PUMP: ['cwp.power', 'cwp.flow_rate'],
  COOLING_TOWER: ['cooling_tower.power'],
  METER: ['hvac_meter.active_power', 'btu_meter.instant_cooling_capacity', 'btu_meter.flow_rate'],
  WEATHER: ['weather.ambient_dry_bulb_temperature'],
  ZONE: ['zone.temperature'],
  ACQUISITION: [],
  OTHER: [],
};

// What a machine's row says under its name.
const DETAIL_KEYS: Partial<Record<PlantCategory, readonly string[]>> = {
  CHILLER: ['chiller.leaving_chilled_water_temperature', 'chiller.compressor_load'],
  CHILLED_WATER_PUMP: ['chwp.flow_rate', 'chwp.frequency'],
  COOLING_WATER_PUMP: ['cwp.flow_rate', 'cwp.frequency'],
  COOLING_TOWER: ['cooling_tower.leaving_water_temperature', 'cooling_tower.fan_speed'],
};

/** A point the device has never reported (or reported only rejected values for). */
const neverRead = (reading: PlantReading) => reading.sampledAt === null;

/** The first of the device's role readings it has; none for a device without a role (a gateway-wired module). */
function leadReading(device: PlantDevice): PlantReading | null {
  for (const key of LEAD_KEYS[device.category]) {
    const reading = device.reading(key);
    if (reading) return reading;
  }
  return null;
}

function deviceRow(device: PlantDevice) {
  const hasRole = LEAD_KEYS[device.category].length > 0;
  const lead = leadReading(device);
  // A module's channels have no one reading to lead with, so its row counts them instead.
  const facts = hasRole
    ? (DETAIL_KEYS[device.category] ?? [])
      .map((key) => device.reading(key))
      .filter((reading): reading is PlantReading => reading !== null && reading !== lead && !neverRead(reading))
      .map((reading) => `${reading.label} ${withUnit(reading)}`)
    : [`${device.readings.length} 个读数`];
  const missing = device.readings.filter(neverRead).length;
  const notes = device.latestSampleAt === null ? ['没有读数']
    : [...(missing > 0 ? [`${missing} 个读数没读到`] : []), ...(device.hasStaleData ? ['读数过期'] : [])];
  const status = deviceStatus(device);
  return {
    deviceId: device.deviceId,
    name: device.name,
    // A meter or sensor never runs: an online one needs no label, only trouble does.
    status: !EQUIPMENT_CATEGORIES.has(device.category) && status.label === '在线' ? null : status,
    value: hasRole ? lead?.display ?? '—' : '',
    unit: lead?.unit ?? '',
    detail: [...facts, ...notes].join(' · '),
  };
}

export type DeviceFilter = 'all' | 'running' | 'stopped' | 'attention';

const FILTERS: readonly { readonly key: DeviceFilter; readonly label: string; readonly keeps: (device: PlantDevice) => boolean }[] = [
  { key: 'all', label: '全部', keeps: () => true },
  { key: 'running', label: '运行', keeps: isRunning },
  { key: 'stopped', label: '停机', keeps: (device) => device.connection !== 'OFFLINE' && device.runState === 'STOPPED' },
  { key: 'attention', label: '需关注', keeps: needsAttention },
];

/** The device list under a filter; the page shows its own note for a Site without devices. */
export function presentDevices(plant: Plant, filter: DeviceFilter) {
  const { keeps, label } = FILTERS.find((each) => each.key === filter)!;
  const equipmentDevices = plant.devices.filter((device) => EQUIPMENT_CATEGORIES.has(device.category));
  const others = plant.devices.filter((device) => !EQUIPMENT_CATEGORIES.has(device.category));
  const offline = others.filter((device) => device.connection === 'OFFLINE').length;
  const groups = [
    { title: '冷站主设备', caption: `${equipmentDevices.length} 台 · ${equipmentDevices.filter(isRunning).length} 台运行`, devices: equipmentDevices },
    { title: '计量和环境', caption: `${others.length} 台 · ${offline === 0 ? '全部在线' : `${offline} 台离线`}`, devices: others },
  ]
    .map((group) => ({ ...group, devices: group.devices.filter(keeps).map(deviceRow) }))
    .filter((group) => group.devices.length > 0);
  return {
    filters: FILTERS.map((each) => ({ key: each.key, label: each.label, count: plant.devices.filter(each.keeps).length })),
    groups,
    empty: groups.length > 0 ? '' : filter === 'attention' ? '没有需要关注的设备。' : `没有${label}的设备。`,
  };
}

/** Today at the Site, and the hourly electricity of the Site and of one device's own meter (null without one). */
export interface DeviceToday {
  readonly start: string;
  readonly now: Date;
  readonly site: readonly DayPoint[];
  readonly device: readonly DayPoint[] | null;
}


export function presentDevice(device: PlantDevice, today: DeviceToday | null) {
  const lead = leadReading(device);
  const second = [...LEAD_KEYS[device.category], ...(DETAIL_KEYS[device.category] ?? [])]
    .map((key) => device.reading(key))
    .find((reading) => reading !== null && reading !== lead && !neverRead(reading));
  const missing = device.readings.filter(neverRead).length;
  return {
    name: device.name,
    kind: CATEGORY_LABELS[device.category],
    status: deviceStatus(device),
    updatedAt: device.latestSampleAt === null || today === null ? '' : siteClock(device.latestSampleAt, today.start),
    metrics: [lead, second].filter((reading) => reading != null).map((reading) => metric(reading.label, reading)),
    readingsNote: device.readings.length === 0 ? '这台设备没有登记读数。'
      : `${device.readings.length} 个读数${missing > 0 ? `，${missing} 个没读到` : ''}。`,
    readings: device.readings.map((reading) => ({ label: reading.label, value: neverRead(reading) ? '没读到' : withUnit(reading), late: !neverRead(reading) && !reading.current })),
    energy: today === null ? null : deviceEnergy(today),
    energyNote: energyNote(device, today),
  };
}

/** Why a machine's electricity today is not shown; meters and sensors are not expected to have their own. */
function energyNote(device: PlantDevice, today: DeviceToday | null): string {
  if (!EQUIPMENT_CATEGORIES.has(device.category)) return '';
  if (today === null) return '这个站点今天和昨天都没有用电读数。';
  return today.device === null ? '这台设备没有单独的电表，看不到它自己的用电。' : '';
}
