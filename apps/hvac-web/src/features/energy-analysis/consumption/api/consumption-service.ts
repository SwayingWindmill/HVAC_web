import type {
  ConsumptionAnalyticsData,
  ConsumptionFilterParams,
  DailyConsumptionRecord,
  SubsystemCostShare,
  TouPeriodData,
} from './consumption-types';

const MOCK_TOU_BREAKDOWN: readonly TouPeriodData[] = [
  {
    period: '尖峰时段 (11:00-13:00 / 18:00-20:00)',
    periodType: 'SHARP',
    hoursDesc: '4小时 / 日',
    rate: 1.485,
    energyKWh: 38400,
    costCNY: 57024,
    energyShare: 17.0,
    costShare: 28.8,
    color: '#ef4444',
  },
  {
    period: '高峰时段 (08:00-11:00 / 13:00-18:00)',
    periodType: 'PEAK',
    hoursDesc: '8小时 / 日',
    rate: 1.120,
    energyKWh: 72800,
    costCNY: 81536,
    energyShare: 32.2,
    costShare: 41.3,
    color: '#f97316',
  },
  {
    period: '平段时段 (07:00-08:00 / 20:00-23:00)',
    periodType: 'FLAT',
    hoursDesc: '4小时 / 日',
    rate: 0.760,
    energyKWh: 41200,
    costCNY: 31312,
    energyShare: 18.2,
    costShare: 15.8,
    color: '#06b6d4',
  },
  {
    period: '低谷时段 (23:00-07:00)',
    periodType: 'VALLEY',
    hoursDesc: '8小时 / 日',
    rate: 0.380,
    energyKWh: 73600,
    costCNY: 27968,
    energyShare: 32.6,
    costShare: 14.1,
    color: '#3b82f6',
  },
];

const MOCK_SUBSYSTEM_COSTS: readonly SubsystemCostShare[] = [
  {
    id: 'chiller-plant',
    name: '冷水机组群 (CH-01~04)',
    category: '冷源系统',
    costCNY: 112700,
    energyKWh: 128900,
    sharePercent: 57.0,
    momPercent: -5.2,
    color: '#2563eb',
  },
  {
    id: 'chilled-cooling-pumps',
    name: '冷冻/冷却输配水泵组',
    category: '输配水力',
    costCNY: 42880,
    energyKWh: 49060,
    sharePercent: 21.7,
    momPercent: -3.8,
    color: '#0ea5e9',
  },
  {
    id: 'ahu-terminals',
    name: '空气处理机组与末端 (AHU/FCU)',
    category: '空调末端',
    costCNY: 28460,
    energyKWh: 32560,
    sharePercent: 14.4,
    momPercent: -2.1,
    color: '#10b981',
  },
  {
    id: 'cooling-towers',
    name: '开式逆流冷却塔群 (CT-01~04)',
    category: '散热末端',
    costCNY: 13608,
    energyKWh: 15480,
    sharePercent: 6.9,
    momPercent: +1.4,
    color: '#f59e0b',
  },
];

function generateDailyRecords(): readonly DailyConsumptionRecord[] {
  const days: DailyConsumptionRecord[] = [];
  const dayNames = ['周日', '周一', '周二', '周三', '周四', '周五', '周六'];

  for (let i = 1; i <= 30; i++) {
    const dayStr = String(i).padStart(2, '0');
    const dateStr = `2026-09-${dayStr}`;
    const dateObj = new Date(2026, 8, i);
    const dayOfWeek = dayNames[dateObj.getDay()];
    const isWeekend = dateObj.getDay() === 0 || dateObj.getDay() === 6;

    // Realistic day load fluctuations
    const baseMultiplier = isWeekend ? 0.72 : 1.0;
    const tempFluctuation = Math.sin(i / 3.5) * 4.2;
    const outdoorTemp = Math.round((28.5 + tempFluctuation) * 10) / 10;
    const tempFactor = 1 + (outdoorTemp - 26) * 0.045;

    const valley = Math.round((2200 + Math.sin(i) * 120) * baseMultiplier);
    const flat = Math.round((1300 + Math.cos(i) * 80) * baseMultiplier);
    const peak = Math.round((2400 + Math.sin(i / 2) * 150) * baseMultiplier * tempFactor);
    const sharp = Math.round((1250 + (i % 4) * 40) * baseMultiplier * tempFactor);

    const total = valley + flat + peak + sharp;
    const totalCost = Math.round(
      valley * 0.380 + flat * 0.760 + peak * 1.120 + sharp * 1.485
    );
    const avgRate = Math.round((totalCost / total) * 1000) / 1000;
    const baseline = Math.round(total * (1.065 + (i % 3) * 0.015));
    const varianceRate = Math.round(((total - baseline) / baseline) * 1000) / 10;
    const peakPower = Math.round((total / 14) * (1.35 + (i % 5) * 0.03));
    const peakHours = ['11:45', '12:15', '14:30', '15:15', '18:30'];

    days.push({
      id: `day-${dateStr}`,
      date: dateStr,
      dayOfWeek,
      totalKWh: total,
      sharpKWh: sharp,
      peakKWh: peak,
      flatKWh: flat,
      valleyKWh: valley,
      totalCostCNY: totalCost,
      averageRate: avgRate,
      baselineKWh: baseline,
      varianceRate,
      outdoorTempAvg: outdoorTemp,
      coolingDegreeDays: Math.max(0, Math.round((outdoorTemp - 18) * 10) / 10),
      peakPowerKW: peakPower,
      peakTime: peakHours[i % peakHours.length],
    });
  }

  return days;
}

const MOCK_DAILY_RECORDS = generateDailyRecords();

export const consumptionService = {
  getConsumptionAnalytics: async (
    params?: ConsumptionFilterParams
  ): Promise<ConsumptionAnalyticsData> => {
    // Simulate brief network latency
    await new Promise((resolve) => setTimeout(resolve, 80));

    let filteredDaily = [...MOCK_DAILY_RECORDS];
    if (params?.search) {
      const q = params.search.toLowerCase();
      filteredDaily = filteredDaily.filter(
        (d) => d.date.includes(q) || d.dayOfWeek.includes(q)
      );
    }

    const totalEnergy = MOCK_TOU_BREAKDOWN.reduce((acc, t) => acc + t.energyKWh, 0);
    const totalCost = MOCK_TOU_BREAKDOWN.reduce((acc, t) => acc + t.costCNY, 0);
    const avgUnitCost = Math.round((totalCost / totalEnergy) * 1000) / 1000;
    const peakCost = MOCK_TOU_BREAKDOWN.filter(
      (t) => t.periodType === 'SHARP' || t.periodType === 'PEAK'
    ).reduce((acc, t) => acc + t.costCNY, 0);

    return {
      summary: {
        totalEnergyKWh: totalEnergy,
        energyMomPercent: -4.2,
        energySavedKWh: 9890,
        totalCostCNY: totalCost,
        costMomPercent: -5.6,
        costSavedCNY: 11450,
        averageUnitCost: avgUnitCost,
        peakTariffShare: Math.round((peakCost / totalCost) * 1000) / 10,
        baselineVariancePercent: -7.4,
        carbonEmissionsTons: Math.round(totalEnergy * 0.00057 * 10) / 10,
        carbonSavedTons: 5.64,
      },
      touBreakdown: MOCK_TOU_BREAKDOWN,
      dailyRecords: filteredDaily,
      subsystemCosts: MOCK_SUBSYSTEM_COSTS,
      periodRange: {
        start: '2026-09-01',
        end: '2026-09-30',
        label: '2026年9月 (本计费周期)',
      },
    };
  },
};
