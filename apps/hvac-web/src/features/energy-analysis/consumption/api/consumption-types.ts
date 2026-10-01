export type EnergyCommodity = 'ELECTRICITY' | 'WATER' | 'GAS' | 'COOLING' | 'HEATING';

export type TimeGranularity = 'DAY' | 'WEEK' | 'MONTH' | 'YEAR';

export interface TouPeriodData {
  readonly period: string;
  readonly periodType: 'SHARP' | 'PEAK' | 'FLAT' | 'VALLEY';
  readonly hoursDesc: string;
  readonly rate: number; // 元/kWh
  readonly energyKWh: number;
  readonly costCNY: number;
  readonly energyShare: number; // 0-100%
  readonly costShare: number; // 0-100%
  readonly color: string;
}

export interface DailyConsumptionRecord {
  readonly id: string;
  readonly date: string;
  readonly dayOfWeek: string;
  readonly totalKWh: number;
  readonly sharpKWh: number;
  readonly peakKWh: number;
  readonly flatKWh: number;
  readonly valleyKWh: number;
  readonly totalCostCNY: number;
  readonly averageRate: number; // 元/kWh
  readonly baselineKWh: number;
  readonly varianceRate: number; // % compared to baseline (negative is savings)
  readonly outdoorTempAvg: number; // °C
  readonly coolingDegreeDays: number;
  readonly peakPowerKW: number;
  readonly peakTime: string;
}

export interface SubsystemCostShare {
  readonly id: string;
  readonly name: string;
  readonly category: string;
  readonly costCNY: number;
  readonly energyKWh: number;
  readonly sharePercent: number;
  readonly momPercent: number; // 环比
  readonly color: string;
}

export interface ConsumptionKpiSummary {
  readonly totalEnergyKWh: number;
  readonly energyMomPercent: number;
  readonly energySavedKWh: number;
  readonly totalCostCNY: number;
  readonly costMomPercent: number;
  readonly costSavedCNY: number;
  readonly averageUnitCost: number; // 元/kWh
  readonly peakTariffShare: number; // 尖峰电费占比 %
  readonly baselineVariancePercent: number;
  readonly carbonEmissionsTons: number;
  readonly carbonSavedTons: number;
}

export interface ConsumptionAnalyticsData {
  readonly summary: ConsumptionKpiSummary;
  readonly touBreakdown: readonly TouPeriodData[];
  readonly dailyRecords: readonly DailyConsumptionRecord[];
  readonly subsystemCosts: readonly SubsystemCostShare[];
  readonly periodRange: {
    readonly start: string;
    readonly end: string;
    readonly label: string;
  };
}

export interface ConsumptionFilterParams {
  readonly period?: 'current-month' | 'last-month' | 'quarter' | 'year';
  readonly commodity?: EnergyCommodity;
  readonly viewMode?: 'OVERVIEW' | 'TRENDS' | 'TOU_TARIFF' | 'LEDGER';
  readonly search?: string;
  readonly minCost?: number;
  readonly maxCost?: number;
}
