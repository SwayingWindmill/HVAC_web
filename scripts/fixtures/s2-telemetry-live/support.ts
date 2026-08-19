import type {
  DeviceObservationPublication,
  DeviceObservationSnapshot,
  RecoveryCursorCheckpointRequest,
  S2TelemetryClient,
  SubscriptionBootstrapRequest,
  SubscriptionBootstrapResponse,
  SubscriptionDescriptor,
  TelemetryKey,
} from '../../../apps/hvac-web/src/api/generated/s2Telemetry.gen';
import type {
  TelemetryTransportConnection,
  TelemetryTransportFactory,
  TelemetryTransportHandlers,
  TelemetryTransportSubscription,
  TelemetryTransportSubscriptionHandlers,
} from '../../../apps/hvac-web/src/platform/telemetry-live/transport-types';

export const deviceA = '018f2e00-3000-7000-8000-000000000001';
export const deviceB = '018f2e00-3000-7000-8000-000000000002';
const tenant = '018f2e00-1000-7000-8000-000000000003';
const site = '018f2e00-4000-7000-8000-000000000001';
export const targetA = { clientSubscriptionId: 'zone-a', deviceId: deviceA, keys: ['temperature'] as TelemetryKey[] };
export const targetB = { clientSubscriptionId: 'zone-b', deviceId: deviceB, keys: ['humidity'] as TelemetryKey[] };

export function assert(condition: unknown, message: string): asserts condition {
  if (!condition) throw new Error(message);
}

export async function waitFor(check: () => boolean, message: string | (() => string)): Promise<void> {
  for (let attempt = 0; attempt < 500; attempt += 1) {
    if (check()) return;
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
  throw new Error(typeof message === 'function' ? message() : message);
}

export function snapshot(deviceId: string, key: string, revision: number, value: number): DeviceObservationSnapshot {
  return {
    schemaVersion: 1, deviceId, tenantId: tenant, siteId: site, businessRevision: revision,
    evaluatedAt: `2026-07-25T00:00:${String(revision).padStart(2, '0')}.000Z`,
    evaluationAvailability: 'AVAILABLE', availabilityReasons: [],
    presence: { applicability: 'APPLICABLE', currentState: 'ONLINE', lastSeenAt: '2026-07-25T00:00:00.000Z', policyRevision: 1, lastKnown: null },
    telemetryReadiness: 'CURRENT', displayState: 'ONLINE',
    values: [{
      key, state: 'PRESENT', value, valueType: 'NUMBER', unit: key === 'temperature' ? 'Cel' : '%',
      sampledAt: '2026-07-25T00:00:00.000Z', receivedAt: '2026-07-25T00:00:00.000Z',
      freshness: 'FRESH', quality: 'GOOD', qualityReasons: [], policyRevision: 1,
    }],
  };
}

let eventSequence = 1;
export function publication(input: {
  subscriptionId: string; deviceId: string; key: string;
  previousRevision: number; revision: number; value: number;
}): DeviceObservationPublication {
  const suffix = String(eventSequence++).padStart(12, '0');
  return {
    schemaVersion: 1, kind: 'DEVICE_OBSERVATION_DELTA', eventId: `018f2e00-9000-7000-8000-${suffix}`,
    subscriptionId: input.subscriptionId, deviceId: input.deviceId,
    previousRevision: input.previousRevision, revision: input.revision,
    evaluatedAt: `2026-07-25T00:01:${String(input.revision).padStart(2, '0')}.000Z`,
    publishedAt: `2026-07-25T00:01:${String(input.revision).padStart(2, '0')}.100Z`,
    evaluationAvailability: 'AVAILABLE', availabilityReasons: [],
    presence: { applicability: 'APPLICABLE', currentState: 'ONLINE', lastSeenAt: '2026-07-25T00:01:00.000Z', policyRevision: 1, lastKnown: null },
    telemetryReadiness: 'CURRENT', displayState: 'ONLINE',
    telemetryChanges: [{
      key: input.key, state: 'PRESENT', value: input.value, valueType: 'NUMBER',
      unit: input.key === 'temperature' ? 'Cel' : '%', sampledAt: '2026-07-25T00:01:00.000Z',
      receivedAt: '2026-07-25T00:01:00.000Z', freshness: 'FRESH', quality: 'GOOD',
      qualityReasons: [], policyRevision: 1,
    }],
  };
}

interface SnapshotQueueItem {
  promise: Promise<DeviceObservationSnapshot>;
  resolve: (value: DeviceObservationSnapshot) => void;
}

export class FakeTelemetry {
  readonly snapshotCalls: Array<{ deviceId: string; keys: string[] }> = [];
  readonly bootstrapRequests: SubscriptionBootstrapRequest[] = [];
  readonly checkpointRequests: RecoveryCursorCheckpointRequest[] = [];
  private readonly queues = new Map<string, SnapshotQueueItem[]>();
  private cursorSequence = 0;

  enqueue(deviceId: string, value?: DeviceObservationSnapshot): SnapshotQueueItem {
    let resolveValue!: (snapshotValue: DeviceObservationSnapshot) => void;
    const item = { promise: new Promise<DeviceObservationSnapshot>((resolve) => { resolveValue = resolve; }), resolve: resolveValue };
    const queue = this.queues.get(deviceId) ?? [];
    queue.push(item);
    this.queues.set(deviceId, queue);
    if (value) item.resolve(value);
    return item;
  }

  client(): S2TelemetryClient {
    return {
      getDeviceObservationSnapshot: async (deviceId: string, keys: TelemetryKey[]) => {
        this.snapshotCalls.push({ deviceId, keys: [...keys] });
        const item = this.queues.get(deviceId)?.shift();
        if (!item) throw new Error(`No Snapshot fixture for ${deviceId}`);
        return item.promise;
      },
      batchGetObservationSnapshots: async () => ({ schemaVersion: 1, items: [] }),
      bootstrapTelemetrySubscriptions: async (request: SubscriptionBootstrapRequest) => {
        this.bootstrapRequests.push(structuredClone(request));
        const recovering = request.subscriptions.every((item) => Boolean(item.recoveryCursor));
        const subscriptions = request.subscriptions.map((item, index): SubscriptionDescriptor => ({
          clientSubscriptionId: item.clientSubscriptionId,
          subscriptionId: `subscription_${item.clientSubscriptionId}_0001`, deviceId: item.deviceId,
          keys: [...item.keys], channel: `s2:opaque_${index}_fixture`,
          recoveryMode: recovering ? 'ATTEMPT_RECOVERY' : 'SNAPSHOT_THEN_LIVE',
          transportPosition: recovering ? { epoch: 'epoch-a', offset: index === 0 ? 6 : 8 } : null,
          recoveryCursor: recovering ? item.recoveryCursor ?? null : null,
        }));
        return {
          schemaVersion: 1, transportProtocol: 'CENTRIFUGO_JSON_V1',
          endpoint: 'ws://127.0.0.1:18000/connection/websocket',
          connectionToken: ['connection', String(this.bootstrapRequests.length)].join(':'),
          expiresAt: '2026-07-25T00:15:00.000Z', subscriptions,
          limits: { maxSubscriptions: 100, maxKeysPerSubscription: 64, maxTotalKeySelections: 2048 },
        } satisfies SubscriptionBootstrapResponse;
      },
      checkpointTelemetryRecoveryCursors: async (request: RecoveryCursorCheckpointRequest) => {
        this.checkpointRequests.push(structuredClone(request));
        return {
          schemaVersion: 1,
          items: request.checkpoints.map((checkpoint) => ({
            subscriptionId: checkpoint.subscriptionId, businessRevision: checkpoint.businessRevision,
            recoveryCursor: ['cursor', String(++this.cursorSequence), checkpoint.subscriptionId].join(':'),
            expiresAt: '2026-07-25T00:12:00.000Z',
          })),
        };
      },
    } as S2TelemetryClient;
  }
}

export class FakeConnection implements TelemetryTransportConnection {
  readonly subscriptions = new Map<string, { handlers: TelemetryTransportSubscriptionHandlers; active: boolean }>();
  constructor(readonly handlers: TelemetryTransportHandlers) {}
  addSubscription(descriptor: SubscriptionDescriptor, handlers: TelemetryTransportSubscriptionHandlers): TelemetryTransportSubscription {
    this.subscriptions.set(descriptor.clientSubscriptionId, { handlers, active: true });
    return { unsubscribe: () => { const item = this.subscriptions.get(descriptor.clientSubscriptionId); if (item) item.active = false; } };
  }
  connect(): void { this.handlers.onConnected(); }
  disconnect(): void { this.handlers.onDisconnected({ code: 0, reason: 'closed' }); }
  subscribed(id: string, context: Partial<Parameters<TelemetryTransportSubscriptionHandlers['onSubscribed']>[0]> = {}): void {
    this.require(id).handlers.onSubscribed({
      recoverable: true, positioned: true, wasRecovering: false, recovered: false,
      hasRecoveredPublications: false, position: { epoch: 'epoch-a', offset: id === 'zone-a' ? 1 : 5 }, ...context,
    });
  }
  publication(id: string, data: unknown, offset: number, epoch = 'epoch-a'): void {
    this.require(id).handlers.onPublication({ data, position: { epoch, offset } });
  }
  subscribing(id: string): void { this.require(id).handlers.onSubscribing(); }
  unsubscribe(id: string, code = 2501, reason = 'scope revoked'): void { this.require(id).handlers.onUnsubscribed({ code, reason }); }
  private require(id: string) {
    const item = this.subscriptions.get(id);
    if (!item || !item.active) throw new Error(`Subscription ${id} is not active`);
    return item;
  }
}

export class FakeTransportFactory implements TelemetryTransportFactory {
  readonly connections: FakeConnection[] = [];
  refreshCapability: (() => Promise<string>) | null = null;
  create(input: Parameters<TelemetryTransportFactory['create']>[0]): TelemetryTransportConnection {
    this.refreshCapability = input.refreshConnectionCapability;
    const connection = new FakeConnection(input.handlers);
    this.connections.push(connection);
    return connection;
  }
  current(): FakeConnection {
    const connection = this.connections.at(-1);
    if (!connection) throw new Error('No transport connection');
    return connection;
  }
}
