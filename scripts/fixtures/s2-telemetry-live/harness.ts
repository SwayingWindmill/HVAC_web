import type { PlatformGatewayClient } from '../../../apps/hvac-web/src/api/generated/platformGateway.gen';
import { createTelemetryLiveClient } from '../../../apps/hvac-web/src/platform/telemetry-live/client';
import { BrowserRecoveryStore } from '../../../apps/hvac-web/src/platform/telemetry-live/storage';
import {
  assert, deviceA, deviceB, FakeTelemetry, FakeTransportFactory,
  publication, snapshot, targetA, targetB, waitFor,
} from './support';

async function run(): Promise<Record<string, unknown>> {
  sessionStorage.clear();
  const telemetry = new FakeTelemetry();
  const transport = new FakeTransportFactory();
  const now = () => new Date('2026-07-25T00:10:00.000Z');
  const store = new BrowserRecoveryStore(sessionStorage, now);
  const platform = {
    getCurrentPrincipal: async () => ({ data: { session: { csrfToken: ['csrf', 'fixture'].join(':') } } }),
  } as unknown as Pick<PlatformGatewayClient, 'getCurrentPrincipal'>;
  const client = createTelemetryLiveClient({
    telemetry: telemetry.client(), platform, transportFactory: transport, recoveryStore: store,
    now,
    setInterval: (() => 1) as unknown as typeof globalThis.setInterval,
    clearInterval: (() => undefined) as typeof globalThis.clearInterval,
  });

  const pendingA = telemetry.enqueue(deviceA);
  const pendingB = telemetry.enqueue(deviceB);
  const session = await client.open([targetA, targetB]);
  const connection = transport.current();
  connection.subscribed('zone-a');
  connection.subscribed('zone-b');
  connection.publication('zone-a', publication({ subscriptionId: 'subscription_zone-a_0001', deviceId: deviceA, key: 'temperature', previousRevision: 1, revision: 2, value: 22 }), 2);
  pendingA.resolve(snapshot(deviceA, 'temperature', 1, 21));
  pendingB.resolve(snapshot(deviceB, 'humidity', 5, 45));
  await waitFor(() => session.getState('zone-a')?.status === 'live' && session.getState('zone-a')?.snapshot?.businessRevision === 2, 'buffered publication was not installed after Snapshot');
  await waitFor(() => session.getState('zone-b')?.status === 'live', 'second exact subscription did not become live');

  connection.publication('zone-a', publication({ subscriptionId: 'subscription_zone-a_0001', deviceId: deviceA, key: 'temperature', previousRevision: 1, revision: 2, value: 99 }), 3);
  assert(session.getState('zone-a')?.snapshot?.businessRevision === 2, 'duplicate revision changed state');
  connection.publication('zone-a', publication({ subscriptionId: 'subscription_zone-a_0001', deviceId: deviceA, key: 'temperature', previousRevision: 2, revision: 3, value: 23 }), 4);
  assert(session.getState('zone-a')?.snapshot?.businessRevision === 3, 'contiguous revision was not applied');

  const staleRefreshA = telemetry.enqueue(deviceA);
  telemetry.enqueue(deviceB, snapshot(deviceB, 'humidity', 5, 45));
  const currentRefreshA = telemetry.enqueue(deviceA);
  telemetry.enqueue(deviceB, snapshot(deviceB, 'humidity', 5, 45));
  const staleRefresh = session.refresh();
  const currentRefresh = session.refresh();
  currentRefreshA.resolve(snapshot(deviceA, 'temperature', 3, 23));
  await currentRefresh;
  staleRefreshA.resolve(snapshot(deviceA, 'temperature', 2, 22));
  await staleRefresh;
  assert(session.getState('zone-a')?.snapshot?.businessRevision === 3, 'stale Snapshot response regressed Business Revision');

  telemetry.enqueue(deviceA, snapshot(deviceA, 'temperature', 5, 25));
  connection.publication('zone-a', publication({ subscriptionId: 'subscription_zone-a_0001', deviceId: deviceA, key: 'temperature', previousRevision: 4, revision: 5, value: 25 }), 5);
  await waitFor(() => session.getState('zone-a')?.snapshot?.businessRevision === 5 && session.getState('zone-a')?.status === 'live', 'gap did not reload authoritative Snapshot');

  connection.subscribing('zone-a');
  connection.subscribed('zone-a', { wasRecovering: true, recovered: true, hasRecoveredPublications: true, position: { epoch: 'epoch-a', offset: 6 } });
  connection.publication('zone-a', publication({ subscriptionId: 'subscription_zone-a_0001', deviceId: deviceA, key: 'temperature', previousRevision: 5, revision: 6, value: 26 }), 6);
  await waitFor(() => session.getState('zone-a')?.status === 'live' && session.getState('zone-a')?.snapshot?.businessRevision === 6, 'continuous reconnect recovery failed');
  const recoveredState = session.getState('zone-a');
  assert(recoveredState?.status === 'live' && recoveredState.recovered === true, `recovery evidence was not retained: ${JSON.stringify(recoveredState)}`);

  telemetry.enqueue(deviceB, snapshot(deviceB, 'humidity', 7, 47));
  connection.subscribing('zone-b');
  connection.subscribed('zone-b', { wasRecovering: true, recovered: false, position: { epoch: 'epoch-reset', offset: 8 } });
  await waitFor(() => session.getState('zone-b')?.snapshot?.businessRevision === 7 && session.getState('zone-b')?.status === 'live', 'failed recovery/epoch reset did not reload Snapshot');

  connection.handlers.onDisconnected({ code: 3008, reason: 'slow consumer' });
  assert(session.getStates().every((state) => state.status === 'snapshot'), 'slow consumer disconnect did not leave normalized Snapshot state');
  telemetry.enqueue(deviceA, snapshot(deviceA, 'temperature', 6, 26));
  telemetry.enqueue(deviceB, snapshot(deviceB, 'humidity', 7, 47));
  connection.subscribed('zone-a', { wasRecovering: false, recovered: false, position: { epoch: 'epoch-a', offset: 6 } });
  connection.subscribed('zone-b', { wasRecovering: true, recovered: false, position: { epoch: 'epoch-a', offset: 8 } });
  await waitFor(
    () => session.getStates().every((state) => state.status === 'live'),
    () => `slow consumer recovery did not return through Snapshot: ${JSON.stringify(session.getStates())}`,
  );

  await session.checkpoint();
  assert(telemetry.checkpointRequests.length === 1 && telemetry.checkpointRequests[0].checkpoints.length === 2, 'checkpoint did not preserve multiple subscriptions');
  assert(store.load(targetA)?.snapshot.businessRevision === 6, 'checkpoint did not persist the exact applied Snapshot');
  const renewed = await transport.refreshCapability?.();
  assert(typeof renewed === 'string' && renewed.startsWith('connection:'), 'connection capability renewal failed');
  assert(telemetry.bootstrapRequests.at(-1)?.subscriptions.every((item) => Boolean(item.recoveryCursor)), 'capability renewal did not use same-scope cursors');

  session.close();
  const restoreTransport = new FakeTransportFactory();
  const restoreClient = createTelemetryLiveClient({
    telemetry: telemetry.client(), platform, transportFactory: restoreTransport, recoveryStore: store,
    now: () => new Date('2026-07-25T00:11:00.000Z'),
    setInterval: (() => 1) as unknown as typeof globalThis.setInterval,
    clearInterval: (() => undefined) as typeof globalThis.clearInterval,
  });
  const snapshotCallsBeforeRestore = telemetry.snapshotCalls.length;
  const restored = await restoreClient.open([targetA, targetB]);
  const restoreConnection = restoreTransport.current();
  restoreConnection.subscribed('zone-a', { wasRecovering: true, recovered: true, hasRecoveredPublications: false, position: { epoch: 'epoch-a', offset: 6 } });
  restoreConnection.subscribed('zone-b', { wasRecovering: true, recovered: true, hasRecoveredPublications: false, position: { epoch: 'epoch-a', offset: 8 } });
  await waitFor(() => restored.getStates().every((state) => state.status === 'live'), 'page restore did not use same-scope Cursor and stored Snapshot');
  assert(telemetry.snapshotCalls.length === snapshotCallsBeforeRestore, 'successful page restore unnecessarily loaded a Snapshot');

  restoreConnection.unsubscribe('zone-b');
  await waitFor(() => restored.getState('zone-b')?.status === 'revoked', 'server revocation did not produce revoked state');
  assert(store.load(targetB) === null, 'revocation retained browser Last Known state');
  restoreConnection.publication('zone-a', publication({ subscriptionId: 'subscription_zone-a_0001', deviceId: deviceB, key: 'temperature', previousRevision: 6, revision: 7, value: 27 }), 7);
  await waitFor(() => restored.getState('zone-a')?.status === 'revoked', 'wrong Device publication did not fail closed');
  assert(store.load(targetA) === null, 'scope violation retained browser Last Known state');
  restoreClient.purge();
  assert(sessionStorage.length === 0, 'logout/Tenant switch purge retained telemetry browser state');

  return {
    schemaVersion: 1, ticket: 66, status: 'passed', multipleExactSubscriptions: true,
    bufferedSnapshotInstall: true, duplicateIgnored: true, contiguousApplied: true,
    staleSnapshotIgnored: true, gapSnapshotFallback: true, reconnectRecovery: true, epochResetSnapshotFallback: true,
    slowConsumerSnapshotFallback: true, checkpointAndPageRestore: true,
    connectionCapabilityRenewal: true, revocationPurgedLastKnown: true,
    wrongScopeFailedClosed: true, tenantSwitchLogoutPurge: true,
    snapshotCalls: telemetry.snapshotCalls.length, bootstrapCalls: telemetry.bootstrapRequests.length,
  };
}

declare global {
  interface Window { __S2_LIVE_RESULT__?: { done: boolean; result?: Record<string, unknown>; error?: string } }
}

window.__S2_LIVE_RESULT__ = { done: false };
void run().then((result) => {
  window.__S2_LIVE_RESULT__ = { done: true, result };
  document.querySelector('#status')!.textContent = 'passed';
}).catch((error: unknown) => {
  window.__S2_LIVE_RESULT__ = { done: true, error: error instanceof Error ? error.stack ?? error.message : String(error) };
  document.querySelector('#status')!.textContent = 'failed';
});
