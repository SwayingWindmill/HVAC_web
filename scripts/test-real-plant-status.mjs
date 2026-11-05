import assert from 'node:assert/strict';
import test from 'node:test';
import { buildPlantView, coolingUncertaintyText, copUncertaintyText, equipmentStatus, isRunning, probeOffsetText, soleReading, tileReadings } from '../apps/hvac-web/src/features/operations/realtime/plant-model.ts';

const device = (category, overrides = {}) => ({
  deviceId: `${category}-${Math.random()}`,
  name: category,
  category,
  connection: 'ONLINE',
  runState: 'RUNNING',
  latestSampleAt: '2026-10-10T06:00:00Z',
  hasStaleData: false,
  reading: (key) => ({ label: key, numeric: 7, state: 'PRESENT', current: true }),
  ...overrides,
});
const plant = (devices) => ({ devices, groups: [], onlineCount: 0, latestSampleAt: null, totalPower: null, coolingCapacity: null, plantCop: null });

test('a 0.9 K header shows about ±16% and says the offset was not applied', () => {
  const cooling = {
    current: false,
    numeric: 512,
    measurement: { relative: 0.158, low: 431, high: 593, lowConfidence: true, copRelative: 0.159, deltaTK: 0.9 },
    probeOffsets: [],
  };
  const plant = {
    devices: [{
      row: {
        operational: {
          points: [{
            probeOffsets: [{ pairCode: 'header-chw', offsetK: 0.22, stddevK: 0.01, sampleCount: 13, suggested: true, applied: false, alarm: 'OFFSET' }],
          }],
        },
      },
    }],
    coolingCapacity: cooling,
    plantCop: 4.1,
    plantCopLowConfidence: true,
  };
  assert.equal(coolingUncertaintyText(cooling), '低可信 · 431–593 kW（±16%），温差相对探头误差偏小');
  assert.equal(copUncertaintyText(plant), '低可信 · 3.45–4.75（±16%），不计入能效均值和诊断');
  assert.equal(copUncertaintyText({ ...plant, plantCop: null }), null);
  assert.equal(probeOffsetText(plant), '探头零点建议把温差减去 0.22 K（未采用，偏差已超过提醒线）');
});

const meterRow = (points) => ({
  device: { id: 'meter-1', displayName: '冷站电表', deviceType: 'METER' },
  binding: { state: 'unbound' },
  telemetryPoints: points.map((point) => ({ sourceKey: point.sourceKey, pointCode: point.key, status: 'ACTIVE' })),
  snapshotResult: { status: 'ok', snapshot: { values: points.map((point) => ({ key: point.key, state: 'PRESENT', value: point.value })) } },
  operational: {
    connection: { state: 'ONLINE' },
    points: points.map((point) => ({
      key: point.key, label: point.key, state: 'PRESENT', displayValue: String(point.value), unit: 'kW', freshness: 'FRESH',
      quality: point.quality ?? 'GOOD', qualityReasons: point.reasons ?? [], health: point.health ?? null,
      measurement: point.measurement ?? null, probeOffsets: [], sampledAt: '2026-11-05T00:00:00Z',
    })),
  },
});

test('a LOW_CONFIDENCE cooling load still gives a live COP, labelled 低可信 with its interval', () => {
  const measurement = { relative: 0.158, low: 431, high: 593, lowConfidence: true, copRelative: 0.159, deltaTK: 0.9 };
  const view = buildPlantView([meterRow([
    { key: 'P', sourceKey: 'hvac_meter.active_power', value: 125 },
    { key: 'Q', sourceKey: 'btu_meter.instant_cooling_capacity', value: 512, quality: 'PARTIAL', reasons: ['LOW_CONFIDENCE'], health: { status: 'SUSPECT', score: 60 }, measurement },
  ])]);
  assert.equal(view.coolingCapacity.current, false);
  assert.equal(view.coolingCapacity.lowConfidence, true);
  assert.equal(view.plantCop, 512 / 125);
  assert.equal(view.plantCopLowConfidence, true);
  assert.ok(Math.abs(view.plantCopRange.low - 4.096 * 0.841) < 1e-9);
  assert.match(copUncertaintyText(view), /^低可信 · 3\.44–4\.75（±16%）/);
});

test('any other doubt still withholds the live COP', () => {
  for (const reasons of [['LOW_CONFIDENCE', 'SPIKE'], ['DERIVED_INPUT']]) {
    const view = buildPlantView([meterRow([
      { key: 'P', sourceKey: 'hvac_meter.active_power', value: 125 },
      { key: 'Q', sourceKey: 'btu_meter.instant_cooling_capacity', value: 512, quality: 'PARTIAL', reasons },
    ])]);
    assert.equal(view.coolingCapacity.lowConfidence, false);
    assert.equal(view.plantCop, null);
  }
  const bad = buildPlantView([meterRow([
    { key: 'P', sourceKey: 'hvac_meter.active_power', value: 125 },
    { key: 'Q', sourceKey: 'btu_meter.instant_cooling_capacity', value: 512, quality: 'PARTIAL', reasons: ['LOW_CONFIDENCE'], health: { status: 'BAD', score: 10 } },
  ])]);
  assert.equal(bad.plantCop, null);
  const confident = buildPlantView([meterRow([
    { key: 'P', sourceKey: 'hvac_meter.active_power', value: 125 },
    { key: 'Q', sourceKey: 'btu_meter.instant_cooling_capacity', value: 512 },
  ])]);
  assert.equal(confident.plantCopLowConfidence, false);
});

test('an offline device is offline whatever run state it last reported', () => {
  assert.equal(isRunning(device('CHILLER')), true);
  assert.equal(isRunning(device('CHILLER', { connection: 'OFFLINE' })), false);
  assert.equal(isRunning(device('CHILLER', { runState: 'STOPPED' })), false);
});

test('equipment counts cover chillers, pumps and towers; freshness covers every device', () => {
  const status = equipmentStatus(plant([
    device('CHILLER'),
    device('CHILLED_WATER_PUMP', { connection: 'OFFLINE', runState: 'FAULT' }),
    device('COOLING_TOWER', { runState: 'FAULT' }),
    device('METER', { runState: 'UNKNOWN', latestSampleAt: null }),
    device('WEATHER', { runState: 'UNKNOWN', hasStaleData: true }),
  ]));
  assert.deepEqual(status, { total: 3, running: 1, faults: 1, offline: 1, withoutData: 1, stale: 1 });
});

test('a device probe stands for its pipe only when the category has one device', () => {
  assert.equal(soleReading(plant([device('CHILLER')]), 'CHILLER', 'chiller.leaving_chilled_water_temperature')?.numeric, 7);
  assert.equal(soleReading(plant([device('CHILLER'), device('CHILLER')]), 'CHILLER', 'chiller.leaving_chilled_water_temperature'), null);
  assert.equal(soleReading(plant([]), 'CHILLER', 'chiller.leaving_chilled_water_temperature'), null);
});

test('a Gateway-wired device without an Asset or source-key namespace is grouped by its device type', () => {
  const row = (deviceType, displayName) => ({
    device: { id: displayName, deviceType, displayName },
    binding: { state: 'unbound' },
    telemetryPoints: [{ sourceKey: 't1', pointCode: 't1', status: 'ACTIVE' }],
    operational: { connection: { state: 'ONLINE' }, points: [] },
  });
  const view = buildPlantView([row('FLOW_METER', '流量计'), row('TEMPERATURE_MODULE', '温度采集模块'), row('SOMETHING_NEW', '未知设备')]);
  assert.deepEqual(view.groups.map((group) => [group.label, group.devices.map((device) => device.name)]), [
    ['计量', ['流量计']], ['采集与控制', ['温度采集模块']], ['其他设备', ['未知设备']],
  ]);
});

test("a device without its role's key readings shows its own measurements, current ones first", () => {
  const readings = { power_total: { label: '有功功率', current: false }, t1: { label: '温度 1', current: true }, t2: { label: '温度 2', current: true } };
  const meter = {
    row: { telemetryPoints: ['power_total', 't1', 't2', 'energy'].map((sourceKey) => ({ sourceKey, status: 'ACTIVE', pointType: sourceKey === 'energy' ? 'COUNTER' : 'TELEMETRY', valueType: 'NUMBER' })) },
    reading: (key) => readings[key] ?? null,
  };
  const own = tileReadings(meter, ['chiller.power'], []);
  assert.equal(own.lead.sourceKey, 't1');
  assert.deepEqual(own.facts.map((reading) => reading.label), ['温度 2', '有功功率']);
  const keyed = tileReadings({ ...meter, reading: (key) => (key === 'chiller.power' ? { label: '功率', current: true } : readings[key] ?? null) }, ['chiller.power'], ['t2']);
  assert.equal(keyed.lead.sourceKey, 'chiller.power');
  assert.deepEqual(keyed.facts.map((reading) => reading.label), ['温度 2']);
});
