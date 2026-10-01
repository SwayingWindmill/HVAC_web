export type IpmvpOption = 'Option A' | 'Option B' | 'Option C' | 'Option D';

export type MvSubsystem = 'CHILLER' | 'PUMP' | 'TOWER' | 'TERMINAL' | 'SYSTEM';

export type MvStatus = 'VERIFYING' | 'CERTIFIED' | 'ADJUSTMENT_REQUIRED';

export interface MvMonthlyRecord {
  readonly month: string;
  readonly rawActualKWh: number;
  readonly adjustedBaselineKWh: number;
  readonly verifiedSavingsKWh: number;
  readonly verifiedCostCNY: number;
  readonly cdd: number; // Cooling Degree Days 气象调整度日数
  readonly realizationRate: number; // 实际节能量 / 预期节能量 %
}

export interface MvAdjustmentItem {
  readonly id: string;
  readonly type: 'ROUTINE_WEATHER' | 'OCCUPANCY' | 'OPERATIONAL_HOURS' | 'BASELOAD_DRIFT';
  readonly label: string;
  readonly description: string;
  readonly adjustmentDeltaKWh: number;
}

export interface MvProjectRecord {
  readonly id: string;
  readonly code: string;
  readonly projectName: string;
  readonly sourceProjectCode: string;
  readonly ipmvpOption: IpmvpOption;
  readonly subsystem: MvSubsystem;
  readonly baselinePeriod: string;
  readonly reportingPeriod: string;
  readonly measurementBoundary: string;
  readonly r2: number; // 判定系数 R²
  readonly cvRmse: number; // 变异系数 CV(RMSE) %
  readonly nmbe: number; // 标准化均方误差 NMBE %
  readonly verifiedSavingsKWh: number;
  readonly verifiedSavingsCostCNY: number;
  readonly targetSavingsKWh: number;
  readonly realizationRate: number; // 节能量达成率 %
  readonly status: MvStatus;
  readonly verifier: string;
  readonly lastAuditDate: string;
  readonly monthlyRecords: readonly MvMonthlyRecord[];
  readonly adjustments: readonly MvAdjustmentItem[];
}

export interface VerificationSummary {
  readonly totalVerifiedSavingsKWh: number;
  readonly totalVerifiedCostCNY: number;
  readonly overallRealizationRate: number; // %
  readonly averageCvRmse: number; // %
  readonly certifiedCount: number;
  readonly verifyingCount: number;
}
