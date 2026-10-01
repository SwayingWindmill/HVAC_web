import {
  OwnerReadError,
  type DeviceTelemetryReadRequest,
  type DeviceTelemetryReader,
  type OwnerReadContext,
  type OwnerReadInput,
  type OwnerReadResult,
} from '../../application/index.js';
import {
  fetchOwnerJson,
  hasExactKeys,
  isInstant,
  isNonEmptyString,
  isRecord,
  normalizeOwnerReaderHttpConfig,
  type OwnerReaderHttpConfig,
} from './owner-http.js';

export type DeviceTelemetryOwnerReaderConfig = OwnerReaderHttpConfig;

export interface DeviceTelemetryPresentStateDto {
  readonly key: string;
  readonly state: 'PRESENT';
  readonly value: unknown;
  readonly valueType: 'NUMBER' | 'STRING' | 'BOOLEAN' | 'JSON';
  readonly unit: string | null;
  readonly sampledAt: string;
  readonly receivedAt: string;
  readonly freshness: 'FRESH' | 'STALE';
  readonly quality: 'GOOD' | 'PARTIAL' | 'ESTIMATED' | 'MANUAL' | 'STALE' | 'INVALID';
  readonly qualityReasons: readonly string[];
  readonly policyRevision: number;
}

export interface DeviceTelemetryMissingStateDto {
  readonly key: string;
  readonly state: 'MISSING';
  readonly freshness: 'MISSING';
  readonly missingReason: 'NEVER_OBSERVED' | 'ONLY_REJECTED_CANDIDATES' | 'POLICY_NOT_CONFIGURED';
  readonly policyRevision: number | null;
}

export type DeviceTelemetryKeyStateDto = DeviceTelemetryPresentStateDto | DeviceTelemetryMissingStateDto;

export interface DeviceObservationSnapshotDto {
  readonly schemaVersion: 1;
  readonly deviceId: string;
  readonly tenantId: string;
  readonly siteId: string;
  readonly businessRevision: number;
  readonly evaluatedAt: string;
  readonly evaluationAvailability: 'AVAILABLE' | 'UNAVAILABLE';
  readonly availabilityReasons: readonly string[];
  readonly presence: Readonly<Record<string, unknown>>;
  readonly telemetryReadiness: 'CURRENT' | 'DEGRADED' | 'INCOMPLETE' | 'NOT_APPLICABLE';
  readonly displayState: 'ONLINE' | 'OFFLINE' | 'STALE' | 'UNKNOWN' | 'UNAVAILABLE' | null;
  readonly values: readonly DeviceTelemetryKeyStateDto[];
}

const snapshotKeys = [
  'schemaVersion',
  'deviceId',
  'tenantId',
  'siteId',
  'businessRevision',
  'evaluatedAt',
  'evaluationAvailability',
  'availabilityReasons',
  'presence',
  'telemetryReadiness',
  'displayState',
  'values',
] as const;
const presentKeys = [
  'key',
  'state',
  'value',
  'valueType',
  'unit',
  'sampledAt',
  'receivedAt',
  'freshness',
  'quality',
  'qualityReasons',
  'policyRevision',
] as const;
const missingKeys = ['key', 'state', 'freshness', 'missingReason', 'policyRevision'] as const;
const uuidV7 = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u;
const telemetryKey = /^[A-Za-z][A-Za-z0-9_.:-]{0,127}$/u;
const maximumPointKeys = 16;
const presentQualities = new Set(['GOOD', 'PARTIAL', 'ESTIMATED', 'MANUAL', 'STALE', 'INVALID']);
const readinessValues = new Set(['CURRENT', 'DEGRADED', 'INCOMPLETE', 'NOT_APPLICABLE']);
const displayStates = new Set(['ONLINE', 'OFFLINE', 'STALE', 'UNKNOWN', 'UNAVAILABLE']);
const valueTypes = new Set(['NUMBER', 'STRING', 'BOOLEAN', 'JSON']);
const missingReasons = new Set(['NEVER_OBSERVED', 'ONLY_REJECTED_CANDIDATES', 'POLICY_NOT_CONFIGURED']);

const isRevision = (value: unknown): value is number => Number.isSafeInteger(value) && Number(value) >= 0;

const decodeKeyState = (value: unknown): DeviceTelemetryKeyStateDto => {
  if (!isRecord(value) || !isNonEmptyString(value.key) || !telemetryKey.test(value.key)) {
    throw new OwnerReadError('OWNER_RESPONSE_INVALID', 'Telemetry Runtime returned an invalid key state.');
  }
  if (value.state === 'PRESENT') {
    if (!hasExactKeys(value, presentKeys)
      || !valueTypes.has(String(value.valueType))
      || (value.unit !== null && typeof value.unit !== 'string')
      || !isInstant(value.sampledAt)
      || !isInstant(value.receivedAt)
      || (value.freshness !== 'FRESH' && value.freshness !== 'STALE')
      || !presentQualities.has(String(value.quality))
      || !Array.isArray(value.qualityReasons)
      || value.qualityReasons.some((reason) => !isNonEmptyString(reason))
      || !isRevision(value.policyRevision)) {
      throw new OwnerReadError('OWNER_RESPONSE_INVALID', 'Telemetry Runtime returned an invalid present key state.');
    }
    return value as unknown as DeviceTelemetryPresentStateDto;
  }
  if (value.state === 'MISSING') {
    if (!hasExactKeys(value, missingKeys)
      || value.freshness !== 'MISSING'
      || !missingReasons.has(String(value.missingReason))
      || (value.policyRevision !== null && !isRevision(value.policyRevision))) {
      throw new OwnerReadError('OWNER_RESPONSE_INVALID', 'Telemetry Runtime returned an invalid missing key state.');
    }
    return value as unknown as DeviceTelemetryMissingStateDto;
  }
  throw new OwnerReadError('OWNER_RESPONSE_INVALID', 'Telemetry Runtime returned an unsupported key state.');
};

const decodeSnapshot = (value: unknown): DeviceObservationSnapshotDto => {
  if (!isRecord(value)
    || !hasExactKeys(value, snapshotKeys)
    || value.schemaVersion !== 1
    || typeof value.deviceId !== 'string' || !uuidV7.test(value.deviceId)
    || typeof value.tenantId !== 'string' || !uuidV7.test(value.tenantId)
    || typeof value.siteId !== 'string' || !uuidV7.test(value.siteId)
    || !isRevision(value.businessRevision)
    || !isInstant(value.evaluatedAt)
    || (value.evaluationAvailability !== 'AVAILABLE' && value.evaluationAvailability !== 'UNAVAILABLE')
    || !Array.isArray(value.availabilityReasons)
    || value.availabilityReasons.some((reason) => !isNonEmptyString(reason))
    || !isRecord(value.presence)
    || !readinessValues.has(String(value.telemetryReadiness))
    || (value.displayState !== null && !displayStates.has(String(value.displayState)))
    || !Array.isArray(value.values)) {
    throw new OwnerReadError('OWNER_RESPONSE_INVALID', 'Telemetry Runtime returned an invalid Device Observation Snapshot.');
  }
  return {
    ...value,
    values: value.values.map(decodeKeyState),
  } as unknown as DeviceObservationSnapshotDto;
};

const requireRequest = (input: OwnerReadInput<DeviceTelemetryReadRequest>): {
  readonly tenantId: string;
  readonly siteId: string;
  readonly deviceId: string;
  readonly pointKeys: readonly string[];
} => {
  const { scope } = input.context;
  const { siteId, deviceId, pointKeys } = input.request.input;
  if (!isNonEmptyString(scope.tenantId)
    || !isNonEmptyString(scope.siteId)
    || scope.siteId !== siteId
    || scope.assetId !== null
    || scope.deviceId !== deviceId
    || !uuidV7.test(siteId)
    || !uuidV7.test(deviceId)
    || pointKeys.length === 0
    || pointKeys.length > maximumPointKeys
    || new Set(pointKeys).size !== pointKeys.length
    || pointKeys.some((key) => !telemetryKey.test(key))) {
    throw new OwnerReadError('OWNER_REQUEST_INVALID', 'The Device Telemetry READ request is outside the authorized scope.');
  }
  return { tenantId: scope.tenantId, siteId, deviceId, pointKeys };
};

const authorizationHeaders = (
  context: OwnerReadContext,
  request: DeviceTelemetryReadRequest,
): Readonly<Record<string, string>> => {
  const grant = context.authorization.toolDelegationGrants?.[request.tool]
    ?? context.authorization.delegationGrant;
  if (context.authorization.decision !== 'ALLOW'
    || !isNonEmptyString(context.authorization.decisionId)
    || !isNonEmptyString(grant)) {
    throw new OwnerReadError('OWNER_REQUEST_INVALID', 'The Device Telemetry authorization context is incomplete.');
  }
  return {
    Accept: 'application/json, application/problem+json',
    Authorization: `Bearer ${grant}`,
    'X-Request-ID': request.requestId,
    ...(context.authorization.traceparent === undefined ? {} : { traceparent: context.authorization.traceparent }),
    ...(context.authorization.tracestate === undefined ? {} : { tracestate: context.authorization.tracestate }),
  };
};

const resultQuality = (snapshot: DeviceObservationSnapshotDto): OwnerReadResult['quality'] => {
  if (snapshot.evaluationAvailability !== 'AVAILABLE' || snapshot.displayState === 'UNAVAILABLE') return 'BAD';
  if (snapshot.displayState === 'STALE'
    || snapshot.values.some((value) => value.state === 'PRESENT' && (value.freshness === 'STALE' || value.quality === 'STALE'))) return 'STALE';
  if (snapshot.values.some((value) => value.state === 'PRESENT' && value.quality === 'INVALID')) return 'BAD';
  if (snapshot.telemetryReadiness !== 'CURRENT'
    || snapshot.values.some((value) => value.state === 'MISSING'
      || (value.state === 'PRESENT' && value.quality !== 'GOOD'))) return 'UNCERTAIN';
  return 'GOOD';
};

export const createDeviceTelemetryOwnerReader = (
  input: DeviceTelemetryOwnerReaderConfig,
): DeviceTelemetryReader => {
  const config = normalizeOwnerReaderHttpConfig(input);
  return Object.freeze({
    async read(ownerInput: OwnerReadInput<DeviceTelemetryReadRequest>) {
      const requested = requireRequest(ownerInput);
      const query = new URLSearchParams();
      for (const key of requested.pointKeys) query.append('key', key);
      const payload = await fetchOwnerJson(config, {
        path: `/internal/v1/devices/${encodeURIComponent(requested.deviceId)}/observation-snapshot?${query}`,
        method: 'GET',
        headers: authorizationHeaders(ownerInput.context, ownerInput.request),
      });
      const snapshot = decodeSnapshot(payload);
      if (snapshot.tenantId !== requested.tenantId
        || snapshot.siteId !== requested.siteId
        || snapshot.deviceId !== requested.deviceId
        || snapshot.values.length !== requested.pointKeys.length
        || snapshot.values.some((value, index) => value.key !== requested.pointKeys[index])) {
        throw new OwnerReadError('OWNER_RESPONSE_INVALID', 'Telemetry Runtime returned a snapshot outside the authorized selection.');
      }
      return Object.freeze({
        requestId: ownerInput.request.requestId,
        owner: 'telemetry-runtime-service' as const,
        scope: { ...ownerInput.context.scope },
        revision: `telemetry-device:${snapshot.businessRevision}`,
        quality: resultQuality(snapshot),
        provenance: 'telemetry-runtime-service:device-observation-snapshot/v1',
        payload: snapshot,
      });
    },
  });
};
