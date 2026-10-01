export interface StandardComparisonItem {
  readonly metricName: string;
  readonly unit: string;
  readonly actualValue: number;
  readonly gbLimitValue: number; // 国标限值
  readonly gbStandardValue: number; // 国标基准值
  readonly advancedValue: number; // 先进值
  readonly complianceLevel: 'ADVANCED' | 'STANDARD' | 'COMPLIANT' | 'NON_COMPLIANT';
  readonly savingsRateVsStandard: number; // %
}

export interface PeerBuildingRanking {
  readonly id: string;
  readonly name: string;
  readonly city: string;
  readonly buildingType: string;
  readonly grossFloorAreaM2: number;
  readonly annualEuiKWhM2: number;
  readonly systemCop: number;
  readonly ranking: number;
  readonly percentile: number; // e.g. 8 (top 8%)
  readonly leedCertification: string;
  readonly isCurrentSite?: boolean;
}

export interface WeatherRegressionPoint {
  readonly month: string;
  readonly coolingDegreeDays: number; // CDD 26°C
  readonly heatingDegreeDays: number; // HDD 18°C
  readonly actualEnergyMWh: number;
  readonly normalizedEnergyMWh: number;
  readonly baselinePredictedMWh: number;
  readonly weatherVariancePercent: number;
}

export interface BenchmarkingKpiSummary {
  readonly actualEuiKWhM2: number;
  readonly gbStandardEuiKWhM2: number;
  readonly euiSavingsPercent: number;
  readonly systemCop: number;
  readonly gbStandardCop: number;
  readonly peerRanking: number;
  readonly totalPeers: number;
  readonly percentileRanking: number;
  readonly weatherNormalizedSavingsPercent: number;
  readonly leedEquivalentScore: number;
}

export interface BenchmarkingAnalyticsData {
  readonly summary: BenchmarkingKpiSummary;
  readonly standards: readonly StandardComparisonItem[];
  readonly peers: readonly PeerBuildingRanking[];
  readonly weatherRegression: readonly WeatherRegressionPoint[];
  readonly periodLabel: string;
}

export interface BenchmarkingFilterParams {
  readonly standardType?: 'GB50189' | 'ASHRAE901' | 'LEED' | 'ENTERPRISE';
  readonly normalization?: 'RAW' | 'AREA' | 'WEATHER';
  readonly search?: string;
}
