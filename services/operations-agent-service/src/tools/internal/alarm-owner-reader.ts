import {
  OwnerReadError,
  type AlarmListReadRequest,
  type AlarmListResult,
  type AlarmReader,
  type OwnerReadInput,
  type OwnerReadResult,
} from '../../application/index.js';
import {
  createOwnerHeaders,
  fetchOwnerJson,
  isInstant,
  isNonEmptyString,
  isRecord,
  normalizeOwnerReaderHttpConfig,
  type OwnerReaderHttpConfig,
} from './owner-http.js';

export type AlarmOwnerReaderConfig = OwnerReaderHttpConfig;

const severities = new Set(['CRITICAL', 'MAJOR', 'MINOR', 'WARNING', 'INFO']);
const conditions = new Set(['ACTIVE', 'CLEARED']);

const invalid = (message: string): never => {
  throw new OwnerReadError('OWNER_RESPONSE_INVALID', message);
};

const optionalInstant = (value: unknown): boolean => value === undefined || (typeof value === 'string' && isInstant(value));

// Only what the Tool says to an operator is checked; the owner validates the rest of an alarm.
const decodeAlarm = (value: unknown, tenantId: string, siteId: string): AlarmListResult['items'][number] => {
  if (!isRecord(value) || value.tenantId !== tenantId || value.siteId !== siteId
    || !isNonEmptyString(value.title) || typeof value.summary !== 'string'
    || !conditions.has(String(value.condition)) || !severities.has(String(value.currentSeverity))
    || !Number.isSafeInteger(value.occurrenceCount)
    || typeof value.firstOccurredAt !== 'string' || !isInstant(value.firstOccurredAt)
    || typeof value.lastOccurredAt !== 'string' || !isInstant(value.lastOccurredAt)
    || !optionalInstant(value.clearedAt)
    || (value.deviceId !== undefined && !isNonEmptyString(value.deviceId))
    || (value.acknowledgement !== undefined && !isRecord(value.acknowledgement))) {
    invalid('The alarm owner returned an invalid alarm.');
  }
  return value as unknown as AlarmListResult['items'][number];
};

/**
 * Lists a Site's alarms from the alarm owner under the context the Gateway signed for the Agent
 * (#594). The owner takes that context in its own header and refuses a delegation header.
 */
export const createAlarmOwnerReader = (input: AlarmOwnerReaderConfig): AlarmReader => {
  const config = normalizeOwnerReaderHttpConfig(input);
  return Object.freeze({
    async read(readInput: OwnerReadInput<AlarmListReadRequest>): Promise<OwnerReadResult> {
      const query = readInput.request.input;
      const { scope } = readInput.context;
      if (query.siteId !== scope.siteId || scope.assetId !== null || scope.deviceId !== null) {
        throw new OwnerReadError('OWNER_REQUEST_INVALID', 'The alarm list is outside the trusted Site scope.');
      }
      const { 'X-Delegation-Grant': grant, ...headers } = createOwnerHeaders(readInput.request.requestId, readInput.context, {
        logicalTool: readInput.request.tool,
        includePolicyRevision: false,
        hasBody: false,
      });
      const search = new URLSearchParams({ condition: query.condition, limit: String(query.limit) });
      const payload = await fetchOwnerJson(config, {
        path: `/internal/v1/sites/${encodeURIComponent(query.siteId)}/alarms?${search.toString()}`,
        method: 'GET',
        headers: { ...headers, 'X-Alarm-Read-Context': grant ?? '' },
      });
      if (!isRecord(payload) || payload.schemaVersion !== 1 || !Array.isArray(payload.items)
        || payload.items.length > query.limit || typeof payload.hasMore !== 'boolean') {
        invalid('The alarm owner returned an invalid alarm list.');
      }
      const list = payload as Record<string, unknown>;
      const result: AlarmListResult = {
        schemaVersion: 1,
        items: (list.items as unknown[]).map((item) => decodeAlarm(item, scope.tenantId, query.siteId)),
        hasMore: list.hasMore as boolean,
      };
      return {
        requestId: readInput.request.requestId,
        owner: 'alarm-service',
        scope: { ...scope },
        revision: 'alarm-list',
        quality: 'GOOD',
        provenance: 'alarm-service:alarm-list/v1',
        payload: result,
      };
    },
  });
};
