import type {
  DeviceObservationPublication,
  DeviceObservationSnapshot,
  SubscriptionDescriptor,
  TelemetryKeyState,
  TransportPosition,
} from '@/api/generated/s2Telemetry.gen';
import {
  cloneSnapshot,
  parsePublication,
  parseSnapshot,
  TelemetryContractError,
} from './contract';
import type {
  TelemetryTransportPublication,
  TelemetryTransportSubscribed,
} from './transport-types';
import type {
  TelemetryLiveState,
  TelemetryLiveTarget,
  TelemetryLiveUnavailableReason,
} from './types';

const maximumBufferedPublications = 256;

export interface CheckpointCandidate {
  subscriptionId: string;
  businessRevision: number;
  transportPosition: TransportPosition;
  snapshot: DeviceObservationSnapshot;
}

interface MachineCallbacks {
  onStateChanged(): void;
  onSnapshotFallback(reason: TelemetryLiveUnavailableReason): void;
  onScopeViolation(): void;
}

interface BufferedPublication {
  data: unknown;
  position: TransportPosition | null;
}

function nowInstant(now: () => Date): string {
  return now().toISOString();
}

function cloneState(state: TelemetryLiveState): TelemetryLiveState {
  return state.snapshot === null
    ? { ...state, keys: [...state.keys] }
    : { ...state, keys: [...state.keys], snapshot: cloneSnapshot(state.snapshot as DeviceObservationSnapshot) };
}

function applyPublication(snapshot: DeviceObservationSnapshot, publication: DeviceObservationPublication): DeviceObservationSnapshot {
  const valueByKey = new Map(snapshot.values.map((value) => [value.key, value]));
  for (const change of publication.telemetryChanges) valueByKey.set(change.key, change);
  const values = snapshot.values.map((value) => valueByKey.get(value.key) as TelemetryKeyState);
  return {
    ...cloneSnapshot(snapshot),
    businessRevision: publication.revision,
    evaluatedAt: publication.evaluatedAt,
    evaluationAvailability: publication.evaluationAvailability,
    availabilityReasons: [...publication.availabilityReasons],
    presence: JSON.parse(JSON.stringify(publication.presence)) as DeviceObservationSnapshot['presence'],
    telemetryReadiness: publication.telemetryReadiness,
    displayState: publication.displayState,
    values: values.map((value) => JSON.parse(JSON.stringify(value)) as TelemetryKeyState),
  };
}

export class SubscriptionStateMachine {
  readonly target: TelemetryLiveTarget;
  readonly descriptor: SubscriptionDescriptor;
  private readonly callbacks: MachineCallbacks;
  private readonly now: () => Date;
  private readonly selectedKeys: ReadonlySet<string>;
  private state: TelemetryLiveState;
  private snapshot: DeviceObservationSnapshot | null = null;
  private buffer: BufferedPublication[] = [];
  private currentPosition: TransportPosition | null = null;
  private recoveryTarget: TransportPosition | null = null;
  private subscribed = false;
  private everSubscribed = false;
  private recoveryWasUsed = false;
  private fallbackPending = false;
  private closed = false;

  constructor(
    target: TelemetryLiveTarget,
    descriptor: SubscriptionDescriptor,
    callbacks: MachineCallbacks,
    now: () => Date,
  ) {
    this.target = {
      clientSubscriptionId: target.clientSubscriptionId,
      deviceId: target.deviceId,
      keys: [...target.keys],
    };
    this.descriptor = descriptor;
    this.callbacks = callbacks;
    this.now = now;
    this.selectedKeys = new Set(target.keys);
    this.state = {
      status: 'initializing',
      clientSubscriptionId: target.clientSubscriptionId,
      deviceId: target.deviceId,
      keys: [...target.keys],
      snapshot: null,
      updatedAt: nowInstant(now),
    };
  }

  getState(): TelemetryLiveState {
    return cloneState(this.state);
  }

  installPersistedSnapshot(value: unknown): void {
    if (this.closed || this.descriptor.recoveryMode !== 'ATTEMPT_RECOVERY') return;
    try {
      this.snapshot = parseSnapshot(value, this.target.deviceId, this.target.keys);
      this.state = {
        status: 'snapshot',
        clientSubscriptionId: this.target.clientSubscriptionId,
        deviceId: this.target.deviceId,
        keys: [...this.target.keys],
        snapshot: cloneSnapshot(this.snapshot),
        reason: 'recovering',
        updatedAt: nowInstant(this.now),
      };
      this.callbacks.onStateChanged();
    } catch (error) {
      this.handleContractError(error);
    }
  }

  installAuthoritativeSnapshot(value: unknown): void {
    if (this.closed) return;
    try {
      const candidate = parseSnapshot(value, this.target.deviceId, this.target.keys);
      if (this.snapshot && candidate.businessRevision < this.snapshot.businessRevision) return;
      this.snapshot = candidate;
      this.fallbackPending = false;
      this.recoveryTarget = null;
      const buffered = this.buffer;
      this.buffer = [];
      for (const publication of buffered) {
        if (!this.applyRawPublication(publication)) return;
      }
      this.state = {
        status: this.subscribed ? 'live' : 'snapshot',
        clientSubscriptionId: this.target.clientSubscriptionId,
        deviceId: this.target.deviceId,
        keys: [...this.target.keys],
        snapshot: cloneSnapshot(this.snapshot),
        ...(this.subscribed
          ? { recovered: false }
          : { reason: 'authoritative-snapshot' as const }),
        updatedAt: nowInstant(this.now),
      } as TelemetryLiveState;
      this.callbacks.onStateChanged();
    } catch (error) {
      this.handleContractError(error);
    }
  }

  onSubscribing(): void {
    if (this.closed) return;
    this.subscribed = false;
    if (this.snapshot) {
      this.state = {
        status: 'snapshot',
        clientSubscriptionId: this.target.clientSubscriptionId,
        deviceId: this.target.deviceId,
        keys: [...this.target.keys],
        snapshot: cloneSnapshot(this.snapshot),
        reason: this.everSubscribed ? 'reconnecting' : 'recovering',
        updatedAt: nowInstant(this.now),
      };
      this.callbacks.onStateChanged();
    }
  }

  onSubscribed(context: TelemetryTransportSubscribed): void {
    if (this.closed) return;
    this.subscribed = true;
    const reconnect = this.everSubscribed;
    this.everSubscribed = true;
    if (!context.recoverable || !context.positioned || context.position === null) {
      this.requestFallback('protocol-violation');
      return;
    }

    if (context.wasRecovering) {
      if (!context.recovered || this.snapshot === null) {
        this.requestFallback('recovery-required');
        return;
      }
      if (this.currentPosition && this.currentPosition.epoch !== context.position.epoch) {
        this.requestFallback('recovery-required');
        return;
      }
      if (!reconnect && this.descriptor.recoveryMode === 'ATTEMPT_RECOVERY') {
        const expected = this.descriptor.transportPosition;
        if (!expected || expected.epoch !== context.position.epoch || expected.offset > context.position.offset) {
          this.requestFallback('recovery-required');
          return;
        }
        this.currentPosition = { ...expected };
      }
      this.recoveryWasUsed = true;
      this.recoveryTarget = { ...context.position };
      if (!context.hasRecoveredPublications || this.currentPosition?.offset === context.position.offset) {
        this.markLive(true);
      }
      return;
    }

    if (reconnect || (this.descriptor.recoveryMode === 'ATTEMPT_RECOVERY' && !reconnect)) {
      this.requestFallback('recovery-required');
      return;
    }
    this.currentPosition = { ...context.position };
    if (this.snapshot) this.markLive(false);
  }

  onPublication(context: TelemetryTransportPublication): void {
    if (this.closed) return;
    if (this.snapshot === null) {
      if (this.buffer.length >= maximumBufferedPublications) {
        this.buffer = [];
        this.requestFallback('recovery-required');
        return;
      }
      this.buffer.push({ data: context.data, position: context.position ? { ...context.position } : null });
      return;
    }
    this.applyRawPublication({ data: context.data, position: context.position ? { ...context.position } : null });
  }

  onTransportDisconnected(): void {
    if (this.closed) return;
    this.subscribed = false;
    if (this.snapshot) {
      this.state = {
        status: 'snapshot',
        clientSubscriptionId: this.target.clientSubscriptionId,
        deviceId: this.target.deviceId,
        keys: [...this.target.keys],
        snapshot: cloneSnapshot(this.snapshot),
        reason: 'reconnecting',
        updatedAt: nowInstant(this.now),
      };
      this.callbacks.onStateChanged();
    }
  }

  markUnavailable(reason: TelemetryLiveUnavailableReason, retryable = true): void {
    if (this.closed || this.state.status === 'revoked') return;
    this.state = {
      status: 'unavailable',
      clientSubscriptionId: this.target.clientSubscriptionId,
      deviceId: this.target.deviceId,
      keys: [...this.target.keys],
      snapshot: this.snapshot ? cloneSnapshot(this.snapshot) : null,
      reason,
      retryable,
      updatedAt: nowInstant(this.now),
    };
    this.callbacks.onStateChanged();
  }

  revoke(): void {
    if (this.closed) return;
    this.closed = true;
    this.snapshot = null;
    this.buffer = [];
    this.currentPosition = null;
    this.recoveryTarget = null;
    this.state = {
      status: 'revoked',
      clientSubscriptionId: this.target.clientSubscriptionId,
      deviceId: this.target.deviceId,
      keys: [...this.target.keys],
      snapshot: null,
      updatedAt: nowInstant(this.now),
    };
    this.callbacks.onStateChanged();
  }

  checkpointCandidate(): CheckpointCandidate | null {
    if (this.closed || this.state.status !== 'live' || !this.snapshot || !this.currentPosition) return null;
    return {
      subscriptionId: this.descriptor.subscriptionId,
      businessRevision: this.snapshot.businessRevision,
      transportPosition: { ...this.currentPosition },
      snapshot: cloneSnapshot(this.snapshot),
    };
  }

  close(): void {
    this.closed = true;
    this.buffer = [];
  }

  private applyRawPublication(context: BufferedPublication): boolean {
    if (!this.snapshot) return false;
    let publication: DeviceObservationPublication;
    try {
      publication = parsePublication(
        context.data,
        this.descriptor.subscriptionId,
        this.target.deviceId,
        this.selectedKeys,
      );
    } catch (error) {
      this.handleContractError(error);
      return false;
    }

    if (publication.revision !== publication.previousRevision + 1) {
      this.requestFallback('protocol-violation');
      return false;
    }
    if (context.position === null) {
      this.requestFallback('protocol-violation');
      return false;
    }
    if (this.currentPosition) {
      if (this.currentPosition.epoch !== context.position.epoch || context.position.offset <= this.currentPosition.offset) {
        if (publication.revision <= this.snapshot.businessRevision) return true;
        this.requestFallback('recovery-required');
        return false;
      }
    }

    if (publication.revision <= this.snapshot.businessRevision) {
      this.currentPosition = { ...context.position };
      this.maybeFinishRecovery();
      return true;
    }
    if (publication.previousRevision !== this.snapshot.businessRevision) {
      this.requestFallback('recovery-required');
      return false;
    }

    this.snapshot = applyPublication(this.snapshot, publication);
    this.currentPosition = { ...context.position };
    if (this.state.status === 'live') {
      this.state = {
        ...this.state,
        snapshot: cloneSnapshot(this.snapshot),
        updatedAt: nowInstant(this.now),
      };
      this.callbacks.onStateChanged();
    }
    this.maybeFinishRecovery();
    return true;
  }

  private maybeFinishRecovery(): void {
    if (!this.recoveryTarget || !this.currentPosition || !this.snapshot) return;
    if (this.currentPosition.epoch !== this.recoveryTarget.epoch) {
      this.requestFallback('recovery-required');
      return;
    }
    if (this.currentPosition.offset >= this.recoveryTarget.offset) this.markLive(true);
  }

  private markLive(recovered: boolean): void {
    if (!this.snapshot || this.closed) return;
    this.recoveryTarget = null;
    this.fallbackPending = false;
    this.state = {
      status: 'live',
      clientSubscriptionId: this.target.clientSubscriptionId,
      deviceId: this.target.deviceId,
      keys: [...this.target.keys],
      snapshot: cloneSnapshot(this.snapshot),
      recovered: recovered || this.recoveryWasUsed,
      updatedAt: nowInstant(this.now),
    };
    this.callbacks.onStateChanged();
  }

  private requestFallback(reason: TelemetryLiveUnavailableReason): void {
    if (this.closed || this.fallbackPending) return;
    this.fallbackPending = true;
    this.markUnavailable(reason, true);
    this.callbacks.onSnapshotFallback(reason);
  }

  private handleContractError(error: unknown): void {
    if (error instanceof TelemetryContractError && error.category === 'scope') {
      this.callbacks.onScopeViolation();
      return;
    }
    this.requestFallback('protocol-violation');
  }
}
