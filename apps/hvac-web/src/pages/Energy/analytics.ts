import { DEVICE_META, TYPE_LABEL, type DeviceType } from '@/pages/Assets/meta';
import { ENERGY_TARIFF, createAnnualEnergy, createDailyEnergy } from './data';

export const ENERGY_SYSTEM_COLORS: Record<DeviceType, string> = {
  chiller: '#0fb5ae',
  pump: '#3b82f6',
  ahu: '#f59e0b',
};

export const ENERGY_SYSTEM_ORDER: DeviceType[] = ['chiller', 'pump', 'ahu'];

const round = (value: number, digits = 0) => {
  const scale = 10 ** digits;
  return Math.round(value * scale) / scale;
};

export const formatDate = (date: Date) => [
  date.getFullYear(),
  String(date.getMonth() + 1).padStart(2, '0'),
  String(date.getDate()).padStart(2, '0'),
].join('-');

export const getWeekStart = (year: number, week: number) => {
  const januaryFourth = new Date(year, 0, 4);
  const day = januaryFourth.getDay() || 7;
  const monday = new Date(year, 0, 4 - day + 1);
  monday.setDate(monday.getDate() + (week - 1) * 7);
  return monday;
};

export interface YearMonthAnalytics {
  month: number;
  label: string;
  energy: number | null;
  previous: number | null;
  target: number | null;
  cost: number | null;
  cop: number | null;
  status: 'actual' | 'mtd' | 'future';
}

export interface YearSystemAnalytics {
  type: DeviceType;
  label: string;
  energy: number;
  previous: number;
  share: number;
  change: number;
}

export interface YearBuildingAnalytics {
  id: string;
  name: string;
  energy: number;
  area: number;
  intensity: number;
  target: number;
  change: number;
}

export function createYearAnalytics(year: number, referenceDate = new Date()) {
  const current = createAnnualEnergy(year, referenceDate);
  const previous = createAnnualEnergy(year - 1, new Date(year - 1, 11, 31));
  const months: YearMonthAnalytics[] = current.map((item, index) => {
    const previousEnergy = previous[index]?.energy ?? 0;
    return {
      month: item.month,
      label: item.label,
      energy: item.status === 'future' ? null : item.energy,
      previous: item.status === 'future' ? null : previousEnergy,
      target: item.status === 'future' ? null : Math.round(previousEnergy * 0.96),
      cost: item.status === 'future' ? null : item.cost,
      cop: item.status === 'future' ? null : item.cop,
      status: item.status,
    };
  });
  const actualMonths = months.filter((item) => item.energy !== null);
  const total = actualMonths.reduce((sum, item) => sum + (item.energy ?? 0), 0);
  const previousTotal = actualMonths.reduce((sum, item) => sum + (item.previous ?? 0), 0);
  const targetTotal = actualMonths.reduce((sum, item) => sum + (item.target ?? 0), 0);
  const weightedCopNumerator = actualMonths.reduce((sum, item) => sum + (item.energy ?? 0) * (item.cop ?? 0), 0);
  const weightedCop = total ? weightedCopNumerator / total : 0;
  const savingRate = previousTotal ? ((previousTotal - total) / previousTotal) * 100 : 0;
  const targetRate = targetTotal ? (total / targetTotal) * 100 : 0;

  const typeWeights: Record<DeviceType, number> = { chiller: 0.59, pump: 0.17, ahu: 0.24 };
  const systems: YearSystemAnalytics[] = ENERGY_SYSTEM_ORDER.map((type, index) => {
    const wave = 0.98 + ((year + index * 7) % 6) / 100;
    const energy = Math.round(total * typeWeights[type] * wave);
    const previousEnergy = Math.round(previousTotal * typeWeights[type] * (1.01 + index * 0.008));
    return {
      type,
      label: TYPE_LABEL[type],
      energy,
      previous: previousEnergy,
      share: total ? round((energy / total) * 100, 1) : 0,
      change: previousEnergy ? round(((energy - previousEnergy) / previousEnergy) * 100, 1) : 0,
    };
  });

  const buildingSeeds = [
    { id: 'b1', name: '总部大楼', area: 58_000, weight: 0.58 },
    { id: 'b2', name: '研发中心', area: 36_000, weight: 0.27 },
    { id: 'b3', name: '数据中心配套区', area: 18_000, weight: 0.15 },
  ];
  const buildings: YearBuildingAnalytics[] = buildingSeeds.map((item, index) => {
    const energy = Math.round(total * item.weight * (0.98 + index * 0.025));
    const target = Math.round(energy * (index === 2 ? 0.94 : 1.04));
    return {
      id: item.id,
      name: item.name,
      energy,
      area: item.area,
      intensity: round(energy / item.area, 1),
      target,
      change: round((index - 1) * 2.7 + Math.sin(year + index) * 1.4, 1),
    };
  }).sort((a, b) => b.energy - a.energy);

  return {
    months,
    systems,
    buildings,
    total,
    previousTotal,
    targetTotal,
    weightedCop,
    savingRate,
    targetRate,
    cost: Math.round(total * ENERGY_TARIFF),
    carbon: Math.round(total * 0.5703),
  };
}

export interface WeekDayAnalytics {
  index: number;
  date: string;
  weekday: string;
  energy: number;
  previous: number;
  offHoursEnergy: number;
  peakPower: number;
  startHour: number;
  stopHour: number;
  status: 'actual' | 'future';
}

export interface WeekScheduleRow extends WeekDayAnalytics {
  compliance: number;
  issue: string;
}

export function createWeekAnalytics(year: number, week: number, referenceDate = new Date()) {
  const start = getWeekStart(year, week);
  const weekdayLabels = ['周一', '周二', '周三', '周四', '周五', '周六', '周日'];
  const days: WeekDayAnalytics[] = Array.from({ length: 7 }, (_, index) => {
    const date = new Date(start);
    date.setDate(start.getDate() + index);
    const future = date.getTime() > new Date(referenceDate.getFullYear(), referenceDate.getMonth(), referenceDate.getDate()).getTime();
    const weekend = index >= 5;
    const season = 1 + Math.sin((date.getMonth() + 1) * 0.72) * 0.16;
    const base = 11_800 * season * (weekend ? 0.72 : 1);
    const wave = 1 + Math.sin((week * 7 + index) * 0.58) * 0.065;
    const energy = future ? 0 : Math.round(base * wave);
    const previous = future ? 0 : Math.round(energy * (1.025 + Math.cos(index * 0.8) * 0.025));
    const offRate = weekend ? 0.27 : 0.13 + (index % 3) * 0.018;
    return {
      index,
      date: formatDate(date),
      weekday: weekdayLabels[index],
      energy,
      previous,
      offHoursEnergy: Math.round(energy * offRate),
      peakPower: future ? 0 : Math.round(820 + energy / 34 + Math.sin(index) * 42),
      startHour: weekend ? 8.6 : 7.2 + (index % 3) * 0.18,
      stopHour: weekend ? 18.2 : 20.1 + (index % 2) * 0.35,
      status: future ? 'future' : 'actual',
    };
  });
  const actualDays = days.filter((item) => item.status === 'actual');
  const total = actualDays.reduce((sum, item) => sum + item.energy, 0);
  const previousTotal = actualDays.reduce((sum, item) => sum + item.previous, 0);
  const offHoursTotal = actualDays.reduce((sum, item) => sum + item.offHoursEnergy, 0);
  const weekendTotal = actualDays.filter((item) => item.index >= 5).reduce((sum, item) => sum + item.energy, 0);
  const scheduleRows: WeekScheduleRow[] = actualDays.map((item) => {
    const expectedStart = item.index >= 5 ? 9 : 7.5;
    const expectedStop = item.index >= 5 ? 18 : 20;
    const deviation = Math.abs(item.startHour - expectedStart) + Math.abs(item.stopHour - expectedStop);
    return {
      ...item,
      compliance: Math.max(72, Math.round(100 - deviation * 15)),
      issue: item.offHoursEnergy / Math.max(item.energy, 1) > 0.2
        ? '非营业时段基荷偏高'
        : item.stopHour > expectedStop + 0.25
          ? '停机时间偏晚'
          : '日程匹配正常',
    };
  });
  const heatmap = days.flatMap((item) => Array.from({ length: 24 }, (_, hour) => {
    if (item.status === 'future') return [hour, item.index, null];
    const occupied = hour >= item.startHour && hour <= item.stopHour;
    const lunchDip = hour === 12 || hour === 13 ? 0.86 : 1;
    const value = occupied
      ? Math.round((520 + Math.sin((hour - 7) / 13 * Math.PI) * 520) * lunchDip * (item.index >= 5 ? 0.72 : 1))
      : Math.round(110 + (hour < 5 ? 28 : 0) + item.index * 4);
    return [hour, item.index, value];
  }));

  return {
    start,
    days,
    scheduleRows,
    heatmap,
    total,
    previousTotal,
    offHoursTotal,
    weekendTotal,
    change: previousTotal ? round(((total - previousTotal) / previousTotal) * 100, 1) : 0,
    offHoursRate: total ? round((offHoursTotal / total) * 100, 1) : 0,
    weekendRate: total ? round((weekendTotal / total) * 100, 1) : 0,
    averageStart: actualDays.length ? round(actualDays.reduce((sum, item) => sum + item.startHour, 0) / actualDays.length, 1) : 0,
    averageStop: actualDays.length ? round(actualDays.reduce((sum, item) => sum + item.stopHour, 0) / actualDays.length, 1) : 0,
  };
}

export type TariffBand = '谷' | '平' | '峰';

export interface DayHourAnalytics {
  hour: number;
  label: string;
  chiller: number | null;
  pump: number | null;
  ahu: number | null;
  totalPower: number | null;
  cumulativeEnergy: number | null;
  cop: number | null;
  tariff: TariffBand;
  occupied: boolean;
}

export interface DayDeviceTimeline {
  id: string;
  name: string;
  type: DeviceType;
  startHour: number;
  stopHour: number;
  runHours: number;
  energy: number;
  starts: number;
  status: 'normal' | 'warning';
}

export interface DayEvent {
  time: string;
  type: 'alarm' | 'fdd' | 'optimize' | 'operation';
  title: string;
  detail: string;
  target?: string;
}

export function createDayAnalytics(dateString: string, referenceDate = new Date()) {
  const [year, month, day] = dateString.split('-').map(Number);
  const selectedDate = new Date(year, month - 1, day);
  const todayStart = new Date(referenceDate.getFullYear(), referenceDate.getMonth(), referenceDate.getDate()).getTime();
  const selectedStart = new Date(year, month - 1, day).getTime();
  const isToday = selectedStart === todayStart;
  const isFuture = selectedStart > todayStart;
  const measuredHour = isFuture ? -1 : isToday ? referenceDate.getHours() : 23;
  const weekend = selectedDate.getDay() === 0 || selectedDate.getDay() === 6;
  let cumulative = 0;
  const hours: DayHourAnalytics[] = Array.from({ length: 24 }, (_, hour) => {
    const measured = hour <= measuredHour;
    const occupied = hour >= (weekend ? 9 : 7) && hour <= (weekend ? 18 : 21);
    const tariff: TariffBand = hour < 7 || hour >= 23 ? '谷' : hour >= 10 && hour <= 15 ? '峰' : '平';
    if (!measured) {
      return {
        hour,
        label: `${String(hour).padStart(2, '0')}:00`,
        chiller: null,
        pump: null,
        ahu: null,
        totalPower: null,
        cumulativeEnergy: null,
        cop: null,
        tariff,
        occupied,
      };
    }
    const curve = occupied ? Math.sin(Math.min(1, Math.max(0, (hour - 6) / 15)) * Math.PI) : 0;
    const daySeed = year + month * 11 + day * 17;
    const chiller = Math.round((occupied ? 260 + curve * 560 : 65) * (0.98 + Math.sin(daySeed + hour) * 0.035));
    const pump = Math.round(occupied ? 95 + curve * 105 : 24);
    const ahu = Math.round((occupied ? 128 + curve * 150 : 42) * (weekend ? 0.75 : 1));
    const totalPower = chiller + pump + ahu;
    cumulative += totalPower;
    return {
      hour,
      label: `${String(hour).padStart(2, '0')}:00`,
      chiller,
      pump,
      ahu,
      totalPower,
      cumulativeEnergy: cumulative,
      cop: round(4.75 + curve * 0.72 - (hour >= 13 && hour <= 15 ? 0.18 : 0), 2),
      tariff,
      occupied,
    };
  });
  const measuredHours = hours.filter((item) => item.totalPower !== null);
  const totalEnergy = measuredHours.reduce((sum, item) => sum + (item.totalPower ?? 0), 0);
  const peak = measuredHours.reduce((max, item) => (item.totalPower ?? 0) > (max.totalPower ?? 0) ? item : max, measuredHours[0] ?? hours[0]);
  const valley = measuredHours.reduce((min, item) => (item.totalPower ?? Number.POSITIVE_INFINITY) < (min.totalPower ?? Number.POSITIVE_INFINITY) ? item : min, measuredHours[0] ?? hours[0]);
  const offHoursEnergy = measuredHours.filter((item) => !item.occupied).reduce((sum, item) => sum + (item.totalPower ?? 0), 0);
  const weightedCop = totalEnergy
    ? measuredHours.reduce((sum, item) => sum + (item.totalPower ?? 0) * (item.cop ?? 0), 0) / totalEnergy
    : 0;
  const tariffEnergy = measuredHours.reduce<Record<TariffBand, number>>((result, item) => {
    result[item.tariff] += item.totalPower ?? 0;
    return result;
  }, { 谷: 0, 平: 0, 峰: 0 });

  const deviceEntries = Object.entries(DEVICE_META);
  const deviceWeights = deviceEntries.map(([id, meta], index) => ({
    id,
    meta,
    weight: meta.ratedPower * (meta.type === 'chiller' ? 1 : meta.type === 'pump' ? 0.78 : 0.63) * (0.94 + (index % 4) * 0.035),
    index,
  }));
  const totalWeight = deviceWeights.reduce((sum, item) => sum + item.weight, 0);
  const devices: DayDeviceTimeline[] = deviceWeights.map(({ id, meta, weight, index }) => {
    const startHour = meta.type === 'chiller' ? 6.8 + index * 0.16 : meta.type === 'pump' ? 6.6 : 7.2 + index * 0.12;
    const stopHour = meta.type === 'ahu' ? 20.4 + (index % 2) * 0.5 : 21.1;
    const runHours = Math.max(0, Math.min(measuredHour + 1, stopHour) - startHour);
    const energy = Math.round(totalEnergy * (totalWeight ? weight / totalWeight : 0));
    const starts = id === 'b1-z1-u2' ? 5 : 1 + (index % 2);
    return {
      id,
      name: meta.name,
      type: meta.type,
      startHour: round(startHour, 1),
      stopHour: round(stopHour, 1),
      runHours: round(runHours, 1),
      energy,
      starts,
      status: starts >= 5 ? 'warning' as const : 'normal' as const,
    };
  }).sort((a, b) => b.energy - a.energy);

  const events: DayEvent[] = isFuture ? [] : [
    { time: '07:02', type: 'operation', title: '冷站按日程启动', detail: '冷冻泵先行 4 分钟，冷水机组 #1 随后投入。' },
    { time: '10:18', type: 'fdd', title: '冷水机组 #2 出现短周期启停', detail: '2 小时内累计启停 5 次，建议检查负荷分配与最小运行时间。', target: 'FDD-77' },
    { time: '13:40', type: 'alarm', title: '空调机组 #7 功率偏高', detail: '实时功率高于相似日基线 18%，持续 26 分钟。', target: 'b1-z3-ahu7' },
    { time: '15:10', type: 'optimize', title: '峰时负荷转移建议', detail: '预计可转移 96 kW，降低当日峰时费用约 ¥118。', target: 'OPT-204' },
  ];

  return {
    selectedDate,
    measuredHour,
    isToday,
    isFuture,
    hours,
    devices,
    events,
    totalEnergy,
    cost: Math.round(totalEnergy * ENERGY_TARIFF),
    peakPower: peak?.totalPower ?? 0,
    peakHour: peak?.label ?? '-',
    valleyPower: valley?.totalPower ?? 0,
    peakValleyGap: Math.max(0, (peak?.totalPower ?? 0) - (valley?.totalPower ?? 0)),
    offHoursEnergy,
    offHoursRate: totalEnergy ? round((offHoursEnergy / totalEnergy) * 100, 1) : 0,
    weightedCop: round(weightedCop, 2),
    tariffEnergy,
  };
}

export function createMonthTypeTotals(year: number, month: number, throughDay: number) {
  const daily = createDailyEnergy(year, month, throughDay);
  return ENERGY_SYSTEM_ORDER.map((type) => ({
    type,
    label: TYPE_LABEL[type],
    value: daily.reduce((sum, item) => sum + item[type], 0),
  }));
}
