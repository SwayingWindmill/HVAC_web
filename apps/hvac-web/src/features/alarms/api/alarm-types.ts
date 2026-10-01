export type AlarmSeverity = 'CRITICAL' | 'MAJOR' | 'MINOR' | 'WARNING' | 'INFO';

export type IssueState = 'OPEN' | 'INVESTIGATING' | 'ACTION_PENDING' | 'VERIFYING' | 'RESOLVED';

export type DiagnosisState = 'PENDING' | 'PUBLISHED' | 'EVIDENCE_LIMITED' | 'ROOT_CAUSE_CONFIRMED';

export type ReliabilityRisk = 'LOW' | 'MEDIUM' | 'HIGH' | 'CRITICAL';

export interface TelemetryReading {
  pointName: string;
  pointCode: string;
  currentValue: number;
  unit: string;
  baselineValue: number;
  deviation: number;
  status: 'NORMAL' | 'WARNING' | 'CRITICAL';
}

export interface HypothesisItem {
  id: string;
  title: string;
  status: 'SUPPORTED' | 'WEAKENED' | 'REJECTED' | 'CANDIDATE' | 'CONFIRMED';
  confidence: number; // e.g. 92%
  rationale: string;
  supportingEvidenceCount: number;
  contradictingEvidenceCount: number;
  verificationMethod: string;
}

export interface VerificationEvidence {
  id: string;
  sensorName: string;
  expectedRange: string;
  actualRange: string;
  isDeviated: boolean;
  timestamp: string;
  detail: string;
}

export interface TimelineEvent {
  timestamp: string;
  operation: string;
  operator: string;
  description: string;
  type: 'TRIGGER' | 'DIAGNOSE' | 'ACKNOWLEDGE' | 'ASSIGN' | 'VERIFY' | 'RESOLVE';
}

export interface AlarmIssueItem {
  id: string;
  code: string;
  title: string;
  findingSummary: string;
  severity: AlarmSeverity;
  state: IssueState;
  diagnosisState: DiagnosisState;
  rootCauseTitle?: string;

  // Object & Location
  deviceId: string;
  deviceLabel: string;
  subsystem: string;
  locationLabel: string;

  // Impact
  avoidableCostWeekly: number;
  avoidableEnergyWeeklyKWh: number;
  reliabilityRisk: ReliabilityRisk;
  comfortImpactHours: number;

  // Time & Duration
  firstOccurredAt: string;
  durationFormatted: string;
  updatedAt: string;
  occurrenceCount: number;

  // Handling
  assigneeName?: string;
  acknowledged: boolean;
  acknowledgedAt?: string;

  // Detailed Investigation Context
  telemetryReadings: TelemetryReading[];
  hypotheses: HypothesisItem[];
  evidences: VerificationEvidence[];
  timeline: TimelineEvent[];
}

export interface AlarmsSummary {
  activeCount: number;
  criticalCount: number;
  majorCount: number;
  minorCount: number;
  unacknowledgedCount: number;
  averageMttaMinutes: number;
  averageMttrMinutes: number;
  confirmedRootCauseCount: number;
  diagnosisCoverageRate: number;
  avoidableCostWeeklyTotal: number;
  avoidableEnergyWeeklyTotalKWh: number;
}

export interface AlarmsFilterParams {
  search?: string;
  severity?: string;
  state?: string;
  diagnosis?: string;
  subsystem?: string;
}

export interface AlarmHeatmapCell {
  dayOfWeek: string; // 周一 to 周日
  hourRange: string; // 00:00 - 04:00, 04:00 - 08:00, etc.
  count: number;
  level: 0 | 1 | 2 | 3 | 4;
}

export interface RecurringContributor {
  deviceName: string;
  subsystem: string;
  alarmCount: number;
  avoidableCost: number;
  percentage: number;
}
