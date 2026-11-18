// Conditional ETag cache for the Registry Site asset model, keyed by (tenantId, siteId).
// Pure TypeScript with injectable storage so Node unit tests run without WeChat globals.

import { GatewayError, type ConditionalGetOutcome, type Gateway } from './gateway';
import type { AssetModel } from './plant';

export interface RawTelemetryPointRecord {
  readonly id: string;
  readonly reportingDeviceId: string;
  readonly pointCode: string;
  readonly sourceKey: string;
  readonly displayName: string;
  readonly pointType: string;
  readonly valueType: string;
  readonly unit?: string | null;
  readonly writable?: boolean;
  readonly status: string;
  readonly sourceMetadata?: Readonly<Record<string, unknown>> | null;
}

export interface RawAssetRelationshipRecord {
  readonly id: string;
  readonly fromType: string;
  readonly fromId: string;
  readonly toType: string;
  readonly toId: string;
  readonly role?: string;
  readonly status: string;
  readonly validFrom: string;
  readonly validTo?: string | null;
  readonly revision: number;
}

export interface RawSiteAssetModel extends AssetModel {
  readonly assets: readonly {
    readonly id: string;
    readonly code?: string;
    readonly displayName: string;
    readonly assetType: string;
    readonly status?: string;
  }[];
  readonly devices: readonly {
    readonly id: string;
    readonly code?: string;
    readonly displayName: string;
    readonly deviceType: string;
    readonly status?: string;
  }[];
  readonly telemetryPoints: readonly RawTelemetryPointRecord[];
  readonly relationships: readonly RawAssetRelationshipRecord[];
}

export interface AssetModelStorage {
  get(key: string): unknown;
  set(key: string, value: unknown): void;
  remove(key: string): void;
  listKeys?(): readonly string[];
}

interface CachedAssetModelEntry {
  readonly tenantId: string;
  readonly siteId: string;
  readonly sessionToken: string | null;
  readonly etag: string | null;
  readonly model: RawSiteAssetModel;
}

export const ASSET_MODEL_STORAGE_PREFIX = 'hvac.asset-model.v1:';
const STORAGE_INDEX_KEY = 'hvac.asset-model.v1.keys';

const isRecord = (value: unknown): value is Record<string, unknown> => (
  typeof value === 'object' && value !== null && !Array.isArray(value)
);

const isCachedAssetModelEntry = (
  value: unknown,
  tenantId: string,
  siteId: string,
): value is CachedAssetModelEntry => {
  if (!isRecord(value)) return false;
  if (value.tenantId !== tenantId || value.siteId !== siteId) return false;
  if (typeof value.etag !== 'string' || value.etag.length === 0) return false;
  if (value.sessionToken !== undefined && value.sessionToken !== null && typeof value.sessionToken !== 'string') {
    return false;
  }
  return isRecord(value.model)
    && Array.isArray(value.model.assets)
    && Array.isArray(value.model.devices)
    && Array.isArray(value.model.telemetryPoints)
    && Array.isArray(value.model.relationships);
};

export function createAssetModelStore(options: {
  readonly client: Pick<Gateway, 'getConditional'>;
  readonly storage: AssetModelStorage;
  readonly sessionToken?: () => string | null;
}) {
  const { client, storage, sessionToken } = options;
  const memory = new Map<string, CachedAssetModelEntry>();
  const inFlight = new Map<string, Promise<RawSiteAssetModel>>();

  const currentSession = (): string | null => sessionToken?.() ?? null;
  const entryKey = (tenantId: string, siteId: string) => `${tenantId}:${siteId}`;
  const storageKey = (tenantId: string, siteId: string) => `${ASSET_MODEL_STORAGE_PREFIX}${tenantId}:${siteId}`;

  const readStoredIndex = (): string[] => {
    const raw = storage.get(STORAGE_INDEX_KEY);
    if (!Array.isArray(raw)) return [];
    return raw.filter((item): item is string => typeof item === 'string' && item.startsWith(ASSET_MODEL_STORAGE_PREFIX));
  };

  const trackStorageKey = (key: string) => {
    const existing = readStoredIndex();
    if (existing.includes(key)) return;
    storage.set(STORAGE_INDEX_KEY, [...existing, key]);
  };

  const untrackStorageKey = (key: string) => {
    const existing = readStoredIndex();
    if (!existing.includes(key)) return;
    const next = existing.filter((candidate) => candidate !== key);
    if (next.length === 0) {
      storage.remove(STORAGE_INDEX_KEY);
    } else {
      storage.set(STORAGE_INDEX_KEY, next);
    }
  };

  const readStoredEntry = (tenantId: string, siteId: string): CachedAssetModelEntry | null => {
    const raw = storage.get(storageKey(tenantId, siteId));
    if (!isCachedAssetModelEntry(raw, tenantId, siteId)) return null;
    if (sessionToken && (raw.sessionToken ?? null) !== currentSession()) return null;
    return raw;
  };

  const dropEntry = (tenantId: string, siteId: string) => {
    memory.delete(entryKey(tenantId, siteId));
    const sKey = storageKey(tenantId, siteId);
    storage.remove(sKey);
    untrackStorageKey(sKey);
  };

  const storeEntry = (tenantId: string, siteId: string, etag: string | null, model: RawSiteAssetModel) => {
    const entry: CachedAssetModelEntry = { tenantId, siteId, sessionToken: currentSession(), etag, model };
    memory.set(entryKey(tenantId, siteId), entry);
    const sKey = storageKey(tenantId, siteId);
    if (etag !== null && etag.length > 0) {
      storage.set(sKey, entry);
      trackStorageKey(sKey);
    } else {
      storage.remove(sKey);
      untrackStorageKey(sKey);
    }
  };

  const fetchWithRevalidation = async (tenantId: string, siteId: string): Promise<RawSiteAssetModel> => {
    const key = entryKey(tenantId, siteId);
    const activeSession = currentSession();
    const mem = memory.get(key);
    const baseline = (mem && (!sessionToken || mem.sessionToken === activeSession) ? mem : null)
      ?? readStoredEntry(tenantId, siteId);
    const ifNoneMatch = baseline?.etag ?? null;
    const path = `/api/v1/sites/${encodeURIComponent(siteId)}/asset-model`;

    let outcome: ConditionalGetOutcome<RawSiteAssetModel>;
    try {
      outcome = await client.getConditional<RawSiteAssetModel>(path, ifNoneMatch);
    } catch (error) {
      if (error instanceof GatewayError && (error.status === 401 || error.status === 403 || error.status === 404)) {
        dropEntry(tenantId, siteId);
      }
      throw error;
    }

    if (outcome.status === 304) {
      if (baseline !== undefined && baseline !== null) {
        const nextEtag = outcome.etag ?? baseline.etag;
        storeEntry(tenantId, siteId, nextEtag, baseline.model);
        return baseline.model;
      }
      const fallback = await client.getConditional<RawSiteAssetModel>(path, null);
      if (fallback.status !== 200) {
        throw new GatewayError('ASSET_MODEL_MISSING', 500);
      }
      storeEntry(tenantId, siteId, fallback.etag, fallback.data);
      return fallback.data;
    }

    storeEntry(tenantId, siteId, outcome.etag, outcome.data);
    return outcome.data;
  };

  const read = (tenantId: string, siteId: string, fresh = false): Promise<RawSiteAssetModel> => {
    const key = entryKey(tenantId, siteId);
    const activeSession = currentSession();
    if (!fresh) {
      const mem = memory.get(key);
      if (mem !== undefined) {
        if (!sessionToken || mem.sessionToken === activeSession) {
          return Promise.resolve(mem.model);
        }
        dropEntry(tenantId, siteId);
      }
    }
    const flightKey = `${key}:${fresh ? '1' : '0'}`;
    const existing = inFlight.get(flightKey);
    if (existing !== undefined) return existing;

    const promise = fetchWithRevalidation(tenantId, siteId).finally(() => {
      if (inFlight.get(flightKey) === promise) inFlight.delete(flightKey);
    });
    inFlight.set(flightKey, promise);
    return promise;
  };

  const clear = () => {
    const keys = new Set<string>(readStoredIndex());
    for (const [k] of memory) {
      keys.add(`${ASSET_MODEL_STORAGE_PREFIX}${k}`);
    }
    for (const candidate of storage.listKeys?.() ?? []) {
      if (candidate.startsWith(ASSET_MODEL_STORAGE_PREFIX)) keys.add(candidate);
    }
    memory.clear();
    inFlight.clear();
    for (const key of keys) storage.remove(key);
    storage.remove(STORAGE_INDEX_KEY);
  };

  const peekCachedEtag = (tenantId: string, siteId: string): string | null => (
    memory.get(entryKey(tenantId, siteId))?.etag ?? readStoredEntry(tenantId, siteId)?.etag ?? null
  );

  return { read, clear, peekCachedEtag };
}
