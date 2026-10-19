/** Why calls of a Tool failed, as the code the record keeps (#624), with how many times. */
export interface AgentToolFailureReason {
  readonly code: string;
  readonly count: number;
}

/** How often each Tool was called on a day, and how often it failed. */
export interface AgentToolUsage {
  readonly toolName: string;
  readonly calls: number;
  readonly failed: number;
  /** The commonest reason first. */
  readonly failureReasons: readonly AgentToolFailureReason[];
}

/**
 * One Site-local day of Runs: how they ended, how long they took and their tokens (#597). Only
 * figures and Tool names: never a question, an answer or an identifier.
 */
export interface AgentUsageDay {
  readonly date: string;
  readonly completed: number;
  readonly failed: number;
  readonly cancelled: number;
  /** Over the day's ended Runs; null when none ended. */
  readonly averageDurationMs: number | null;
  readonly inputTokens: number;
  readonly outputTokens: number;
  readonly tools: readonly AgentToolUsage[];
}

export interface AgentUsage {
  readonly schemaVersion: 1;
  readonly timezone: string;
  /** Every day from the first to the last asked, a day without Runs as zeros. */
  readonly days: readonly AgentUsageDay[];
}

export interface AgentUsageReader {
  /** fromDate and toDate are Site-local dates (YYYY-MM-DD), both included; a Run counts on the day it started. */
  daily(tenantId: string, siteId: string, timezone: string, fromDate: string, toDate: string): Promise<AgentUsage>;
}
