import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import vm from 'node:vm';

import {
  CHANNEL_COILS,
  COMMAND_TOPIC,
  DEFAULT_SEQUENCE_TIMINGS,
  ET1010_UNIT_ID,
  PULSE_COILS,
  PulseResetError,
  REPLY_TOPIC,
  SONGYAXUAN_GATEWAY_ID,
  SongyaxuanCommandExecutor,
  TELEMETRY_TOPIC,
  WINNER_CONTROLLER_ID,
  type CommandReplyEnvelope,
  type DownlinkCommandEnvelope,
  type ExecutorPersistedState,
  type ExecutorStateStore,
  type SequenceDiagnosticEvent,
  type SequenceTimings,
  type UnitCoilPlan,
  type UplinkTelemetryMessage,
} from '../edge/node-red/songyaxuan-command-executor.ts';
import { BUS_PRIORITY_NORMAL, BUS_PRIORITY_PULSE_RESET, Rs485BusLock } from '../edge/node-red/rs485-bus-lock.ts';
import { ConcentratorRpcExecutor, REG, type ModbusBusClient } from '../edge/node-red/concentrator-mqtt-rpc.ts';
import { buildOnboardingSql, checkSiteFile } from './site-onboarding.mjs';

const UUID_V7_REGEX = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

function assertValidTelemetryEnvelope20(message: UplinkTelemetryMessage) {
  assert.equal(message.topic, TELEMETRY_TOPIC);
  const p = message.payload;
  assert.deepEqual(Object.keys(p).sort(), [
    'gatewayId',
    'messageId',
    'payload',
    'replay',
    'schemaVersion',
    'sequence',
    'timestamp',
  ]);
  assert.equal(p.schemaVersion, '2.0');
  assert.equal(p.gatewayId, SONGYAXUAN_GATEWAY_ID);
  assert.match(p.messageId, UUID_V7_REGEX);
  assert.equal(typeof p.timestamp, 'number');
  assert.ok(Number.isInteger(p.timestamp) && p.timestamp >= 0 && p.timestamp < 2 ** 48);
  assert.equal(typeof p.sequence, 'number');
  assert.ok(Number.isSafeInteger(p.sequence) && p.sequence >= p.timestamp * 1000);
  assert.equal(p.replay, false);
  assert.ok(Array.isArray(p.payload.devices) && p.payload.devices.length > 0);
  for (const device of p.payload.devices) {
    assert.ok(device.deviceId.length > 0);
    assert.ok(Number.isInteger(device.deviceTimestamp) && device.deviceTimestamp >= 0);
    assert.ok(Array.isArray(device.points) && device.points.length > 0);
    for (const point of device.points) {
      assert.match(point.code, /^[a-z][a-z0-9_]{0,127}$/);
      assert.ok(point.value !== null && point.value !== undefined);
      assert.ok(Number.isInteger(point.quality) && point.quality >= 0 && point.quality <= 255);
    }
  }
}

function runPlatformEnvelopeNode(
  payload: Record<string, Array<{ ts: number; values: Record<string, number> }>>,
  existingState?: Map<string, unknown>,
) {
  const source = readFileSync(new URL('../edge/node-red/songyaxuan-platform-envelope.js', import.meta.url), 'utf8');
  const state = existingState ?? new Map<string, unknown>();
  const sandbox = {
    msg: { payload },
    context: {
      get: (key: string) => state.get(key),
      set: (key: string, value: unknown) => state.set(key, value),
    },
    flow: {
      get: (key: string) => state.get(`flow:${key}`),
      set: (key: string, value: unknown) => state.set(`flow:${key}`, value),
    },
    Math,
    Date,
    Number,
    Object,
    Array,
    crypto: undefined,
  };
  const fn = vm.runInNewContext(`(function(msg, context, flow) {\n${source}\n})`, sandbox);
  return fn(sandbox.msg, sandbox.context, sandbox.flow) as {
    topic: string;
    payload: {
      schemaVersion: string;
      gatewayId: string;
      timestamp: number;
      sequence: number;
      payload: { devices: Array<{ deviceId: string; deviceTimestamp: number; points: Array<{ code: string; value: number | string; quality: number }> }> };
    };
  };
}

function createMemoryStateStore(initial: ExecutorPersistedState | null = null) {
  let current: ExecutorPersistedState | null = initial ? structuredClone(initial) : null;
  const savedSnapshots: ExecutorPersistedState[] = [];
  const store: ExecutorStateStore = {
    async load() {
      return current ? structuredClone(current) : null;
    },
    async save(state) {
      current = structuredClone(state);
      savedSnapshots.push(structuredClone(state));
    },
  };
  return {
    store,
    savedSnapshots,
    getState: () => (current ? structuredClone(current) : null),
  };
}

function createFakeExecutor(options: {
  failOnCoil?: number;
  failResetOnCoil?: number;
  startNowMs?: number;
  coilReader?: (coilAddress: number) => Promise<0 | 1>;
  chillerPowerReader?: (unitCode: UnitCoilPlan['unitCode']) => Promise<number | null>;
  chillerStopPowerKw?: number;
  stateStore?: ExecutorStateStore;
  busLock?: Rs485BusLock;
  sleepMs?: (durationMs: number, signal?: AbortSignal) => Promise<void>;
  sequenceTimings?: Partial<SequenceTimings>;
  onWriteCoil?: (unitId: number, coilAddress: number, value: 0 | 1) => Promise<void> | void;
} = {}) {
  let now = options.startNowMs ?? 1_763_120_000_000;
  let seq = 0;
  const writes: Array<{ unitId: number; coil: number; value: 0 | 1; atMs: number }> = [];
  const replies: Array<{ topic: string; reply: CommandReplyEnvelope }> = [];
  const telemetries: UplinkTelemetryMessage[] = [];
  const diagnostics: SequenceDiagnosticEvent[] = [];
  const failOnceOnReset = options.failResetOnCoil;

  const executor = new SongyaxuanCommandExecutor({
    coilWriter: {
      async writeCoil(unitId, coilAddress, value) {
        if (options.onWriteCoil) {
          await options.onWriteCoil(unitId, coilAddress, value);
        }
        if (options.failOnCoil === coilAddress && value === 1) {
          throw new Error('simulated modbus timeout');
        }
        if (failOnceOnReset === coilAddress && value === 0) {
          throw new Error('simulated modbus reset failure');
        }
        writes.push({ unitId, coil: coilAddress, value, atMs: now });
      },
    },
    coilReader: options.coilReader,
    chillerPowerReader: options.chillerPowerReader,
    chillerStopPowerKw: options.chillerStopPowerKw,
    stateStore: options.stateStore,
    busLock: options.busLock,
    publishReply: (topic, reply) => {
      replies.push({ topic, reply });
    },
    publishTelemetry: (message) => {
      assertValidTelemetryEnvelope20(message);
      telemetries.push(message);
    },
    onDiagnostic: (event) => {
      diagnostics.push(event);
    },
    sequenceTimings: options.sequenceTimings,
    nowMs: () => now,
    sleepMs: options.sleepMs ?? (async (ms, signal) => {
      if (signal?.aborted) return;
      now += ms;
    }),
    uuidv7: () => `019a7000-0000-7000-8000-${String(++seq).padStart(12, '0')}`,
  });

  return {
    executor,
    writes,
    replies,
    telemetries,
    diagnostics,
    advanceMs(delta: number) {
      now += delta;
    },
    getNowMs: () => now,
  };
}

function makeCommand(overrides: Partial<DownlinkCommandEnvelope>): DownlinkCommandEnvelope {
  const now = overrides.issuedAt ?? 1_763_120_000_000;
  return {
    schemaVersion: '2.0',
    messageId: 'att-1',
    commandId: 'cmd-1',
    traceId: 'trace-1',
    issuedAt: now,
    expireAt: now + 15_000,
    deviceId: 'ctrl-chwp-1',
    commandCode: 'START',
    params: {},
    policy: { requiresReadback: true, verificationWindowMs: 15_000 },
    executionFence: 1,
    payloadHash: 'hash-1',
    ...overrides,
  };
}

test('songyaxuan-platform-envelope emits u51..u54 and controller run_state projections', () => {
  const out = runPlatformEnvelopeNode({
    et1010: [
      {
        ts: 1_763_120_000_000,
        values: {
          u11: 1, u12: 0, u13: 1, u14: 0,
          u21: 0, u22: 1, u23: 0, u24: 0,
          u51: 0, u52: 1, u53: 0, u54: 0,
        },
      },
    ],
  });
  assert.equal(out.topic, TELEMETRY_TOPIC);
  const byDevice = new Map(out.payload.payload.devices.map((d) => [d.deviceId, d.points]));
  assert.equal(byDevice.get('et1010')?.find((p) => p.code === 'u52')?.value, 1);
  assert.equal(byDevice.get('ctrl-chwp-1')?.find((p) => p.code === 'run_state')?.value, 'RUNNING');
  assert.equal(byDevice.get('ctrl-cwp-1')?.find((p) => p.code === 'run_state')?.value, 'STOPPED');
  assert.equal(byDevice.get('ctrl-tower-1')?.find((p) => p.code === 'run_state')?.value, 'RUNNING');
  assert.equal(byDevice.get('ctrl-cwp-2')?.find((p) => p.code === 'run_state')?.value, 'RUNNING');

  const meterBOut = runPlatformEnvelopeNode({
    'ammeter-unit3': [{ ts: 1_763_120_001_000, values: { powerTotal: 142.5, combinedActiveTotalElectricalEnergy: 1000 } }],
  });
  const chillerB = meterBOut.payload.payload.devices.find((d) => d.deviceId === 'ctrl-chiller-b');
  assert.equal(chillerB?.points[0]?.value, 'RUNNING');

  const meterAOut = runPlatformEnvelopeNode({
    'ammeter-unit4': [{ ts: 1_763_120_002_000, values: { powerTotal: 0.2, combinedActiveTotalElectricalEnergy: 76938 } }],
  });
  const chillerA = meterAOut.payload.payload.devices.find((d) => d.deviceId === 'ctrl-chiller-a');
  assert.equal(chillerA?.points[0]?.value, 'STOPPED');
});

test('songyaxuan-platform-envelope uses per-meter hysteresis for A/B vs combined-feeder C/D', () => {
  const source = readFileSync(new URL('../edge/node-red/songyaxuan-platform-envelope.js', import.meta.url), 'utf8');
  const state = new Map<string, unknown>();
  const payload: Record<string, Array<{ ts: number; values: Record<string, number> }>> = {};
  const sandbox = {
    msg: { payload },
    context: {
      get: (key: string) => state.get(key),
      set: (key: string, value: unknown) => state.set(key, value),
    },
    flow: {
      get: (key: string) => state.get(`flow:${key}`),
      set: (key: string, value: unknown) => state.set(`flow:${key}`, value),
    },
    Math,
    Date,
    Number,
    Object,
    Array,
    crypto: undefined,
  };
  const fn: (
    msg: typeof sandbox.msg,
    context: typeof sandbox.context,
    flow: typeof sandbox.flow,
  ) => ReturnType<typeof runPlatformEnvelopeNode> = vm.runInNewContext(
    `(function(msg, context, flow) {\n${source}\n})`,
    sandbox,
  );
  const runStep = (device: string, powerTotal: number, ts: number) => {
    sandbox.msg = {
      payload: {
        [device]: [{ ts, values: { powerTotal, combinedActiveTotalElectricalEnergy: 1000 } }],
      },
    };
    const out = fn(sandbox.msg, sandbox.context, sandbox.flow);
    return out.payload.payload.devices.find((d) => d.deviceId === device)?.points.find((p) => p.code === 'run_state')?.value;
  };

  let ts = 1_763_120_100_000;
  // A/B meters measure chiller only: 38 kW starts immediately.
  assert.equal(runStep('ammeter-unit4', 38, ts += 1000), 'RUNNING');
  assert.equal(runStep('ammeter-unit3', 38, ts += 1000), 'RUNNING');

  // C/D meters include ~30 kW auxiliaries: 38 kW and 57.75 kW stay STOPPED; > 57.75 kW starts; <= 45 kW stops.
  for (const cdMeter of ['ammeter-unit1', 'ammeter-unit2'] as const) {
    assert.equal(runStep(cdMeter, 38, ts += 1000), 'STOPPED', `${cdMeter} at 38 kW`);
    assert.equal(runStep(cdMeter, 57.75, ts += 1000), 'STOPPED', `${cdMeter} at 57.75 kW`);
    assert.equal(runStep(cdMeter, 58, ts += 1000), 'RUNNING', `${cdMeter} at 58 kW`);
    assert.equal(runStep(cdMeter, 48, ts += 1000), 'RUNNING', `${cdMeter} holds at 48 kW`);
    assert.equal(runStep(cdMeter, 45, ts += 1000), 'STOPPED', `${cdMeter} stops at 45 kW`);
    assert.equal(runStep(cdMeter, 48, ts += 1000), 'STOPPED', `${cdMeter} remains stopped at 48 kW`);
  }
});

test('executor handles LEVEL coils 0 and 14 with immediate state uplink and schemaVersion 2.0 reply', async () => {
  const { executor, writes, replies, telemetries } = createFakeExecutor();
  await executor.initialize();
  writes.length = 0;
  telemetries.length = 0;

  const r1 = await executor.handleCommand(makeCommand({
    commandId: 'cmd-chwp-1-start',
    deviceId: 'ctrl-chwp-1',
    commandCode: 'START',
    pointCode: 'start',
    actuationMode: 'LEVEL',
    channel: 'u11',
    coilAddress: 0,
    executionFence: 1,
  }));
  assert.equal(r1.schemaVersion, '2.0');
  assert.equal(r1.edgeStatus, 'EXECUTED');
  assert.equal(r1.reasonCode, null);
  assert.equal(r1.executionEvidence.winnerControllerId, WINNER_CONTROLLER_ID);
  assert.equal(replies[0]?.topic, REPLY_TOPIC);
  assert.deepEqual(writes.map((w) => [w.unitId, w.coil, w.value]), [[ET1010_UNIT_ID, 0, 1]]);
  assert.equal(telemetries[0]?.topic, TELEMETRY_TOPIC);
  assert.equal(telemetries[0]?.payload.payload.devices[1]?.points[0]?.value, 'RUNNING');

  const r2 = await executor.handleCommand(makeCommand({
    commandId: 'cmd-tower-4-stop',
    deviceId: 'ctrl-tower-4',
    commandCode: 'STOP',
    pointCode: 'stop',
    actuationMode: 'LEVEL',
    channel: 'u43',
    coilAddress: 14,
    executionFence: 1,
  }));
  assert.equal(r2.edgeStatus, 'EXECUTED');
  assert.deepEqual(writes[1] && [writes[1].coil, writes[1].value], [14, 0]);
});

test('executor executes PULSE coils 3 and 16..19 and guarantees 0 reset in finally', async () => {
  const { executor, writes } = createFakeExecutor();
  await executor.initialize();
  writes.length = 0;

  const startChillerA = await executor.handleCommand(makeCommand({
    commandId: 'cmd-chiller-a-start',
    deviceId: 'ctrl-chiller-a',
    commandCode: 'START',
    actuationMode: 'PULSE',
    channel: 'u14',
    coilAddress: 3,
    pulseDurationMs: 500,
    executionFence: 1,
  }));
  assert.equal(startChillerA.edgeStatus, 'EXECUTED');

  const stopCoils = [
    ['ctrl-chiller-a', 'u51', 16],
    ['ctrl-chiller-b', 'u52', 17],
    ['ctrl-chiller-c', 'u53', 18],
    ['ctrl-chiller-d', 'u54', 19],
  ] as const;
  for (const [idx, [deviceId, channel, coil]] of stopCoils.entries()) {
    const res = await executor.handleCommand(makeCommand({
      commandId: `cmd-stop-${idx}`,
      deviceId,
      commandCode: 'STOP',
      actuationMode: 'PULSE',
      channel,
      coilAddress: coil,
      pulseDurationMs: 500,
      executionFence: idx + 10,
    }));
    assert.equal(res.edgeStatus, 'EXECUTED');
  }
  assert.deepEqual(
    writes.map((w) => [w.coil, w.value]),
    [
      [3, 1], [3, 0],
      [16, 1], [16, 0],
      [17, 1], [17, 0],
      [18, 1], [18, 0],
      [19, 1], [19, 0],
    ],
  );
  assert.equal(writes[1].atMs - writes[0].atMs, 500);

  const failing = createFakeExecutor({ failResetOnCoil: 16 });
  const resetFail = await failing.executor.handleCommand(makeCommand({
    commandId: 'cmd-reset-fail',
    deviceId: 'ctrl-chiller-a',
    commandCode: 'STOP',
    actuationMode: 'PULSE',
    channel: 'u51',
    coilAddress: 16,
    pulseDurationMs: 500,
    executionFence: 1,
  }));
  assert.equal(resetFail.edgeStatus, 'FAILED');
  assert.equal(resetFail.reasonCode, 'PULSE_RESET_FAILED');
});

test('executor accepts START_UNIT_SEQUENCE and STOP_UNIT_SEQUENCE within 1s and executes hardware timings asynchronously', async () => {
  assert.deepEqual(DEFAULT_SEQUENCE_TIMINGS, {
    pumpToCoolingPumpMs: 30_000,
    coolingPumpToTowerMs: 30_000,
    towerToChillerPulseMs: 60_000,
    chillerStopCoastDownMs: 30_000,
    auxiliaryStopStepMs: 10_000,
  });

  const { executor, writes, getNowMs } = createFakeExecutor({ startNowMs: 1_763_120_000_000 });
  await executor.initialize();
  writes.length = 0;
  const t0 = getNowMs();
  const startRes = await executor.handleCommand(makeCommand({
    commandId: 'cmd-unit-a-start',
    deviceId: 'ctrl-chiller-a',
    commandCode: 'START_UNIT_SEQUENCE',
    actuationMode: 'UNIT_SEQUENCE',
    channel: 'unit-a',
    executionFence: 1,
  }));
  const startElapsed = getNowMs() - t0;
  assert.equal(startRes.edgeStatus, 'EXECUTED');
  assert.equal(startRes.executionEvidence.applied.text, 'unit-a:sequence-accepted');
  assert.ok(startElapsed < 1_000, `start sequence reply took ${startElapsed}ms >= 1000ms`);
  assert.deepEqual(writes.map((w) => [w.coil, w.value]), [[0, 1]]);

  await executor.waitForActiveSequences();
  assert.deepEqual(
    writes.map((w) => [w.coil, w.value]),
    [[0, 1], [1, 1], [2, 1], [3, 1], [3, 0]],
  );
  assert.deepEqual(
    writes.map((w) => w.atMs - t0),
    [0, 30_000, 60_000, 120_000, 120_500],
  );

  writes.length = 0;
  const t1 = getNowMs();
  const stopRes = await executor.handleCommand(makeCommand({
    commandId: 'cmd-unit-a-stop',
    issuedAt: t1,
    expireAt: t1 + 15_000,
    deviceId: 'ctrl-chiller-a',
    commandCode: 'STOP_UNIT_SEQUENCE',
    actuationMode: 'UNIT_SEQUENCE',
    channel: 'unit-a',
    executionFence: 2,
  }));
  const stopElapsed = getNowMs() - t1;
  assert.equal(stopRes.edgeStatus, 'EXECUTED');
  assert.equal(stopRes.executionEvidence.applied.text, 'unit-a:sequence-accepted');
  assert.ok(stopElapsed < 1_000, `stop sequence reply took ${stopElapsed}ms >= 1000ms`);
  assert.deepEqual(writes.map((w) => [w.coil, w.value]), [[16, 1], [16, 0]]);

  await executor.waitForActiveSequences();
  assert.deepEqual(
    writes.map((w) => [w.coil, w.value]),
    [[16, 1], [16, 0], [2, 0], [1, 0], [0, 0]],
  );
  assert.deepEqual(
    writes.map((w) => w.atMs - t1),
    [0, 500, 30_500, 40_500, 50_500],
  );
});

test('executor enforces mid-sequence abort without stopping running steady pumps, expiry, and stale fences', async () => {
  let step = 0;
  let now = 1_763_120_000_000;
  const writes: Array<[number, 0 | 1]> = [];
  const replies: CommandReplyEnvelope[] = [];
  let executorRef: SongyaxuanCommandExecutor | null = null;
  executorRef = new SongyaxuanCommandExecutor({
    coilWriter: {
      async writeCoil(_unitId, coil, value) {
        writes.push([coil, value]);
      },
    },
    publishReply: (_topic, reply) => {
      replies.push(reply);
    },
    nowMs: () => now,
    sleepMs: async (ms, signal) => {
      step += 1;
      if (step === 2) {
        executorRef?.abortUnitSequence('unit-a');
      }
      if (!signal?.aborted) now += ms;
    },
  });

  await executorRef.initialize();
  writes.length = 0;

  const accepted = await executorRef.handleCommand(makeCommand({
    commandId: 'cmd-abort-seq',
    deviceId: 'ctrl-chiller-a',
    commandCode: 'START_UNIT_SEQUENCE',
    actuationMode: 'UNIT_SEQUENCE',
    channel: 'unit-a',
    executionFence: 2,
  }));
  assert.equal(accepted.edgeStatus, 'EXECUTED');
  assert.equal(accepted.executionEvidence.applied.text, 'unit-a:sequence-accepted');

  await executorRef.waitForActiveSequences();
  // CHWP(0) and CWP(1) were started before step 2 abort and must remain untouched (no [0,0] or [1,0] writes, no chiller pulse [3,1]).
  assert.deepEqual(writes, [[0, 1], [1, 1]]);

  const staleFence = await executorRef.handleCommand(makeCommand({
    commandId: 'cmd-stale',
    deviceId: 'ctrl-chiller-a',
    commandCode: 'STOP',
    actuationMode: 'PULSE',
    channel: 'u51',
    coilAddress: 16,
    executionFence: 1,
  }));
  assert.equal(staleFence.edgeStatus, 'REJECTED');
  assert.equal(staleFence.reasonCode, 'STALE_EXECUTION_FENCE');

  const expired = await executorRef.handleCommand(makeCommand({
    commandId: 'cmd-expired',
    deviceId: 'ctrl-chiller-a',
    commandCode: 'STOP',
    actuationMode: 'PULSE',
    channel: 'u51',
    coilAddress: 16,
    expireAt: now - 1,
    executionFence: 3,
  }));
  assert.equal(expired.edgeStatus, 'EXPIRED');
  assert.equal(expired.reasonCode, 'COMMAND_EXPIRED');
  assert.equal(COMMAND_TOPIC, 'hvac/v1/01a14000-4000-7000-8000-000000000100/down/command');
});

test('executor aborts START_UNIT_SEQUENCE before chiller pulse when coilReader reports cwp=0', async () => {
  const readCoils: number[] = [];
  const { executor, writes, telemetries, diagnostics } = createFakeExecutor({
    coilReader: async (coilAddress) => {
      readCoils.push(coilAddress);
      return coilAddress === 1 ? 0 : 1;
    },
  });
  await executor.initialize();
  writes.length = 0;
  telemetries.length = 0;

  const accepted = await executor.handleCommand(makeCommand({
    commandId: 'cmd-prepulse-guard',
    deviceId: 'ctrl-chiller-a',
    commandCode: 'START_UNIT_SEQUENCE',
    actuationMode: 'UNIT_SEQUENCE',
    channel: 'unit-a',
    executionFence: 1,
  }));
  assert.equal(accepted.edgeStatus, 'EXECUTED');
  assert.equal(accepted.executionEvidence.applied.text, 'unit-a:sequence-accepted');

  await executor.waitForActiveSequences();
  assert.deepEqual(readCoils, [0, 1, 2]);
  assert.deepEqual(writes.map((w) => [w.coil, w.value]), [[0, 1], [1, 1], [2, 1]]);
  assert.ok(diagnostics.some((event) => event.code === 'PRE_PULSE_COIL_OPEN' && event.unitCode === 'unit-a'));
  const lastDevices = telemetries.at(-1)?.payload.payload.devices ?? [];
  assert.equal(lastDevices.find((d) => d.deviceId === 'et1010')?.points.find((p) => p.code === 'u12')?.value, 0);
  assert.equal(lastDevices.find((d) => d.deviceId === 'ctrl-cwp-1')?.points.find((p) => p.code === 'run_state')?.value, 'STOPPED');
});

test('executor immediately aborts unit countdown when single-device STOP arrives for ctrl-chwp-1', async () => {
  let resolveEnteredSleep: () => void = () => {};
  const enteredSleep = new Promise<void>((resolve) => {
    resolveEnteredSleep = resolve;
  });
  const { executor, writes } = createFakeExecutor({
    sleepMs: (_ms, signal) => new Promise((resolve) => {
      resolveEnteredSleep();
      if (signal?.aborted) {
        resolve();
        return;
      }
      signal?.addEventListener('abort', () => resolve(), { once: true });
    }),
  });
  await executor.initialize();
  writes.length = 0;

  const startReply = await executor.handleCommand(makeCommand({
    commandId: 'cmd-seq-before-stop',
    deviceId: 'ctrl-chiller-a',
    commandCode: 'START_UNIT_SEQUENCE',
    actuationMode: 'UNIT_SEQUENCE',
    channel: 'unit-a',
    executionFence: 1,
  }));
  assert.equal(startReply.edgeStatus, 'EXECUTED');
  assert.equal(startReply.executionEvidence.applied.text, 'unit-a:sequence-accepted');

  await enteredSleep;
  const stopReply = await executor.handleCommand(makeCommand({
    commandId: 'cmd-stop-chwp1',
    deviceId: 'ctrl-chwp-1',
    commandCode: 'STOP',
    actuationMode: 'LEVEL',
    channel: 'u11',
    coilAddress: 0,
    executionFence: 1,
  }));
  assert.equal(stopReply.edgeStatus, 'EXECUTED');

  await executor.waitForActiveSequences();
  assert.deepEqual(writes.map((w) => [w.coil, w.value]), [[0, 1], [0, 0]]);
});

test('executor handles EMERGENCY_DO_RELEASE by aborting all active sequences and zeroing coils 0..19', async () => {
  let sleepingCount = 0;
  let resolveAllSleeping: () => void = () => {};
  const allSleeping = new Promise<void>((resolve) => {
    resolveAllSleeping = resolve;
  });

  const { executor, writes, telemetries } = createFakeExecutor({
    sleepMs: (_ms, signal) => new Promise((resolve) => {
      sleepingCount += 1;
      if (sleepingCount >= 4) resolveAllSleeping();
      if (signal?.aborted) {
        resolve();
        return;
      }
      signal?.addEventListener('abort', () => resolve(), { once: true });
    }),
  });

  for (const [idx, unit] of (['a', 'b', 'c', 'd'] as const).entries()) {
    const reply = await executor.handleCommand(makeCommand({
      commandId: `cmd-start-${unit}`,
      deviceId: `ctrl-chiller-${unit}`,
      commandCode: 'START_UNIT_SEQUENCE',
      actuationMode: 'UNIT_SEQUENCE',
      channel: `unit-${unit}`,
      executionFence: idx + 1,
    }));
    assert.equal(reply.edgeStatus, 'EXECUTED');
    assert.equal(reply.executionEvidence.applied.text, `unit-${unit}:sequence-accepted`);
  }

  await allSleeping;
  writes.length = 0;
  telemetries.length = 0;

  const emergencyReply = await executor.handleCommand(makeCommand({
    commandId: 'cmd-emergency-stop-all',
    deviceId: 'et1010',
    pointCode: 'emergency_stop_all',
    commandCode: 'STOP',
    actuationMode: 'EMERGENCY_DO_RELEASE',
    channel: 'all-do',
    executionFence: 1,
  }));
  await executor.waitForActiveSequences();

  assert.equal(emergencyReply.edgeStatus, 'EXECUTED');
  assert.equal(emergencyReply.executionEvidence.applied.text, 'all-do:0');
  assert.deepEqual(
    writes.map((w) => [w.coil, w.value]),
    Array.from({ length: 20 }, (_, coil): [number, 0 | 1] => [coil, 0]),
  );

  const devices = telemetries.at(-1)?.payload.payload.devices ?? [];
  const expectedControllers = [
    'ctrl-chwp-1', 'ctrl-cwp-1', 'ctrl-tower-1',
    'ctrl-chwp-2', 'ctrl-cwp-2', 'ctrl-tower-2',
    'ctrl-chwp-3', 'ctrl-cwp-3', 'ctrl-tower-3',
    'ctrl-chwp-4', 'ctrl-cwp-4', 'ctrl-tower-4',
  ];
  for (const ctrl of expectedControllers) {
    assert.equal(devices.find((d) => d.deviceId === ctrl)?.points.find((p) => p.code === 'run_state')?.value, 'STOPPED');
  }
  const et1010 = devices.find((d) => d.deviceId === 'et1010');
  assert.equal(et1010?.points.find((p) => p.code === 'run_state')?.value, 'STOPPED');
  for (const ch of Object.keys(CHANNEL_COILS)) {
    assert.equal(et1010?.points.find((p) => p.code === ch)?.value, 0, `expected ${ch} to be 0`);
  }

  writes.length = 0;
  const fallbackReply = await executor.handleCommand(makeCommand({
    commandId: 'cmd-emergency-stop-fallback',
    deviceId: 'et1010',
    pointCode: 'emergency_stop_all',
    commandCode: 'STOP',
    executionFence: 2,
  }));
  assert.equal(fallbackReply.edgeStatus, 'EXECUTED');
  assert.equal(fallbackReply.executionEvidence.applied.text, 'all-do:0');
  assert.equal(writes.length, 20);
});

test('executor initializes all 8 pulse coils and recovers pending coils with retry', async () => {
  const persisted = createMemoryStateStore({
    lastSequence: 1_763_120_000_000 * 1000 + 50,
    lastFenceByDevice: { 'ctrl-chwp-1': 5 },
    executedCommandIds: ['cmd-before-crash'],
    pendingPulseCoils: [3, 19],
  });

  let coil19FailuresRemaining = 2;
  const { executor, writes, telemetries, diagnostics } = createFakeExecutor({
    stateStore: persisted.store,
    onWriteCoil: (_unitId, coil, value) => {
      if (coil === 19 && value === 0 && coil19FailuresRemaining > 0) {
        coil19FailuresRemaining -= 1;
        throw new Error('transient RS485 collision');
      }
    },
  });

  await executor.initialize();
  assert.deepEqual(
    writes.map((w) => [w.coil, w.value]),
    PULSE_COILS.map((coil) => [coil, 0]),
  );
  assert.equal(coil19FailuresRemaining, 0);
  assert.ok(diagnostics.some((d) => d.code === 'STARTUP_PENDING_PULSE_RESET' && d.level === 'warn'));
  assert.equal(telemetries.length, 1);
  assertValidTelemetryEnvelope20(telemetries[0]!);
  assert.ok(telemetries[0]!.payload.sequence > 1_763_120_000_000 * 1000 + 50);
  const etPoints = telemetries[0]!.payload.payload.devices.find((d) => d.deviceId === 'et1010')?.points ?? [];
  assert.equal(etPoints.length, 8);
  assert.ok(etPoints.every((p) => p.value === 0));
  assert.deepEqual(persisted.getState()?.pendingPulseCoils, []);
});

test('executor auto-initializes pulse coils before first command when initialize() was skipped', async () => {
  const autoStore = createMemoryStateStore();
  const autoExec = createFakeExecutor({ stateStore: autoStore.store });
  const levelReply = await autoExec.executor.handleCommand(makeCommand({
    commandId: 'cmd-auto-init-level',
    deviceId: 'ctrl-chwp-1',
    commandCode: 'START',
    actuationMode: 'LEVEL',
    channel: 'u11',
    coilAddress: 0,
  }));
  assert.equal(levelReply.edgeStatus, 'EXECUTED');
  assert.deepEqual(
    autoExec.writes.map((w) => [w.coil, w.value]),
    [...PULSE_COILS.map((coil) => [coil, 0]), [0, 1]],
  );
});

test('executor keeps unconfirmed pendingPulseCoils on initialize reset failure', async () => {
  const startStore = createMemoryStateStore({
    lastSequence: 0,
    lastFenceByDevice: {},
    executedCommandIds: [],
    pendingPulseCoils: [16],
  });
  const initFailing = createFakeExecutor({
    stateStore: startStore.store,
    failResetOnCoil: 16,
  });
  await initFailing.executor.initialize();
  assert.deepEqual(startStore.getState()?.pendingPulseCoils, [16]);
  assert.ok(initFailing.diagnostics.some((d) => d.code === 'PULSE_RESET_FAILED' && d.level === 'error'));
  const telemetryPoints = initFailing.telemetries[0]?.payload.payload.devices[0]?.points ?? [];
  assert.equal(telemetryPoints.length, 7);
  assert.equal(telemetryPoints.some((p) => p.code === 'u51'), false);
});

test('executor persists pendingPulseCoils before pulse write and clears after 3rd reset attempt', async () => {
  const pulseStore = createMemoryStateStore();
  let resetAttempts = 0;
  let savedPendingBeforeCoil1 = false;
  const pulseExec = createFakeExecutor({
    stateStore: pulseStore.store,
    onWriteCoil: (_unitId, coil, value) => {
      if (coil === 3 && value === 1) {
        savedPendingBeforeCoil1 = (pulseStore.getState()?.pendingPulseCoils ?? []).includes(3);
      }
      if (coil === 3 && value === 0 && pulseExec.writes.some((w) => w.coil === 3 && w.value === 1)) {
        resetAttempts += 1;
        if (resetAttempts < 3) {
          throw new Error(`reset attempt ${resetAttempts} failed`);
        }
      }
    },
  });
  await pulseExec.executor.initialize();
  const pulseReply = await pulseExec.executor.handleCommand(makeCommand({
    commandId: 'cmd-pulse-retry-ok',
    deviceId: 'ctrl-chiller-a',
    commandCode: 'START',
    actuationMode: 'PULSE',
    channel: 'u14',
    coilAddress: 3,
    pulseDurationMs: 500,
    executionFence: 1,
  }));
  assert.equal(pulseReply.edgeStatus, 'EXECUTED');
  assert.equal(savedPendingBeforeCoil1, true);
  assert.equal(resetAttempts, 3);
  assert.deepEqual(pulseStore.getState()?.pendingPulseCoils, []);
});

test('executor reports PULSE_RESET_FAILED and keeps pending coil when executePulse reset fails permanently', async () => {
  const permStore = createMemoryStateStore();
  const permFail = createFakeExecutor({
    stateStore: permStore.store,
    failResetOnCoil: 17,
  });
  await permFail.executor.initialize();
  permFail.diagnostics.length = 0;
  const permReply = await permFail.executor.handleCommand(makeCommand({
    commandId: 'cmd-pulse-perm-fail',
    deviceId: 'ctrl-chiller-b',
    commandCode: 'STOP',
    actuationMode: 'PULSE',
    channel: 'u52',
    coilAddress: 17,
    pulseDurationMs: 500,
    executionFence: 1,
  }));
  assert.equal(permReply.edgeStatus, 'FAILED');
  assert.equal(permReply.reasonCode, 'PULSE_RESET_FAILED');
  assert.deepEqual(permStore.getState()?.pendingPulseCoils, [17]);
  assert.ok(permFail.diagnostics.some((d) => d.code === 'PULSE_RESET_FAILED' && d.commandId === 'cmd-pulse-perm-fail'));
  assert.ok(new PulseResetError('x') instanceof Error);
});

test('executor validates NaN/non-finite timestamps, checks duplicate/expiry before mutating fence, and persists monotonic sequence across restarts', async () => {
  const shared = createMemoryStateStore();
  const run1 = createFakeExecutor({ stateStore: shared.store, startNowMs: 1_763_120_000_000 });
  await run1.executor.initialize();
  run1.writes.length = 0;

  const nanExpire = await run1.executor.handleCommand(makeCommand({
    commandId: 'cmd-nan-expire',
    expireAt: Number.NaN,
    executionFence: 5,
  }));
  assert.equal(nanExpire.edgeStatus, 'REJECTED');
  assert.equal(nanExpire.reasonCode, 'INVALID_COMMAND_ENVELOPE');

  const nanIssued = await run1.executor.handleCommand(makeCommand({
    commandId: 'cmd-nan-issued',
    issuedAt: Number.NaN,
    expireAt: 1_763_120_015_000,
    executionFence: 5,
  }));
  assert.equal(nanIssued.edgeStatus, 'REJECTED');
  assert.equal(nanIssued.reasonCode, 'INVALID_COMMAND_ENVELOPE');

  const badWindow = await run1.executor.handleCommand(makeCommand({
    commandId: 'cmd-bad-window',
    issuedAt: 1_763_120_000_000,
    expireAt: 1_763_120_000_000,
    executionFence: 5,
  }));
  assert.equal(badWindow.edgeStatus, 'REJECTED');
  assert.equal(badWindow.reasonCode, 'INVALID_COMMAND_ENVELOPE');

  const expiredHighFence = await run1.executor.handleCommand(makeCommand({
    commandId: 'cmd-expired-fence5',
    issuedAt: 1_763_119_900_000,
    expireAt: 1_763_119_999_000,
    executionFence: 5,
  }));
  assert.equal(expiredHighFence.edgeStatus, 'EXPIRED');
  assert.equal(expiredHighFence.reasonCode, 'COMMAND_EXPIRED');

  const validFence5 = await run1.executor.handleCommand(makeCommand({
    commandId: 'cmd-valid-fence5',
    deviceId: 'ctrl-chwp-1',
    commandCode: 'START',
    actuationMode: 'LEVEL',
    channel: 'u11',
    coilAddress: 0,
    executionFence: 5,
  }));
  assert.equal(validFence5.edgeStatus, 'EXECUTED');
  assert.equal(run1.writes.length, 1);

  const lastSeqBeforeRestart = shared.getState()?.lastSequence ?? 0;
  assert.ok(lastSeqBeforeRestart >= 1_763_120_000_000 * 1000);

  const run2 = createFakeExecutor({ stateStore: shared.store, startNowMs: 1_763_119_990_000 });
  await run2.executor.initialize();
  run2.writes.length = 0;

  assert.ok((run2.telemetries[0]?.payload.sequence ?? 0) > lastSeqBeforeRestart);

  const duplicate = await run2.executor.handleCommand(makeCommand({
    commandId: 'cmd-valid-fence5',
    issuedAt: 1_763_119_990_000,
    expireAt: 1_763_120_005_000,
    executionFence: 6,
  }));
  assert.equal(duplicate.edgeStatus, 'REJECTED');
  assert.equal(duplicate.reasonCode, 'DUPLICATE_COMMAND');
  assert.equal(run2.writes.length, 0);

  const staleAfterRestart = await run2.executor.handleCommand(makeCommand({
    commandId: 'cmd-stale-restart',
    issuedAt: 1_763_119_990_000,
    expireAt: 1_763_120_005_000,
    executionFence: 5,
  }));
  assert.equal(staleAfterRestart.edgeStatus, 'REJECTED');
  assert.equal(staleAfterRestart.reasonCode, 'STALE_EXECUTION_FENCE');
});

test('Rs485BusLock enforces mutual exclusion, priority ordering, and FIFO tie-breaking without microtask barge-in', async () => {
  const lock = new Rs485BusLock();
  const order: string[] = [];
  let active = 0;
  let maxActive = 0;

  let releaseGate!: () => void;
  const gate = new Promise<void>((resolve) => {
    releaseGate = resolve;
  });

  const first = lock.runExclusive(BUS_PRIORITY_NORMAL, async () => {
    active += 1;
    maxActive = Math.max(maxActive, active);
    order.push('first-start');
    await gate;
    order.push('first-end');
    active -= 1;
  });
  const low1 = lock.runExclusive(BUS_PRIORITY_NORMAL, async () => {
    active += 1;
    maxActive = Math.max(maxActive, active);
    order.push('low-1');
    active -= 1;
  });
  const low2 = lock.runExclusive(BUS_PRIORITY_NORMAL, async () => {
    active += 1;
    maxActive = Math.max(maxActive, active);
    order.push('low-2');
    active -= 1;
  });
  const high1 = lock.runExclusive(BUS_PRIORITY_PULSE_RESET, async () => {
    active += 1;
    maxActive = Math.max(maxActive, active);
    order.push('high-1');
    active -= 1;
  });
  const high2 = lock.runExclusive(BUS_PRIORITY_PULSE_RESET, async () => {
    active += 1;
    maxActive = Math.max(maxActive, active);
    order.push('high-2');
    active -= 1;
  });

  releaseGate();
  await first;
  const bargeIn = lock.runExclusive(BUS_PRIORITY_PULSE_RESET, async () => {
    active += 1;
    maxActive = Math.max(maxActive, active);
    order.push('barge-in');
    active -= 1;
  });
  await Promise.all([low1, low2, high1, high2, bargeIn]);
  assert.equal(maxActive, 1);
  assert.deepEqual(order, ['first-start', 'first-end', 'high-1', 'high-2', 'barge-in', 'low-1', 'low-2']);
});

test('Rs485BusLock lets pulse-reset preempt queued ConcentratorRpcExecutor transactions', async () => {
  const sharedLock = new Rs485BusLock();
  const busEvents: string[] = [];
  let pulseSleepEntered!: () => void;
  const inPulseSleep = new Promise<void>((resolve) => {
    pulseSleepEntered = resolve;
  });
  let finishPulseSleep!: () => void;
  const pulseSleepGate = new Promise<void>((resolve) => {
    finishPulseSleep = resolve;
  });
  let firstRpcEntered!: () => void;
  const firstRpcStarted = new Promise<void>((resolve) => {
    firstRpcEntered = resolve;
  });
  let releaseFirstRpc!: () => void;
  const firstRpcGate = new Promise<void>((resolve) => {
    releaseFirstRpc = resolve;
  });
  let rpcCounter = 0;
  const modbus: ModbusBusClient = {
    async readHoldingRegisters(_unitId, address) {
      if (address === REG.EID_BASE) {
        rpcCounter += 1;
        const idx = rpcCounter;
        busEvents.push(`rpc-${idx}-start`);
        if (idx === 1) {
          firstRpcEntered();
          await firstRpcGate;
        }
      }
      if (address === REG.EID_BASE + 180 * 2) {
        busEvents.push(`rpc-${rpcCounter}-end`);
      }
      return new Array(40).fill(0);
    },
    async readInputRegisters() {
      return new Array(40).fill(0);
    },
    async writeRegister() {},
    async writeRegisters() {},
  };
  const rpcExec = new ConcentratorRpcExecutor(modbus, { settleMs: 0, busLock: sharedLock });
  const cmdExec = createFakeExecutor({
    busLock: sharedLock,
    onWriteCoil: (_unitId, coil, value) => {
      busEvents.push(`coil-${coil}=${value}`);
    },
    sleepMs: async () => {
      pulseSleepEntered();
      await pulseSleepGate;
    },
  });
  await cmdExec.executor.initialize();
  busEvents.length = 0;

  const pulsePromise = cmdExec.executor.handleCommand(makeCommand({
    commandId: 'cmd-bus-pulse',
    deviceId: 'ctrl-chiller-a',
    commandCode: 'START',
    actuationMode: 'PULSE',
    channel: 'u14',
    coilAddress: 3,
    executionFence: 1,
  }));
  await inPulseSleep;

  const rpcFirst = rpcExec.execute({
    schemaVersion: '1.0',
    requestId: '018f3d00-3000-7000-8000-000000000001',
    gatewayId: '018f3d00-2000-7000-8000-000000000001',
    unitId: 1,
    operation: 'READ_EIDS',
    issuedAt: 1000,
    expireAt: Date.now() + 10_000,
  });
  await firstRpcStarted;

  const rpcQueued = rpcExec.execute({
    schemaVersion: '1.0',
    requestId: '018f3d00-3000-7000-8000-000000000002',
    gatewayId: '018f3d00-2000-7000-8000-000000000001',
    unitId: 1,
    operation: 'READ_EIDS',
    issuedAt: 1000,
    expireAt: Date.now() + 10_000,
  });

  finishPulseSleep();
  await Promise.resolve();
  await Promise.resolve();
  releaseFirstRpc();

  const [res1, res2, pulseReply] = await Promise.all([rpcFirst, rpcQueued, pulsePromise]);
  assert.equal(res1.status, 'OK');
  assert.equal(res2.status, 'OK');
  assert.equal(pulseReply.edgeStatus, 'EXECUTED');
  assert.deepEqual(busEvents, ['coil-3=1', 'rpc-1-start', 'rpc-1-end', 'coil-3=0', 'rpc-2-start', 'rpc-2-end']);
});

test('executor supports explicit ABORT_UNIT_SEQUENCE without pulsing idle chillers', async () => {
  let releaseSleep!: () => void;
  const sleepEntered = new Promise<void>((resolve) => {
    releaseSleep = resolve;
  });
  let finishSleep!: () => void;
  const abortExec = createFakeExecutor({
    sleepMs: (_ms, signal) => new Promise((resolve) => {
      releaseSleep();
      finishSleep = resolve;
      if (signal?.aborted) {
        resolve();
        return;
      }
      signal?.addEventListener('abort', () => resolve(), { once: true });
    }),
  });
  await abortExec.executor.initialize();
  abortExec.writes.length = 0;

  const startRes = await abortExec.executor.handleCommand(makeCommand({
    commandId: 'cmd-start-unit-a-abortable',
    deviceId: 'ctrl-chiller-a',
    commandCode: 'START_UNIT_SEQUENCE',
    actuationMode: 'UNIT_SEQUENCE',
    channel: 'unit-a',
    executionFence: 1,
  }));
  assert.equal(startRes.edgeStatus, 'EXECUTED');
  await sleepEntered;

  const abortByCode = await abortExec.executor.handleCommand(makeCommand({
    commandId: 'cmd-abort-unit-a',
    deviceId: 'ctrl-chiller-a',
    commandCode: 'ABORT_UNIT_SEQUENCE',
    actuationMode: 'ABORT_UNIT_SEQUENCE',
    channel: 'unit-a',
    executionFence: 2,
  }));
  assert.equal(abortByCode.edgeStatus, 'EXECUTED');
  assert.equal(abortByCode.executionEvidence.applied.text, 'unit-a:sequence-aborted');
  await abortExec.executor.waitForActiveSequences();
  assert.deepEqual(abortExec.writes.map((w) => [w.coil, w.value]), [[0, 1]]);
  finishSleep?.();

  const idleAbort = await abortExec.executor.handleCommand(makeCommand({
    commandId: 'cmd-abort-unit-b-idle',
    deviceId: 'ctrl-chiller-b',
    commandCode: 'STOP_UNIT_SEQUENCE',
    actuationMode: 'ABORT_UNIT_SEQUENCE',
    channel: 'unit-b',
    executionFence: 1,
  }));
  assert.equal(idleAbort.edgeStatus, 'EXECUTED');
  assert.equal(idleAbort.executionEvidence.applied.text, 'unit-b:sequence-aborted');
  assert.equal(abortExec.writes.length, 1);

  const badAbortRoute = await abortExec.executor.handleCommand(makeCommand({
    commandId: 'cmd-abort-invalid-route',
    deviceId: 'ctrl-chwp-1',
    commandCode: 'ABORT_UNIT_SEQUENCE',
    executionFence: 1,
  }));
  assert.equal(badAbortRoute.edgeStatus, 'REJECTED');
  assert.equal(badAbortRoute.reasonCode, 'UNKNOWN_ACTUATION_ROUTE');
});

test('STOP_UNIT_SEQUENCE holds auxiliaries when chillerPowerReader remains above 5 kW', async () => {
  let powerChecks = 0;
  const heldExec = createFakeExecutor({
    chillerPowerReader: async () => {
      powerChecks += 1;
      return 84.5;
    },
  });
  await heldExec.executor.initialize();
  heldExec.writes.length = 0;
  const stopHeld = await heldExec.executor.handleCommand(makeCommand({
    commandId: 'cmd-stop-held',
    deviceId: 'ctrl-chiller-a',
    commandCode: 'STOP_UNIT_SEQUENCE',
    actuationMode: 'UNIT_SEQUENCE',
    channel: 'unit-a',
    executionFence: 1,
  }));
  assert.equal(stopHeld.edgeStatus, 'EXECUTED');
  await heldExec.executor.waitForActiveSequences();
  assert.equal(powerChecks, 3);
  assert.deepEqual(heldExec.writes.map((w) => [w.coil, w.value]), [[16, 1], [16, 0]]);
  assert.ok(heldExec.diagnostics.some((d) => d.code === 'CHILLER_STOP_UNCONFIRMED_AUX_HELD' && d.unitCode === 'unit-a'));
});

test('STOP_UNIT_SEQUENCE completes auxiliary shutdown once chillerPowerReader drops to <= 5 kW on recheck', async () => {
  let recheckSample = 0;
  const coastExec = createFakeExecutor({
    chillerPowerReader: async () => {
      recheckSample += 1;
      return recheckSample === 1 ? 18.0 : 3.2;
    },
  });
  await coastExec.executor.initialize();
  coastExec.writes.length = 0;
  await coastExec.executor.handleCommand(makeCommand({
    commandId: 'cmd-stop-coast-ok',
    deviceId: 'ctrl-chiller-a',
    commandCode: 'STOP_UNIT_SEQUENCE',
    actuationMode: 'UNIT_SEQUENCE',
    channel: 'unit-a',
    executionFence: 1,
  }));
  await coastExec.executor.waitForActiveSequences();
  assert.equal(recheckSample, 2);
  assert.deepEqual(coastExec.writes.map((w) => [w.coil, w.value]), [[16, 1], [16, 0], [2, 0], [1, 0], [0, 0]]);
});

test('songyaxuan-platform-envelope decouples C/D auxiliary load from chiller run_state', () => {
  const ctx = new Map<string, unknown>();
  const ts = 1_763_120_000_000;

  runPlatformEnvelopeNode({
    et1010: [{ ts, values: { u31: 1, u32: 1, u33: 1, u41: 1, u42: 1, u43: 1 } }],
  }, ctx);

  const unitCPumpsOnly = runPlatformEnvelopeNode({
    'ammeter-unit1': [{ ts: ts + 1000, values: { powerTotal: 34, combinedActiveTotalElectricalEnergy: 100 } }],
  }, ctx);
  const meterC = unitCPumpsOnly.payload.payload.devices.find((d) => d.deviceId === 'ammeter-unit1');
  const chillerC = unitCPumpsOnly.payload.payload.devices.find((d) => d.deviceId === 'ctrl-chiller-c');
  assert.equal(meterC?.points.find((p) => p.code === 'power_total')?.value, 34);
  assert.equal(meterC?.points.find((p) => p.code === 'run_state')?.value, 'STOPPED');
  assert.equal(chillerC?.points.find((p) => p.code === 'run_state')?.value, 'STOPPED');

  const unitCCompressorOn = runPlatformEnvelopeNode({
    'ammeter-unit1': [{ ts: ts + 2000, values: { powerTotal: 55, combinedActiveTotalElectricalEnergy: 101 } }],
  }, ctx);
  assert.equal(unitCCompressorOn.payload.payload.devices.find((d) => d.deviceId === 'ctrl-chiller-c')?.points[0]?.value, 'RUNNING');

  const unitCCompressorOff = runPlatformEnvelopeNode({
    'ammeter-unit1': [{ ts: ts + 3000, values: { powerTotal: 38, combinedActiveTotalElectricalEnergy: 102 } }],
  }, ctx);
  assert.equal(unitCCompressorOff.payload.payload.devices.find((d) => d.deviceId === 'ctrl-chiller-c')?.points[0]?.value, 'STOPPED');

  const combinedSameMsg = runPlatformEnvelopeNode({
    'ammeter-unit2': [{ ts: ts + 4000, values: { powerTotal: 34, combinedActiveTotalElectricalEnergy: 200 } }],
    et1010: [{ ts: ts + 4000, values: { u41: 1, u42: 1, u43: 1 } }],
  });
  assert.equal(combinedSameMsg.payload.payload.devices.find((d) => d.deviceId === 'ctrl-chiller-d')?.points[0]?.value, 'STOPPED');

  const unitDCompressorOn = runPlatformEnvelopeNode({
    'ammeter-unit2': [{ ts: ts + 5000, values: { powerTotal: 55, combinedActiveTotalElectricalEnergy: 201 } }],
  }, ctx);
  assert.equal(unitDCompressorOn.payload.payload.devices.find((d) => d.deviceId === 'ctrl-chiller-d')?.points[0]?.value, 'RUNNING');

  // Local MCC manual pumps with ET1010 coils at 0 fall back to C/D feeder hysteresis ([57.75, 45]).
  const manualCtx = new Map<string, unknown>();
  runPlatformEnvelopeNode({
    et1010: [{ ts: ts + 6000, values: { u31: 0, u32: 0, u33: 0, u41: 0, u42: 0, u43: 0 } }],
  }, manualCtx);
  for (const auxKw of [20.6, 34.4]) {
    const manualAux = runPlatformEnvelopeNode({
      'ammeter-unit2': [{ ts: ts + 7000 + auxKw, values: { powerTotal: auxKw, combinedActiveTotalElectricalEnergy: 202 } }],
    }, manualCtx);
    assert.equal(manualAux.payload.payload.devices.find((d) => d.deviceId === 'ammeter-unit2')?.points.find((p) => p.code === 'run_state')?.value, 'STOPPED');
    assert.equal(manualAux.payload.payload.devices.find((d) => d.deviceId === 'ctrl-chiller-d')?.points[0]?.value, 'STOPPED');
  }
  const manualChillerOn = runPlatformEnvelopeNode({
    'ammeter-unit2': [{ ts: ts + 8000, values: { powerTotal: 58, combinedActiveTotalElectricalEnergy: 203 } }],
  }, manualCtx);
  assert.equal(manualChillerOn.payload.payload.devices.find((d) => d.deviceId === 'ammeter-unit2')?.points.find((p) => p.code === 'run_state')?.value, 'RUNNING');
  assert.equal(manualChillerOn.payload.payload.devices.find((d) => d.deviceId === 'ctrl-chiller-d')?.points[0]?.value, 'RUNNING');

  // Live ET1010 UID31 modbusRead emits raw coil keys u0..u19; normalize to u11..u54 for storage and coil subtraction.
  const rawCtx = new Map<string, unknown>();
  const rawOut = runPlatformEnvelopeNode({
    et1010: [{
      ts: ts + 9000,
      values: {
        u0: 1, u1: 0, u2: 0, u3: 0,
        u4: 0, u5: 0, u6: 0, u7: 0,
        u8: 0, u9: 0, u10: 0, u11: 0,
        u12: 1, u13: 1, u14: 0, u15: 0,
        u16: 0, u17: 0, u18: 0, u19: 1,
      },
    }],
    'ammeter-unit2': [{ ts: ts + 9000, values: { powerTotal: 34, combinedActiveTotalElectricalEnergy: 204 } }],
  }, rawCtx);
  const rawDevices = new Map(rawOut.payload.payload.devices.map((d) => [d.deviceId, d.points]));
  assert.equal(rawDevices.get('et1010')?.find((p) => p.code === 'u11')?.value, 1);
  assert.equal(rawDevices.get('et1010')?.find((p) => p.code === 'u41')?.value, 1);
  assert.equal(rawDevices.get('et1010')?.find((p) => p.code === 'u54')?.value, 1);
  assert.equal(rawDevices.get('et1010')?.find((p) => p.code === 'u0'), undefined);
  assert.equal(rawDevices.get('ctrl-chwp-1')?.find((p) => p.code === 'run_state')?.value, 'RUNNING');
  assert.equal(rawDevices.get('ctrl-chwp-4')?.find((p) => p.code === 'run_state')?.value, 'RUNNING');
  assert.equal(rawDevices.get('ctrl-cwp-4')?.find((p) => p.code === 'run_state')?.value, 'RUNNING');
  // Unit D subtracts 30 kW (u41+u42) -> chillerKw 4 kW <= 12.75 kW -> STOPPED.
  assert.equal(rawDevices.get('ammeter-unit2')?.find((p) => p.code === 'run_state')?.value, 'STOPPED');
  assert.equal(rawDevices.get('ctrl-chiller-d')?.find((p) => p.code === 'run_state')?.value, 'STOPPED');
});

test('songyaxuan.site.json enforces chwp-main start interlock and 120s run_state freshness', () => {
  const site = JSON.parse(readFileSync(new URL('../deploy/sites/songyaxuan.site.json', import.meta.url), 'utf8'));
  const alarmRules = JSON.parse(readFileSync(new URL('../deploy/sites/songyaxuan.alarm-rules.json', import.meta.url), 'utf8'));
  assert.deepEqual(checkSiteFile(site, alarmRules), []);

  for (const chillerCode of ['ctrl-chiller-a', 'ctrl-chiller-b', 'ctrl-chiller-c', 'ctrl-chiller-d']) {
    const dev = site.devices.find((d: { code: string }) => d.code === chillerCode);
    const startPt = dev.points.find((p: { code: string }) => p.code === 'start');
    const hasMainFlowInterlock = startPt.sourceMetadata.interlocks.some(
      (item: { device: string; point: string; expected: string }) =>
        item.device === 'chwp-main' && item.point === 'run_state' && item.expected === 'RUNNING',
    );
    assert.equal(hasMainFlowInterlock, true, `${chillerCode}/start missing chwp-main interlock`);
  }

  const { telemetry } = buildOnboardingSql(site);
  const expected120Devices = [
    'chwp-main',
    'ctrl-chwp-1', 'ctrl-chwp-2', 'ctrl-chwp-3', 'ctrl-chwp-4',
    'ctrl-cwp-1', 'ctrl-cwp-2', 'ctrl-cwp-3', 'ctrl-cwp-4',
    'ctrl-tower-1', 'ctrl-tower-2', 'ctrl-tower-3', 'ctrl-tower-4',
  ];
  for (const code of expected120Devices) {
    const dev = site.devices.find((d: { code: string }) => d.code === code);
    const row = telemetry.split('\n').find((line: string) => line.startsWith(`('${dev.id}', 'run_state',`));
    assert.ok(row, `missing freshness row for ${code}/run_state`);
    assert.match(row, new RegExp(`^\\('${dev.id}', 'run_state', 1, 120, true, 60,`), `${code}/run_state must have fresh_within_seconds=120`);
  }
});

test('executor echoes W3C 32-hex traceId verbatim on CommandReplyEnvelope', async () => {
  const { executor, replies } = createFakeExecutor();
  await executor.initialize();

  const traceId = '4bf92f3577b34da6a3ce929d0e0e4736';
  const reply = await executor.handleCommand(makeCommand({
    commandId: 'cmd-trace-echo',
    traceId,
    deviceId: 'ctrl-chwp-1',
    commandCode: 'START',
    pointCode: 'start',
    actuationMode: 'LEVEL',
    channel: 'u11',
    coilAddress: 0,
    executionFence: 7,
  }));
  assert.equal(reply.edgeStatus, 'EXECUTED');
  assert.equal(reply.traceId, traceId);
  assert.equal(replies.length, 1);
  assert.equal(replies[0]?.reply.traceId, traceId);
});
