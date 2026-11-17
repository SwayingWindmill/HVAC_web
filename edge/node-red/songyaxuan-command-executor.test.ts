import test from 'node:test';
import assert from 'node:assert/strict';
import {
  SongyaxuanCommandExecutor,
  type CommandReplyEnvelope,
  type DownlinkCommandEnvelope,
  type ExecutorPersistedState,
  type ExecutorStateStore,
  type SequenceDiagnosticEvent,
} from './songyaxuan-command-executor.ts';

function createCommand(overrides: Partial<DownlinkCommandEnvelope>): DownlinkCommandEnvelope {
  return {
    schemaVersion: '2.0',
    messageId: 'msg-1',
    commandId: 'cmd-1',
    issuedAt: 1_763_120_000_000,
    expireAt: 1_763_120_060_000,
    deviceId: 'ctrl-chiller-a',
    commandCode: 'START_UNIT_SEQUENCE',
    executionFence: 1,
    payloadHash: 'hash-1',
    ...overrides,
  };
}

function createHarness(options: {
  stateStore?: ExecutorStateStore;
  sleepMs?: (durationMs: number, signal?: AbortSignal) => Promise<void>;
  onWriteCoil?: (coil: number, value: 0 | 1) => Promise<void> | void;
  sequenceTimings?: Partial<{
    pumpToCoolingPumpMs: number;
    coolingPumpToTowerMs: number;
    towerToChillerPulseMs: number;
    chillerStopCoastDownMs: number;
    auxiliaryStopStepMs: number;
  }>;
} = {}) {
  const writes: Array<{ unitId: number; coil: number; value: 0 | 1 }> = [];
  const replies: CommandReplyEnvelope[] = [];
  const diagnostics: SequenceDiagnosticEvent[] = [];
  const telemetries: Array<{ deviceId: string; code: string; value: number | string }> = [];

  const executor = new SongyaxuanCommandExecutor({
    coilWriter: {
      async writeCoil(unitId, coil, value) {
        if (options.onWriteCoil) {
          await options.onWriteCoil(coil, value);
        }
        writes.push({ unitId, coil, value });
      },
    },
    publishReply(_topic, reply) {
      replies.push(reply);
    },
    publishTelemetry(msg) {
      for (const dev of msg.payload.payload.devices) {
        for (const pt of dev.points) {
          telemetries.push({ deviceId: dev.deviceId, code: pt.code, value: pt.value });
        }
      }
    },
    onDiagnostic(event) {
      diagnostics.push(event);
    },
    stateStore: options.stateStore,
    sleepMs: options.sleepMs,
    sequenceTimings: options.sequenceTimings,
    nowMs: () => 1_763_120_001_000,
  });

  return { executor, writes, replies, diagnostics, telemetries };
}

test('defaultSleepMs clears pending timers immediately when unit sequence is aborted', async () => {
  const originalSetTimeout = globalThis.setTimeout;
  const originalClearTimeout = globalThis.clearTimeout;
  const activeTimers = new Set<ReturnType<typeof setTimeout>>();
  let clearedCount = 0;

  globalThis.setTimeout = ((fn: (...args: unknown[]) => void, delay?: number, ...args: unknown[]) => {
    const id = originalSetTimeout(() => {
      activeTimers.delete(id);
      fn(...args);
    }, delay);
    activeTimers.add(id);
    return id;
  }) as typeof setTimeout;
  globalThis.clearTimeout = ((id: ReturnType<typeof setTimeout> | undefined) => {
    if (id !== undefined && activeTimers.delete(id)) {
      clearedCount += 1;
    }
    return originalClearTimeout(id);
  }) as typeof clearTimeout;

  try {
    const { executor, writes } = createHarness({
      sequenceTimings: {
        pumpToCoolingPumpMs: 60_000,
        coolingPumpToTowerMs: 60_000,
        towerToChillerPulseMs: 60_000,
      },
    });
    await executor.initialize();

    const startReply = await executor.handleCommand(createCommand({
      commandId: 'cmd-timer-start',
      commandCode: 'START_UNIT_SEQUENCE',
      executionFence: 1,
    }));
    assert.equal(startReply.edgeStatus, 'EXECUTED');

    // Give launchSequenceTask one turn to enter sleepAbortable(60_000).
    await new Promise((resolve) => originalSetTimeout(resolve, 15));
    assert.equal(activeTimers.size, 1);

    const startedAt = Date.now();
    const abortReply = await executor.handleCommand(createCommand({
      commandId: 'cmd-timer-abort',
      commandCode: 'ABORT_UNIT_SEQUENCE',
      actuationMode: 'ABORT_UNIT_SEQUENCE',
      executionFence: 2,
    }));
    assert.equal(abortReply.edgeStatus, 'EXECUTED');
    await executor.waitForActiveSequences();
    const elapsedMs = Date.now() - startedAt;

    assert.ok(elapsedMs < 1_000, `expected immediate abort release, elapsed=${elapsedMs}ms`);
    assert.equal(activeTimers.size, 0, 'all long sleep timers must be cleared on abort');
    assert.ok(clearedCount >= 1, 'clearTimeout must be called when aborting sequence wait');
    assert.equal(writes.some((w) => w.coil === 1), false, 'cwp coil must not activate after abort');
  } finally {
    globalThis.setTimeout = originalSetTimeout;
    globalThis.clearTimeout = originalClearTimeout;
  }
});

test('custom signal-unaware sleepMs still releases sequence waits promptly upon AbortSignal', async () => {
  const { executor, writes } = createHarness({
    sleepMs: (ms) => new Promise((resolve) => setTimeout(resolve, ms).unref()),
    sequenceTimings: {
      pumpToCoolingPumpMs: 60_000,
    },
  });
  await executor.initialize();

  await executor.handleCommand(createCommand({
    commandId: 'cmd-unaware-start',
    commandCode: 'START_UNIT_SEQUENCE',
    executionFence: 1,
  }));
  await new Promise((resolve) => setTimeout(resolve, 15));

  const startedAt = Date.now();
  await executor.handleCommand(createCommand({
    commandId: 'cmd-unaware-abort',
    commandCode: 'ABORT_UNIT_SEQUENCE',
    actuationMode: 'ABORT_UNIT_SEQUENCE',
    executionFence: 2,
  }));
  await executor.waitForActiveSequences();
  assert.ok(Date.now() - startedAt < 1_000);
  assert.equal(writes.some((w) => w.coil === 1), false);
});

test('concurrent START_UNIT_SEQUENCE and ABORT_UNIT_SEQUENCE serialize cleanly during slow persistState', async () => {
  let savedState: ExecutorPersistedState | null = null;
  const stateStore: ExecutorStateStore = {
    async load() {
      return savedState ? structuredClone(savedState) : null;
    },
    async save(state) {
      await new Promise((resolve) => setTimeout(resolve, 25));
      savedState = structuredClone(state);
    },
  };

  const { executor, writes } = createHarness({
    stateStore,
    sequenceTimings: {
      pumpToCoolingPumpMs: 10,
      coolingPumpToTowerMs: 10,
      towerToChillerPulseMs: 10,
    },
  });
  await executor.initialize();
  writes.length = 0;

  const [startReply, abortReply] = await Promise.all([
    executor.handleCommand(createCommand({
      commandId: 'cmd-race-start',
      commandCode: 'START_UNIT_SEQUENCE',
      executionFence: 10,
    })),
    executor.handleCommand(createCommand({
      commandId: 'cmd-race-abort',
      commandCode: 'ABORT_UNIT_SEQUENCE',
      actuationMode: 'ABORT_UNIT_SEQUENCE',
      executionFence: 11,
    })),
  ]);

  await executor.waitForActiveSequences();
  assert.equal(abortReply.edgeStatus, 'EXECUTED');
  // Either START completed synchronously before ABORT aborted its background tail, or was pre-aborted cleanly;
  // in both cases no chiller start pulse coil (3) may fire after abort.
  assert.ok(startReply.edgeStatus === 'EXECUTED' || startReply.reasonCode === 'SEQUENCE_ABORTED_BY_OPERATOR');
  assert.equal(writes.some((w) => w.coil === 3 && w.value === 1), false);
});

test('persistState failure rolls back in-memory fence/dedup state so retry succeeds cleanly', async () => {
  let savedState: ExecutorPersistedState | null = null;
  let failNextSave = false;
  const stateStore: ExecutorStateStore = {
    async load() {
      return savedState ? structuredClone(savedState) : null;
    },
    async save(state) {
      if (failNextSave) {
        failNextSave = false;
        throw new Error('disk full');
      }
      savedState = structuredClone(state);
    },
  };

  const { executor, writes, diagnostics } = createHarness({ stateStore });
  await executor.initialize();
  writes.length = 0;

  failNextSave = true;
  const failedReply = await executor.handleCommand(createCommand({
    commandId: 'cmd-persist-retry',
    deviceId: 'ctrl-chwp-1',
    commandCode: 'START',
    executionFence: 5,
  }));
  assert.equal(failedReply.edgeStatus, 'FAILED');
  assert.equal(failedReply.reasonCode, 'STATE_PERSIST_FAILED');
  assert.equal(writes.length, 0, 'failed persistence must block Modbus writes');
  assert.ok(diagnostics.some((d) => d.code === 'STATE_PERSIST_FAILED' && d.commandId === 'cmd-persist-retry'));

  const retryReply = await executor.handleCommand(createCommand({
    commandId: 'cmd-persist-retry',
    deviceId: 'ctrl-chwp-1',
    commandCode: 'START',
    executionFence: 5,
  }));
  assert.equal(retryReply.edgeStatus, 'EXECUTED');
  assert.deepEqual(writes, [{ unitId: 31, coil: 0, value: 1 }]);
});

test('executePulse defensively resets coil when writeCoil(1) fails and cleans up on abort', async () => {
  let savedState: ExecutorPersistedState | null = null;
  const getSavedState = (): ExecutorPersistedState | null => savedState;
  const stateStore: ExecutorStateStore = {
    async load() {
      return savedState ? structuredClone(savedState) : null;
    },
    async save(state) {
      savedState = structuredClone(state);
    },
  };

  const failWrite = createHarness({
    stateStore,
    onWriteCoil(coil, value) {
      if (coil === 3 && value === 1) {
        throw new Error('modbus timeout after frame sent');
      }
    },
  });
  await failWrite.executor.initialize();
  failWrite.writes.length = 0;

  const reply = await failWrite.executor.handleCommand(createCommand({
    commandId: 'cmd-pulse-write-fail',
    commandCode: 'START',
    deviceId: 'ctrl-chiller-a',
    executionFence: 1,
    pulseDurationMs: 20,
  }));
  assert.equal(reply.edgeStatus, 'FAILED');
  assert.equal(reply.reasonCode, 'MODBUS_WRITE_FAILED');
  assert.ok(failWrite.writes.some((w) => w.coil === 3 && w.value === 0), 'defensive reset to 0 must execute');
  assert.deepEqual(getSavedState()?.pendingPulseCoils, []);

  // Abort during an active direct sequence chiller stop pulse must immediately reset pulse coil to 0.
  const abortPulse = createHarness({
    stateStore,
    sequenceTimings: { chillerStopCoastDownMs: 5_000 },
  });
  await abortPulse.executor.initialize();
  abortPulse.writes.length = 0;

  const stopPromise = abortPulse.executor.handleCommand(createCommand({
    commandId: 'cmd-stop-pulse-abort',
    commandCode: 'STOP_UNIT_SEQUENCE',
    executionFence: 2,
    pulseDurationMs: 5_000,
  }));
  await new Promise((resolve) => setTimeout(resolve, 20));
  const emerReply = await abortPulse.executor.handleCommand(createCommand({
    commandId: 'cmd-emerg-after-pulse',
    deviceId: 'et1010',
    commandCode: 'STOP',
    pointCode: 'emergency_stop_all',
    actuationMode: 'EMERGENCY_DO_RELEASE',
    channel: 'all-do',
    executionFence: 3,
  }));
  const stopReply = await stopPromise;
  await abortPulse.executor.waitForActiveSequences();

  assert.equal(emerReply.edgeStatus, 'EXECUTED');
  assert.equal(stopReply.edgeStatus, 'REJECTED');
  assert.equal(stopReply.reasonCode, 'EMERGENCY_DO_RELEASE');
  assert.ok(abortPulse.writes.some((w) => w.coil === 16 && w.value === 0));
  assert.deepEqual(getSavedState()?.pendingPulseCoils, []);
});

test('initialize preserves newest MAX_EXECUTED_COMMAND_IDS on oversized persisted state and tolerates bad entries', async () => {
  const oversizedIds = Array.from({ length: 1005 }, (_, i) => `old-cmd-${i}`);
  const stateStore: ExecutorStateStore = {
    async load() {
      return {
        lastSequence: -5 as unknown as number,
        lastFenceByDevice: { 'ctrl-chwp-1': 9, bad: -1 },
        executedCommandIds: oversizedIds,
        pendingPulseCoils: [999, 3],
      };
    },
    async save() {},
  };

  const { executor, writes } = createHarness({ stateStore });
  await executor.initialize();

  // Newest ID (old-cmd-1004) must still trigger duplicate detection; expired tail old-cmd-0 trimmed.
  const dupReply = await executor.handleCommand(createCommand({
    commandId: 'old-cmd-1004',
    deviceId: 'ctrl-chwp-1',
    commandCode: 'START',
    executionFence: 20,
  }));
  assert.equal(dupReply.edgeStatus, 'REJECTED');
  assert.equal(dupReply.reasonCode, 'DUPLICATE_COMMAND');

  const staleReply = await executor.handleCommand(createCommand({
    commandId: 'fresh-stale-check',
    deviceId: 'ctrl-chwp-1',
    commandCode: 'START',
    executionFence: 9,
  }));
  assert.equal(staleReply.edgeStatus, 'REJECTED');
  assert.equal(staleReply.reasonCode, 'STALE_EXECUTION_FENCE');
  assert.ok(writes.some((w) => w.coil === 3 && w.value === 0));
});
