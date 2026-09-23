import type {
  DeviceObservationSnapshot,
  OpaqueRecoveryCursor,
  TelemetryKey,
} from '@/api/generated/s2Telemetry.gen';
import { cloneSnapshot, parseSnapshot, scopeKey } from './contract';

const storagePrefix = 'hvac:s2:telemetry-live:v1:';

export interface PersistedRecoveryRecord {
  schemaVersion: 1;
  clientSubscriptionId: string;
  deviceId: string;
  keys: TelemetryKey[];
  snapshot: DeviceObservationSnapshot;
  recoveryCursor: OpaqueRecoveryCursor;
  cursorExpiresAt: string;
  savedAt: string;
}

export interface RecoveryStore {
  load(target: { clientSubscriptionId: string; deviceId: string; keys: ReadonlyArray<string> }): PersistedRecoveryRecord | null;
  save(record: PersistedRecoveryRecord): void;
  remove(clientSubscriptionId: string): void;
  clear(): void;
}

function storageKey(clientSubscriptionId: string): string {
  return `${storagePrefix}${encodeURIComponent(clientSubscriptionId)}`;
}

function validateRecord(
  value: unknown,
  target: { clientSubscriptionId: string; deviceId: string; keys: ReadonlyArray<string> },
  now: Date,
): PersistedRecoveryRecord | null {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return null;
  const input = value as Record<string, unknown>;
  if (input.schemaVersion !== 1
    || input.clientSubscriptionId !== target.clientSubscriptionId
    || input.deviceId !== target.deviceId
    || !Array.isArray(input.keys)
    || input.keys.length !== target.keys.length
    || input.keys.some((key, index) => key !== target.keys[index])
    || typeof input.recoveryCursor !== 'string'
    || input.recoveryCursor.length === 0
    || typeof input.cursorExpiresAt !== 'string'
    || !Number.isFinite(Date.parse(input.cursorExpiresAt))
    || Date.parse(input.cursorExpiresAt) <= now.getTime()
    || typeof input.savedAt !== 'string'
    || !Number.isFinite(Date.parse(input.savedAt))) {
    return null;
  }
  try {
    const snapshot = parseSnapshot(input.snapshot, target.deviceId, target.keys);
    return {
      schemaVersion: 1,
      clientSubscriptionId: target.clientSubscriptionId,
      deviceId: target.deviceId,
      keys: [...target.keys] as TelemetryKey[],
      snapshot,
      recoveryCursor: input.recoveryCursor,
      cursorExpiresAt: input.cursorExpiresAt,
      savedAt: input.savedAt,
    };
  } catch {
    return null;
  }
}

export class BrowserRecoveryStore implements RecoveryStore {
  private readonly memory = new Map<string, string>();
  private readonly storage: Storage | null;
  private readonly now: () => Date;

  constructor(
    storage: Storage | null = typeof window === 'undefined' ? null : window.sessionStorage,
    now: () => Date = () => new Date(),
  ) {
    this.storage = storage;
    this.now = now;
  }

  load(target: { clientSubscriptionId: string; deviceId: string; keys: ReadonlyArray<string> }): PersistedRecoveryRecord | null {
    const key = storageKey(target.clientSubscriptionId);
    const serialized = this.read(key);
    if (!serialized) return null;
    try {
      const record = validateRecord(JSON.parse(serialized), target, this.now());
      if (!record || scopeKey(record.deviceId, record.keys) !== scopeKey(target.deviceId, target.keys)) {
        this.remove(target.clientSubscriptionId);
        return null;
      }
      return record;
    } catch {
      this.remove(target.clientSubscriptionId);
      return null;
    }
  }

  save(record: PersistedRecoveryRecord): void {
    const safeRecord: PersistedRecoveryRecord = {
      ...record,
      keys: [...record.keys],
      snapshot: cloneSnapshot(record.snapshot),
    };
    const serialized = JSON.stringify(safeRecord);
    const key = storageKey(record.clientSubscriptionId);
    this.memory.set(key, serialized);
    try {
      this.storage?.setItem(key, serialized);
    } catch {
      // The in-memory copy remains available for this page when storage is full or disabled.
    }
  }

  remove(clientSubscriptionId: string): void {
    const key = storageKey(clientSubscriptionId);
    this.memory.delete(key);
    try {
      this.storage?.removeItem(key);
    } catch {
      // Revocation cleanup remains complete for the in-memory state.
    }
  }

  clear(): void {
    this.memory.clear();
    if (!this.storage) return;
    try {
      const keys: string[] = [];
      for (let index = 0; index < this.storage.length; index += 1) {
        const key = this.storage.key(index);
        if (key?.startsWith(storagePrefix)) keys.push(key);
      }
      for (const key of keys) this.storage.removeItem(key);
    } catch {
      // Browser storage may be unavailable; all process-local state is already cleared.
    }
  }

  private read(key: string): string | null {
    try {
      const persisted = this.storage?.getItem(key);
      if (persisted) {
        this.memory.set(key, persisted);
        return persisted;
      }
    } catch {
      // Use the process-local copy when browser storage cannot be read.
    }
    return this.memory.get(key) ?? null;
  }
}
