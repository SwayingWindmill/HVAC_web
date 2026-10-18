// What an Agent Tool needs to read a platform owner on the operator's behalf: the authorization
// the Gateway granted for this Session, the narrow read requests each owner accepts, and the
// bounded result every owner reader returns. The Gateway exchanges the Session's service
// delegation for an owner grant right before each read.

/** The Tenant and Site a read stays within; a device read narrows it to one device. */
export interface OwnerScope {
  readonly tenantId: string;
  readonly siteId: string | null;
  readonly assetId: string | null;
  readonly deviceId: string | null;
}

export interface AuthorizationDecision {
  readonly decision: 'ALLOW' | 'DENY';
  readonly decisionId: string;
  readonly reason?: string;
  readonly delegationGrant?: string;
  readonly toolDelegationGrants?: Readonly<Partial<Record<ToolAuthorizableReadRequest['tool'], string>>>;
  readonly policyRevision?: string;
  /** The signed-in operator the Session acts for. */
  readonly operator?: { readonly id: string; readonly issuer: string };
  readonly capabilities?: readonly string[];
  readonly traceparent?: string;
  readonly tracestate?: string;
}

export type RegistryReadRequest =
  | {
    readonly requestId: string;
    readonly tool: 'registry.getSite';
    readonly input: { readonly siteId: string };
  }
  | {
    readonly requestId: string;
    readonly tool: 'registry.listSiteAssets';
    readonly input: { readonly siteId: string };
  }
  | {
    readonly requestId: string;
    readonly tool: 'registry.getSiteAssetModel';
    readonly input: { readonly siteId: string };
  };

export interface DeviceTelemetryReadRequest {
  readonly requestId: string;
  readonly tool: 'telemetry.current.getDeviceObservationSnapshot';
  readonly input: {
    readonly siteId: string;
    readonly deviceId: string;
    readonly pointKeys: readonly string[];
  };
}

/** Readings of one device aggregated into Site-calendar buckets by the history owner. */
export interface TelemetryHistoryReadRequest {
  readonly requestId: string;
  readonly tool: 'telemetry.history.aggregateDeviceHistory';
  readonly input: {
    readonly tenantId: string;
    readonly siteId: string;
    readonly deviceId: string;
    readonly keys: readonly string[];
    readonly from: string;
    readonly to: string;
    readonly granularity: 'QUARTER_HOUR' | 'HOUR' | 'DAY';
    readonly timezone: string;
    readonly qualityPolicy: 'VALID_ONLY';
  };
}

export interface TelemetryHistoryBucket {
  readonly telemetryKey: string;
  readonly pointType: 'TELEMETRY' | 'COUNTER' | 'STATE';
  readonly unit: string | null;
  readonly periodStart: string;
  readonly periodEnd: string;
  readonly completeness: number;
  readonly gauge?: { readonly average: number; readonly minimum: number; readonly maximum: number };
  readonly counter?: { readonly deltaSum: number };
  readonly state?: { readonly lastValue: unknown };
}

export interface TelemetryHistoryAggregate {
  readonly schemaVersion: 1;
  readonly deviceId: string;
  readonly buckets: readonly TelemetryHistoryBucket[];
  /** Readings are projected up to here; null when nothing has been projected yet. */
  readonly projectionWatermark: string | null;
}

export type EnergyGranularity = 'hour' | 'day' | 'month';
export type EnergyQualityPolicy = 'VALID_ONLY' | 'VALID_AND_SUSPECT';

export interface EnergyAnalyticsReadRequest {
  readonly requestId: string;
  readonly tool: 'analytics.getEnergySeries';
  readonly input: {
    readonly tenantId: string;
    readonly siteId: string;
    readonly energyType: 'electricity' | 'cooling';
    readonly granularity: EnergyGranularity;
    readonly timezone: string;
    readonly from: string;
    readonly to: string;
    readonly qualityPolicy: EnergyQualityPolicy;
    /** `device` adds each sub-metered device's series beside the Site total. */
    readonly breakdown?: 'device';
  };
}

/** A query over one semantic view's catalog members (#573); the Site is the server's, never the model's. */
export interface SemanticQueryReadRequest {
  readonly requestId: string;
  readonly tool: 'analytics.semanticQuery';
  readonly input: {
    readonly tenantId: string;
    readonly siteId: string;
    readonly view: string;
    readonly measures: readonly string[];
    readonly dimensions?: readonly string[];
    readonly granularity: EnergyGranularity;
    readonly timezone: string;
    readonly from: string;
    readonly to: string;
  };
}

/** A Site's alarms from the alarm owner, active or recently cleared (#594). */
export interface AlarmListReadRequest {
  readonly requestId: string;
  readonly tool: 'alarm.listAlarms';
  readonly input: {
    readonly siteId: string;
    readonly condition: 'ACTIVE' | 'CLEARED';
    readonly limit: number;
  };
}

export type AlarmOperation = 'PUBLISH' | 'ACKNOWLEDGE' | 'ASSIGN' | 'UNASSIGN' | 'SUPPRESS' | 'UNSUPPRESS' | 'CLEAR';

/** What the alarm Tool tells an operator about each alarm, taken from the owner's alarm and nothing more. */
export interface AlarmListResult {
  readonly items: readonly {
    readonly deviceId?: string;
    readonly title: string;
    readonly summary: string;
    readonly currentSeverity: 'CRITICAL' | 'MAJOR' | 'MINOR' | 'WARNING' | 'INFO';
    readonly acknowledged: boolean;
    readonly assigned: boolean;
    readonly suppressed: boolean;
    readonly occurrenceCount: number;
    readonly firstOccurredAt: string;
    readonly lastOccurredAt: string;
    readonly clearedAt?: string;
    /** What happened to the alarm and when: raised, acknowledged, cleared… */
    readonly timeline: readonly { readonly operation: AlarmOperation; readonly occurredAt: string }[];
  }[];
  readonly hasMore: boolean;
}

export type ToolAuthorizableReadRequest =
  | RegistryReadRequest
  | DeviceTelemetryReadRequest
  | TelemetryHistoryReadRequest
  | EnergyAnalyticsReadRequest
  | SemanticQueryReadRequest
  | AlarmListReadRequest;

/** The semantic query's answer: titled columns and rows in column order, nulls kept as unknown. */
export interface SemanticQueryResult {
  readonly schemaVersion: 1;
  readonly columns: readonly {
    readonly name: string;
    readonly title: string;
    readonly kind: 'time' | 'dimension' | 'measure';
  }[];
  readonly rows: readonly (readonly (string | number | null)[])[];
  readonly metadata: {
    readonly dataWatermark?: string;
    readonly datasetRevision: string;
    readonly partial: boolean;
  };
}

/** The Energy analytics owner's series: points plus the provenance that says how complete they are. */
export interface EnergySeriesPoint {
  readonly periodStart: string;
  readonly periodEnd: string;
  readonly energyKWh: number;
  /** Electricity's estimated energy charge at the Site's import Tariff; cooling has none. */
  readonly cost?: EnergyCost;
}

/** Electricity priced at the import Tariff, without demand charges; unpriced energy is unknown, not free. */
export interface EnergyCost {
  readonly amountCNY: number;
  readonly unpricedKWh: number;
  readonly periods: readonly { readonly periodCode: string; readonly energyKWh: number; readonly amountCNY: number }[];
}

export interface EnergyQualitySummary {
  readonly valid: number;
  readonly suspect: number;
  readonly invalid: number;
}

export interface EnergySeriesMetadata {
  readonly requestedGranularity: EnergyGranularity;
  readonly actualGranularity: EnergyGranularity;
  readonly dataWatermark?: string;
  readonly aggregateWatermark?: string;
  readonly datasetRevision: string;
  readonly partial: boolean;
  readonly qualitySummary: EnergyQualitySummary;
}

export interface EnergySeries {
  readonly schemaVersion: 1;
  readonly points: readonly EnergySeriesPoint[];
  /** Present when the request asked for the device breakdown. */
  readonly devices?: readonly { readonly deviceId: string; readonly points: readonly EnergySeriesPoint[] }[];
  readonly metadata: EnergySeriesMetadata;
}

export interface OwnerReadResult {
  readonly requestId: string;
  readonly owner: 'registry' | 'telemetry-runtime-service' | 'telemetry-query-service' | 'alarm-service';
  readonly scope: OwnerScope;
  readonly revision: string;
  readonly quality: 'GOOD' | 'UNCERTAIN' | 'BAD' | 'STALE';
  readonly provenance: string;
  readonly payload: unknown;
}

export type OwnerReadErrorCode =
  | 'OWNER_REQUEST_INVALID'
  | 'OWNER_RESOURCE_NOT_FOUND'
  | 'OWNER_READ_TIMEOUT'
  | 'OWNER_READ_UNAVAILABLE'
  | 'OWNER_RESPONSE_TOO_LARGE'
  | 'OWNER_RESPONSE_INVALID';

export class OwnerReadError extends Error {
  readonly code: OwnerReadErrorCode;

  constructor(code: OwnerReadErrorCode, message: string, options?: ErrorOptions) {
    super(message, options);
    this.name = 'OwnerReadError';
    this.code = code;
  }
}

export interface OwnerReadContext {
  readonly sessionId: string;
  readonly runId: string;
  readonly scope: OwnerScope;
  readonly authorization: AuthorizationDecision;
  readonly correlationId: string;
}

export interface OwnerReadInput<TRequest extends ToolAuthorizableReadRequest> {
  readonly request: TRequest;
  readonly context: OwnerReadContext;
}

export interface ToolAuthorizationGrant {
  readonly delegationGrant: string;
  readonly policyRevision?: string;
}

export interface ToolAuthorizationReader {
  authorize(input: OwnerReadInput<ToolAuthorizableReadRequest>): Promise<ToolAuthorizationGrant>;
}

export interface RegistryReader {
  read(input: OwnerReadInput<RegistryReadRequest>): Promise<OwnerReadResult>;
}

export interface DeviceTelemetryReader {
  read(input: OwnerReadInput<DeviceTelemetryReadRequest>): Promise<OwnerReadResult>;
}

export interface TelemetryHistoryReader {
  read(input: OwnerReadInput<TelemetryHistoryReadRequest>): Promise<OwnerReadResult>;
}

export interface EnergyAnalyticsReader {
  read(input: OwnerReadInput<EnergyAnalyticsReadRequest>): Promise<OwnerReadResult>;
}

export interface SemanticQueryReader {
  read(input: OwnerReadInput<SemanticQueryReadRequest>): Promise<OwnerReadResult>;
}

export interface AlarmReader {
  read(input: OwnerReadInput<AlarmListReadRequest>): Promise<OwnerReadResult>;
}
