import {
  OwnerReadError,
  type FddFindingsReadRequest,
  type FddFindingsResult,
  type FddReader,
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

export type FddOwnerReaderConfig = OwnerReaderHttpConfig;

const invalid = (message: string): never => {
  throw new OwnerReadError('OWNER_RESPONSE_INVALID', message);
};

// Only what the Tool says to an operator is kept: no identifiers, evidence or model references.
const decodeFinding = (value: unknown, tenantId: string, siteId: string): FddFindingsResult['items'][number] => {
  if (!isRecord(value) || value.tenantId !== tenantId || value.siteId !== siteId
    || !isNonEmptyString(value.assetId) || !isNonEmptyString(value.findingType)
    || !isInstant(value.evaluationFrom) || !isInstant(value.evaluationTo) || !isInstant(value.createdAt)
    || typeof value.observedValue !== 'number' || typeof value.thresholdValue !== 'number' || typeof value.valueUnit !== 'string') {
    invalid('FDD returned an invalid finding.');
  }
  const finding = value as Record<string, unknown>;
  return {
    assetId: finding.assetId as string,
    findingType: finding.findingType as string,
    evaluationFrom: finding.evaluationFrom as string,
    evaluationTo: finding.evaluationTo as string,
    observedValue: finding.observedValue as number,
    thresholdValue: finding.thresholdValue as number,
    valueUnit: finding.valueUnit as string,
    linkedToAlarm: isNonEmptyString(finding.alarmId),
    linkedToWorkOrder: isNonEmptyString(finding.workOrderId),
    createdAt: finding.createdAt as string,
  };
};

/**
 * Lists a Site's recent findings from FDD under the context the Gateway signed for the Agent
 * (#595). FDD reads the context from its own header.
 */
export const createFddOwnerReader = (input: FddOwnerReaderConfig): FddReader => {
  const config = normalizeOwnerReaderHttpConfig(input);
  return Object.freeze({
    async read(readInput: OwnerReadInput<FddFindingsReadRequest>): Promise<OwnerReadResult> {
      const query = readInput.request.input;
      const { scope } = readInput.context;
      if (query.siteId !== scope.siteId || scope.assetId !== null || scope.deviceId !== null) {
        throw new OwnerReadError('OWNER_REQUEST_INVALID', 'The FDD findings read is outside the trusted Site scope.');
      }
      // createOwnerHeaders refuses a read without its grant, so the grant is always there.
      const { 'X-Delegation-Grant': grant, ...headers } = createOwnerHeaders(readInput.request.requestId, readInput.context, {
        logicalTool: readInput.request.tool,
        includePolicyRevision: false,
        hasBody: false,
      }) as Record<string, string> & { readonly 'X-Delegation-Grant': string };
      const payload = await fetchOwnerJson(config, {
        path: `/v1/sites/${encodeURIComponent(query.siteId)}/fdd/findings?limit=${query.limit}`,
        method: 'GET',
        headers: { ...headers, 'X-FDD-Context': grant },
      });
      if (!isRecord(payload) || !Array.isArray(payload.items) || payload.items.length > query.limit) {
        invalid('FDD returned an invalid findings list.');
      }
      const result: FddFindingsResult = {
        items: ((payload as Record<string, unknown>).items as unknown[]).map((item) => decodeFinding(item, scope.tenantId, query.siteId)),
      };
      return {
        requestId: readInput.request.requestId,
        owner: 'fdd-service',
        scope: { ...scope },
        revision: 'fdd-findings',
        quality: 'GOOD',
        provenance: 'fdd-service:findings/v1',
        payload: result,
      };
    },
  });
};
