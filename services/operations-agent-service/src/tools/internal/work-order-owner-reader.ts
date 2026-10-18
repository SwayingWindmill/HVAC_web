import {
  OwnerReadError,
  type OwnerReadInput,
  type OwnerReadResult,
  type WorkOrderListReadRequest,
  type WorkOrderListResult,
  type WorkOrderReader,
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

export type WorkOrderOwnerReaderConfig = OwnerReaderHttpConfig;

const OWNER_LIST_SCHEMA = 1;
const priorities = new Set(['URGENT', 'HIGH', 'MEDIUM', 'LOW']);
const statuses = new Set(['DRAFT', 'OPEN', 'IN_PROGRESS', 'BLOCKED', 'COMPLETED', 'CANCELLED']);

const invalid = (message: string): never => {
  throw new OwnerReadError('OWNER_RESPONSE_INVALID', message);
};

// Only what the Tool says to an operator is kept: no identifiers, assignees or timeline actors.
const decodeWorkOrder = (value: unknown, tenantId: string, siteId: string): WorkOrderListResult['items'][number] => {
  if (!isRecord(value) || value.tenantId !== tenantId || value.siteId !== siteId
    || !isNonEmptyString(value.title) || typeof value.description !== 'string'
    || !priorities.has(String(value.priority)) || !statuses.has(String(value.status))
    || !isInstant(value.createdAt) || !isInstant(value.updatedAt)
    || (value.dueAt !== undefined && !isInstant(value.dueAt)) || !Array.isArray(value.sourceReferences)) {
    invalid('The work-order owner returned an invalid work order.');
  }
  const workOrder = value as Record<string, unknown>;
  return {
    title: workOrder.title as string,
    description: workOrder.description as string,
    priority: workOrder.priority as WorkOrderListResult['items'][number]['priority'],
    status: workOrder.status as WorkOrderListResult['items'][number]['status'],
    assigned: workOrder.assigneeId !== undefined || workOrder.teamId !== undefined,
    ...(workOrder.dueAt === undefined ? {} : { dueAt: workOrder.dueAt as string }),
    sources: (workOrder.sourceReferences as unknown[]).flatMap((source) => (isRecord(source) && isNonEmptyString(source.domain) ? [source.domain] : [])),
    createdAt: workOrder.createdAt as string,
    updatedAt: workOrder.updatedAt as string,
  };
};

/**
 * Lists a Site's work orders from the work-order owner under the context the Gateway signed for
 * the Agent (#595). The owner takes that context in its own header and refuses a delegation header.
 */
export const createWorkOrderOwnerReader = (input: WorkOrderOwnerReaderConfig): WorkOrderReader => {
  const config = normalizeOwnerReaderHttpConfig(input);
  return Object.freeze({
    async read(readInput: OwnerReadInput<WorkOrderListReadRequest>): Promise<OwnerReadResult> {
      const query = readInput.request.input;
      const { scope } = readInput.context;
      if (query.siteId !== scope.siteId || scope.assetId !== null || scope.deviceId !== null) {
        throw new OwnerReadError('OWNER_REQUEST_INVALID', 'The work-order list is outside the trusted Site scope.');
      }
      // createOwnerHeaders refuses a read without its grant, so the grant is always there.
      const { 'X-Delegation-Grant': grant, ...headers } = createOwnerHeaders(readInput.request.requestId, readInput.context, {
        logicalTool: readInput.request.tool,
        includePolicyRevision: false,
        hasBody: false,
      }) as Record<string, string> & { readonly 'X-Delegation-Grant': string };
      const payload = await fetchOwnerJson(config, {
        path: `/internal/v1/sites/${encodeURIComponent(query.siteId)}/work-orders?limit=${query.limit}`,
        method: 'GET',
        headers: { ...headers, 'X-Work-Order-Read-Context': grant },
      });
      // The owner's list envelope is schema 1 (libs/workordermodel).
      if (!isRecord(payload) || payload.schemaVersion !== OWNER_LIST_SCHEMA || !Array.isArray(payload.items)
        || payload.items.length > query.limit || typeof payload.hasMore !== 'boolean') {
        invalid('The work-order owner returned an invalid list.');
      }
      const list = payload as Record<string, unknown>;
      const result: WorkOrderListResult = {
        items: (list.items as unknown[]).map((item) => decodeWorkOrder(item, scope.tenantId, query.siteId)),
        hasMore: list.hasMore as boolean,
      };
      return {
        requestId: readInput.request.requestId,
        owner: 'work-order-service',
        scope: { ...scope },
        revision: 'work-order-list',
        quality: 'GOOD',
        provenance: 'work-order-service:work-order-list/v1',
        payload: result,
      };
    },
  });
};
