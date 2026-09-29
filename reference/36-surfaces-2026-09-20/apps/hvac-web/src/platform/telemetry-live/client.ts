import {
  createS2TelemetryClient,
  S2TelemetryClientError,
  type ClientSubscriptionId,
  type OpaqueRecoveryCursor,
  type RecoveryCursorCheckpointResult,
  type S2TelemetryClient,
  type SubscriptionBootstrapResponse,
  type SubscriptionDescriptor,
  type TelemetryKey,
} from '@/api/generated/s2Telemetry.gen';
import {
  createPlatformGatewayClient,
  type PlatformGatewayClient,
} from '@/api/generated/platformGateway.gen';
import { normalizeTarget } from './contract';
import { CentrifugoTransportFactory } from './centrifugo-transport';
import { SubscriptionStateMachine, type CheckpointCandidate } from './state-machine';
import { BrowserRecoveryStore, type RecoveryStore } from './storage';
import type {
  TelemetryTransportConnection,
  TelemetryTransportFactory,
  TelemetryTransportSubscription,
} from './transport-types';
import type {
  TelemetryLiveClient,
  TelemetryLiveSession,
  TelemetryLiveState,
  TelemetryLiveTarget,
  TelemetryLiveUnavailableReason,
} from './types';

const maximumSubscriptions = 100;
const maximumKeysPerSubscription = 64;
const maximumTotalKeySelections = 2048;
const maximumConnectionCapabilityLifetimeMs = 300_000;
const maximumRecoveryCursorLifetimeMs = 120_000;
const checkpointIntervalMs = 30_000;

interface LiveClientDependencies {
  telemetry: S2TelemetryClient;
  platform: Pick<PlatformGatewayClient, 'getCurrentPrincipal'>;
  transportFactory: TelemetryTransportFactory;
  recoveryStore: RecoveryStore;
  now: () => Date;
  setInterval: typeof globalThis.setInterval;
  clearInterval: typeof globalThis.clearInterval;
}

interface ActiveSubscription {
  target: TelemetryLiveTarget;
  descriptor: SubscriptionDescriptor;
  machine: SubscriptionStateMachine;
  transport: TelemetryTransportSubscription | null;
}

function cloneTarget(target: TelemetryLiveTarget): TelemetryLiveTarget {
  return {
    clientSubscriptionId: target.clientSubscriptionId,
    deviceId: target.deviceId,
    keys: [...target.keys],
  };
}

function stableStates(active: ReadonlyArray<ActiveSubscription>): TelemetryLiveState[] {
  return active.map((item) => item.machine.getState());
}

function exactArray(left: ReadonlyArray<string>, right: ReadonlyArray<string>): boolean {
  return left.length === right.length && left.every((value, index) => value === right[index]);
}

function isRevocation(code: number, reason: string): boolean {
  return code === 2501 || /revok|scope|permission|authoriz/i.test(reason);
}

function validateBootstrap(
  response: SubscriptionBootstrapResponse,
  targets: ReadonlyArray<TelemetryLiveTarget>,
  now: Date,
): SubscriptionBootstrapResponse {
  if (response.schemaVersion !== 1 || response.transportProtocol !== 'CENTRIFUGO_JSON_V1') {
    throw new Error('Telemetry bootstrap contract version is unsupported');
  }
  const endpoint = new URL(response.endpoint);
  const loopback = endpoint.hostname === '127.0.0.1' || endpoint.hostname === 'localhost' || endpoint.hostname === '::1';
  if (endpoint.protocol !== 'wss:' && !(endpoint.protocol === 'ws:' && loopback)) {
    throw new Error('Telemetry bootstrap returned an insecure endpoint');
  }
  const expiresAt = Date.parse(response.expiresAt);
  if (!response.connectionToken
    || !Number.isFinite(expiresAt)
    || expiresAt <= now.getTime()
    || expiresAt > now.getTime() + maximumConnectionCapabilityLifetimeMs) {
    throw new Error('Telemetry bootstrap returned an invalid connection capability');
  }
  if (response.limits.maxSubscriptions !== maximumSubscriptions
    || response.limits.maxKeysPerSubscription !== maximumKeysPerSubscription
    || response.limits.maxTotalKeySelections !== maximumTotalKeySelections) {
    throw new Error('Telemetry bootstrap limits drifted');
  }
  if (response.subscriptions.length !== targets.length) throw new Error('Telemetry bootstrap returned a partial capability set');
  const seenSubscriptionIds = new Set<string>();
  const seenChannels = new Set<string>();
  response.subscriptions.forEach((descriptor, index) => {
    const target = targets[index];
    if (descriptor.clientSubscriptionId !== target.clientSubscriptionId
      || descriptor.deviceId !== target.deviceId
      || !exactArray(descriptor.keys, target.keys)) {
      throw new Error('Telemetry bootstrap scope/order drifted');
    }
    if (seenSubscriptionIds.has(descriptor.subscriptionId) || seenChannels.has(descriptor.channel)) {
      throw new Error('Telemetry bootstrap returned duplicate capabilities');
    }
    seenSubscriptionIds.add(descriptor.subscriptionId);
    seenChannels.add(descriptor.channel);
    if (descriptor.recoveryMode === 'SNAPSHOT_THEN_LIVE') {
      if (descriptor.transportPosition !== null || descriptor.recoveryCursor !== null) {
        throw new Error('New telemetry subscription contained recovery authority');
      }
    } else if (!descriptor.transportPosition || !descriptor.recoveryCursor) {
      throw new Error('Recovery subscription omitted its owner-issued position/cursor');
    }
  });
  return response;
}

class LiveSession implements TelemetryLiveSession {
  private readonly dependencies: LiveClientDependencies;
  private readonly targets: TelemetryLiveTarget[];
  private readonly onClosed: () => void;
  private readonly active: ActiveSubscription[] = [];
  private readonly listeners = new Set<(states: ReadonlyArray<TelemetryLiveState>) => void>();
  private transport: TelemetryTransportConnection | null = null;
  private interval: ReturnType<typeof globalThis.setInterval> | null = null;
  private closed = false;
  private refreshCapabilityPromise: Promise<string> | null = null;

  constructor(dependencies: LiveClientDependencies, targets: TelemetryLiveTarget[], onClosed: () => void) {
    this.dependencies = dependencies;
    this.targets = targets;
    this.onClosed = onClosed;
  }

  async start(signal?: AbortSignal): Promise<void> {
    if (signal?.aborted) throw signal.reason;
    const bootstrap = await this.bootstrap(true, signal);
    this.installBootstrap(bootstrap);
    this.transport = this.dependencies.transportFactory.create({
      endpoint: bootstrap.endpoint,
      connectionCapability: bootstrap.connectionToken,
      refreshConnectionCapability: () => this.refreshConnectionCapability(),
      handlers: {
        onConnecting: () => this.active.forEach((item) => item.machine.onSubscribing()),
        onConnected: () => undefined,
        onDisconnected: () => this.active.forEach((item) => item.machine.onTransportDisconnected()),
        onError: () => this.active.forEach((item) => item.machine.markUnavailable('transport-unavailable', true)),
      },
    });
    for (const item of this.active) this.attachTransport(item);
    this.transport.connect();
    this.interval = this.dependencies.setInterval(() => {
      void this.checkpoint().catch(() => undefined);
    }, checkpointIntervalMs);
  }

  getState(clientSubscriptionId: ClientSubscriptionId): TelemetryLiveState | undefined {
    return this.active.find((item) => item.target.clientSubscriptionId === clientSubscriptionId)?.machine.getState();
  }

  getStates(): ReadonlyArray<TelemetryLiveState> {
    return stableStates(this.active);
  }

  subscribe(listener: (states: ReadonlyArray<TelemetryLiveState>) => void): () => void {
    this.listeners.add(listener);
    listener(this.getStates());
    return () => this.listeners.delete(listener);
  }

  async refresh(): Promise<void> {
    await Promise.all(this.active.map((item) => this.loadSnapshot(item, 'snapshot-unavailable')));
  }

  async checkpoint(): Promise<void> {
    if (this.closed) return;
    const candidates = this.active
      .map((item) => ({ item, candidate: item.machine.checkpointCandidate() }))
      .filter((value): value is { item: ActiveSubscription; candidate: CheckpointCandidate } => value.candidate !== null);
    if (candidates.length === 0) return;
    const csrf = await this.csrf();
    const response = await this.dependencies.telemetry.checkpointTelemetryRecoveryCursors({
      checkpoints: candidates.map(({ candidate }) => ({
        subscriptionId: candidate.subscriptionId,
        businessRevision: candidate.businessRevision,
        transportPosition: { ...candidate.transportPosition },
      })),
    }, { csrfToken: csrf });
    if (response.items.length !== candidates.length) throw new Error('Telemetry checkpoint returned a partial result');
    response.items.forEach((result, index) => this.persistCheckpoint(candidates[index], result));
  }

  close(): void {
    if (this.closed) return;
    this.closed = true;
    if (this.interval !== null) this.dependencies.clearInterval(this.interval);
    this.interval = null;
    for (const item of this.active) {
      item.transport?.unsubscribe();
      item.machine.close();
    }
    this.transport?.disconnect();
    this.transport = null;
    this.listeners.clear();
    this.onClosed();
  }

  revokeAll(): void {
    if (this.closed) return;
    for (const item of this.active) this.revoke(item);
    this.close();
  }

  private async bootstrap(useStoredCursors: boolean, signal?: AbortSignal): Promise<SubscriptionBootstrapResponse> {
    const stored = this.targets.map((target) => useStoredCursors ? this.dependencies.recoveryStore.load(target) : null);
    const csrf = await this.csrf(signal);
    try {
      const response = await this.dependencies.telemetry.bootstrapTelemetrySubscriptions({
        subscriptions: this.targets.map((target, index) => ({
          clientSubscriptionId: target.clientSubscriptionId,
          deviceId: target.deviceId,
          keys: [...target.keys] as TelemetryKey[],
          ...(stored[index] ? { recoveryCursor: stored[index]?.recoveryCursor as OpaqueRecoveryCursor } : {}),
        })),
      }, { csrfToken: csrf, signal });
      return validateBootstrap(response, this.targets, this.dependencies.now());
    } catch (error) {
      if (useStoredCursors && error instanceof S2TelemetryClientError && error.problem.code === 'RECOVERY_CURSOR_INVALID') {
        for (const target of this.targets) this.dependencies.recoveryStore.remove(target.clientSubscriptionId);
        return this.bootstrap(false, signal);
      }
      throw error;
    }
  }

  private installBootstrap(bootstrap: SubscriptionBootstrapResponse): void {
    this.active.length = 0;
    bootstrap.subscriptions.forEach((descriptor, index) => {
      const target = this.targets[index];
      let item: ActiveSubscription;
      const machine = new SubscriptionStateMachine(target, descriptor, {
        onStateChanged: () => this.emit(),
        onSnapshotFallback: (reason) => { void this.loadSnapshot(item, reason); },
        onScopeViolation: () => this.revoke(item),
      }, this.dependencies.now);
      item = { target, descriptor, machine, transport: null };
      const persisted = this.dependencies.recoveryStore.load(target);
      if (descriptor.recoveryMode === 'ATTEMPT_RECOVERY' && persisted) machine.installPersistedSnapshot(persisted.snapshot);
      this.active.push(item);
    });
    this.emit();
  }

  private attachTransport(item: ActiveSubscription): void {
    if (!this.transport) throw new Error('Telemetry transport is not initialized');
    item.transport = this.transport.addSubscription(item.descriptor, {
      onSubscribing: () => item.machine.onSubscribing(),
      onSubscribed: (context) => {
        item.machine.onSubscribed(context);
        if (item.descriptor.recoveryMode === 'SNAPSHOT_THEN_LIVE' && item.machine.getState().snapshot === null) {
          void this.loadSnapshot(item, 'snapshot-unavailable');
        }
      },
      onPublication: (context) => item.machine.onPublication(context),
      onUnsubscribed: (context) => {
        if (isRevocation(context.code, context.reason)) this.revoke(item);
        else item.machine.markUnavailable('transport-unavailable', true);
      },
      onError: () => item.machine.markUnavailable('transport-unavailable', true),
    });
  }

  private async loadSnapshot(item: ActiveSubscription, reason: TelemetryLiveUnavailableReason): Promise<void> {
    if (this.closed || item.machine.getState().status === 'revoked') return;
    try {
      const snapshot = await this.dependencies.telemetry.getDeviceObservationSnapshot(
        item.target.deviceId,
        [...item.target.keys],
      );
      item.machine.installAuthoritativeSnapshot(snapshot);
    } catch (error) {
      if (error instanceof S2TelemetryClientError && error.problem.code === 'RESOURCE_NOT_FOUND') {
        this.revoke(item);
        return;
      }
      item.machine.markUnavailable(reason, true);
    }
  }

  private revoke(item: ActiveSubscription): void {
    this.dependencies.recoveryStore.remove(item.target.clientSubscriptionId);
    item.transport?.unsubscribe();
    item.transport = null;
    item.machine.revoke();
  }

  private persistCheckpoint(
    captured: { item: ActiveSubscription; candidate: CheckpointCandidate },
    result: RecoveryCursorCheckpointResult,
  ): void {
    const expiresAt = Date.parse(result.expiresAt);
    const now = this.dependencies.now().getTime();
    if (result.subscriptionId !== captured.candidate.subscriptionId
      || result.businessRevision !== captured.candidate.businessRevision
      || !result.recoveryCursor
      || !Number.isFinite(expiresAt)
      || expiresAt <= now
      || expiresAt > now + maximumRecoveryCursorLifetimeMs) {
      throw new Error('Telemetry checkpoint scope/revision drifted');
    }
    this.dependencies.recoveryStore.save({
      schemaVersion: 1,
      clientSubscriptionId: captured.item.target.clientSubscriptionId,
      deviceId: captured.item.target.deviceId,
      keys: [...captured.item.target.keys] as TelemetryKey[],
      snapshot: captured.candidate.snapshot,
      recoveryCursor: result.recoveryCursor,
      cursorExpiresAt: result.expiresAt,
      savedAt: this.dependencies.now().toISOString(),
    });
  }

  private async refreshConnectionCapability(): Promise<string> {
    if (this.refreshCapabilityPromise) return this.refreshCapabilityPromise;
    this.refreshCapabilityPromise = (async () => {
      await this.checkpoint();
      const bootstrap = await this.bootstrap(true);
      bootstrap.subscriptions.forEach((descriptor, index) => {
        const current = this.active[index].descriptor;
        if (descriptor.recoveryMode !== 'ATTEMPT_RECOVERY'
          || descriptor.subscriptionId !== current.subscriptionId
          || descriptor.channel !== current.channel
          || descriptor.deviceId !== current.deviceId
          || !exactArray(descriptor.keys, current.keys)) {
          throw new Error('Telemetry capability renewal changed subscription scope');
        }
      });
      return bootstrap.connectionToken;
    })().finally(() => {
      this.refreshCapabilityPromise = null;
    });
    return this.refreshCapabilityPromise;
  }

  private async csrf(signal?: AbortSignal): Promise<string> {
    const principal = await this.dependencies.platform.getCurrentPrincipal({ signal });
    const csrf = principal.data.session.csrfToken;
    if (!csrf) throw new Error('Authenticated Session omitted CSRF capability');
    return csrf;
  }

  private emit(): void {
    const states = this.getStates();
    for (const listener of this.listeners) listener(states);
  }
}

class DefaultTelemetryLiveClient implements TelemetryLiveClient {
  private readonly dependencies: LiveClientDependencies;
  private readonly sessions = new Set<LiveSession>();

  constructor(dependencies: LiveClientDependencies) {
    this.dependencies = dependencies;
  }

  async open(targets: ReadonlyArray<TelemetryLiveTarget>, options?: { signal?: AbortSignal }): Promise<TelemetryLiveSession> {
    if (targets.length === 0 || targets.length > maximumSubscriptions) throw new Error('Telemetry live target count is out of bounds');
    const normalized = targets.map((target) => normalizeTarget(target));
    const seen = new Set<string>();
    let totalKeys = 0;
    for (const target of normalized) {
      if (seen.has(target.clientSubscriptionId)) throw new Error('Telemetry live clientSubscriptionId is duplicated');
      seen.add(target.clientSubscriptionId);
      totalKeys += target.keys.length;
    }
    if (totalKeys > maximumTotalKeySelections) throw new Error('Telemetry live total key selection exceeds the contract');
    let session!: LiveSession;
    session = new LiveSession(
      this.dependencies,
      normalized.map((target) => cloneTarget(target)),
      () => this.sessions.delete(session),
    );
    try {
      await session.start(options?.signal);
      this.sessions.add(session);
      return session;
    } catch (error) {
      session.close();
      throw error;
    }
  }

  purge(): void {
    for (const session of this.sessions) session.revokeAll();
    this.sessions.clear();
    this.dependencies.recoveryStore.clear();
  }
}

export interface TelemetryLiveClientDependencies {
  telemetry?: S2TelemetryClient;
  platform?: Pick<PlatformGatewayClient, 'getCurrentPrincipal'>;
  transportFactory?: TelemetryTransportFactory;
  recoveryStore?: RecoveryStore;
  now?: () => Date;
  setInterval?: typeof globalThis.setInterval;
  clearInterval?: typeof globalThis.clearInterval;
}

export function createTelemetryLiveClient(dependencies: TelemetryLiveClientDependencies = {}): TelemetryLiveClient {
  const now = dependencies.now ?? (() => new Date());
  return new DefaultTelemetryLiveClient({
    telemetry: dependencies.telemetry ?? createS2TelemetryClient(),
    platform: dependencies.platform ?? createPlatformGatewayClient(),
    transportFactory: dependencies.transportFactory ?? new CentrifugoTransportFactory(),
    recoveryStore: dependencies.recoveryStore ?? new BrowserRecoveryStore(undefined, now),
    now,
    setInterval: dependencies.setInterval ?? globalThis.setInterval.bind(globalThis),
    clearInterval: dependencies.clearInterval ?? globalThis.clearInterval.bind(globalThis),
  });
}
