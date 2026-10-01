export type SubsystemCategory =
  | 'CHILLER_PLANT'
  | 'CHILLED_WATER_PUMPS'
  | 'CONDENSER_WATER_PUMPS'
  | 'COOLING_TOWERS'
  | 'AHU_TERMINALS'
  | 'VENTILATION'
  | 'LIGHTING_POWER'
  | 'AUXILIARY';

export interface BreakdownSubsystemItem {
  readonly id: string;
  readonly name: string;
  readonly category: SubsystemCategory;
  readonly categoryLabel: string;
  readonly energyKWh: number;
  readonly sharePercent: number; // 0-100%
  readonly momPercent: number; // 环比
  readonly yoyPercent: number; // 同比
  readonly deviceCount: number;
  readonly meterCount: number;
  readonly currentPowerKW: number;
  readonly efficiencyMetric: string;
  readonly color: string;
}

export interface EnergyFlowNode {
  readonly id: string;
  readonly name: string;
  readonly level: 'SOURCE' | 'TRANSFORM' | 'CONSUMER' | 'OUTPUT';
  readonly energyKWh: number;
  readonly percent: number;
  readonly color: string;
}

export interface EnergyFlowLink {
  readonly source: string;
  readonly target: string;
  readonly valueKWh: number;
  readonly label?: string;
}

export interface SubmeteringPointRecord {
  readonly id: string;
  readonly meterCode: string;
  readonly meterName: string;
  readonly systemPath: string; // e.g. "暖通空调 > 冷水机组 > 1#离心冷水机"
  readonly category: SubsystemCategory;
  readonly location: string; // e.g. "B2层冷冻机房低压配电柜 AA02"
  readonly todayKWh: number;
  readonly monthKWh: number;
  readonly currentPowerKW: number;
  readonly powerFactor: number; // 功率因数
  readonly momVariancePercent: number;
  readonly healthStatus: 'ONLINE' | 'WARNING' | 'OFFLINE';
  readonly lastActiveTime: string;
}

export interface BreakdownKpiSummary {
  readonly totalEnergyKWh: number;
  readonly chillerSharePercent: number;
  readonly pumpingSharePercent: number;
  readonly terminalSharePercent: number;
  readonly coolingTowerSharePercent: number;
  readonly systemCop: number;
  readonly pumpingEfficiency: number; // kW/kW
  readonly activeMeterCount: number;
  readonly totalMeterCount: number;
}

export interface BreakdownAnalyticsData {
  readonly summary: BreakdownKpiSummary;
  readonly subsystems: readonly BreakdownSubsystemItem[];
  readonly flowNodes: readonly EnergyFlowNode[];
  readonly flowLinks: readonly EnergyFlowLink[];
  readonly submeters: readonly SubmeteringPointRecord[];
  readonly periodLabel: string;
}

export interface BreakdownFilterParams {
  readonly category?: SubsystemCategory | 'ALL';
  readonly period?: 'current-month' | 'last-month' | 'quarter' | 'year';
  readonly search?: string;
}
