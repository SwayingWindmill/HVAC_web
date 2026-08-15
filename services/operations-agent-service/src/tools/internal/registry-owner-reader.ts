import { createHash } from 'node:crypto';

import {
  OwnerReadError,
  type OwnerReadInput,
  type OwnerReadResult,
  type RegistryReadRequest,
  type RegistryReader,
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

export interface RegistrySiteDto {
  readonly id: string;
  readonly tenantId: string;
  readonly code: string;
  readonly displayName: string;
  readonly timezone: string;
  readonly status: string;
  readonly revision: number;
  readonly createdAt: string;
  readonly updatedAt: string;
}

export interface RegistryEquipmentDto {
  readonly id: string;
  readonly tenantId: string;
  readonly siteId: string;
  readonly code: string;
  readonly displayName: string;
  readonly equipmentType: string;
  readonly status: string;
  readonly revision: number;
  readonly createdAt: string;
  readonly updatedAt: string;
}

export type RegistryOwnerPayload =
  | { readonly kind: 'SITE'; readonly site: RegistrySiteDto }
  | {
    readonly kind: 'SITE_EQUIPMENT';
    readonly siteId: string;
    readonly equipment: readonly RegistryEquipmentDto[];
  };

export type RegistryOwnerReaderConfig = OwnerReaderHttpConfig;

const siteKeys = [
  'id',
  'tenantId',
  'code',
  'displayName',
  'timezone',
  'status',
  'revision',
  'createdAt',
  'updatedAt',
] as const;

const equipmentKeys = [
  'id',
  'tenantId',
  'siteId',
  'code',
  'displayName',
  'equipmentType',
  'status',
  'revision',
  'createdAt',
  'updatedAt',
] as const;

const collectionKeys = ['items', 'nextCursor', 'hasMore'] as const;
const maximumPages = 10;
const pageLimit = 200;

const isRevision = (value: unknown): value is number => (
  typeof value === 'number' && Number.isSafeInteger(value) && value >= 0
);

const isUuidV7 = (value: unknown): value is string => (
  typeof value === 'string'
  && /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/iu.test(value)
);

const isTimezone = (value: unknown): value is string => {
  if (!isNonEmptyString(value)) return false;
  try {
    new Intl.DateTimeFormat('en-US', { timeZone: value }).format(0);
    return true;
  } catch {
    return false;
  }
};

const decodeSite = (value: unknown): RegistrySiteDto => {
  if (!isRecord(value)
    || !hasExactKeys(value, siteKeys)
    || !isUuidV7(value.id)
    || !isUuidV7(value.tenantId)
    || !isNonEmptyString(value.code)
    || !isNonEmptyString(value.displayName)
    || !isTimezone(value.timezone)
    || !isNonEmptyString(value.status)
    || !isRevision(value.revision)
    || !isInstant(value.createdAt)
    || !isInstant(value.updatedAt)
    || Date.parse(value.createdAt) > Date.parse(value.updatedAt)) {
    throw new OwnerReadError(
      'OWNER_RESPONSE_INVALID',
      'The Registry Owner returned an invalid Site representation.',
    );
  }
  return value as unknown as RegistrySiteDto;
};

const decodeEquipment = (value: unknown): RegistryEquipmentDto => {
  if (!isRecord(value)
    || !hasExactKeys(value, equipmentKeys)
    || !isUuidV7(value.id)
    || !isUuidV7(value.tenantId)
    || !isUuidV7(value.siteId)
    || !isNonEmptyString(value.code)
    || !isNonEmptyString(value.displayName)
    || !isNonEmptyString(value.equipmentType)
    || !isNonEmptyString(value.status)
    || !isRevision(value.revision)
    || !isInstant(value.createdAt)
    || !isInstant(value.updatedAt)
    || Date.parse(value.createdAt) > Date.parse(value.updatedAt)) {
    throw new OwnerReadError(
      'OWNER_RESPONSE_INVALID',
      'The Registry Owner returned an invalid Equipment representation.',
    );
  }
  return value as unknown as RegistryEquipmentDto;
};

const assertSiteScope = (
  input: OwnerReadInput<RegistryReadRequest>,
): { readonly tenantId: string; readonly siteId: string } => {
  const { scope } = input.context;
  const requestedSiteId = input.request.input.siteId;
  if (!isNonEmptyString(scope.tenantId)
    || !isNonEmptyString(scope.siteId)
    || scope.siteId !== requestedSiteId
    || scope.equipmentId !== null
    || scope.deviceId !== null) {
    throw new OwnerReadError(
      'OWNER_REQUEST_INVALID',
      'The Registry READ request is outside the authorized Site Scope.',
    );
  }
  return { tenantId: scope.tenantId, siteId: scope.siteId };
};

const siteResult = (
  input: OwnerReadInput<RegistryReadRequest>,
  site: RegistrySiteDto,
): OwnerReadResult => ({
  requestId: input.request.requestId,
  owner: 'registry',
  scope: { ...input.context.scope },
  revision: `registry-site:${site.revision}`,
  quality: 'GOOD',
  provenance: 'platform-core-service:registry-site/v1',
  payload: { kind: 'SITE', site } satisfies RegistryOwnerPayload,
});

const readSite = async (
  input: OwnerReadInput<RegistryReadRequest>,
  config: ReturnType<typeof normalizeOwnerReaderHttpConfig>,
): Promise<OwnerReadResult> => {
  const requestedScope = assertSiteScope(input);
  const payload = await fetchOwnerJson(config, {
    path: `/internal/v1/registry/sites/${encodeURIComponent(requestedScope.siteId)}`,
    method: 'GET',
    headers: createOwnerHeaders(input.request.requestId, input.context, {
      logicalTool: input.request.tool,
      includePolicyRevision: true,
      hasBody: false,
    }),
  });
  const site = decodeSite(payload);
  if (site.id !== requestedScope.siteId
    || site.tenantId !== requestedScope.tenantId) {
    throw new OwnerReadError(
      'OWNER_RESPONSE_INVALID',
      'The Registry Site identity does not match the authorized request.',
    );
  }
  return siteResult(input, site);
};

const decodeCollection = (value: unknown): {
  readonly items: readonly unknown[];
  readonly nextCursor: string | null;
  readonly hasMore: boolean;
} => {
  if (!isRecord(value)
    || !hasExactKeys(value, collectionKeys)
    || !Array.isArray(value.items)
    || value.items.length > pageLimit
    || typeof value.hasMore !== 'boolean'
    || (value.nextCursor !== null && !isNonEmptyString(value.nextCursor))
    || value.hasMore !== (value.nextCursor !== null)) {
    throw new OwnerReadError(
      'OWNER_RESPONSE_INVALID',
      'The Registry Owner returned an invalid collection page.',
    );
  }
  return {
    items: value.items,
    nextCursor: value.nextCursor as string | null,
    hasMore: value.hasMore,
  };
};

const readSiteEquipment = async (
  input: OwnerReadInput<RegistryReadRequest>,
  config: ReturnType<typeof normalizeOwnerReaderHttpConfig>,
): Promise<OwnerReadResult> => {
  const requestedScope = assertSiteScope(input);
  const equipment: RegistryEquipmentDto[] = [];
  const identities = new Set<string>();
  let cursor: string | null = null;
  let page = 0;
  do {
    page += 1;
    if (page > maximumPages) {
      throw new OwnerReadError(
        'OWNER_RESPONSE_TOO_LARGE',
        'The Registry Equipment collection exceeded the accepted page budget.',
      );
    }
    const query = new URLSearchParams({ limit: String(pageLimit) });
    if (cursor !== null) query.set('cursor', cursor);
    const payload = await fetchOwnerJson(config, {
      path: `/internal/v1/registry/sites/${encodeURIComponent(requestedScope.siteId)}/equipment?${query}`,
      method: 'GET',
      headers: createOwnerHeaders(
        page === 1 ? input.request.requestId : `${input.request.requestId}:page:${page}`,
        input.context,
        { logicalTool: input.request.tool, includePolicyRevision: true, hasBody: false },
      ),
    });
    const collection = decodeCollection(payload);
    for (const item of collection.items) {
      const decoded = decodeEquipment(item);
      if (decoded.tenantId !== requestedScope.tenantId
        || decoded.siteId !== requestedScope.siteId
        || identities.has(decoded.id)) {
        throw new OwnerReadError(
          'OWNER_RESPONSE_INVALID',
          'The Registry Equipment identity does not match the authorized request.',
        );
      }
      identities.add(decoded.id);
      equipment.push(decoded);
    }
    cursor = collection.hasMore ? collection.nextCursor : null;
  } while (cursor !== null);

  const collectionRevision = createHash('sha256')
    .update(JSON.stringify(
      [...equipment]
        .sort((left, right) => left.id.localeCompare(right.id))
        .map(({ id, revision }) => [id, revision]),
    ))
    .digest('hex');
  return {
    requestId: input.request.requestId,
    owner: 'registry',
    scope: { ...input.context.scope },
    revision: `registry-site-equipment:sha256:${collectionRevision}`,
    quality: 'GOOD',
    provenance: 'platform-core-service:registry-site-equipment/v1',
    payload: {
      kind: 'SITE_EQUIPMENT',
      siteId: requestedScope.siteId,
      equipment,
    } satisfies RegistryOwnerPayload,
  };
};

export const createRegistryOwnerReader = (
  input: RegistryOwnerReaderConfig,
): RegistryReader => {
  const config = normalizeOwnerReaderHttpConfig(input);
  const reader: RegistryReader = {
    async read(
      readInput: OwnerReadInput<RegistryReadRequest>,
    ): Promise<OwnerReadResult> {
      if (readInput.request.tool === 'registry.getSite') {
        return readSite(readInput, config);
      }
      return readSiteEquipment(readInput, config);
    },
  };
  return Object.freeze(reader);
};
