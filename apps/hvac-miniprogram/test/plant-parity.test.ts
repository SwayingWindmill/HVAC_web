import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
// The web's own pipeline: the device rows the 实时运行 and 设备 pages build, then the plant view.
import { buildAssetsRows } from '../../hvac-web/src/features/assets/model.ts';
import { buildPlantView } from '../../hvac-web/src/features/operations/realtime/plant-model.ts';
import { buildPlant } from '../miniprogram/lib/plant.ts';

// Recorded from the local platform for the web's browser tests (apps/hvac-web/e2e/fixtures).
const fixture = (name: string) => JSON.parse(readFileSync(new URL(`../../hvac-web/e2e/fixtures/${name}`, import.meta.url), 'utf8'));
const assetModel = fixture('asset-model.json');
const snapshots = fixture('snapshots.json');
const now = new Date('2026-10-01T15:30:00Z');

// The same plant on a bad day: the chiller offline, a pump's readings late and partly missing, the meter unreadable.
function badDay() {
  const items = structuredClone(snapshots.items);
  const deviceNamed = (prefix: string) => assetModel.devices.find((device: { displayName: string }) => device.displayName.startsWith(prefix)).id;
  const item = (prefix: string) => items.find((candidate: { deviceId: string }) => candidate.deviceId === deviceNamed(prefix));
  item('CHILLER-01').snapshot.presence.currentState = 'OFFLINE';
  const pump = item('CHWP-01').snapshot;
  pump.values[0] = { ...pump.values[0], freshness: 'STALE' };
  pump.values[1] = { key: pump.values[1].key, state: 'MISSING', missingReason: 'NEVER_OBSERVED' };
  Object.assign(item('METER-HVAC-TOTAL'), { status: 'ERROR', snapshot: undefined, problem: { code: 'TELEMETRY_UNAVAILABLE' } });
  return { ...snapshots, items };
}

for (const [day, recorded] of [['a normal day', snapshots], ['a bad day', badDay()]] as const) test(`the mini program reads the plant exactly as the web does on ${day}: every device, its state and every reading`, () => {
  const snapshots = recorded;
  const byDeviceId = new Map(snapshots.items.map((item: { deviceId: string; status: string; snapshot: unknown; problem: unknown }) => [
    item.deviceId, item.status === 'OK' ? { status: 'ok', snapshot: item.snapshot } : { status: 'error', problem: item.problem },
  ]));
  const web = buildPlantView(buildAssetsRows({ assetModel, snapshots: byDeviceId as never, now }));
  const phone = buildPlant(assetModel, snapshots, now);

  const sourceKeys = (deviceId: string) => assetModel.telemetryPoints
    .filter((point: { reportingDeviceId: string; status: string }) => point.reportingDeviceId === deviceId && point.status === 'ACTIVE')
    .map((point: { sourceKey: string }) => point.sourceKey);
  const summary = (device: { deviceId: string; name: string; category: string; connection: string; runState: string; latestSampleAt: string | null; hasStaleData: boolean; reading: (key: string) => { label: string; display: string; unit: string | null; numeric: number | null; current: boolean } | null }) => ({
    deviceId: device.deviceId, name: device.name, category: device.category, connection: device.connection, runState: device.runState,
    latestSampleAt: device.latestSampleAt, hasStaleData: device.hasStaleData,
    readings: sourceKeys(device.deviceId).map((key: string) => {
      const reading = device.reading(key);
      return reading === null ? [key, null] : [key, reading.label, reading.display, reading.unit, reading.numeric, reading.current];
    }),
  });

  assert.ok(web.devices.length > 0);
  // The web lists devices by room and asset; the phone lists them by role, so compare them device by device.
  const byId = (left: { deviceId: string }, right: { deviceId: string }) => left.deviceId.localeCompare(right.deviceId);
  assert.deepEqual([...phone.devices].sort(byId).map(summary), [...web.devices].sort(byId).map(summary));
  assert.deepEqual(phone.groups.map((group) => [group.category, group.devices.map((device) => device.deviceId)]),
    web.groups.map((group) => [group.category, group.devices.map((device) => device.deviceId)]));
  assert.deepEqual([phone.onlineCount, phone.latestSampleAt, phone.plantCop, phone.totalPower?.display, phone.coolingCapacity?.display],
    [web.onlineCount, web.latestSampleAt, web.plantCop, web.totalPower?.display, web.coolingCapacity?.display]);
});
