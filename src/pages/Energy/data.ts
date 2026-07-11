import { DEVICE_META, TYPE_LABEL, type DeviceType } from '@/pages/Assets/meta';

export const ENERGY_TARIFF = 0.78;

export interface DailyEnergyPoint {
  day: number;
  dateLabel: string;
  chiller: number;
  pump: number;
  ahu: number;
  total: number;
  cost: number;
  cop: number;
}

export interface MonthlyEnergyPoint {
  month: number;
  label: string;
  energy: number;
  cost: number;
  cop: number;
}

export interface DeviceEnergyRow {
  id: string;
  name: string;
  type: DeviceType;
  zoneName: string;
  runHours: number;
  energy: number;
  unitEnergy: number;
  share: number;
  periodChange: number;
}

const MONTH_SEASON = [0.72, 0.69, 0.74, 0.84, 0.98, 1.16, 1.31, 1.28, 1.08, 0.93, 0.8, 0.73];
const TYPE_BASE_SHARE: Record<DeviceType, number> = {
  chiller: 0.58,
  pump: 0.18,
  ahu: 0.24,
};

const round = (value: number, digits = 0) => {
  const scale = 10 ** digits;
  return Math.round(value * scale) / scale;
};

function daysInMonth(year: number, month: number) {
  return new Date(year, month, 0).getDate();
}

function yearEfficiencyFactor(year: number) {
  return 0.98 ** (year - 2024);
}

function dailyBase(year: number, month: number) {
  return 12_200 * MONTH_SEASON[month - 1] * yearEfficiencyFactor(year);
}

export function createDailyEnergy(year: number, month: number): DailyEnergyPoint[] {
  const count = daysInMonth(year, month);
  const base = dailyBase(year, month);

  return Array.from({ length: count }, (_, index) => {
    const day = index + 1;
    const weekday = new Date(year, month - 1, day).getDay();
    const occupancy = weekday === 0 || weekday === 6 ? 0.82 : 1;
    const operatingWave = 1 + Math.sin((day + month) * 0.72) * 0.07 + Math.cos(day * 0.31) * 0.035;
    const total = round(base * occupancy * operatingWave);

    const chillerShare = TYPE_BASE_SHARE.chiller + Math.sin(day * 0.29) * 0.025;
    const pumpShare = TYPE_BASE_SHARE.pump + Math.cos(day * 0.37) * 0.014;
    const chiller = round(total * chillerShare);
    const pump = round(total * pumpShare);
    const ahu = Math.max(0, total - chiller - pump);
    const cop = round(5.08 + (year - 2024) * 0.045 + Math.sin((month + day / count) * 0.8) * 0.16, 2);

    return {
      day,
      dateLabel: `${month}/${day}`,
      chiller,
      pump,
      ahu,
      total,
      cost: round(total * ENERGY_TARIFF),
      cop,
    };
  });
}

export function createAnnualEnergy(year: number): MonthlyEnergyPoint[] {
  return Array.from({ length: 12 }, (_, index) => {
    const month = index + 1;
    const daily = createDailyEnergy(year, month);
    const energy = daily.reduce((sum, item) => sum + item.total, 0);
    const cop = daily.reduce((sum, item) => sum + item.cop, 0) / Math.max(daily.length, 1);

    return {
      month,
      label: `${month}月`,
      energy,
      cost: round(energy * ENERGY_TARIFF),
      cop: round(cop, 2),
    };
  });
}

export function createDeviceEnergy(monthTotal: number, year: number, month: number): DeviceEnergyRow[] {
  const entries = Object.entries(DEVICE_META);
  const weighted = entries.map(([id, meta], index) => {
    const utilization = meta.type === 'chiller' ? 1 : meta.type === 'pump' ? 0.76 : 0.58;
    const deviceWave = 0.94 + ((index * 17 + month * 7 + year) % 13) / 100;
    return {
      id,
      meta,
      weight: meta.ratedPower * utilization * deviceWave,
      index,
    };
  });
  const totalWeight = weighted.reduce((sum, item) => sum + item.weight, 0);

  return weighted
    .map(({ id, meta, weight, index }) => {
      const share = totalWeight ? weight / totalWeight : 0;
      const energy = round(monthTotal * share);
      const dailyHours = meta.type === 'chiller' ? 17.2 : meta.type === 'pump' ? 14.8 : 11.6;
      const runHours = round(daysInMonth(year, month) * dailyHours * (0.91 + (index % 4) * 0.025));
      const periodChange = round((((index * 11 + month * 3) % 15) - 7) / 2, 1);

      return {
        id,
        name: meta.name,
        type: meta.type,
        zoneName: meta.zoneName,
        runHours,
        energy,
        unitEnergy: round(energy / Math.max(runHours, 1), 1),
        share: round(share * 100, 1),
        periodChange,
      };
    })
    .sort((a, b) => b.energy - a.energy);
}

export function getMonthTotal(year: number, month: number) {
  return createDailyEnergy(year, month).reduce((sum, item) => sum + item.total, 0);
}

export function getPreviousMonth(year: number, month: number) {
  return month === 1 ? { year: year - 1, month: 12 } : { year, month: month - 1 };
}

export function getSelectedDayIndex(year: number, month: number) {
  const now = new Date();
  if (now.getFullYear() === year && now.getMonth() + 1 === month) {
    return Math.max(0, Math.min(now.getDate() - 1, daysInMonth(year, month) - 1));
  }
  return daysInMonth(year, month) - 1;
}

export function createEnergyCsv(
  year: number,
  month: number,
  daily: DailyEnergyPoint[],
  devices: DeviceEnergyRow[],
  annual: MonthlyEnergyPoint[],
) {
  const lines = [
    ['能耗分析导出', `${year}年${month}月`],
    [],
    ['每日能耗'],
    ['日期', TYPE_LABEL.chiller, TYPE_LABEL.pump, TYPE_LABEL.ahu, '总能耗(kWh)', '电费(元)', 'COP'],
    ...daily.map((item) => [
      `${year}-${String(month).padStart(2, '0')}-${String(item.day).padStart(2, '0')}`,
      item.chiller,
      item.pump,
      item.ahu,
      item.total,
      item.cost,
      item.cop,
    ]),
    [],
    ['设备累计能耗'],
    ['设备ID', '设备', '类别', '区域', '运行时间(h)', '累计能耗(kWh)', '单位运行时能耗(kWh/h)', '占比(%)', '环比(%)'],
    ...devices.map((item) => [
      item.id,
      item.name,
      TYPE_LABEL[item.type],
      item.zoneName,
      item.runHours,
      item.energy,
      item.unitEnergy,
      item.share,
      item.periodChange,
    ]),
    [],
    ['年度月度汇总'],
    ['月份', '能耗(kWh)', '电费(元)', 'COP'],
    ...annual.map((item) => [item.label, item.energy, item.cost, item.cop]),
  ];

  return `\uFEFF${lines.map((row) => row.map((cell) => `"${String(cell ?? '').replace(/"/g, '""')}"`).join(',')).join('\r\n')}`;
}
