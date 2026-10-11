import assert from 'node:assert/strict';
import test from 'node:test';
import { controlTargets } from '../apps/hvac-web/src/features/control/control-model.ts';

const at = '2026-10-01T00:00:00.000Z';
const site = { tenantId: 't', siteId: 's' };
const asset = (id, displayName, assetType = 'CHILLER') => ({ id, ...site, code: displayName, displayName, assetType, status: 'ACTIVE', revision: 1, createdAt: at, updatedAt: at });
const point = (id, deviceId, pointCode, pointType, sourceMetadata = {}, extra = {}) => ({
  id, ...site, reportingDeviceId: deviceId, pointCode, sourceKey: `chiller.${pointCode}`, displayName: pointCode,
  pointType, valueType: 'NUMBER', unit: null, writable: pointType === 'COMMAND', sampleIntervalMS: 1000, publishIntervalMS: 1000,
  staleAfterMS: 3000, sourceMetadata, status: 'ACTIVE', revision: 1, createdAt: at, updatedAt: at, ...extra,
});
const controls = (id, pointId, assetId, validTo = null) => ({
  id, ...site, fromType: 'POINT', fromId: pointId, toType: 'ASSET', toId: assetId, role: 'CONTROLS', status: 'ACTIVE',
  validFrom: at, validTo, revision: 1, createdAt: at, updatedAt: at,
});

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

  const targets = controlTargets(model, new Date('2026-10-11T00:00:00Z'));
  assert.deepEqual(targets.map((target) => target.assetName), ['CHILLER-01']);
  const [chiller] = targets;
  assert.equal(chiller.deviceId, 'd-chiller');
  assert.deepEqual(chiller.actions.map((action) => [action.capability, action.label]), [['START', '启动冷水机'], ['STOP', '停止冷水机']]);
  assert.deepEqual(chiller.settings, [{
    assetId: 'a-chiller', pointId: 'p-setpoint', capability: 'SET_CHILLED_WATER_TEMPERATURE_SETPOINT', label: '冷冻水出水设定温度',
    parameterKey: 'setpointC', unit: '°C', minimum: 5, maximum: 12, step: 0.5, feedbackSourceKey: 'chiller.chilled_water_temperature_setpoint',
  }]);
});

test('plant assets are offered in the order water flows through the plant', () => {
  const model = {
    assets: [asset('a-ct', 'CT-01', 'COOLING_TOWER'), asset('a-cwp', 'CWP-01', 'COOLING_WATER_PUMP'), asset('a-chiller', 'CHILLER-01'), asset('a-chwp', 'CHWP-01', 'CHILLED_WATER_PUMP')],
    telemetryPoints: ['a-ct', 'a-cwp', 'a-chiller', 'a-chwp'].map((id) => point(`p-${id}`, `d-${id}`, 'start', 'COMMAND', { capability: 'START' })),
    relationships: ['a-ct', 'a-cwp', 'a-chiller', 'a-chwp'].map((id) => controls(`r-${id}`, `p-${id}`, id)),
  };
  assert.deepEqual(controlTargets(model, new Date('2026-10-11T00:00:00Z')).map((target) => target.assetName), ['CHILLER-01', 'CHWP-01', 'CWP-01', 'CT-01']);
});
