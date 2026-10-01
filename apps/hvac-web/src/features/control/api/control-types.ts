export type ControlAuthorityMode = 'AI_CLOSED_LOOP' | 'MANUAL_LOCAL' | 'SCHEDULED_BASE';

export interface ControlAuthoritySummary {
  readonly activeAuthority: ControlAuthorityMode;
  readonly activeAuthorityLabel: string;
  readonly bacnetPriority: number; // e.g. 10 for AI, 8 for Manual Override
  readonly activeOverrideCount: number;
  readonly readbackAlignmentPercent: number; // e.g. 100%
  readonly safetyInterlockStatus: 'ALL_PASSED' | 'WARNING' | 'TRIPPED';
  readonly securityLevel: string; // e.g. 'Level 2 · 变频与温控授权'
  readonly todayCommandCount: number;
}

export interface ControlStrategyItem {
  readonly id: string;
  readonly name: string;
  readonly code: string;
  readonly status: 'ACTIVE' | 'STANDBY' | 'CANDIDATE' | 'DISABLED';
  readonly description: string;
  readonly expectedSavingsPercent: number;
  readonly parameters: readonly {
    readonly name: string;
    readonly value: string;
    readonly defaultVal: string;
  }[];
  readonly lastActivatedAt: string;
  readonly author: string;
}

export type CommandLifecycleState =
  | 'REQUESTED'
  | 'ACKNOWLEDGED'
  | 'READBACK_MATCHED'
  | 'VERIFIED'
  | 'FAILED';

export interface ControlCommandItem {
  readonly id: string;
  readonly targetAssetId: string;
  readonly targetAssetName: string;
  readonly capability: string;
  readonly capabilityLabel: string;
  readonly requestedValue: string;
  readonly readbackValue: string;
  readonly unit: string;
  readonly priority: number;
  readonly state: CommandLifecycleState;
  readonly interlocksPassed: boolean;
  readonly preconditions: readonly string[];
  readonly operator: string;
  readonly executedAt: string;
  readonly impactRadius: string;
  readonly reason: string;
}

export interface SafetyInterlockRule {
  readonly id: string;
  readonly name: string;
  readonly scope: string;
  readonly condition: string;
  readonly action: string;
  readonly status: 'HEALTHY' | 'ALERT' | 'BYPASSED';
  readonly lastTested: string;
}

export interface ControlWorkspaceData {
  readonly summary: ControlAuthoritySummary;
  readonly strategies: readonly ControlStrategyItem[];
  readonly commands: readonly ControlCommandItem[];
  readonly interlocks: readonly SafetyInterlockRule[];
}

export interface ControlFilterParams {
  readonly view?: 'STRATEGIES' | 'COMMANDS' | 'INTERLOCKS';
  readonly scope?: string;
  readonly search?: string;
}
