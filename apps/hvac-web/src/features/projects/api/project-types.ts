export type ProjectSubsystem = 'CHILLER' | 'PUMP' | 'TOWER' | 'TERMINAL' | 'SYSTEM';

export type ProjectCategory = 'ZERO_LOW_COST' | 'CONTROL_STRATEGY' | 'EQUIPMENT_RETROFIT';

export type ProjectStage =
  | 'PLANNING'
  | 'DESIGN'
  | 'IMPLEMENTING'
  | 'COMMISSIONING'
  | 'MV_VERIFYING'
  | 'COMPLETED';

export type MilestoneStatus = 'DONE' | 'IN_PROGRESS' | 'PENDING';

export interface ProjectMilestone {
  readonly id: string;
  readonly title: string;
  readonly targetDate: string;
  readonly status: MilestoneStatus;
  readonly owner: string;
  readonly remark?: string;
}

export interface TechnicalHandoverSpec {
  readonly setpointChanges: string;
  readonly interlockRules: string;
  readonly rollbackTrigger: string;
  readonly safetyBoundaries: string;
}

export interface MvPlanSummary {
  readonly ipmvpOption: 'Option A' | 'Option B' | 'Option C' | 'Option D';
  readonly measurementBoundary: string;
  readonly baselineModel: string;
  readonly reportingPeriod: string;
  readonly expectedVerifiedSavings: string;
}

export interface EnergySavingProject {
  readonly id: string;
  readonly code: string;
  readonly title: string;
  readonly sourceOpportunityCode: string;
  readonly sourceOpportunityTitle: string;
  readonly subsystem: ProjectSubsystem;
  readonly category: ProjectCategory;
  readonly stage: ProjectStage;
  readonly progress: number; // 0 - 100
  readonly budgetCNY: number;
  readonly spentCostCNY: number;
  readonly expectedAnnualSavingsKWh: number;
  readonly expectedAnnualSavingsCostCNY: number;
  readonly paybackMonths: number;
  readonly owner: string;
  readonly location: string;
  readonly startDate: string;
  readonly targetCompletionDate: string;
  readonly targetDevices: readonly string[];
  readonly summary: string;
  readonly milestones: readonly ProjectMilestone[];
  readonly technicalHandover: TechnicalHandoverSpec;
  readonly mvPlan: MvPlanSummary;
}

export interface ProjectsSummary {
  readonly totalCount: number;
  readonly inProgressCount: number;
  readonly completedCount: number;
  readonly totalBudgetCNY: number;
  readonly totalExpectedAnnualSavingsKWh: number;
  readonly totalExpectedSavingsCostCNY: number;
  readonly onTimeMilestoneRate: number; // e.g. 92.5%
}
