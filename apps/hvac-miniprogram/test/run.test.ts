import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { buildPlant } from '../miniprogram/lib/plant.ts';
import { hourlyCop, presentDevice, presentDevices, presentPlantNow, presentRealtime } from '../miniprogram/lib/run.ts';

// Recorded from the local platform for the web's browser tests: one chiller, its pumps and tower running.
const fixture = (name: string) => JSON.parse(readFileSync(new URL(`../../hvac-web/e2e/fixtures/${name}`, import.meta.url), 'utf8'));
const assetModel = fixture('asset-model.json');
const snapshots = fixture('snapshots.json');
const now = new Date('2026-10-01T15:30:00Z');
const plant = buildPlant(assetModel, snapshots, now);

/** The recorded plant with some devices' snapshots changed. */
function plantWith(change: (snapshotOf: (name: string) => { presence: { currentState: string }; values: { key: string; value: unknown; freshness: string }[] }) => void) {
  const items = structuredClone(snapshots.items);
  change((name) => {
    const deviceId = assetModel.devices.find((device: { displayName: string }) => device.displayName.startsWith(name)).id;
    return items.find((item: { deviceId: string }) => item.deviceId === deviceId).snapshot;
  });
  return buildPlant(assetModel, { ...snapshots, items }, now);
}

test('实时运行 leads with whether the plant runs, its power, cooling and efficiency, then the water loops and the air', () => {
  const view = presentRealtime(plant);
  assert.deepEqual(view.status, { label: '运行中', tone: 'running', detail: '主要设备 4 / 4 台运行' });
  assert.deepEqual(view.metrics, [
    { label: '冷站总功率', value: '211.9', unit: 'kW' },
    { label: '制冷量', value: '864', unit: 'kW' },
    { label: '能效', value: '4.08', unit: '' },
  ]);
  assert.equal(view.copNote, '');
  assert.deepEqual(view.loops, [
    { name: '冷冻水', headline: '供 7 °C · 回 12.6 °C', detail: '温差 5.6 °C · 流量 132 m³/h', late: false },
    { name: '冷却水', headline: '出塔 30.7 °C · 进塔 34.1 °C', detail: '与湿球差 3.7 °C · 流量 260 m³/h', late: false },
    { name: '室内', headline: '24.1 °C', detail: '', late: false },
    { name: '室外', headline: '34 °C', detail: '湿球 27 °C · 相对湿度 65 %RH', late: false },
  ]);
  assert.equal(view.devices, '4 / 4 台运行 · 8 / 8 在线');
});

test('a stopped plant says so and why there is no efficiency; a late reading is marked, an offline device is not running', () => {
  const stopped = presentRealtime(plantWith((snapshotOf) => {
    for (const name of ['CHILLER-01', 'CHWP-01', 'CWP-01', 'CT-01']) {
      const runState = snapshotOf(name).values.find((value) => value.key === 'run_state');
      if (runState) runState.value = 'STOPPED';
    }
    snapshotOf('BTU-METER-01').values.find((value) => value.key === 'instant_cooling_capacity')!.value = 0;
    snapshotOf('WEATHER-STATION-01').values.forEach((value) => { value.freshness = 'STALE'; });
  }));
  assert.deepEqual(stopped.status, { label: '已停机', tone: 'stopped', detail: '主要设备 0 / 4 台运行' });
  assert.equal(stopped.metrics[2].value, '—');
  assert.equal(stopped.copNote, '停机时不算能效。');
  assert.deepEqual(stopped.loops.find((loop) => loop.name === '室外'), { name: '室外', headline: '34 °C', detail: '湿球 27 °C · 相对湿度 65 %RH', late: true });

  const offline = presentRealtime(plantWith((snapshotOf) => { snapshotOf('CHILLER-01').presence.currentState = 'OFFLINE'; }));
  assert.equal(offline.status?.detail, '主要设备 3 / 4 台运行');
  assert.equal(offline.devices, '3 / 4 台运行 · 7 / 8 在线');
});

test('today by the hour: efficiency where both meters read, no cooling drawn flat, hours without readings marked and named', () => {
  // The Site day starts at 16:00 UTC (Asia/Shanghai midnight); it is now 16:30 at the Site.
  const start = '2026-10-20T16:00:00.000Z';
  const hour = (index: number, energyKWh: number) => {
    const from = Date.parse(start) + index * 3_600_000;
    return { periodStart: new Date(from).toISOString(), periodEnd: new Date(from + 3_600_000).toISOString(), energyKWh };
  };
  const electricity = [0, 1, 2, 3, 4, 5, 6].map((index) => hour(index, 1))
    .concat([7, 8, 9, 10, 11].map((index) => hour(index, 100)), [hour(15, 200), hour(16, 50)]);
  const cooling = [0, 1, 2, 3, 4, 5, 6].map((index) => hour(index, 0))
    .concat([500, 550, 600, 520, 588].map((kWh, offset) => hour(7 + offset, kWh)), [hour(15, 900)]);
  const chart = hourlyCop(electricity, cooling, start, new Date(Date.parse(start) + 16.5 * 3_600_000));

  assert.equal(chart.bars.length, 24);
  assert.deepEqual(chart.bars.map((bar) => bar.state).join(' '), [
    ...Array(7).fill('idle'), ...Array(5).fill('value'), ...Array(3).fill('missing'), 'value', ...Array(8).fill('later'),
  ].join(' '));
  assert.deepEqual(chart.bars.slice(7, 12).map((bar) => bar.height), [83, 92, 100, 87, 98]);
  assert.deepEqual(chart.scale, ['6', '3', '0']);
  assert.equal(chart.peak, '今天最高 6.00（9 点）');
  assert.equal(chart.gaps, '12–14 点没有读数');

  const empty = hourlyCop([], [], start, new Date(Date.parse(start) + 3_600_000));
  assert.equal(empty.peak, '');
  assert.equal(empty.gaps, '0 点没有读数');
});

test('设备 lists the main equipment, then meters and the air, each with its state, its lead reading and what is wrong', () => {
  const view = presentDevices(plant, 'all');
  assert.deepEqual(view.filters.map((filter) => [filter.label, filter.count]), [['全部', 8], ['运行', 4], ['停机', 0], ['需关注', 0]]);
  assert.deepEqual(view.groups.map((group) => [group.title, group.caption]), [['冷站主设备', '4 台 · 4 台运行'], ['计量和环境', '4 台 · 全部在线']]);
  assert.deepEqual(view.groups[0].devices[0], {
    deviceId: '018f3e00-4000-7000-8000-000000000001', name: 'CHILLER-01', status: { label: '运行', tone: 'running' },
    value: '155.7', unit: 'kW', detail: '冷冻水出水温度 7 °C · 压缩机负载 72%',
  });
  assert.deepEqual(view.groups[1].devices.map((device) => [device.name, device.value, device.unit, device.detail]), [
    ['ZONE-SENSOR-01 通信端点', '24.1', '°C', ''],
    ['BTU-METER-01', '864', 'kW', ''],
    ['METER-HVAC-TOTAL', '211.9', 'kW', ''],
    ['WEATHER-STATION-01', '34', '°C', ''],
  ]);
});

test('需关注 gathers what is offline, faulty or late; a filter with nothing in it says so', () => {
  const bad = plantWith((snapshotOf) => {
    snapshotOf('CHILLER-01').presence.currentState = 'OFFLINE';
    const pump = snapshotOf('CHWP-01');
    pump.values.find((value) => value.key === 'flow_rate')!.freshness = 'STALE';
    Object.assign(pump.values.find((value) => value.key === 'frequency')!, { state: 'MISSING', value: undefined });
  });
  const attention = presentDevices(bad, 'attention');
  assert.deepEqual(attention.filters.map((filter) => filter.count), [8, 3, 0, 2]);
  assert.deepEqual(attention.groups.map((group) => group.devices.map((device) => [device.name, device.status.label, device.detail])), [[
    ['CHILLER-01', '离线', '冷冻水出水温度 7 °C · 压缩机负载 72%'],
    ['CHWP-01', '运行', '冷冻水流量 132 m³/h · 1 个读数没读到 · 读数过期'],
  ]]);
  assert.equal(presentDevices(plant, 'stopped').empty, '没有停机的设备。');
});

/** An hour of energy, counted from the Site's midnight. */
const siteHour = (start: string, index: number, energyKWh: number) => {
  const from = Date.parse(start) + index * 3_600_000;
  return { periodStart: new Date(from).toISOString(), periodEnd: new Date(from + 3_600_000).toISOString(), energyKWh };
};

test('a device opens on its state, its two lead readings, every reading, and the electricity it used today by the hour', () => {
  // The recording was made at 23:30 on 1 October at the Site (UTC+8).
  const start = '2026-09-30T16:00:00.000Z';
  const chiller = plant.devices.find((device) => device.name === 'CHILLER-01')!;
  const view = presentDevice(chiller, {
    start, now,
    site: [500, 600, 597].map((kWh, index) => siteHour(start, index, kWh)),
    device: [300, 400, 335].map((kWh, index) => siteHour(start, index, kWh)),
  });
  assert.deepEqual([view.name, view.kind, view.status, view.updatedAt], ['CHILLER-01', '冷水机组', { label: '运行', tone: 'running' }, '23:29:52']);
  assert.deepEqual(view.metrics, [{ label: '主机功率', value: '155.7', unit: 'kW' }, { label: '冷冻水出水温度', value: '7', unit: '°C' }]);
  assert.equal(view.readingsNote, '11 个读数。');
  assert.deepEqual(view.readings.slice(0, 2), [
    { label: '冷冻水设定温度', value: '7 °C', late: false },
    { label: '压缩机负载', value: '72%', late: false },
  ]);
  assert.deepEqual(view.energy && [view.energy.total, view.energy.share, view.energy.gaps], ['1,035', '占全站 61%', '3–22 点没有读数']);
  assert.deepEqual(view.energy?.bars.slice(0, 4).map((bar) => [bar.state, bar.height]), [['value', 75], ['value', 100], ['value', 84], ['missing', 0]]);
  assert.deepEqual(view.energy?.scale, ['400', '200', '0']);

  // A device without its own electricity meter has no electricity of its own to show.
  assert.equal(presentDevice(chiller, { start, now, site: [], device: null }).energy, null);
});

test('the overview says in one card whether the plant runs, with its power, supply water and room temperature', () => {
  assert.deepEqual(presentPlantNow(plant), {
    status: { label: '运行中', tone: 'running', detail: '主要设备 4 / 4 台运行' },
    summary: '4 / 4 台运行 · 8 / 8 在线',
    metrics: [{ label: '功率', value: '211.9', unit: 'kW' }, { label: '供水', value: '7', unit: '°C' }, { label: '室内', value: '24.1', unit: '°C' }],
  });
  assert.equal(presentPlantNow(buildPlant({ ...assetModel, devices: [] }, snapshots, now)), null);
});
