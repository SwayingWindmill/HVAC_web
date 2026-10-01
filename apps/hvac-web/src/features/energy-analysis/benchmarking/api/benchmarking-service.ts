import type {
  BenchmarkingAnalyticsData,
  BenchmarkingFilterParams,
  PeerBuildingRanking,
  StandardComparisonItem,
  WeatherRegressionPoint,
} from './benchmarking-types';

const MOCK_STANDARDS: readonly StandardComparisonItem[] = [
  {
    metricName: '建筑单位面积用电强度 (EUI)',
    unit: 'kWh/(㎡·a)',
    actualValue: 108.5,
    gbLimitValue: 155.0,
    gbStandardValue: 135.0,
    advancedValue: 110.0,
    complianceLevel: 'ADVANCED',
    savingsRateVsStandard: -19.6,
  },
  {
    metricName: '冷站系统全年综合能效比 (SCOP)',
    unit: 'kW/kW',
    actualValue: 5.28,
    gbLimitValue: 4.20,
    gbStandardValue: 4.65,
    advancedValue: 5.20,
    complianceLevel: 'ADVANCED',
    savingsRateVsStandard: +13.5,
  },
  {
    metricName: '冷冻/冷却输配能效比 (WT/EC)',
    unit: 'kW/kW',
    actualValue: 0.038,
    gbLimitValue: 0.055,
    gbStandardValue: 0.048,
    advancedValue: 0.040,
    complianceLevel: 'ADVANCED',
    savingsRateVsStandard: -20.8,
  },
  {
    metricName: '空调箱风机单位风量耗功率 (Ws)',
    unit: 'W/(m³/h)',
    actualValue: 0.18,
    gbLimitValue: 0.28,
    gbStandardValue: 0.24,
    advancedValue: 0.20,
    complianceLevel: 'ADVANCED',
    savingsRateVsStandard: -25.0,
  },
  {
    metricName: '冷却塔逼近度 (Approach Temp)',
    unit: '°C',
    actualValue: 2.8,
    gbLimitValue: 4.5,
    gbStandardValue: 3.5,
    advancedValue: 3.0,
    complianceLevel: 'ADVANCED',
    savingsRateVsStandard: -20.0,
  },
];

const MOCK_PEERS: readonly PeerBuildingRanking[] = [
  {
    id: 'peer-sh-plaza66',
    name: '上海恒隆广场',
    city: '上海',
    buildingType: '高端商业综合体',
    grossFloorAreaM2: 268000,
    annualEuiKWhM2: 108.5,
    systemCop: 5.28,
    ranking: 5,
    percentile: 8,
    leedCertification: 'LEED Platinum',
    isCurrentSite: true,
  },
  {
    id: 'peer-sh-ifc',
    name: '上海国金中心 IFC',
    city: '上海',
    buildingType: '高端商业综合体',
    grossFloorAreaM2: 180000,
    annualEuiKWhM2: 98.2,
    systemCop: 5.52,
    ranking: 1,
    percentile: 2,
    leedCertification: 'LEED Platinum',
  },
  {
    id: 'peer-sh-iapm',
    name: '上海环贸 iapm 商场',
    city: '上海',
    buildingType: '商业与写字楼',
    grossFloorAreaM2: 130000,
    annualEuiKWhM2: 102.4,
    systemCop: 5.40,
    ranking: 2,
    percentile: 3,
    leedCertification: 'LEED Gold',
  },
  {
    id: 'peer-hz-hubin',
    name: '杭州湖滨银泰 in77',
    city: '杭州',
    buildingType: '开放式商业街区',
    grossFloorAreaM2: 210000,
    annualEuiKWhM2: 105.8,
    systemCop: 5.34,
    ranking: 3,
    percentile: 5,
    leedCertification: 'LEED Gold',
  },
  {
    id: 'peer-nj-deji',
    name: '南京德基广场',
    city: '南京',
    buildingType: '高端商业综合体',
    grossFloorAreaM2: 160000,
    annualEuiKWhM2: 107.1,
    systemCop: 5.30,
    ranking: 4,
    percentile: 6,
    leedCertification: 'LEED Platinum',
  },
  {
    id: 'peer-sz-center',
    name: '苏州中心商场',
    city: '苏州',
    buildingType: '超大型购物中心',
    grossFloorAreaM2: 300000,
    annualEuiKWhM2: 118.6,
    systemCop: 4.95,
    ranking: 12,
    percentile: 18,
    leedCertification: 'LEED Gold',
  },
  {
    id: 'peer-sh-taikoo',
    name: '前滩太古里',
    city: '上海',
    buildingType: '低密零售街区',
    grossFloorAreaM2: 120000,
    annualEuiKWhM2: 124.0,
    systemCop: 4.88,
    ranking: 18,
    percentile: 28,
    leedCertification: 'LEED Platinum',
  },
  {
    id: 'peer-wx-mixc',
    name: '无锡万象城',
    city: '无锡',
    buildingType: '滨水购物中心',
    grossFloorAreaM2: 150000,
    annualEuiKWhM2: 132.5,
    systemCop: 4.62,
    ranking: 28,
    percentile: 43,
    leedCertification: '国标二星',
  },
];

const MOCK_WEATHER_REGRESSION: readonly WeatherRegressionPoint[] = [
  { month: '01月', coolingDegreeDays: 0, heatingDegreeDays: 380, actualEnergyMWh: 142.5, normalizedEnergyMWh: 148.0, baselinePredictedMWh: 155.0, weatherVariancePercent: -4.5 },
  { month: '02月', coolingDegreeDays: 0, heatingDegreeDays: 340, actualEnergyMWh: 135.2, normalizedEnergyMWh: 140.2, baselinePredictedMWh: 146.5, weatherVariancePercent: -4.3 },
  { month: '03月', coolingDegreeDays: 12, heatingDegreeDays: 210, actualEnergyMWh: 128.0, normalizedEnergyMWh: 132.5, baselinePredictedMWh: 138.0, weatherVariancePercent: -4.0 },
  { month: '04月', coolingDegreeDays: 45, heatingDegreeDays: 60, actualEnergyMWh: 148.6, normalizedEnergyMWh: 152.0, baselinePredictedMWh: 160.2, weatherVariancePercent: -5.1 },
  { month: '05月', coolingDegreeDays: 120, heatingDegreeDays: 0, actualEnergyMWh: 185.4, normalizedEnergyMWh: 190.5, baselinePredictedMWh: 202.0, weatherVariancePercent: -5.7 },
  { month: '06月', coolingDegreeDays: 240, heatingDegreeDays: 0, actualEnergyMWh: 215.8, normalizedEnergyMWh: 220.4, baselinePredictedMWh: 236.8, weatherVariancePercent: -6.9 },
  { month: '07月', coolingDegreeDays: 410, heatingDegreeDays: 0, actualEnergyMWh: 268.4, normalizedEnergyMWh: 274.0, baselinePredictedMWh: 298.5, weatherVariancePercent: -8.2 },
  { month: '08月', coolingDegreeDays: 430, heatingDegreeDays: 0, actualEnergyMWh: 275.2, normalizedEnergyMWh: 280.6, baselinePredictedMWh: 304.2, weatherVariancePercent: -7.8 },
  { month: '09月', coolingDegreeDays: 220, heatingDegreeDays: 0, actualEnergyMWh: 226.0, normalizedEnergyMWh: 231.2, baselinePredictedMWh: 248.0, weatherVariancePercent: -6.8 },
  { month: '10月', coolingDegreeDays: 50, heatingDegreeDays: 40, actualEnergyMWh: 156.0, normalizedEnergyMWh: 160.0, baselinePredictedMWh: 168.5, weatherVariancePercent: -5.0 },
  { month: '11月', coolingDegreeDays: 5, heatingDegreeDays: 190, actualEnergyMWh: 138.4, normalizedEnergyMWh: 142.1, baselinePredictedMWh: 147.2, weatherVariancePercent: -3.5 },
  { month: '12月', coolingDegreeDays: 0, heatingDegreeDays: 360, actualEnergyMWh: 146.8, normalizedEnergyMWh: 151.0, baselinePredictedMWh: 158.0, weatherVariancePercent: -4.4 },
];

export const benchmarkingService = {
  getBenchmarkingAnalytics: async (
    params?: BenchmarkingFilterParams
  ): Promise<BenchmarkingAnalyticsData> => {
    await new Promise((resolve) => setTimeout(resolve, 80));

    let filteredPeers = [...MOCK_PEERS];
    if (params?.search) {
      const q = params.search.toLowerCase();
      filteredPeers = filteredPeers.filter(
        (p) =>
          p.name.toLowerCase().includes(q) ||
          p.city.toLowerCase().includes(q) ||
          p.buildingType.toLowerCase().includes(q)
      );
    }

    return {
      summary: {
        actualEuiKWhM2: 108.5,
        gbStandardEuiKWhM2: 135.0,
        euiSavingsPercent: -19.6,
        systemCop: 5.28,
        gbStandardCop: 4.65,
        peerRanking: 5,
        totalPeers: 65,
        percentileRanking: 8,
        weatherNormalizedSavingsPercent: -5.8,
        leedEquivalentScore: 88,
      },
      standards: MOCK_STANDARDS,
      peers: filteredPeers,
      weatherRegression: MOCK_WEATHER_REGRESSION,
      periodLabel: '2026年度迄今 (累计统计)',
    };
  },
};
