import assert from 'node:assert/strict';
import test from 'node:test';
import type { Asset, AssetRelationship, Device, TelemetryPoint } from '../apps/hvac-web/src/api/generated/platformGateway.gen.ts';
import type { PlantReading } from '../apps/hvac-web/src/features/operations/realtime/plant-model.ts';
import { blockingInterlocks, controlTargets, deviceInlineReadings, plantUnits, unitActivePower } from '../apps/hvac-web/src/features/control/control-model.ts';
import {
  evaluateDeviceGates,
  isSingleLoopCombined,
  orderHydraulicChain,
} from '../apps/hvac-web/src/features/control/control-gates.ts';
import { controlErrorMessage, stepUpErrorPresentation } from '../apps/hvac-web/src/features/control/control-errors.ts';
import {
  activePriorityLocks,
  describeControlAlarmError,
  describeControlAlarmEvent,
  formatLockRemaining,
} from '../apps/hvac-web/src/features/control/control-priority-lock.ts';

const at = '2026-10-01T00:00:00.000Z';
const site = { tenantId: 't', siteId: 's' };
const asset = (id: string, displayName: string, assetType = 'CHILLER'): Asset => ({
  id, ...site, code: displayName, displayName, assetType, status: 'ACTIVE', revision: 1, createdAt: at, updatedAt: at,
});
const device = (id: string, code: string, deviceType = 'CHILLER_CONTROLLER', status: Device['status'] = 'ACTIVE'): Device => ({
  id, ...site, code, displayName: code, deviceType, status, revision: 1, createdAt: at, updatedAt: at,
});
const boundMeter = (id: string, deviceId: string, assetId: string, validTo: string | null = null): AssetRelationship => ({
  id, ...site, fromType: 'DEVICE', fromId: deviceId, toType: 'ASSET', toId: assetId, role: 'OBSERVES', status: 'ACTIVE',
  validFrom: at, validTo, revision: 1, createdAt: at, updatedAt: at,
});
const point = (
  id: string,
  deviceId: string,
  pointCode: string,
  pointType: TelemetryPoint['pointType'],
  sourceMetadata: Record<string, unknown> = {},
  extra: Partial<TelemetryPoint> = {},
): TelemetryPoint => ({
  id, ...site, reportingDeviceId: deviceId, sensorId: null, pointCode, sourceKey: `chiller.${pointCode}`, displayName: pointCode,
  pointType, valueType: 'NUMBER', unit: null, writable: pointType === 'COMMAND', sampleIntervalMs: 1000, publishIntervalMs: 1000,
  staleAfterMs: 3000, counterDecreaseMode: null, counterRolloverModulus: null, sourceMetadata, status: 'ACTIVE', revision: 1, createdAt: at, updatedAt: at, ...extra,
});
const controls = (id: string, pointId: string, assetId: string, validTo: string | null = null): AssetRelationship => ({
  id, ...site, fromType: 'POINT', fromId: pointId, toType: 'ASSET', toId: assetId, role: 'CONTROLS', status: 'ACTIVE',
  validFrom: at, validTo, revision: 1, createdAt: at, updatedAt: at,
});
const now = new Date('2026-10-11T00:00:00Z');

test('a plant asset offers the actions and settings its command points control, with the reading that confirms each', () => {
  const model = {
    assets: [asset('a-chiller', 'CHILLER-01'), asset('a-retired', 'CHILLER-OLD')],
    telemetryPoints: [
      point('p-stop', 'd-chiller', 'stop', 'COMMAND', { capability: 'STOP', feedbackPointKey: 'run_state' }, { displayName: '停止冷水机' }),
      point('p-start', 'd-chiller', 'start', 'COMMAND', { capability: 'START', feedbackPointKey: 'run_state' }, { displayName: '启动冷水机' }),
      point('p-setpoint', 'd-chiller', 'set_chilled_water_temperature_setpoint', 'COMMAND', {
        capability: 'SET_CHILLED_WATER_TEMPERATURE_SETPOINT', parameterKey: 'setpointC', minimum: 5, maximum: 12, step: 0.5,
        feedbackPointKey: 'chilled_water_temperature_setpoint',
      }, { displayName: '冷冻水出水设定温度', unit: 'Cel' }),
      point('p-feedback', 'd-chiller', 'chilled_water_temperature_setpoint', 'TELEMETRY'),
      point('p-old', 'd-old', 'start', 'COMMAND', { capability: 'START' }),
    ],
    relationships: [
      controls('r1', 'p-stop', 'a-chiller'), controls('r2', 'p-start', 'a-chiller'), controls('r3', 'p-setpoint', 'a-chiller'),
      controls('r4', 'p-old', 'a-retired', '2026-10-02T00:00:00.000Z'),
    ],
  };

  const targets = controlTargets(model, now);
  assert.deepEqual(targets.map((target) => target.assetName), ['CHILLER-01']);
  const [chiller] = targets;
  assert.equal(chiller.deviceId, 'd-chiller');
  assert.deepEqual(chiller.actions.map((action) => [action.capability, action.label]), [['START', '启动冷水机'], ['STOP', '停止冷水机']]);
  assert.deepEqual(chiller.settings, [{
    assetId: 'a-chiller', pointId: 'p-setpoint', capability: 'SET_CHILLED_WATER_TEMPERATURE_SETPOINT', label: '冷冻水出水设定温度',
    parameterKey: 'setpointC', unit: '°C', minimum: 5, maximum: 12, step: 0.5, feedbackSourceKey: 'chiller.chilled_water_temperature_setpoint',
  }]);
});

test('the emergency release that drops every output channel is never offered as a device stop', () => {
  const model = {
    assets: [asset('a-loop', '冷冻水总管', 'CHILLED_WATER_LOOP'), asset('a-pump', '1 号冷冻泵', 'CHILLED_WATER_PUMP')],
    telemetryPoints: [
      point('p-emergency', 'd-et1010', 'emergency_stop_all', 'COMMAND', { capability: 'STOP', actuationMode: 'EMERGENCY_DO_RELEASE', feedbackPointKey: 'run_state' }),
      point('p-pump', 'd-et1010', 'chwp_1_stop', 'COMMAND', { capability: 'STOP', actuationMode: 'LEVEL', feedbackPointKey: 'run_state' }),
    ],
    relationships: [controls('r1', 'p-emergency', 'a-loop'), controls('r2', 'p-pump', 'a-pump')],
  };
  assert.deepEqual(controlTargets(model, now).map((target) => [target.assetName, target.actions.map((action) => action.pointId)]), [['1 号冷冻泵', ['p-pump']]]);
});

test('plant assets are offered in the order water flows through the plant', () => {
  const ids = ['a-ct', 'a-cwp', 'a-chiller', 'a-chwp'];
  const model = {
    assets: [asset('a-ct', 'CT-01', 'COOLING_TOWER'), asset('a-cwp', 'CWP-01', 'COOLING_WATER_PUMP'), asset('a-chiller', 'CHILLER-01'), asset('a-chwp', 'CHWP-01', 'CHILLED_WATER_PUMP')],
    telemetryPoints: ids.map((id) => point(`p-${id}`, `d-${id}`, 'start', 'COMMAND', { capability: 'START' })),
    relationships: ids.map((id) => controls(`r-${id}`, `p-${id}`, id)),
  };
  assert.deepEqual(controlTargets(model, now).map((target) => target.assetName), ['CHILLER-01', 'CHWP-01', 'CWP-01', 'CT-01']);
});

test('a chiller and the pumps and tower its compiled interlockChecks name form one unit with its active HVAC_POWER_METER; unnamed assets stay apart', () => {
  const chillerInterlocks = [
    { deviceId: 'd-chwp', key: 'run_state', expected: 'RUNNING', reason: '1# 冻水泵需先处于运行状态，方可脉冲开启 1# 主机' },
    { deviceId: 'd-tower', key: 'run_state', expected: 'RUNNING', reason: '1# 冷却塔风机需先处于运行状态，方可脉冲开启 1# 主机' },
    { deviceId: 'd-ignored-key', key: 'fault_code', expected: 'STOPPED', reason: 'non run_state checks are ignored' },
  ];
  const model = {
    assets: [asset('a-chiller', 'A 机组'), asset('a-chiller-b', 'B 机组'), asset('a-chwp', '1 号冷冻泵', 'CHILLED_WATER_PUMP'), asset('a-tower', '1 号冷却塔', 'COOLING_TOWER'), asset('a-spare', '5 号冷却塔', 'COOLING_TOWER')],
    devices: [
      device('d-chiller', 'ctrl-chiller-a'),
      device('d-chiller-b', 'ctrl-chiller-b'),
      device('d-chwp', 'ctrl-chwp-1'),
      device('d-tower', 'ctrl-tower-1'),
      device('d-spare', 'ctrl-tower-5'),
      device('d-meter-a', 'meter-uid4', 'HVAC_POWER_METER'),
      device('d-meter-retired', 'meter-old', 'HVAC_POWER_METER', 'RETIRED'),
      device('d-meter-expired', 'meter-exp', 'HVAC_POWER_METER'),
    ],
    telemetryPoints: [
      point('p-start', 'd-chiller', 'start', 'COMMAND', { capability: 'START', interlockChecks: chillerInterlocks }),
      point('p-seq', 'd-chiller', 'stop_unit_sequence', 'COMMAND', { capability: 'STOP_UNIT_SEQUENCE' }),
      point('p-start-b', 'd-chiller-b', 'start', 'COMMAND', { capability: 'START' }),
      // Legacy sourceMetadata.interlocks is deleted and no longer parsed.
      point('p-chwp', 'd-chwp', 'stop', 'COMMAND', {
        capability: 'STOP',
        interlockChecks: [{ deviceId: 'd-chiller', expected: 'STOPPED', reason: '1# 主机尚未停机，禁止断开 1# 冻水泵' }],
        interlocks: [{ device: 'ctrl-tower-5', expected: 'STOPPED', reason: 'legacy field ignored' }],
      }),
      point('p-tower', 'd-tower', 'stop', 'COMMAND', { capability: 'STOP' }),
      point('p-spare', 'd-spare', 'stop', 'COMMAND', { capability: 'STOP' }),
    ],
    relationships: [
      controls('r1', 'p-start', 'a-chiller'),
      controls('r2', 'p-seq', 'a-chiller'),
      controls('r2b', 'p-start-b', 'a-chiller-b'),
      controls('r3', 'p-chwp', 'a-chwp'),
      controls('r4', 'p-tower', 'a-tower'),
      controls('r5', 'p-spare', 'a-spare'),
      boundMeter('m1', 'd-meter-a', 'a-chiller'),
      boundMeter('m2', 'd-meter-retired', 'a-chiller-b'),
      boundMeter('m3', 'd-meter-expired', 'a-chiller-b', '2026-10-05T00:00:00.000Z'),
    ],
  };
  const { units, others } = plantUnits(controlTargets(model, now), model, now);
  assert.deepEqual(units.map((unit) => [unit.chiller.assetName, unit.members.map((member) => member.assetName), unit.powerMeterDeviceId]), [
    ['A 机组', ['1 号冷冻泵', '1 号冷却塔'], 'd-meter-a'],
    ['B 机组', [], null],
  ]);
  assert.deepEqual(others.map((target) => target.assetName), ['5 号冷却塔']);
  assert.deepEqual(units[0].chiller.actions.map((action) => action.capability), ['START', 'STOP_UNIT_SEQUENCE']);

  const start = units[0].chiller.actions[0];
  const blocked = blockingInterlocks(start, new Map([['ctrl-chwp-1', 'RUNNING'], ['ctrl-tower-1', 'STOPPED']]));
  assert.deepEqual(blocked.map((interlock) => interlock.deviceCode), ['ctrl-tower-1']);
  // A device whose state is unreadable blocks too: the plant refuses what it cannot verify.
  assert.equal(blockingInterlocks(start, new Map()).length, 2);
  // Only compiled interlockChecks (not legacy interlocks) appear on chwp STOP.
  const chwpTarget = units[0].members.find((member) => member.assetId === 'a-chwp');
  assert.deepEqual(chwpTarget?.actions[0].interlocks.map((item) => item.deviceCode), ['ctrl-chiller-a']);
});

test('unitActivePower reads fresh or low-confidence unit power and returns null for stale, missing, or unbound meters', () => {
  const reading = (overrides: Partial<PlantReading>): PlantReading => ({
    label: '有功功率',
    display: '186.2 kW',
    unit: 'kW',
    numeric: 186.2,
    current: true,
    lowConfidence: false,
    measurement: null,
    probeOffsets: [],
    state: 'PRESENT',
    freshness: 'FRESH',
    sampledAt: '2026-10-11T00:00:00.000Z',
    ...overrides,
  });

  const active = unitActivePower({ reading: (key) => (key === 'hvac_meter.active_power' ? reading({ numeric: 186.2 }) : null) });
  assert.equal(active?.numeric, 186.2);

  const lowConfidence = unitActivePower({ reading: (key) => (key === 'chiller.power' ? reading({ current: false, lowConfidence: true, numeric: 94.5 }) : null) });
  assert.equal(lowConfidence?.numeric, 94.5);
  assert.equal(lowConfidence?.lowConfidence, true);

  // Stale or absent readings never render stale numbers.
  assert.equal(unitActivePower({ reading: (key) => (key === 'hvac_meter.active_power' ? reading({ current: false, lowConfidence: false, freshness: 'STALE' }) : null) }), null);
  assert.equal(unitActivePower({ reading: (key) => (key === 'hvac_meter.active_power' ? reading({ state: 'MISSING', numeric: null, current: false }) : null) }), null);
  assert.equal(unitActivePower(undefined), null);
});

test('stepUpErrorPresentation distinguishes all four TOTP reasons and flags enrollment only on MFA_NOT_CONFIGURED', () => {
  const invalid = stepUpErrorPresentation('CODE_INVALID', true);
  assert.equal(invalid.showEnrollLink, false);
  assert.match(invalid.message, /动态口令不对/);

  const replayed = stepUpErrorPresentation('CODE_REPLAYED', true);
  assert.equal(replayed.showEnrollLink, false);
  assert.match(replayed.message, /已经用过/);

  const notConfigured = stepUpErrorPresentation('MFA_NOT_CONFIGURED', true);
  assert.equal(notConfigured.showEnrollLink, true);
  assert.match(notConfigured.message, /开通两步验证/);

  const locked = stepUpErrorPresentation('MFA_LOCKED', true);
  assert.equal(locked.showEnrollLink, false);
  assert.match(locked.message, /暂时锁定/);

  const promptFirst = stepUpErrorPresentation(undefined, false);
  assert.equal(promptFirst.showEnrollLink, false);
  assert.match(promptFirst.message, /高风险/);
});

test('controlErrorMessage translates DEVICE_PRIORITY_LOCKED and COMMAND_PRIORITY_INVALID', () => {
  assert.match(controlErrorMessage('DEVICE_PRIORITY_LOCKED', 'fallback'), /更高优先级的安全联锁或人工指令锁定/);
  assert.match(controlErrorMessage('COMMAND_PRIORITY_INVALID', 'fallback'), /指令优先级与控制来源不匹配/);
});

test('activePriorityLocks filters expired locks and formats remaining countdown and DLQ reasons in Chinese', () => {
  const nowMs = Date.parse('2026-11-18T10:00:00Z');
  const active = activePriorityLocks({
    devicePriorityLocks: [
      {
        deviceId: 'd-active',
        lockedPriority: 'SAFETY_INTERLOCK',
        commandSource: 'OPERATOR',
        lockedBy: 'u-1',
        lockedUntil: '2026-11-18T10:45:00Z',
        reason: '压差联锁',
        updatedAt: '2026-11-18T09:45:00Z',
      },
      {
        deviceId: 'd-schedule',
        lockedPriority: 'SCHEDULE',
        commandSource: 'SCHEDULE',
        lockedBy: 'schedule-engine',
        lockedUntil: '2026-11-18T11:30:00Z',
        reason: '定时日程控制',
        updatedAt: '2026-11-18T09:50:00Z',
      },
      {
        deviceId: 'd-expired',
        lockedPriority: 'MANUAL_OVERRIDE',
        commandSource: 'OPERATOR',
        lockedBy: 'u-1',
        lockedUntil: '2026-11-18T09:59:00Z',
        reason: '已过期',
        updatedAt: '2026-11-18T09:00:00Z',
      },
    ],
  }, nowMs);
  assert.equal(active.size, 2);
  assert.equal(active.get('d-active')?.lockedPriority, 'SAFETY_INTERLOCK');
  assert.equal(active.get('d-schedule')?.lockedPriority, 'SCHEDULE');
  assert.equal(formatLockRemaining('2026-11-18T10:45:00Z', nowMs), '剩余 45 分钟');
  assert.equal(formatLockRemaining('2026-11-18T12:10:00Z', nowMs), '剩余 2 小时 10 分钟');
  assert.equal(formatLockRemaining('2026-11-18T09:59:00Z', nowMs), '即将到期');
  assert.equal(describeControlAlarmEvent('PRIORITY_LOCK'), '优先级锁定');
  assert.equal(describeControlAlarmEvent('OUTCOME_UNKNOWN'), '执行结果未知');
  assert.equal(describeControlAlarmEvent('PULSE_DELIVERED_POWER_UNCONFIRMED'), '脉冲发出后功率未确认');
  assert.equal(describeControlAlarmEvent('EMERGENCY_DO_RELEASE'), '紧急输出释放');
  assert.equal(describeControlAlarmError('TIMEOUT'), '下游告警服务超时未响应');
  assert.equal(describeControlAlarmError('VERIFY_TIMEOUT'), '设备状态读回超时未确认');
  assert.equal(describeControlAlarmError('DISPATCH_REJECTED'), '安全校验未通过导致下发拒收');
  assert.equal(describeControlAlarmError('CONTROL_ALARM_HTTP_FAILED'), '下游告警服务通信失败');
});

test('deviceInlineReadings returns curated present readings up to maxCount, preserves stale/low-confidence readings, and supports fallback keys', () => {
  const mkReading = (label: string, overrides: Partial<PlantReading> = {}): PlantReading => ({
    label,
    display: '7.0',
    unit: '°C',
    numeric: 7.0,
    current: true,
    lowConfidence: false,
    measurement: null,
    probeOffsets: [],
    state: 'PRESENT',
    freshness: 'FRESH',
    sampledAt: '2026-10-11T00:00:00.000Z',
    ...overrides,
  });

  assert.deepEqual(deviceInlineReadings({ assetType: 'CHILLER', settings: [] }, undefined), []);

  const chillerValues: Readonly<Record<string, PlantReading>> = {
    'chiller.leaving_chilled_water_temperature': mkReading('冷冻水出水温度', { display: '7.0', unit: '°C' }),
    'chiller.chilled_water_temperature_setpoint': mkReading('冷冻水设定温度', { state: 'MISSING', display: '—', numeric: null, current: false }),
    'chiller.compressor_load': mkReading('压缩机负载', { display: '72', unit: '%', freshness: 'STALE', current: false }),
    'chiller.power': mkReading('主机功率', { display: '155.7', unit: 'kW', current: false, lowConfidence: true }),
    'chiller.cop': mkReading('主机 COP', { display: '5.55', unit: null }),
  };
  const chillerReadings = deviceInlineReadings(
    { assetType: 'CHILLER', settings: [] },
    { reading: (key) => chillerValues[key] ?? null },
  );
  assert.deepEqual(
    chillerReadings.map((item) => [item.key, item.shortLabel]),
    [
      ['chiller.leaving_chilled_water_temperature', '出水'],
      ['chiller.compressor_load', '负载'],
      ['chiller.power', '功率'],
      ['chiller.cop', 'COP'],
    ],
  );
  assert.equal(chillerReadings[1].reading.freshness, 'STALE');
  assert.equal(chillerReadings[2].reading.lowConfidence, true);

  const pumpValues: Readonly<Record<string, PlantReading>> = {
    'chwp.frequency': mkReading('运行频率', { display: '30', unit: 'Hz' }),
    'chwp.flow_rate': mkReading('冷冻水流量', { display: '132', unit: 'm³/h' }),
    'chwp.power': mkReading('水泵功率', { display: '9.7', unit: 'kW' }),
  };
  assert.deepEqual(
    deviceInlineReadings(
      { assetType: 'CHILLED_WATER_PUMP', settings: [] },
      { reading: (key) => pumpValues[key] ?? null },
    ).map((item) => [item.key, item.shortLabel]),
    [
      ['chwp.frequency', '频率'],
      ['chwp.flow_rate', '流量'],
      ['chwp.power', '功率'],
    ],
  );

  const towerValues: Readonly<Record<string, PlantReading>> = {
    'cooling_tower.fan_speed': mkReading('风机转速', { display: '80', unit: '%' }),
    'cooling_tower.leaving_water_temperature': mkReading('出塔水温', { display: '30.7', unit: '°C' }),
    'cooling_tower.power': mkReading('冷却塔功率', { display: '9.5', unit: 'kW' }),
  };
  assert.deepEqual(
    deviceInlineReadings(
      { assetType: 'COOLING_TOWER', settings: [] },
      { reading: (key) => towerValues[key] ?? null },
    ).map((item) => [item.key, item.shortLabel]),
    [
      ['cooling_tower.fan_speed', '转速'],
      ['cooling_tower.leaving_water_temperature', '出塔'],
      ['cooling_tower.power', '功率'],
    ],
  );

  const customValues: Readonly<Record<string, PlantReading>> = {
    'vfd.frequency_feedback': mkReading('变频运行频率', { display: '42', unit: 'Hz' }),
    'valve.custom_target': mkReading('阀门反馈开度', { display: '55', unit: '%' }),
  };
  assert.deepEqual(
    deviceInlineReadings(
      {
        assetType: 'CUSTOM_CONTROLLER',
        settings: [
          {
            assetId: 'a-1',
            pointId: 'p-1',
            capability: 'SET_FREQUENCY',
            label: '频率设定',
            parameterKey: 'hz',
            unit: 'Hz',
            minimum: 20,
            maximum: 50,
            step: 1,
            feedbackSourceKey: 'vfd.frequency_feedback',
          },
          {
            assetId: 'a-1',
            pointId: 'p-2',
            capability: 'SET_OPENING',
            label: '开度设定',
            parameterKey: 'open',
            unit: '%',
            minimum: 0,
            maximum: 100,
            step: 5,
            feedbackSourceKey: 'valve.custom_target',
          },
        ],
      },
      { reading: (key) => customValues[key] ?? null },
    ).map((item) => [item.key, item.shortLabel]),
    [
      ['vfd.frequency_feedback', '频率'],
      ['valve.custom_target', '阀门反馈开度'],
    ],
  );
});

test('evaluateDeviceGates checks all five safety gates in order and surfaces interlock jump targets', () => {
  const model = {
    assets: [
      asset('a-chiller', 'C 机组主机'),
      asset('a-tower', '3 号冷却塔', 'COOLING_TOWER'),
      asset('a-cwp', '3 号冷却泵', 'COOLING_WATER_PUMP'),
      asset('a-chwp', '3 号冷冻泵', 'CHILLED_WATER_PUMP'),
    ],
    devices: [
      device('d-chiller', 'ctrl-chiller-c'),
      device('d-tower', 'ctrl-tower-3'),
      device('d-cwp', 'ctrl-cwp-3'),
      device('d-chwp', 'ctrl-chwp-3'),
    ],
    telemetryPoints: [
      point('p-chiller-start', 'd-chiller', 'start', 'COMMAND', {
        capability: 'START',
        interlockChecks: [{ deviceId: 'd-tower', key: 'run_state', expected: 'RUNNING', reason: '须先开启 3 号冷却塔' }],
      }, { displayName: '启动主机' }),
      point('p-chiller-stop', 'd-chiller', 'stop', 'COMMAND', { capability: 'STOP' }, { displayName: '停止主机' }),
      point('p-chiller-sp', 'd-chiller', 'set_chilled_water_temperature_setpoint', 'COMMAND', {
        capability: 'SET_CHILLED_WATER_TEMPERATURE_SETPOINT',
        parameterKey: 'setpointC',
        minimum: 6,
        maximum: 12,
        step: 0.5,
      }, { displayName: '冷冻水出水设定温度', unit: 'Cel' }),
      point('p-tower-start', 'd-tower', 'start', 'COMMAND', { capability: 'START' }, { displayName: '启动冷却塔' }),
      point('p-cwp-start', 'd-cwp', 'start', 'COMMAND', { capability: 'START' }, { displayName: '启动冷却泵' }),
      point('p-chwp-start', 'd-chwp', 'start', 'COMMAND', { capability: 'START' }, { displayName: '启动冷冻泵' }),
    ],
    relationships: [
      controls('r1', 'p-chiller-start', 'a-chiller'),
      controls('r1b', 'p-chiller-stop', 'a-chiller'),
      controls('r1c', 'p-chiller-sp', 'a-chiller'),
      controls('r2', 'p-tower-start', 'a-tower'),
      controls('r3', 'p-cwp-start', 'a-cwp'),
      controls('r4', 'p-chwp-start', 'a-chwp'),
    ],
  };

  const targets = controlTargets(model, now);
  const { units, others } = plantUnits(targets, model, now);
  const chillerTarget = targets.find((target) => target.assetId === 'a-chiller')!;
  const deviceIdByCode = new Map(model.devices.map((item) => [item.code, item.id]));
  const nameByDeviceCode = new Map([
    ['ctrl-chiller-c', 'C 机组主机'],
    ['ctrl-tower-3', '3 号冷却塔'],
  ]);
  const baseInput = {
    target: chillerTarget,
    device: { deviceId: 'd-chiller', connection: 'ONLINE' as const, runState: 'STOPPED' as const, latestSampleAt: '2026-10-11T00:00:00Z' },
    mode: 'REMOTE_AUTO' as const,
    maintenance: null,
    lock: undefined,
    priorityLock: undefined,
    commands: [],
    targets,
    runStateByDeviceCode: new Map([['ctrl-tower-3', 'STOPPED'], ['ctrl-chiller-c', 'STOPPED']]),
    nameByDeviceCode,
    deviceIdByCode,
    myId: 'u-1',
    timeZone: 'Asia/Shanghai',
    nowMs: Date.parse('2026-10-11T00:01:00Z'),
  };

  // Gate 5 blocked by stopped tower with jump target resolved to 3 号冷却塔.
  const interlockBlocked = evaluateDeviceGates(baseInput);
  assert.equal(interlockBlocked.canAct, false);
  assert.equal(interlockBlocked.canSet, true);
  assert.equal(interlockBlocked.blockingGateIndex, 4);
  assert.equal(interlockBlocked.gates[4].status, 'fail');
  assert.equal(interlockBlocked.gates[4].jumpTargetAssetId, 'a-tower');
  assert.equal(interlockBlocked.gates[4].jumpTargetName, '3 号冷却塔');

  // Gate 1 blocks everything in manual override.
  const manualBlocked = evaluateDeviceGates({ ...baseInput, mode: 'MANUAL_OVERRIDE' });
  assert.equal(manualBlocked.canAct, false);
  assert.equal(manualBlocked.canSet, false);
  assert.equal(manualBlocked.blockingGateIndex, 0);
  assert.equal(manualBlocked.gates[0].status, 'fail');

  // Gate 3 maintenance window blocks starting and setpoints, yet warns and permits stopping a running chiller.
  const maintStart = evaluateDeviceGates({
    ...baseInput,
    maintenance: { id: 'm1', title: '冷却塔清洗', startDate: '2026-10-10', endDate: '2026-10-12', reason: '' },
    runStateByDeviceCode: new Map([['ctrl-tower-3', 'RUNNING']]),
  });
  assert.equal(maintStart.canAct, false);
  assert.equal(maintStart.canSet, false);
  assert.equal(maintStart.gates[2].status, 'fail');

  const maintStop = evaluateDeviceGates({
    ...baseInput,
    device: { deviceId: 'd-chiller', connection: 'ONLINE', runState: 'RUNNING', latestSampleAt: '2026-10-11T00:00:00Z' },
    maintenance: { id: 'm1', title: '冷却塔清洗', startDate: '2026-10-10', endDate: '2026-10-12', reason: '' },
  });
  assert.equal(maintStop.canAct, true);
  assert.equal(maintStop.canSet, false);
  assert.equal(maintStop.gates[2].status, 'warn');

  // Single-loop combined chain ordering places tower -> cooling pump -> chiller -> chilled pump.
  const simTargets = targets.map((t) => ({ ...t }));
  const standaloneOnly = controlTargets({
    assets: model.assets,
    devices: model.devices,
    telemetryPoints: model.telemetryPoints.map((p) => (
      p.id === 'p-chiller-start' ? { ...p, sourceMetadata: { capability: 'START' } } : p
    )),
    relationships: model.relationships,
  }, now);
  const simGrouped = plantUnits(standaloneOnly, model, now);
  assert.equal(isSingleLoopCombined(simGrouped.units, simGrouped.others), true);
  assert.deepEqual(
    orderHydraulicChain(simGrouped.units[0]!, simGrouped.others).map((item) => item.assetType),
    ['COOLING_TOWER', 'COOLING_WATER_PUMP', 'CHILLER', 'CHILLED_WATER_PUMP'],
  );
  assert.equal(units[0]?.members.length, 1);
  assert.equal(others.length, 2);
  assert.equal(simTargets.length, 4);
});
