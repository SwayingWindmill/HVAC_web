import type {
  ClientSubscriptionId,
  DeviceObservationSnapshot,
  TelemetryKey,
  UUIDv7,
} from '@/api/generated/s2Telemetry.gen';

export interface TelemetryLiveTarget {
  clientSubscriptionId: ClientSubscriptionId;
  deviceId: UUIDv7;
  keys: ReadonlyArray<TelemetryKey>;
}

export type TelemetryLiveUnavailableReason =
  | 'snapshot-unavailable'
  | 'transport-unavailable'
  | 'recovery-required'
  | 'protocol-violation';

interface TelemetryLiveStateBase {
  clientSubscriptionId: ClientSubscriptionId;
  deviceId: UUIDv7;
  keys: ReadonlyArray<TelemetryKey>;
  updatedAt: string;
}

export interface TelemetryLiveInitializingState extends TelemetryLiveStateBase {
  status: 'initializing';
  snapshot: null;
}

export interface TelemetryLiveSnapshotState extends TelemetryLiveStateBase {
  status: 'snapshot';
  snapshot: Readonly<DeviceObservationSnapshot>;
  reason: 'authoritative-snapshot' | 'recovering' | 'reconnecting';
}

export interface TelemetryLiveCurrentState extends TelemetryLiveStateBase {
  status: 'live';
  snapshot: Readonly<DeviceObservationSnapshot>;
  recovered: boolean;
}

export interface TelemetryLiveUnavailableState extends TelemetryLiveStateBase {
  status: 'unavailable';
  snapshot: Readonly<DeviceObservationSnapshot> | null;
  reason: TelemetryLiveUnavailableReason;
  retryable: boolean;
}

export interface TelemetryLiveRevokedState extends TelemetryLiveStateBase {
  status: 'revoked';
  snapshot: null;
}

export type TelemetryLiveState =
  | TelemetryLiveInitializingState
  | TelemetryLiveSnapshotState
  | TelemetryLiveCurrentState
  | TelemetryLiveUnavailableState
  | TelemetryLiveRevokedState;

export interface TelemetryLiveSession {
  getState(clientSubscriptionId: ClientSubscriptionId): TelemetryLiveState | undefined;
  getStates(): ReadonlyArray<TelemetryLiveState>;
  subscribe(listener: (states: ReadonlyArray<TelemetryLiveState>) => void): () => void;
  refresh(): Promise<void>;
  checkpoint(): Promise<void>;
  close(): void;
}

export interface TelemetryLiveClient {
  open(targets: ReadonlyArray<TelemetryLiveTarget>, options?: { signal?: AbortSignal }): Promise<TelemetryLiveSession>;
  purge(): void;
}
