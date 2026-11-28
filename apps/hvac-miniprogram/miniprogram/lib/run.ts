// What the 运行 tab shows of the plant: whether it runs, its power, cooling and efficiency, the water
// loops and the air, as the web's 实时运行 page reads them (apps/hvac-web/src/features/operations/realtime).

import { grouped } from './format';
import { evaluatePlantCop, siteClock, type DayPoint } from './today';
import { CATEGORY_LABELS, EQUIPMENT_CATEGORIES, isRunning, loopDifferentialPressure, plantReading, toStandaloneUnit, type ParallelBranch, type Plant, type PlantCategory, type PlantDevice, type PlantReading, type PlantUnit, type WaterLoopKind } from './plant';

export type StatusTone = 'running' | 'stopped' | 'fault' | 'offline' | 'neutral';

export interface Metric {
  readonly label: string;
  /** Its lucide icon in assets/icon; '' when the phone cannot tell what it measures. */
  readonly icon: string;
  readonly value: string;
  readonly unit: string;
  /** Not current (late or of doubtful quality): drawn dimmed, never as current. */
  readonly late: boolean;
  /** Measured but with a wide uncertainty: drawn normally with a 低可信 badge, left out of averages. */
  readonly lowConfidence?: boolean;
  readonly note?: string;
}

/**
 * A reading's figure, written the same way everywhere (#677): every temperature to one decimal, so
 * 7.0 sits beside 12.6, and thousands separators from 1,000 up.
 */
function figure(reading: PlantReading): string {
  if (reading.numeric === null) return reading.display;
  if (reading.unit === '°C') return grouped(reading.numeric, 1);
  if (Math.abs(reading.numeric) < 1000) return reading.display;
  return grouped(reading.numeric, reading.display.split('.')[1]?.length ?? 0);
}

const ICON_BY_UNIT: Readonly<Record<string, string>> = {
  kW: 'zap', kWh: 'zap', '°C': 'thermometer', 'm³/h': 'waves-horizontal', Hz: 'activity', '%': 'percent', '%RH': 'droplet', kPa: 'gauge', bar: 'gauge',
};

/**
 * The lucide icon (assets/icon) of what a reading measures, so a list of readings scans by kind
 * (#677): cooling, electricity, temperature, flow, frequency, a share, humidity, on/off, a fault, a
 * ratio. '' when the phone cannot tell.
 */
export function readingIcon(reading: PlantReading): string {
  const key = reading.sourceKey;
  if (key.endsWith('.run_state')) return 'power';
  if (key.endsWith('.fault_code')) return 'triangle-alert';
  if (key.includes('cooling_capacity') || key.includes('cooling_energy')) return 'snowflake';
  if (reading.unit === null) return key.endsWith('.cop') || key.endsWith('.power_factor') ? 'gauge' : '';
  return ICON_BY_UNIT[reading.unit] ?? '';
}

/** A reading with its unit, as an operator reads it aloud: 7.0 °C, 72%, 132 m³/h. */
export function withUnit(reading: PlantReading): string {
  if (reading.unit === null) return figure(reading);
  return reading.unit === '%' ? `${figure(reading)}%` : `${figure(reading)} ${reading.unit}`;
}

/**
 * The devices the mini program shows and counts: the gateway's temperature and control modules are
 * of no use to duty staff and are left out (#677); their trouble reaches 通知 as alarms.
 */
const unitsOf = (plant: Plant): readonly PlantUnit[] => plant.units ?? plant.devices.map(toStandaloneUnit);
const listed = (plant: Plant) => unitsOf(plant).filter((unit) => unit.category !== 'ACQUISITION');

function equipment(plant: Plant) {
  const machines = unitsOf(plant).filter((unit) => EQUIPMENT_CATEGORIES.has(unit.category));
  return {
    total: machines.length,
    running: machines.filter(isRunning).length,
    faults: machines.filter((unit) => unit.connection !== 'OFFLINE' && unit.runState === 'FAULT').length,
  };
}

function deviceSummary(plant: Plant): string {
  const { total, running } = equipment(plant);
  // Running counts physical equipment units; online counts non-acquisition communication devices.
  const endpoints = plant.devices.filter((device) => device.category !== 'ACQUISITION');
  return `${running} / ${total} 台运行 · ${endpoints.filter((device) => device.connection === 'ONLINE').length} / ${endpoints.length} 在线`;
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

function percentText(relative: number): string {
  const percent = relative * 100;
  return `±${percent >= 10 ? Math.round(percent) : percent.toFixed(1)}%`;
}

/** 低可信 · 431–593 kW（±16%），温差相对探头误差偏小; 不确定度 ±5.1% for a confident figure. */
function uncertaintyNote(relative: number | undefined, lowConfidence: boolean, range?: { readonly low: number; readonly high: number; readonly digits: number; readonly unit: string }): string {
  if (relative == null || !Number.isFinite(relative)) return '';
  const text = percentText(relative);
  if (!lowConfidence) return `不确定度 ${text}`;
  const interval = range && Number.isFinite(range.low) && Number.isFinite(range.high) && range.high > 0
    ? `${range.low.toFixed(range.digits)}–${range.high.toFixed(range.digits)}${range.unit}（${text}）`
    : `不确定度 ${text}`;
  return `低可信 · ${interval}，温差相对探头误差偏小`;
}

function copUncertaintyNote(plant: Plant): string {
  const band = plant.coolingCapacity?.measurement;
  if (plant.plantCop === null || !band) return '';
  const relative = band.copRelative ?? band.relative;
  if (!plant.plantCopLowConfidence) return uncertaintyNote(relative, false);
  return uncertaintyNote(relative, true, { low: Math.max(0, plant.plantCop * (1 - relative)), high: plant.plantCop * (1 + relative), digits: 2, unit: '' })
    .replace('，温差相对探头误差偏小', '');
}

function copNote(plant: Plant): string {
  if (equipment(plant).running === 0) return '停机时不算能效。';
  if (plant.plantCop !== null && plant.plantCopLowConfidence) return '低可信的能效照常显示，不计入能效均值和诊断。';
  if (plant.plantCop !== null) return '';
  if (plant.totalPower === null || plant.coolingCapacity === null) return '没有空调总电表或冷量计，算不出能效。';
  return '功率或制冷量读数不是最新的，暂不算能效。';
}

/** A reading as a figure; with `decimals`, written as the web's headline figures are (fixed decimals, thousands separators). */
function metric(label: string, reading: PlantReading | null, decimals?: number): Metric {
  const icon = reading === null ? '' : readingIcon(reading);
  if (reading === null || neverRead(reading)) return { label, icon, value: '—', unit: reading?.unit ?? '', late: false };
  const value = decimals !== undefined && reading.numeric !== null ? grouped(reading.numeric, decimals) : figure(reading);
  const band = reading.measurement;
  const note = band ? uncertaintyNote(band.relative, band.lowConfidence, { low: band.low, high: band.high, digits: 0, unit: ` ${reading.unit ?? ''}`.trimEnd() }) : '';
  return {
    label, icon, value, unit: reading.unit ?? '', late: !reading.current && !reading.lowConfidence,
    ...(reading.lowConfidence ? { lowConfidence: true } : {}),
    ...(note ? { note } : {}),
  };
}

// The same loops the web's 水系统与室内外环境 table lists; a loop shows once its lead reading has come in.
const LOOPS: readonly {
  readonly name: string;
  readonly waterLoop?: WaterLoopKind;
  /** Its lucide icon in assets/icon. */
  readonly icon: string;
  readonly lead: string;
  readonly headline: readonly (readonly [string, string])[];
  readonly detail: readonly (readonly [string, string])[];
}[] = [
  {
    name: '冷冻水',
    waterLoop: 'CHILLED_WATER',
    icon: 'snowflake',
    lead: 'btu_meter.temperature_difference',
    headline: [['供', 'btu_meter.supply_water_temperature'], ['回', 'btu_meter.return_water_temperature']],
    detail: [['温差', 'btu_meter.temperature_difference'], ['流量', 'btu_meter.flow_rate']],
  },
  {
    name: '冷却水',
    waterLoop: 'CONDENSER_WATER',
    icon: 'droplets',
    lead: 'cooling_tower.approach_temperature',
    headline: [['出塔', 'cooling_tower.leaving_water_temperature'], ['进塔', 'cooling_tower.entering_water_temperature']],
    detail: [['与湿球差', 'cooling_tower.approach_temperature'], ['流量', 'cwp.flow_rate']],
  },
  { name: '室内', icon: 'house', lead: 'zone.temperature', headline: [['', 'zone.temperature']], detail: [['宴会厅', 'zone.banquet_temperature']] },
  {
    name: '室外',
    icon: 'cloud-sun',
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
    const dpReading = loop.waterLoop ? loopDifferentialPressure(plant, loop.waterLoop) : null;
    const validDp = dpReading && !neverRead(dpReading) ? dpReading : null;
    const branchCount = loop.waterLoop ? (plant.loopBranches[loop.waterLoop] ?? 0) : 0;
    const readings = [
      ...[...loop.headline, ...loop.detail].map(([, key]) => received(plant, key)).filter((reading) => reading !== null),
      ...(validDp ? [validDp] : []),
    ];
    const line = (keys: typeof loop.headline) => keys
      .map(([label, key]) => ({ label, reading: received(plant, key) }))
      .filter((entry) => entry.reading !== null)
      .map((entry) => `${entry.label ? `${entry.label} ` : ''}${withUnit(entry.reading!)}`)
      .join(' · ');
    const detailParts = [
      line(loop.detail),
      validDp ? `压差 ${withUnit(validDp)}` : '',
      branchCount > 0 ? `${branchCount} 条并联支路` : '',
    ].filter(Boolean);
    return { name: loop.name, icon: loop.icon, headline: line(loop.headline) || '—', detail: detailParts.join(' · '), late: readings.some((reading) => !reading.current) };
  });
}

const BRANCH_DEVICE_KEYS: readonly string[] = [
  'chiller.leaving_chilled_water_temperature',
  'chiller.compressor_load',
  'chiller.power',
  'chwp.frequency',
  'cwp.frequency',
  'vfd.frequency_feedback',
  'chwp.flow_rate',
  'cwp.flow_rate',
  'chwp.power',
  'cwp.power',
  'cooling_tower.fan_speed',
  'cooling_tower.leaving_water_temperature',
  'valve.open_state',
  'valve.position_feedback',
  'dp_sensor.differential_pressure',
  'header.differential_pressure',
  'branch.flow_rate',
  'btu_meter.flow_rate',
  'hvac_meter.active_power',
];

function presentBranchDevice(device: PlantDevice) {
  const valveState = device.reading('valve.open_state');
  const status = valveState && !neverRead(valveState) && valveState.display !== '—' && device.connection !== 'OFFLINE'
    ? { label: valveState.display, tone: (valveState.display === '全开' ? 'running' : 'neutral') as StatusTone }
    : deviceStatus(device);
  const metrics = BRANCH_DEVICE_KEYS
    .map((key) => device.reading(key))
    .filter((reading): reading is PlantReading => reading !== null && !neverRead(reading))
    .slice(0, 3)
    .map((reading) => `${reading.label} ${withUnit(reading)}`)
    .join(' · ');
  return {
    deviceId: device.deviceId,
    name: device.name,
    status,
    metrics,
  };
}

function presentBranches(branches: readonly ParallelBranch[]) {
  return branches.map((branch) => ({
    key: branch.key,
    loopLabel: branch.loopLabel,
    branchCode: branch.branchCode,
    summary: branch.equipmentCount > 0
      ? `${branch.runningCount} / ${branch.equipmentCount} 台运行`
      : `${branch.devices.length} 台设备`,
    hasAlert: branch.hasAlert,
    devices: branch.devices.map(presentBranchDevice),
  }));
}

export function presentRealtime(plant: Plant) {
  return {
    status: plantStatus(plant),
    metrics: [
      // The web's 实时运行 writes power to 0.1 kW and cooling to whole kW.
      metric('冷站总功率', plant.totalPower, 1),
      metric('制冷量', plant.coolingCapacity, 0),
      {
        label: '能效', icon: 'gauge', value: grouped(plantCop(plant), 2), unit: '', late: false,
        ...(plantCop(plant) !== null && plant.plantCopLowConfidence ? { lowConfidence: true } : {}),
        ...(plant.plantCop !== null && plant.coolingCapacity?.measurement ? { note: copUncertaintyNote(plant) } : {}),
      },
    ],
    copNote: copNote(plant),
    loops: loops(plant),
    branches: presentBranches(plant.branches),
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
 * `alsoFit` raises the scale for what is drawn beside the bars.
 */
function hourBars(start: string, now: Date, valueAt: (hour: number) => number | 'idle' | undefined, top: (max: number) => number, alsoFit = 0) {
  const from = Date.parse(start);
  const hours = Array.from({ length: 24 }, (_, hour) => {
    const value = valueAt(hour);
    if (value === 'idle') return { hour, state: 'idle' as BarState, value: null };
    if (value !== undefined) return { hour, state: 'value' as BarState, value };
    return { hour, state: (from + (hour + 1) * HOUR_MS <= now.getTime() ? 'missing' : 'later') as BarState, value: null };
  });
  const best = hours.reduce<(typeof hours)[number] | null>((most, entry) => (entry.value !== null && (most === null || entry.value > most.value!) ? entry : most), null);
  const scale = top(Math.max(best?.value ?? 0, alsoFit));
  return {
    best,
    top: scale,
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
    return evaluatePlantCop(cooled, used).cop ?? 'idle';
  }, (max) => Math.max(1, Math.ceil(max)));
  return { bars, scale, gaps, peak: best === null ? '' : `今天最高 ${grouped(best.value, 2)}（${best.hour} 点）` };
}

/**
 * Today's electricity hour by hour, with yesterday's same hour marked on each bar, over the same
 * hourly readings the overview's 今天用能 sums. One scale fits both days; an hour yesterday without
 * a reading, or one later than now, has no mark (-1).
 */
export function hourlyElectricity(today: readonly DayPoint[], yesterday: readonly DayPoint[], start: string, now: Date, yesterdayStart?: string) {
  const todayAt = byHour(today, Date.parse(start));
  const yesterdayFrom = yesterdayStart === undefined ? Date.parse(start) - 24 * HOUR_MS : Date.parse(yesterdayStart);
  const yesterdayAt = byHour(yesterday, yesterdayFrom);
  const { bars, top, scale, gaps } = hourBars(start, now, (hour) => todayAt.get(hour), scaleTop, Math.max(0, ...yesterdayAt.values()));
  return {
    bars: bars.map((bar) => {
      const before = yesterdayAt.get(bar.hour);
      return { ...bar, mark: before === undefined ? -1 : Math.round((before / top) * 100) };
    }),
    scale,
    gaps,
    compared: yesterdayAt.size > 0,
  };
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
  if (device.connection === 'SLEEPING') return { label: '休眠中', tone: 'neutral' };
  if (device.runState === 'FAULT') return { label: device.faultCode ? `故障 ${device.faultCode}` : '故障', tone: 'fault' };
  if (device.runState === 'RUNNING') return { label: '运行', tone: 'running' };
  if (device.runState === 'STOPPED') return { label: '停机', tone: 'stopped' };
  if (device.connection === 'ONLINE') return { label: '在线', tone: 'neutral' };
  return { label: '状态未知', tone: 'neutral' };
}

/** Offline, faulty, or reporting late: what an operator should look at. */
export const needsAttention = (device: PlantUnit | PlantDevice) =>
  device.connection === 'OFFLINE'
  || ('offlineMemberCount' in device && device.offlineMemberCount > 0)
  || device.runState === 'FAULT'
  || device.hasStaleData;

// The reading a row leads with: a machine's power (a pump without a power reading, its flow), else the first of its role's key readings (the web's KEY_READINGS).
const LEAD_KEYS: Readonly<Record<PlantCategory, readonly string[]>> = {
  CHILLER: ['chiller.power'],
  CHILLED_WATER_PUMP: ['chwp.power', 'chwp.flow_rate', 'chwp.frequency'],
  COOLING_WATER_PUMP: ['cwp.power', 'cwp.flow_rate', 'cwp.frequency'],
  COOLING_TOWER: ['cooling_tower.power'],
  METER: ['hvac_meter.active_power', 'btu_meter.instant_cooling_capacity', 'btu_meter.flow_rate', 'dp_sensor.differential_pressure', 'header.differential_pressure', 'branch.flow_rate', 'chwp.flow_rate', 'cooling_tower.leaving_water_temperature'],
  WEATHER: ['weather.ambient_dry_bulb_temperature'],
  ZONE: ['zone.temperature', 'zone.banquet_temperature'],
  ACQUISITION: ['valve.open_state', 'valve.position_feedback', 'vfd.frequency_feedback'],
  OTHER: [],
};

// What a machine's row says under its name.
const DETAIL_KEYS: Partial<Record<PlantCategory, readonly string[]>> = {
  CHILLER: ['chiller.leaving_chilled_water_temperature', 'chiller.compressor_load'],
  CHILLED_WATER_PUMP: ['chwp.flow_rate', 'chwp.frequency'],
  COOLING_WATER_PUMP: ['cwp.flow_rate', 'cwp.frequency'],
  COOLING_TOWER: ['cooling_tower.leaving_water_temperature', 'cooling_tower.fan_speed'],
  ACQUISITION: ['valve.position_feedback', 'vfd.frequency_feedback'],
  METER: ['branch.flow_rate', 'dp_sensor.differential_pressure', 'cooling_tower.entering_water_temperature'],
};

/** A point the device has never reported (or reported only rejected values for). */
const neverRead = (reading: PlantReading) => reading.sampledAt === null;

/** The first of the device's role readings it has; none for a device without a role (OTHER). */
function leadReading(device: PlantDevice): PlantReading | null {
  for (const key of LEAD_KEYS[device.category]) {
    const reading = device.reading(key);
    if (reading) return reading;
  }
  return null;
}

function deviceRow(device: PlantDevice) {
  const lead = leadReading(device);
  const hasRole = lead !== null || LEAD_KEYS[device.category].filter((key) => !key.startsWith('valve.') && !key.startsWith('vfd.')).length > 0;
  // A device without a role has no one reading to lead with, so its row counts its readings instead.
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
  // A meter or sensor never runs: an online one needs no label, only trouble does. One that never
  // reported says only 没有读数, without 在线 or a dash beside it.
  const silent = device.latestSampleAt === null;
  const showValue = hasRole && !silent;
  return {
    deviceId: device.deviceId,
    name: device.name,
    status: (silent || !EQUIPMENT_CATEGORIES.has(device.category)) && status.label === '在线' ? null : status,
    value: showValue ? (lead === null ? '—' : figure(lead)) : '',
    unit: showValue ? (lead?.unit ?? '') : '',
    detail: [...facts, ...notes].join(' · '),
  };
}

export type DeviceFilter = 'all' | 'running' | 'stopped' | 'attention';

const FILTERS: readonly { readonly key: DeviceFilter; readonly label: string; readonly keeps: (device: PlantUnit) => boolean }[] = [
  { key: 'all', label: '全部', keeps: () => true },
  { key: 'running', label: '运行', keeps: isRunning },
  { key: 'stopped', label: '停机', keeps: (device) => device.connection !== 'OFFLINE' && device.runState === 'STOPPED' },
  { key: 'attention', label: '需关注', keeps: needsAttention },
];

/** The device list under a filter; falls back to category grouping when no parallel branches are configured. */
export function presentDevices(plant: Plant, filter: DeviceFilter) {
  const { keeps, label } = FILTERS.find((each) => each.key === filter)!;
  const devices = listed(plant);
  const rawGroups: { readonly title: string; readonly caption: string; readonly devices: readonly PlantUnit[] }[] = [];
  if (plant.branches.length > 0) {
    const branchedIds = new Set<string>();
    for (const branch of plant.branches) {
      for (const device of branch.devices) branchedIds.add(device.unitId);
      const caption = branch.equipmentCount > 0
        ? `${branch.devices.length} 台 · ${branch.runningCount} 台运行`
        : `${branch.devices.length} 台设备`;
      rawGroups.push({
        title: `${branch.loopLabel} · 支路 ${branch.branchCode}`,
        caption,
        devices: branch.devices,
      });
    }
    const unbranchedEquip = devices.filter((device) => !branchedIds.has(device.unitId) && EQUIPMENT_CATEGORIES.has(device.category));
    const unbranchedOthers = devices.filter((device) => !branchedIds.has(device.unitId) && !EQUIPMENT_CATEGORIES.has(device.category));
    if (unbranchedEquip.length > 0) {
      rawGroups.push({
        title: '冷站主设备',
        caption: `${unbranchedEquip.length} 台 · ${unbranchedEquip.filter(isRunning).length} 台运行`,
        devices: unbranchedEquip,
      });
    }
    if (unbranchedOthers.length > 0) {
      const offline = unbranchedOthers.filter((device) => device.connection === 'OFFLINE').length;
      rawGroups.push({
        title: '计量和环境',
        caption: `${unbranchedOthers.length} 台 · ${offline === 0 ? '全部在线' : `${offline} 台离线`}`,
        devices: unbranchedOthers,
      });
    }
  } else {
    const equipmentDevices = devices.filter((device) => EQUIPMENT_CATEGORIES.has(device.category));
    const others = devices.filter((device) => !EQUIPMENT_CATEGORIES.has(device.category));
    const offline = others.filter((device) => device.connection === 'OFFLINE').length;
    rawGroups.push(
      { title: '冷站主设备', caption: `${equipmentDevices.length} 台 · ${equipmentDevices.filter(isRunning).length} 台运行`, devices: equipmentDevices },
      { title: '计量和环境', caption: `${others.length} 台 · ${offline === 0 ? '全部在线' : `${offline} 台离线`}`, devices: others },
    );
  }
  const groups = rawGroups
    .map((group) => ({ ...group, devices: group.devices.filter(keeps).map(deviceRow) }))
    .filter((group) => group.devices.length > 0);
  return {
    filters: FILTERS.map((each) => ({ key: each.key, label: each.label, count: devices.filter(each.keeps).length })),
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
      : `${device.readings.length} 个读数${missing > 0 ? `，${missing} 个没读到` : ''}`,
    readings: device.readings.map((reading) => ({ label: reading.label, icon: readingIcon(reading), value: neverRead(reading) ? '没读到' : withUnit(reading), late: !neverRead(reading) && !reading.current })),
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
