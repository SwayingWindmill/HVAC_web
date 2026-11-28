import assert from 'node:assert/strict';
import test from 'node:test';
import type { RawSiteAssetModel } from '../miniprogram/lib/asset-model-cache.ts';
import {
  buildActionConfirmationIntent,
  buildSettingConfirmationIntent,
  canSubmitCommands,
  canSubmitHighRiskCommands,
  clampAndRoundSetting,
  createCommandSubmitMutex,
  dispatchControlCommand,
  evaluateControlGuards,
  fetchSiteAutoControlLease,
  mapCommandSubmitError,
  normalizeTotpCode,
  parseDeviceControlTargets,
  presentAutoControlLease,
  presentCommandStatus,
  reconcileActiveCommandStatus,
  startCommandPolling,
  type AutoControlLeaseState,
  type CommandRecord,
  type SiteControlState,
} from '../miniprogram/lib/control.ts';
import { createGateway, type ChunkedTransport, type TransportRequest } from '../miniprogram/lib/gateway.ts';
import type { PlantDevice } from '../miniprogram/lib/plant.ts';

const noStreams: ChunkedTransport = () => { throw new Error('not used'); };

const memorySession = (token: string | null) => {
  let current = token;
  return { get: () => current, set: (value: string) => { current = value; }, clear: () => { current = null; } };
};

const BANNED_DESIGN_WORDS = [
  '台账', '核算', '测量边界', '分表', '回执', '待核对', '工况',
  '认领', '矩阵', '泳道', '拆解', '寻优', '本期间', '小时桶',
];

function sampleDevice(overrides: Partial<PlantDevice> = {}): PlantDevice {
  return {
    deviceId: 'dev-chiller-1',
    name: '1 号冷机控制器',
    category: 'CHILLER',
    assetId: null,
    assetType: null,
    waterLoop: null,
    branchCode: null,
    connection: 'ONLINE',
    runState: 'STOPPED',
    faultCode: null,
    latestSampleAt: '2026-11-18T10:00:00.000Z',
    hasStaleData: false,
    readings: [
      {
        sourceKey: 'chiller.chilled_water_out_temp_setpoint',
        label: '冷冻水出水温度设定',
        display: '7',
        numeric: 7.0,
        unit: '°C',
        current: true,
        lowConfidence: false,
        stale: false,
        sampledAt: '2026-11-18T10:00:00.000Z',
      },
    ],
    reading(key: string) {
      return this.readings.find((item) => item.sourceKey === key) ?? null;
    },
    ...overrides,
  };
}

function sampleAssetModel(): RawSiteAssetModel {
  return {
    assets: [{ id: 'asset-chiller-1', code: 'chiller-1', displayName: '1 号离心冷机', assetType: 'CHILLER', status: 'ACTIVE' }],
    devices: [
      { id: 'dev-chiller-1', code: 'chiller-plc-1', displayName: '1 号冷机控制器', deviceType: 'CHILLER', status: 'ACTIVE' },
      { id: 'dev-pump-1', code: 'pump-plc-1', displayName: '1 号冷冻泵控制器', deviceType: 'CHILLED_WATER_PUMP', status: 'ACTIVE' },
    ],
    telemetryPoints: [
      {
        id: 'pt-fb-sp',
        reportingDeviceId: 'dev-chiller-1',
        pointCode: 'chilled_water_out_temp_setpoint',
        sourceKey: 'chiller.chilled_water_out_temp_setpoint',
        displayName: '冷冻水出水温度设定反馈',
        pointType: 'STATE',
        valueType: 'NUMBER',
        unit: '°C',
        writable: false,
        status: 'ACTIVE',
      },
      {
        id: 'pt-cmd-start',
        reportingDeviceId: 'dev-chiller-1',
        pointCode: 'cmd_start',
        sourceKey: 'chiller.cmd_start',
        displayName: '冷机启动',
        pointType: 'COMMAND',
        valueType: 'BOOLEAN',
        writable: true,
        status: 'ACTIVE',
        sourceMetadata: {
          capability: 'START',
          interlockChecks: [{ deviceId: 'dev-pump-1', key: 'run_state', expected: 'RUNNING', reason: '1 号冷冻泵未运行，禁止启动冷机' }],
        },
      },
      {
        id: 'pt-cmd-stop',
        reportingDeviceId: 'dev-chiller-1',
        pointCode: 'cmd_stop',
        sourceKey: 'chiller.cmd_stop',
        displayName: '冷机停止',
        pointType: 'COMMAND',
        valueType: 'BOOLEAN',
        writable: true,
        status: 'ACTIVE',
        sourceMetadata: { capability: 'STOP' },
      },
      {
        id: 'pt-cmd-reset',
        reportingDeviceId: 'dev-chiller-1',
        pointCode: 'cmd_reset',
        sourceKey: 'chiller.cmd_reset',
        displayName: '故障复位',
        pointType: 'COMMAND',
        valueType: 'BOOLEAN',
        writable: true,
        status: 'ACTIVE',
        sourceMetadata: { capability: 'RESET_FAULT' },
      },
      {
        id: 'pt-cmd-emg',
        reportingDeviceId: 'dev-chiller-1',
        pointCode: 'cmd_emergency_do',
        sourceKey: 'chiller.cmd_emergency_do',
        displayName: '全站紧急断电',
        pointType: 'COMMAND',
        valueType: 'BOOLEAN',
        writable: true,
        status: 'ACTIVE',
        sourceMetadata: { capability: 'STOP', actuationMode: 'EMERGENCY_DO_RELEASE' },
      },
      {
        id: 'pt-cmd-setpoint',
        reportingDeviceId: 'dev-chiller-1',
        pointCode: 'cmd_sp',
        sourceKey: 'chiller.cmd_sp',
        displayName: '冷冻水出水温度设定',
        pointType: 'COMMAND',
        valueType: 'NUMBER',
        unit: '°C',
        writable: true,
        status: 'ACTIVE',
        sourceMetadata: {
          capability: 'SET_CHILLED_WATER_TEMPERATURE_SETPOINT',
          parameterKey: 'setpointC',
          minimum: 5,
          maximum: 15,
          step: 0.5,
          feedbackPointKey: 'chilled_water_out_temp_setpoint',
        },
      },
      {
        id: 'pt-cmd-readonly',
        reportingDeviceId: 'dev-chiller-1',
        pointCode: 'cmd_ro',
        sourceKey: 'chiller.cmd_ro',
        displayName: '非可写测试点',
        pointType: 'COMMAND',
        valueType: 'NUMBER',
        writable: false,
        status: 'ACTIVE',
        sourceMetadata: { capability: 'SET_FREQUENCY', parameterKey: 'hz', minimum: 30, maximum: 50 },
      },
    ],
    relationships: [
      { id: 'r1', fromType: 'POINT', fromId: 'pt-cmd-start', toType: 'ASSET', toId: 'asset-chiller-1', role: 'CONTROLS', status: 'ACTIVE', validFrom: '2026-01-01T00:00:00Z', revision: 1 },
      { id: 'r2', fromType: 'POINT', fromId: 'pt-cmd-stop', toType: 'ASSET', toId: 'asset-chiller-1', role: 'CONTROLS', status: 'ACTIVE', validFrom: '2026-01-01T00:00:00Z', revision: 1 },
      { id: 'r3', fromType: 'POINT', fromId: 'pt-cmd-reset', toType: 'ASSET', toId: 'asset-chiller-1', role: 'CONTROLS', status: 'ACTIVE', validFrom: '2026-01-01T00:00:00Z', revision: 1 },
      { id: 'r4', fromType: 'POINT', fromId: 'pt-cmd-emg', toType: 'ASSET', toId: 'asset-chiller-1', role: 'CONTROLS', status: 'ACTIVE', validFrom: '2026-01-01T00:00:00Z', revision: 1 },
      { id: 'r5', fromType: 'POINT', fromId: 'pt-cmd-setpoint', toType: 'ASSET', toId: 'asset-chiller-1', role: 'CONTROLS', status: 'ACTIVE', validFrom: '2026-01-01T00:00:00Z', revision: 1 },
      { id: 'r6', fromType: 'POINT', fromId: 'pt-cmd-readonly', toType: 'ASSET', toId: 'asset-chiller-1', role: 'CONTROLS', status: 'ACTIVE', validFrom: '2026-01-01T00:00:00Z', revision: 1 },
    ],
  };
}

function remoteAutoState(overrides: Partial<SiteControlState> = {}): SiteControlState {
  return {
    tenantId: 'tenant-1',
    siteId: 'site-1',
    controlMode: 'REMOTE_AUTO',
    reason: '',
    updatedAt: '2026-11-18T10:00:00.000Z',
    revision: 1,
    assetLocks: [],
    activeMaintenanceWindow: null,
    ...overrides,
  };
}

test('canSubmitCommands enforces principal capabilities fail-closed for read-only viewers', () => {
  assert.equal(canSubmitCommands(null), false);
  assert.equal(canSubmitCommands({ authorization: { capabilities: ['site.read', 'asset-model.read', 'telemetry.read'] } }), false);
  assert.equal(canSubmitCommands({ authorization: { capabilities: ['site.read', 'schedule.request'] } }), true);
  assert.equal(canSubmitCommands({ authorization: { capabilities: ['schedule.manage'] } }), true);
  assert.equal(canSubmitCommands({ authorization: { capabilities: ['command.submit.medium'] } }), true);
});

test('parseDeviceControlTargets excludes EMERGENCY_DO_RELEASE and non-writable points cleanly', () => {
  const targets = parseDeviceControlTargets(sampleAssetModel(), 'dev-chiller-1', new Date('2026-11-18T10:00:00Z'));
  assert.equal(targets.length, 1);
  assert.deepEqual(targets[0].actions.map((item) => item.capability), ['START', 'STOP', 'RESET_FAULT']);
  assert.equal(targets[0].settings.length, 1);
  assert.equal(targets[0].settings[0].parameterKey, 'setpointC');
  assert.equal(targets[0].settings[0].unit, '°C');
  assert.equal(targets[0].settings[0].feedbackSourceKey, 'chiller.chilled_water_out_temp_setpoint');
});

test('evaluateControlGuards blocks site manual override, maintenance locks, maintenance window, offline, interlocks, and open commands', () => {
  const [target] = parseDeviceControlTargets(sampleAssetModel(), 'dev-chiller-1', new Date('2026-11-18T10:00:00Z'));

  // 1. Interlock blocks START when pump-1 is STOPPED; unblocks when pump-1 is RUNNING.
  const stoppedPump = evaluateControlGuards({
    target,
    device: sampleDevice({ runState: 'STOPPED' }),
    controlState: remoteAutoState(),
    commands: [],
    runStateByDeviceId: new Map([['dev-chiller-1', 'STOPPED'], ['dev-pump-1', 'STOPPED']]),
  });
  const startBlocked = stoppedPump.actions.find((a) => a.capability === 'START')!;
  assert.equal(startBlocked.disabled, true);
  assert.match(startBlocked.disabledReason, /1 号冷冻泵未运行/);
  assert.equal(stoppedPump.settings[0].disabled, false);

  const runningPump = evaluateControlGuards({
    target,
    device: sampleDevice({ runState: 'STOPPED' }),
    controlState: remoteAutoState(),
    commands: [],
    runStateByDeviceId: new Map([['dev-chiller-1', 'STOPPED'], ['dev-pump-1', 'RUNNING']]),
  });
  assert.equal(runningPump.actions.find((a) => a.capability === 'START')!.disabled, false);

  // 2. Site MANUAL_OVERRIDE disables all controls cleanly.
  const manualMode = evaluateControlGuards({
    target,
    device: sampleDevice({ runState: 'RUNNING' }),
    controlState: remoteAutoState({ controlMode: 'MANUAL_OVERRIDE', reason: '现场就地切换' }),
    commands: [],
  });
  assert.equal(manualMode.guard.blocked, true);
  assert.equal(manualMode.guard.badge, '人工接管中');
  assert.ok(manualMode.actions.every((a) => a.disabled));
  assert.ok(manualMode.settings.every((s) => s.disabled));

  // 3. Asset MAINTENANCE_LOCK shows hanging operator & reason and blocks controls.
  const maintLock = evaluateControlGuards({
    target,
    device: sampleDevice({ runState: 'STOPPED' }),
    controlState: remoteAutoState({
      assetLocks: [{
        siteId: 'site-1',
        assetId: 'asset-chiller-1',
        lockMode: 'MAINTENANCE_LOCK',
        reason: '拆洗冷凝器铜管',
        updatedByName: '张工',
        updatedAt: '2026-11-18T09:00:00Z',
        revision: 2,
      }],
    }),
    commands: [],
    runStateByDeviceId: new Map([['dev-pump-1', 'RUNNING']]),
  });
  assert.equal(maintLock.guard.blocked, true);
  assert.equal(maintLock.guard.badge, '检修锁定');
  assert.match(maintLock.guard.detail, /拆洗冷凝器铜管.*张工/);
  assert.ok(maintLock.actions.every((a) => a.disabled));
  assert.ok(maintLock.settings.every((s) => s.disabled));

  // 4. Active maintenance window allows STOP/RESET_FAULT on running/fault devices, blocks START and settings.
  const maintWindow = evaluateControlGuards({
    target,
    device: sampleDevice({ runState: 'RUNNING' }),
    controlState: remoteAutoState({
      activeMaintenanceWindow: { id: 'mw-1', title: '冬季保养窗口', startDate: '2026-11-18', endDate: '2026-11-19', reason: '年度检修' },
    }),
    commands: [],
  });
  assert.equal(maintWindow.actions.find((a) => a.capability === 'STOP')!.disabled, false);
  assert.equal(maintWindow.settings[0].disabled, true);
  assert.match(maintWindow.settings[0].disabledReason, /检修窗口/);

  // 5. Open in-flight command prevents stacked submissions.
  const inFlight: CommandRecord = {
    commandId: 'cmd-open-1',
    siteId: 'site-1',
    deviceId: 'dev-chiller-1',
    pointId: 'pt-cmd-setpoint',
    capability: 'SET_CHILLED_WATER_TEMPERATURE_SETPOINT',
    status: 'DISPATCHING',
    parameters: { setpointC: 8.5 },
    createdAt: '2026-11-18T10:00:00Z',
    updatedAt: '2026-11-18T10:00:01Z',
  };
  const stacked = evaluateControlGuards({
    target,
    device: sampleDevice({ runState: 'RUNNING' }),
    controlState: remoteAutoState(),
    commands: [inFlight],
  });
  assert.equal(stacked.openCommand?.active, true);
  assert.equal(stacked.actions.find((a) => a.capability === 'STOP')!.disabled, true);
  assert.equal(stacked.settings[0].disabled, true);
});

test('mistouch-proof confirmation intents format before/after values cleanly and round stepper bounds', () => {
  const [target] = parseDeviceControlTargets(sampleAssetModel(), 'dev-chiller-1', new Date('2026-11-18T10:00:00Z'));
  const setting = target.settings[0];
  assert.equal(clampAndRoundSetting(0.1 + 7.2, setting), 7.5);
  assert.equal(clampAndRoundSetting(99, setting), 15);
  assert.equal(clampAndRoundSetting(-10, setting), 5);

  const settingIntent = buildSettingConfirmationIntent({
    setting,
    assetName: target.assetName,
    deviceName: '1 号冷机控制器',
    currentNumeric: 7.0,
    targetValue: 8.5,
  });
  assert.equal(settingIntent.beforeValue, '7 °C');
  assert.equal(settingIntent.afterValue, '8.5 °C');
  assert.deepEqual(settingIntent.parameters, { setpointC: 8.5 });
  assert.equal(settingIntent.highRisk, false);
  assert.ok(settingIntent.idempotencyKey.length >= 12);

  const startAction = target.actions.find((a) => a.capability === 'START')!;
  const startIntent = buildActionConfirmationIntent({
    action: startAction,
    assetName: target.assetName,
    device: sampleDevice({ runState: 'STOPPED' }),
  });
  assert.equal(startIntent.beforeValue, '已停止');
  assert.equal(startIntent.afterValue, '启动运行');
  assert.equal(startIntent.highRisk, true);
  assert.equal(startIntent.requiresTotp, true);
  assert.equal(normalizeTotpCode(' 12a34b567 '), '123456');
});

test('submit mutex drops double-taps, reuses identical Idempotency-Key across weak-network & 428 retries, and preserves clean Chinese errors', async () => {
  const [target] = parseDeviceControlTargets(sampleAssetModel(), 'dev-chiller-1', new Date('2026-11-18T10:00:00Z'));
  const intent = buildSettingConfirmationIntent({
    setting: target.settings[0],
    assetName: target.assetName,
    deviceName: '1 号冷机控制器',
    currentNumeric: 7.0,
    targetValue: 8.5,
  });

  const sent: TransportRequest[] = [];
  let attempt = 0;
  let releasePending!: () => void;
  const firstPendingGate = new Promise<void>((resolve) => { releasePending = resolve; });

  const gateway = createGateway({
    origin: 'https://hvac.example.test',
    chunked: noStreams,
    session: memorySession('mini-bearer-1'),
    onSignedOut: () => {},
    transport: async (request) => {
      sent.push(request);
      attempt += 1;
      if (attempt === 1) {
        await firstPendingGate;
        throw new Error('request:fail timeout');
      }
      if (attempt === 2) {
        return { statusCode: 428, data: { code: 'STEP_UP_REQUIRED', reason: 'CODE_INVALID' } };
      }
      return {
        statusCode: 202,
        data: {
          commandId: '01900000-0000-7000-8000-000000000001',
          siteId: 'site-1',
          deviceId: 'dev-chiller-1',
          pointId: intent.pointId,
          capability: intent.capability,
          status: 'QUEUED',
          parameters: intent.parameters,
          createdAt: '2026-11-18T10:00:00Z',
          updatedAt: '2026-11-18T10:00:00Z',
        },
      };
    },
  });

  // 1. Mutex prevents concurrent double-tap dispatch while request 1 is in flight.
  const mutex = createCommandSubmitMutex();
  const tap1 = mutex.run(() => dispatchControlCommand(gateway, intent));
  const tap2 = await mutex.run(() => dispatchControlCommand(gateway, intent));
  assert.equal(tap2.acquired, false);
  releasePending();

  await assert.rejects(tap1, (err: unknown) => {
    const mapped = mapCommandSubmitError(err, false);
    assert.equal(mapped.kind, 'network');
    assert.equal(mapped.canRetrySameKey, true);
    return true;
  });

  // 2. Network retry keeps identical Idempotency-Key -> hits 428 CODE_INVALID.
  await assert.rejects(dispatchControlCommand(gateway, intent, '000000'), (err: unknown) => {
    const mapped = mapCommandSubmitError(err, true);
    assert.equal(mapped.kind, 'step-up');
    assert.equal(mapped.canRetrySameKey, true);
    assert.match(mapped.message, /动态口令不对/);
    return true;
  });

  // 3. TOTP retry with valid 6-digit code reuses the EXACT same Idempotency-Key -> 202 Accepted.
  const accepted = await dispatchControlCommand(gateway, intent, '123456');
  assert.equal(accepted.status, 'QUEUED');
  assert.equal(sent.length, 3);
  assert.equal(sent[0].header['Idempotency-Key'], intent.idempotencyKey);
  assert.equal(sent[1].header['Idempotency-Key'], intent.idempotencyKey);
  assert.equal(sent[2].header['Idempotency-Key'], intent.idempotencyKey);

  // 4. Verify all error translations obey DESIGN.md (zero banned copy, zero raw HTTP codes/UUIDs).
  for (const [status, body] of [
    [428, { code: 'STEP_UP_REQUIRED', reason: 'CODE_REPLAYED' }],
    [428, { code: 'STEP_UP_REQUIRED', reason: 'MFA_NOT_CONFIGURED' }],
    [428, { code: 'STEP_UP_REQUIRED', reason: 'MFA_LOCKED' }],
    [409, { code: 'COMMAND_CONTROL_LOCKED' }],
    [409, { code: 'DEVICE_PRIORITY_LOCKED' }],
    [409, { code: 'COMMAND_INTERLOCK_BLOCKED' }],
    [403, { code: 'FORBIDDEN' }],
  ] as const) {
    const errGateway = createGateway({
      origin: 'https://hvac.example.test',
      chunked: noStreams,
      session: memorySession('s'),
      onSignedOut: () => {},
      transport: async () => ({ statusCode: status, data: body }),
    });
    await assert.rejects(dispatchControlCommand(errGateway, intent, '123456'), (err: unknown) => {
      const mapped = mapCommandSubmitError(err, true);
      assert.equal(/\b(403|409|428)\b/.test(mapped.message), false);
      assert.equal(mapped.message.includes('—'), false);
      for (const banned of BANNED_DESIGN_WORDS) {
        assert.equal(mapped.message.includes(banned), false, `banned word ${banned} leaked in: ${mapped.message}`);
      }
      return true;
    });
  }
});

test('non-blocking polling transitions 202 QUEUED -> SUCCEEDED and cancels cleanly on page hide/unload', async () => {
  let pollCount = 0;
  const statuses: string[] = [];
  const gateway = createGateway({
    origin: 'https://hvac.example.test',
    chunked: noStreams,
    session: memorySession('mini-bearer-1'),
    onSignedOut: () => {},
    transport: async () => {
      pollCount += 1;
      if (pollCount === 1) throw new Error('weak signal blip');
      const status = pollCount === 2 ? 'DISPATCHING' : 'SUCCEEDED';
      return {
        statusCode: 200,
        data: {
          commandId: 'cmd-1',
          siteId: 'site-1',
          deviceId: 'dev-chiller-1',
          pointId: 'pt-cmd-setpoint',
          capability: 'SET_CHILLED_WATER_TEMPERATURE_SETPOINT',
          status,
          parameters: { setpointC: 8.5 },
          createdAt: '2026-11-18T10:00:00Z',
          updatedAt: '2026-11-18T10:00:03Z',
        },
      };
    },
  });

  await new Promise<void>((resolve, reject) => {
    const handle = startCommandPolling({
      client: gateway,
      commandId: 'cmd-1',
      intervalMs: 10,
      maxAttempts: 10,
      onUpdate: (_cmd, presented) => {
        statuses.push(presented.status);
        for (const banned of BANNED_DESIGN_WORDS) {
          assert.equal(presented.detail.includes(banned), false);
        }
        if (!presented.active) {
          handle.cancel();
          resolve();
        }
      },
      onTimeout: () => reject(new Error('unexpected timeout')),
      onError: (msg) => reject(new Error(msg)),
    });
  });
  assert.deepEqual(statuses, ['DISPATCHING', 'SUCCEEDED']);

  // Cancellation stops pending timers before any poll executes.
  let cancelledPollRan = false;
  const cancelledGateway = createGateway({
    origin: 'https://hvac.example.test',
    chunked: noStreams,
    session: memorySession('mini-bearer-1'),
    onSignedOut: () => {},
    transport: async () => {
      cancelledPollRan = true;
      return { statusCode: 200, data: {} };
    },
  });
  const cancelledHandle = startCommandPolling({
    client: cancelledGateway,
    commandId: 'cmd-cancel',
    intervalMs: 20,
    onUpdate: () => { cancelledPollRan = true; },
    onTimeout: () => { cancelledPollRan = true; },
    onError: () => { cancelledPollRan = true; },
  });
  cancelledHandle.cancel();
  await new Promise((resolve) => setTimeout(resolve, 40));
  assert.equal(cancelledPollRan, false);

  // OUTCOME_UNKNOWN presents clear operator action without banned words.
  const unknownPresented = presentCommandStatus({
    commandId: 'cmd-2',
    siteId: 'site-1',
    deviceId: 'dev-chiller-1',
    pointId: 'pt-cmd-stop',
    capability: 'STOP',
    status: 'OUTCOME_UNKNOWN',
    parameters: {},
    createdAt: '2026-11-18T10:00:00Z',
    updatedAt: '2026-11-18T10:01:00Z',
  });
  assert.equal(unknownPresented.active, false);
  assert.match(unknownPresented.detail, /现场确认/);
});

test('reconcileActiveCommandStatus settles stale active command banners after onHide or polling timeout', () => {
  const activeBanner = presentCommandStatus({
    commandId: 'cmd-pending',
    siteId: 'site-1',
    deviceId: 'dev-chiller-1',
    pointId: 'pt-cmd-stop',
    capability: 'STOP',
    status: 'DISPATCHING',
    parameters: {},
    createdAt: '2026-11-18T10:00:00Z',
    updatedAt: '2026-11-18T10:00:01Z',
  });
  assert.equal(activeBanner.active, true);

  // 1. If openStatus is still active, preserve live openStatus.
  const stillOpen = reconcileActiveCommandStatus(activeBanner, activeBanner, []);
  assert.equal(stillOpen?.active, true);
  assert.equal(stillOpen?.status, 'DISPATCHING');

  // 2. When backend settled the command while page was hidden or polling timed out, transition to terminal state.
  const settledInHistory = reconcileActiveCommandStatus(activeBanner, null, [{
    commandId: 'cmd-pending',
    siteId: 'site-1',
    deviceId: 'dev-chiller-1',
    pointId: 'pt-cmd-stop',
    capability: 'STOP',
    status: 'SUCCEEDED',
    parameters: {},
    createdAt: '2026-11-18T10:00:00Z',
    updatedAt: '2026-11-18T10:00:05Z',
  }]);
  assert.equal(settledInHistory?.active, false);
  assert.equal(settledInHistory?.status, 'SUCCEEDED');
  assert.equal(settledInHistory?.label, '已执行');

  // 3. Even if the command fell out of the recent command slice, mark active: false so the banner stays dismissible.
  const missingFromSlice = reconcileActiveCommandStatus(activeBanner, null, []);
  assert.equal(missingFromSlice?.active, false);
  assert.equal(missingFromSlice?.label, '状态已更新');

  // 4. Dismissed or already settled banners remain untouched.
  assert.equal(reconcileActiveCommandStatus(null, null, []), null);
  assert.deepEqual(reconcileActiveCommandStatus(settledInHistory, null, []), settledInHistory);
});

test('auto-control lease banner presents closed-loop, shadow, manual backoff precedence, and graceful 404 fallback', async () => {
  const now = new Date('2026-11-21T06:00:00Z'); // 14:00 Asia/Shanghai
  const baseLease: AutoControlLeaseState = {
    tenantId: 'tenant-1',
    siteId: 'site-1',
    confirmationMode: 'AUTO_WITH_LEASE',
    activeLease: {
      id: 'lease-1',
      status: 'ACTIVE',
      expiresAt: '2026-11-21T10:30:00Z', // 18:30 Asia/Shanghai
      executionMode: 'CLOSED_LOOP',
      supplyTempMinC: 6.0,
      supplyTempMaxC: 12.0,
      pumpFreqMinHz: 30.0,
      pumpFreqMaxHz: 48.0,
      fanSpeedMinPct: 35,
      fanSpeedMaxPct: 90,
      manualBackoffUntil: null,
    },
  };

  // 1. Closed-loop active shows multi-loop bounds & expiry clock without banned words.
  const closedLoop = presentAutoControlLease(baseLease, now, 'Asia/Shanghai');
  assert.ok(closedLoop);
  assert.equal(closedLoop.tone, 'good');
  assert.equal(closedLoop.badge, 'AI 自动托管中');
  assert.match(closedLoop.detail, /18:30/);
  assert.match(closedLoop.detail, /供水 6\.0–12\.0 °C/);
  assert.match(closedLoop.detail, /水泵 30\.0–48\.0 Hz/);
  assert.match(closedLoop.detail, /风机 35–90%/);

  // 2. Manual backoff takes precedence over active lease.
  const backedOff = presentAutoControlLease({
    ...baseLease,
    activeLease: {
      ...baseLease.activeLease!,
      manualBackoffUntil: '2026-11-21T07:45:00Z', // 15:45 Asia/Shanghai
    },
  }, now, 'Asia/Shanghai');
  assert.ok(backedOff);
  assert.equal(backedOff.tone, 'warning');
  assert.equal(backedOff.badge, '人工退避保护中');
  assert.match(backedOff.detail, /15:45/);

  // 3. Shadow mode displays neutral banner.
  const shadow = presentAutoControlLease({
    ...baseLease,
    activeLease: {
      ...baseLease.activeLease!,
      executionMode: 'SHADOW',
    },
  }, now, 'Asia/Shanghai');
  assert.ok(shadow);
  assert.equal(shadow.tone, 'neutral');
  assert.equal(shadow.badge, '影子跟跑中');

  // Verify no banned DESIGN.md words appear across states.
  for (const banner of [closedLoop, backedOff, shadow]) {
    const combined = `${banner.badge} ${banner.headline} ${banner.detail}`;
    for (const banned of BANNED_DESIGN_WORDS) {
      assert.equal(combined.includes(banned), false);
    }
  }

  // 4. Expired or revoked leases return null.
  assert.equal(presentAutoControlLease({
    ...baseLease,
    activeLease: { ...baseLease.activeLease!, expiresAt: '2026-11-21T05:59:00Z' },
  }, now, 'Asia/Shanghai'), null);
  assert.equal(presentAutoControlLease({ ...baseLease, activeLease: null }, now, 'Asia/Shanghai'), null);

  // 5. 404 or missing optimization service returns null gracefully.
  const notFoundGw = createGateway({
    origin: 'https://hvac.example.test',
    chunked: noStreams,
    session: memorySession('mini-bearer-1'),
    onSignedOut: () => {},
    transport: async (_req: TransportRequest) => ({
      statusCode: 404,
      data: { code: 'RESOURCE_NOT_FOUND', detail: 'missing' },
    }),
  });
  const missingResult = await fetchSiteAutoControlLease(notFoundGw, 'site-1');
  assert.equal(missingResult, null);
});

test('parseDeviceControlTargets aggregates commands from a sibling controller bound to the same Asset', () => {
  const base = sampleAssetModel();
  const extended: RawSiteAssetModel = {
    ...base,
    devices: [
      ...base.devices,
      { id: 'meter-chiller-1', code: 'meter-chiller-1', displayName: '1 号冷机电表', deviceType: 'HVAC_POWER_METER', status: 'ACTIVE' },
    ],
    relationships: [
      ...base.relationships,
      { id: 'r-bind-ctrl', fromType: 'DEVICE', fromId: 'dev-chiller-1', toType: 'ASSET', toId: 'asset-chiller-1', role: 'PRIMARY_CONTROLLER', status: 'ACTIVE', validFrom: '2026-01-01T00:00:00Z', revision: 1 },
      { id: 'r-bind-meter', fromType: 'DEVICE', fromId: 'meter-chiller-1', toType: 'ASSET', toId: 'asset-chiller-1', role: 'MEASURES', status: 'ACTIVE', validFrom: '2026-01-01T00:00:00Z', revision: 1 },
    ],
  };
  const targets = parseDeviceControlTargets(extended, 'meter-chiller-1', new Date('2026-11-18T10:00:00Z'));
  assert.equal(targets.length, 1);
  assert.equal(targets[0].deviceId, 'dev-chiller-1');
  assert.deepEqual(targets[0].actions.map((item) => item.capability), ['START', 'STOP', 'RESET_FAULT']);
});

test('canSubmitHighRiskCommands separates parameter tuning from remote start/stop rights', () => {
  assert.equal(canSubmitHighRiskCommands(null), false);
  assert.equal(canSubmitHighRiskCommands({ authorization: { capabilities: ['command.submit.low'] } }), false);
  assert.equal(canSubmitHighRiskCommands({ authorization: { capabilities: ['command.submit.medium'] } }), false);
  assert.equal(canSubmitHighRiskCommands({ authorization: { capabilities: ['command.submit.high'] } }), true);
  assert.equal(canSubmitHighRiskCommands({ authorization: { capabilities: ['command.submit.critical'] } }), true);
  assert.equal(canSubmitHighRiskCommands({ authorization: { capabilities: ['schedule.request'] } }), false);
  assert.equal(canSubmitHighRiskCommands({ authorization: { capabilities: ['schedule.manage'] } }), true);
});

test('evaluateControlGuards handles high-risk rights, SLEEPING/UNKNOWN connection, and priority locks', () => {
  const [target] = parseDeviceControlTargets(sampleAssetModel(), 'dev-chiller-1', new Date('2026-11-18T10:00:00Z'));
  const nowMs = Date.parse('2026-11-18T10:00:00.000Z');

  // 1. Medium/low command right disables high-risk start/stop actions while keeping settings active.
  const lowRight = evaluateControlGuards({
    target,
    device: sampleDevice({ runState: 'STOPPED' }),
    controlState: remoteAutoState(),
    commands: [],
    runStateByDeviceId: new Map([['dev-pump-1', 'RUNNING']]),
    nowMs,
    canExecuteHighRisk: false,
  });
  assert.equal(lowRight.guard.blocked, false);
  assert.equal(lowRight.actions.find((a) => a.capability === 'START')?.disabledReason, '当前账号仅开通参数调节权限，不能远程启停设备');
  assert.equal(lowRight.actions.find((a) => a.capability === 'STOP')?.disabled, true);
  assert.equal(lowRight.settings[0].disabled, false);

  // 2. SLEEPING and UNKNOWN connection block controls with clear Chinese copy.
  const sleeping = evaluateControlGuards({
    target,
    device: sampleDevice({ connection: 'SLEEPING', runState: 'STOPPED' }),
    controlState: remoteAutoState(),
    commands: [],
    nowMs,
  });
  assert.equal(sleeping.guard.blocked, true);
  assert.equal(sleeping.guard.badge, '设备休眠中');
  assert.equal(buildActionConfirmationIntent({ action: target.actions[0], assetName: target.assetName, device: sampleDevice({ connection: 'SLEEPING' }) }).beforeValue, '休眠中');

  const unknownConn = evaluateControlGuards({
    target,
    device: sampleDevice({ connection: 'UNKNOWN', runState: 'STOPPED' }),
    controlState: remoteAutoState(),
    commands: [],
    nowMs,
  });
  assert.equal(unknownConn.guard.blocked, true);
  assert.equal(unknownConn.guard.badge, '连接未确认');
  assert.equal(buildActionConfirmationIntent({ action: target.actions[0], assetName: target.assetName, device: sampleDevice({ connection: 'UNKNOWN' }) }).beforeValue, '连接未确认');

  // 3. Active SAFETY_INTERLOCK blocks control; expired SAFETY_INTERLOCK and lower PROTECTION_RULE do not block manual override.
  const safetyLocked = evaluateControlGuards({
    target,
    device: sampleDevice({ runState: 'STOPPED' }),
    controlState: remoteAutoState({
      devicePriorityLocks: [{
        deviceId: 'dev-chiller-1',
        lockedPriority: 'SAFETY_INTERLOCK',
        lockedUntil: '2026-11-18T11:00:00.000Z',
        reason: '蒸发器防冻联锁激活',
      }],
    }),
    commands: [],
    nowMs,
  });
  assert.equal(safetyLocked.guard.blocked, true);
  assert.equal(safetyLocked.guard.badge, '安全联锁锁定');
  assert.match(safetyLocked.guard.detail, /蒸发器防冻联锁激活/);

  const expiredSafety = evaluateControlGuards({
    target,
    device: sampleDevice({ runState: 'STOPPED' }),
    controlState: remoteAutoState({
      devicePriorityLocks: [
        { deviceId: 'dev-chiller-1', lockedPriority: 'SAFETY_INTERLOCK', lockedUntil: '2026-11-18T09:59:00.000Z' },
        { deviceId: 'dev-chiller-1', lockedPriority: 'PROTECTION_RULE', lockedUntil: '2026-11-18T11:00:00.000Z' },
      ],
    }),
    commands: [],
    nowMs,
  });
  assert.equal(expiredSafety.guard.blocked, false);
});
