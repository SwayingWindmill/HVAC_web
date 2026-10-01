export type DemandStatus = 'NORMAL' | 'WARNING' | 'CRITICAL';

export type FlexibilityStatus = 'READY' | 'STANDBY' | 'LOCKED';

export interface RollingDemandPoint {
  readonly time: string; // e.g. "14:15"
  readonly actualDemandKW: number;
  readonly rolling15MinKW: number;
  readonly contractLimitKW: number;
  readonly warningThresholdKW: number; // 90%
  readonly outdoorTemp: number;
  readonly status: DemandStatus;
}

export interface LoadDurationPoint {
  readonly percentile: number; // 0-100%
  readonly hours: number; // 0-720h
  readonly loadKW: number;
  readonly loadType: 'BASE' | 'INTERMEDIATE' | 'PEAK';
}

export interface DemandSheddingCandidate {
  readonly id: string;
  readonly name: string;
  readonly category: string;
  readonly systemPath: string;
  readonly currentPowerKW: number;
  readonly sheddingPotentialKW: number;
  readonly responseTimeMinutes: number;
  readonly constraintDescription: string;
  readonly flexibilityStatus: FlexibilityStatus;
  readonly availableDurationMinutes: number;
  readonly comfortImpactScore: 'LOW' | 'MEDIUM' | 'HIGH';
}

export interface DemandKpiSummary {
  readonly currentDemandKW: number;
  readonly contractDemandKW: number;
  readonly declaredCapacityKVA: number;
  readonly peakDemandThisMonthKW: number;
  readonly peakDemandTime: string;
  readonly demandSafetyMarginKW: number; // 契约需量 - 本月最高需量
  readonly demandSafetyMarginPercent: number;
  readonly totalSheddingAvailableKW: number;
  readonly transformerLoadRatePercent: number;
  readonly penaltyRiskLevel: 'NONE' | 'LOW' | 'MODERATE' | 'HIGH';
}

export interface DemandAnalyticsData {
  readonly summary: DemandKpiSummary;
  readonly rollingDemand: readonly RollingDemandPoint[];
  readonly loadDuration: readonly LoadDurationPoint[];
  readonly sheddingCandidates: readonly DemandSheddingCandidate[];
  readonly periodLabel: string;
}

export interface DemandFilterParams {
  readonly period?: 'today' | 'yesterday' | 'month' | 'peak-day';
  readonly viewMode?: 'ROLLING' | 'LDC' | 'SHEDDING';
  readonly search?: string;
}
