import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import vm from 'node:vm';

import {
  CHANNEL_COILS,
  COMMAND_TOPIC,
  DEFAULT_SEQUENCE_TIMINGS,
  ET1010_UNIT_ID,
  REPLY_TOPIC,
  SongyaxuanCommandExecutor,
  TELEMETRY_TOPIC,
  WINNER_CONTROLLER_ID,
  type CommandReplyEnvelope,
  type DownlinkCommandEnvelope,
  type SequenceDiagnosticEvent,
  type SequenceTimings,
  type UplinkTelemetryMessage,
} from '../edge/node-red/songyaxuan-command-executor.ts';

function runPlatformEnvelopeNode(payload: Record<string, Array<{ ts: number; values: Record<string, number> }>>) {
  const source = readFileSync(new URL('../edge/node-red/songyaxuan-platform-envelope.js', import.meta.url), 'utf8');
  const state = new Map<string, unknown>();
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
      payload: { devices: Array<{ deviceId: string; deviceTimestamp: number; points: Array<{ code: string; value: number | string; quality: number }> }> };
    };
  };
}

function createFakeExecutor(options: {
  failOnCoil?: number;
  failResetOnCoil?: number;
  startNowMs?: number;
  coilReader?: (coilAddress: number) => Promise<0 | 1>;
  sleepMs?: (durationMs: number, signal?: AbortSignal) => Promise<void>;
  sequenceTimings?: Partial<SequenceTimings>;
} = {}) {
  let now = options.startNowMs ?? 1_763_120_000_000;
  let seq = 0;
  const writes: Array<{ unitId: number; coil: number; value: 0 | 1; atMs: number }> = [];
  const replies: Array<{ topic: string; reply: CommandReplyEnvelope }> = [];
  const telemetries: UplinkTelemetryMessage[] = [];
  const diagnostics: SequenceDiagnosticEvent[] = [];
  let failOnceOnReset = options.failResetOnCoil;

  const executor = new SongyaxuanCommandExecutor({
    coilWriter: {
      async writeCoil(unitId, coilAddress, value) {
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
    publishReply: (topic, reply) => {
      replies.push({ topic, reply });
    },
    publishTelemetry: (message) => {
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

test('executor handles LEVEL coils 0 and 14 with immediate state uplink and schemaVersion 2.0 reply', async () => {
  const { executor, writes, replies, telemetries } = createFakeExecutor();
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
