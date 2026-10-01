export type SubsystemCategory = 'CHILLER' | 'PUMP' | 'TOWER' | 'TERMINAL' | 'SYSTEM';
export type ComplexityLevel = 'ZERO_LOW_COST' | 'MEDIUM' | 'CAPITAL';
export type OpportunityPriority = 'critical' | 'high' | 'medium';
export type OpportunityStatus =
  | 'IDENTIFIED'
  | 'REVIEWING'
  | 'APPROVED'
  | 'IN_PROGRESS'
  | 'VERIFYING'
  | 'CLOSED';

export interface EvidenceMetricItem {
  readonly label: string;
  readonly baseline: string;
  readonly actual: string;
  readonly unit: string;
  readonly delta: string;
  readonly isFavorable: boolean;
}

export interface OpportunityAuditItem {
  readonly time: string;
  readonly action: string;
  readonly actor: string;
  readonly note: string;
}

export interface EcoOpportunity {
  readonly id: string;
  readonly code: string;
  readonly title: string;
  readonly targetObject: string;
  readonly targetSystem: string;
  readonly location: string;
  readonly subsystem: SubsystemCategory;
  readonly complexity: ComplexityLevel;
  readonly annualSavingsKWh: number;
  readonly annualSavingsCostCNY: number;
  readonly investmentCostCNY: number;
  readonly paybackMonths: number;
  readonly confidence: number;
  readonly priority: OpportunityPriority;
  readonly owner: string;
  readonly status: OpportunityStatus;
  readonly detectedAt: string;
  readonly summary: string;
  readonly mechanismAnalysis: string;
  readonly baselineCondition: string;
  readonly proposedCondition: string;
  readonly calculationMethod: string;
  readonly riskNotice: string;
  readonly evidenceMetrics: readonly EvidenceMetricItem[];
  readonly auditHistory: readonly OpportunityAuditItem[];
}

export interface OpportunitiesSummary {
  readonly totalCount: number;
  readonly totalAnnualSavingsKWh: number;
  readonly totalAnnualSavingsCostCNY: number;
  readonly averagePaybackMonths: number;
  readonly zeroCostRatio: number;
  readonly highPriorityCount: number;
  readonly approvedCount: number;
  readonly inProgressCount: number;
}
