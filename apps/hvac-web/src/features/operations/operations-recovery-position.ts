export interface OperationsInvestigationRecoveryScope {
  readonly tenantId: string;
  readonly siteId: string;
  readonly investigationId: string;
}

export interface OperationsInvestigationRecoveryPositionStore {
  load(scope: OperationsInvestigationRecoveryScope): string | undefined;
  save(scope: OperationsInvestigationRecoveryScope, position: string): void;
  clear(scope: OperationsInvestigationRecoveryScope): void;
  clearSite?(scope: Pick<OperationsInvestigationRecoveryScope, 'tenantId' | 'siteId'>): void;
}

interface RecoveryStorage {
  readonly length: number;
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
  key(index: number): string | null;
}

const recoveryPositionPattern = /^(0|[1-9]\d*):(0|[1-9]\d*)$/u;
const recoveryStoragePrefix = 'hvac.operations.recovery-position.v1';

export const normalizeOperationsRecoveryPosition = (value: unknown): string | undefined => {
  if (typeof value !== 'string') return undefined;
  const normalized = value.trim();
  return normalized.length <= 128 && recoveryPositionPattern.test(normalized)
    ? normalized
    : undefined;
};

const recoveryStorageKey = (scope: OperationsInvestigationRecoveryScope): string => [
  recoveryStoragePrefix,
  encodeURIComponent(scope.tenantId),
  encodeURIComponent(scope.siteId),
  encodeURIComponent(scope.investigationId),
].join(':');

const recoveryStorageSitePrefix = (
  scope: Pick<OperationsInvestigationRecoveryScope, 'tenantId' | 'siteId'>,
): string => `${recoveryStoragePrefix}:${encodeURIComponent(scope.tenantId)}:${encodeURIComponent(scope.siteId)}:`;

const browserSessionStorage = (): RecoveryStorage | null => {
  try {
    return typeof globalThis.sessionStorage === 'undefined' ? null : globalThis.sessionStorage;
  } catch {
    return null;
  }
};

export const createOperationsInvestigationRecoveryPositionStore = (
  storage: RecoveryStorage | null = browserSessionStorage(),
): OperationsInvestigationRecoveryPositionStore => Object.freeze({
  load(scope: OperationsInvestigationRecoveryScope) {
    if (storage === null) return undefined;
    const key = recoveryStorageKey(scope);
    try {
      const stored = storage.getItem(key);
      const normalized = normalizeOperationsRecoveryPosition(stored);
      if (stored !== null && normalized === undefined) storage.removeItem(key);
      return normalized;
    } catch {
      return undefined;
    }
  },
  save(scope: OperationsInvestigationRecoveryScope, position: string) {
    if (storage === null) return;
    const normalized = normalizeOperationsRecoveryPosition(position);
    if (normalized === undefined) return;
    try {
      storage.setItem(recoveryStorageKey(scope), normalized);
    } catch {
      // Recovery metadata must never make the authoritative stream fail.
    }
  },
  clear(scope: OperationsInvestigationRecoveryScope) {
    if (storage === null) return;
    try {
      storage.removeItem(recoveryStorageKey(scope));
    } catch {
      // Protected-state purge remains best effort when browser storage is unavailable.
    }
  },
  clearSite(scope: Pick<OperationsInvestigationRecoveryScope, 'tenantId' | 'siteId'>) {
    if (storage === null) return;
    const prefix = recoveryStorageSitePrefix(scope);
    try {
      const keys = Array.from({ length: storage.length }, (_value, index) => storage.key(index))
        .filter((key): key is string => key !== null && key.startsWith(prefix));
      for (const key of keys) storage.removeItem(key);
    } catch {
      // Site-level protected-state purge remains best effort when browser storage is unavailable.
    }
  },
});
