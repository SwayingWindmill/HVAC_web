import {
  OwnerReadError,
  type OwnerReadInput,
  type OwnerReadResult,
  type SemanticQueryReadRequest,
  type SemanticQueryReader,
  type SemanticQueryResult,
} from '../../application/index.js';
import {
  createOwnerHeaders,
  fetchOwnerJson,
  hasExactKeys,
  isInstant,
  isNonEmptyString,
  isRecord,
  normalizeOwnerReaderHttpConfig,
  type OwnerReaderHttpConfig,
} from './owner-http.js';

export type SemanticQueryOwnerReaderConfig = OwnerReaderHttpConfig;

const maximumRows = 2_000;
const columnKinds = new Set(['time', 'dimension', 'measure']);

const invalid = (message: string): never => {
  throw new OwnerReadError('OWNER_RESPONSE_INVALID', message);
};

const decodeColumns = (value: unknown): SemanticQueryResult['columns'] => {
  if (!Array.isArray(value) || value.length < 2) invalid('The semantic query owner returned invalid columns.');
  return (value as unknown[]).map((column) => {
    if (!isRecord(column) || !hasExactKeys(column, ['name', 'title', 'kind'])
      || !isNonEmptyString(column.name) || typeof column.title !== 'string' || !columnKinds.has(String(column.kind))) {
      invalid('The semantic query owner returned an invalid column.');
    }
    return column as unknown as SemanticQueryResult['columns'][number];
  });
};

const decodeRows = (value: unknown, columns: SemanticQueryResult['columns']): SemanticQueryResult['rows'] => {
  if (!Array.isArray(value) || value.length > maximumRows) invalid('The semantic query owner returned invalid rows.');
  return (value as unknown[]).map((row) => {
    if (!Array.isArray(row) || row.length !== columns.length) invalid('The semantic query owner returned a row of the wrong width.');
    (row as unknown[]).forEach((cell, index) => {
      const kind = columns[index]?.kind;
      const valid = kind === 'time' ? typeof cell === 'string' && isInstant(cell)
        : kind === 'dimension' ? cell === null || typeof cell === 'string'
          : cell === null || (typeof cell === 'number' && Number.isFinite(cell));
      if (!valid) invalid('The semantic query owner returned an invalid cell.');
    });
    return row as SemanticQueryResult['rows'][number];
  });
};

const decodeMetadata = (value: unknown): SemanticQueryResult['metadata'] => {
  if (!isRecord(value)
    || !(hasExactKeys(value, ['datasetRevision', 'partial']) || hasExactKeys(value, ['dataWatermark', 'datasetRevision', 'partial']))
    || !isNonEmptyString(value.datasetRevision) || typeof value.partial !== 'boolean'
    || (value.dataWatermark !== undefined && !(typeof value.dataWatermark === 'string' && isInstant(value.dataWatermark)))) {
    invalid('The semantic query owner returned invalid metadata.');
  }
  return value as unknown as SemanticQueryResult['metadata'];
};

const decodeResult = (value: unknown): SemanticQueryResult => {
  if (!isRecord(value) || !hasExactKeys(value, ['schemaVersion', 'columns', 'rows', 'metadata']) || value.schemaVersion !== 1) {
    invalid('The semantic query owner returned an invalid response.');
  }
  const record = value as Record<string, unknown>;
  const columns = decodeColumns(record.columns);
  return { schemaVersion: 1, columns, rows: decodeRows(record.rows, columns), metadata: decodeMetadata(record.metadata) };
};

// Reads the Telemetry Query Service's semantic query (#573) under the exact grant the Gateway issued.
export const createSemanticQueryOwnerReader = (input: SemanticQueryOwnerReaderConfig): SemanticQueryReader => {
  const config = normalizeOwnerReaderHttpConfig(input);
  return Object.freeze({
    async read(readInput: OwnerReadInput<SemanticQueryReadRequest>): Promise<OwnerReadResult> {
      const query = readInput.request.input;
      const { scope } = readInput.context;
      if (query.tenantId !== scope.tenantId || query.siteId !== scope.siteId || scope.assetId !== null || scope.deviceId !== null) {
        throw new OwnerReadError('OWNER_REQUEST_INVALID', 'The semantic query is outside the trusted Site scope.');
      }
      const payload = await fetchOwnerJson(config, {
        path: '/internal/v1/analytics/semantic-query',
        method: 'POST',
        headers: createOwnerHeaders(readInput.request.requestId, readInput.context, {
          logicalTool: readInput.request.tool,
          includePolicyRevision: false,
          hasBody: true,
        }),
        body: JSON.stringify(query),
      });
      const result = decodeResult(payload);
      return {
        requestId: readInput.request.requestId,
        owner: 'telemetry-query-service',
        scope: { ...readInput.context.scope },
        revision: result.metadata.datasetRevision,
        quality: result.metadata.partial ? 'UNCERTAIN' : 'GOOD',
        provenance: 'telemetry-query-service:semantic-query/v1',
        payload: result,
      };
    },
  });
};
