import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import vm from 'node:vm';

import {
  COMMAND_TOPIC,
  ET1010_UNIT_ID,
  REPLY_TOPIC,
  SongyaxuanCommandExecutor,
  TELEMETRY_TOPIC,
  WINNER_CONTROLLER_ID,
  type CommandReplyEnvelope,
  type DownlinkCommandEnvelope,
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

function createFakeExecutor(options: { failOnCoil?: number; failResetOnCoil?: number; startNowMs?: number } = {}) {
  let now = options.startNowMs ?? 1_763_120_000_000;
  let seq = 0;
  const writes: Array<{ unitId: number; coil: number; value: 0 | 1; atMs: number }> = [];
  const replies: Array<{ topic: string; reply: CommandReplyEnvelope }> = [];
  const telemetries: UplinkTelemetryMessage[] = [];
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
    publishReply: (topic, reply) => {
      replies.push({ topic, reply });
    },
    publishTelemetry: (message) => {
      telemetries.push(message);
    },
    nowMs: () => now,
    sleepMs: async (ms, signal) => {
      if (signal?.aborted) return;
      now += ms;
    },
    uuidv7: () => `019a7000-0000-7000-8000-${String(++seq).padStart(12, '0')}`,
  });

  return {
    executor,
    writes,
    replies,
    telemetries,
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

  const meterOut = runPlatformEnvelopeNode({
    'ammeter-unit3': [{ ts: 1_763_120_001_000, values: { powerTotal: 142.5, combinedActiveTotalElectricalEnergy: 1000 } }],
  });
  const chillerA = meterOut.payload.payload.devices.find((d) => d.deviceId === 'ctrl-chiller-a');
  assert.equal(chillerA?.points[0]?.value, 'RUNNING');
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

test('executor runs START_UNIT_SEQUENCE and STOP_UNIT_SEQUENCE in exact order within 15s reply window', async () => {
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
  assert.ok(startElapsed < 15_000, `start sequence took ${startElapsed}ms >= 15000ms`);
  assert.deepEqual(
    writes.map((w) => [w.coil, w.value]),
    [[0, 1], [1, 1], [2, 1], [3, 1], [3, 0]],
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
  assert.ok(stopElapsed < 15_000, `stop sequence took ${stopElapsed}ms >= 15000ms`);
  assert.deepEqual(
    writes.map((w) => [w.coil, w.value]),
    [[16, 1], [16, 0], [2, 0], [1, 0], [0, 0]],
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

  const aborted = await executorRef.handleCommand(makeCommand({
    commandId: 'cmd-abort-seq',
    deviceId: 'ctrl-chiller-a',
    commandCode: 'START_UNIT_SEQUENCE',
    actuationMode: 'UNIT_SEQUENCE',
    channel: 'unit-a',
    executionFence: 2,
  }));
  assert.equal(aborted.edgeStatus, 'REJECTED');
  assert.equal(aborted.reasonCode, 'SEQUENCE_ABORTED_BY_OPERATOR');
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
