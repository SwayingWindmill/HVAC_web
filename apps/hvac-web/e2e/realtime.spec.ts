import { SITE_ID } from './gateway';
import { expect, test } from './test';

let consoleErrors: string[] = [];
let pageErrors: string[] = [];

test.beforeEach(({ page }) => {
  consoleErrors = [];
  pageErrors = [];
  page.on('console', (message) => {
    if (message.type() === 'error') consoleErrors.push(message.text());
  });
  page.on('pageerror', (error) => {
    pageErrors.push(error.message);
  });
});

test.afterEach(() => {
  expect(consoleErrors, 'unexpected console errors').toEqual([]);
  expect(pageErrors, 'unexpected page errors').toEqual([]);
});

test('the plant view shows current plant figures from the Snapshot', async ({ page }) => {
  await page.goto(`/operations/realtime?site=${SITE_ID}`);
  const plant = page.getByRole('region', { name: '冷站运行' });
  await expect(plant.getByRole('tab', { name: /冷站 COP/ })).toHaveAttribute('aria-selected', 'true');
  await expect(plant.getByRole('tab', { name: /冷站 COP/ })).toContainText(/\d\.\d{2}/);
  await expect(page.getByRole('link', { name: /主要设备 4 \/ 4 运行/ })).toBeVisible();
  const loops = page.getByRole('table', { name: '水系统与室内外环境' });
  await expect(loops.getByRole('row', { name: /室内/ })).toContainText(/24\.1\s*°C/);
  // Without a realtime grant the page says it is polling rather than pretending to stream.
  await expect(page.getByText('每 30 秒刷新')).toBeVisible();
  // …and the header says nothing is subscribed instead of reporting a lost connection.
  const status = page.getByTestId('real-realtime-status');
  await expect(status).toHaveAttribute('data-realtime-state', 'idle');
  await expect(status).toHaveText('未订阅');
});

test('choosing a metric keeps it in the URL', async ({ page }) => {
  await page.goto(`/operations/realtime?site=${SITE_ID}`);
  await page.getByRole('region', { name: '冷站运行' }).getByRole('tab', { name: /^冷站总功率/ }).click();
  await expect(page).toHaveURL(/metric=power/);
  await page.getByRole('radio', { name: '24 小时' }).click();
  await expect(page).toHaveURL(/window=24h/);
});

test('the plant machines open from the running count in the equipment ledger', async ({ page }) => {
  await page.goto(`/operations/realtime?site=${SITE_ID}`);
  await page.getByRole('link', { name: /主要设备/ }).click();
  await expect(page).toHaveURL(/\/operations\/systems-devices\?/);
  await expect(page.getByRole('button', { name: /CHWP-01/ })).toBeVisible();
});

test('missing quarter-hour buckets break the realtime trend curve instead of bridging across an outage', async ({ page, gateway }) => {
  gateway.aggregateHandler = (query) => {
    if (query.granularity !== 'QUARTER_HOUR' || !query.keys.includes('active_power')) return undefined;
    const fromMs = Date.parse(query.from);
    const quarterMs = 15 * 60_000;
    const indices = [0, 1, 2, 3, 8, 9, 10, 11];
    const buckets = indices.map((index) => ({
      telemetryKey: 'active_power',
      pointId: '01910000-0000-7000-8000-000000000035',
      pointRevision: 1,
      pointType: 'TELEMETRY',
      unit: 'kW',
      periodStart: new Date(fromMs + index * quarterMs).toISOString(),
      periodEnd: new Date(fromMs + (index + 1) * quarterMs).toISOString(),
      quality: { good: 60, partial: 0, estimated: 0, manual: 0, stale: 0, invalid: 0 },
      completeness: 1,
      gauge: { average: 210 + index, minimum: 200, maximum: 225, first: 210, last: 215, sampleCount: 60 },
    }));
    return {
      body: {
        schemaVersion: 1,
        tenantId: '018f3d00-0000-7000-8000-000000000001',
        siteId: SITE_ID,
        deviceId: query.deviceId,
        buckets,
        metadata: {
          requestedFrom: query.from,
          requestedTo: query.to,
          granularity: 'QUARTER_HOUR',
          timezone: query.timezone,
          qualityPolicy: query.qualityPolicy,
          projectionWatermark: query.to,
          returnedBuckets: buckets.length,
        },
      },
    };
  };

  await page.goto(`/operations/realtime?site=${SITE_ID}&metric=power`);
  const plant = page.getByRole('region', { name: '冷站运行' });
  const curve = plant.locator('path.recharts-area-curve').first();
  await expect(curve).toBeVisible();
  const pathD = (await curve.getAttribute('d')) ?? '';
  expect(pathD.match(/M/g)?.length).toBe(2);
});

test('displays banquet indoor temperature and wireless concentrator telemetry without overflow or console errors', async ({ page, gateway }) => {
  const zoneDeviceId = '018f3e00-4000-7000-8000-000000000008';
  const moduleDeviceId = '018f3e00-4000-7000-8000-000000000020';
  const zonePoint = gateway.assetModel.telemetryPoints.find(
    (point: { reportingDeviceId: string; sourceKey: string }) =>
      point.reportingDeviceId === zoneDeviceId && point.sourceKey === 'zone.temperature',
  );
  const zoneSnapshotItem = gateway.snapshots.items.find(
    (item: { deviceId: string }) => item.deviceId === zoneDeviceId,
  );
  expect(zonePoint).toBeTruthy();
  expect(zoneSnapshotItem).toBeTruthy();

  let nextPointSuffix = 101;
  const clonePoint = (pointCode: string, sourceKey: string, displayName: string, unit: string, reportingDeviceId: string) => ({
    ...zonePoint,
    id: `01910000-0000-7000-8000-10000000${String(nextPointSuffix++).padStart(4, '0')}`,
    reportingDeviceId,
    pointCode,
    sourceKey,
    displayName,
    unit,
  });

  const nextDevices = [
    ...gateway.assetModel.devices,
    {
      ...gateway.assetModel.devices.find((device: { id: string }) => device.id === zoneDeviceId),
      id: moduleDeviceId,
      code: 'temperature-module',
      displayName: '无线测温集中器 (UID 20)',
      deviceType: 'TEMPERATURE_MODULE',
    },
  ];
  const nextPoints = [
    ...gateway.assetModel.telemetryPoints,
    clonePoint('banquet_room_temperature', 'zone.banquet_temperature', '宴会厅室内温度', 'Cel', zoneDeviceId),
    clonePoint('t14', 'acquisition.t14', '冷却水塔水 2（通道 14）', 'Cel', moduleDeviceId),
    clonePoint('t11', 'acquisition.t11', '冷却塔内侧总管（通道 11）', 'Cel', moduleDeviceId),
    clonePoint('t51', 'acquisition.t51', '冷却塔内侧总管（通道 51）', 'Cel', moduleDeviceId),
    clonePoint('t12', 'acquisition.t12', '冷却塔出水（通道 12）', 'Cel', moduleDeviceId),
    clonePoint('t55', 'acquisition.t55', '冷却塔出水（通道 55）', 'Cel', moduleDeviceId),
    clonePoint('v1', 'acquisition.v1', 'A 机组冷冻水出水（通道 1）电池电压', 'mV', moduleDeviceId),
    clonePoint('rssi1', 'acquisition.rssi1', 'A 机组冷冻水出水（通道 1）信号强度', 'dBm', moduleDeviceId),
  ];

  gateway.assetModel = {
    ...gateway.assetModel,
    devices: nextDevices,
    telemetryPoints: nextPoints,
    counts: {
      ...gateway.assetModel.counts,
      deviceEndpoints: nextDevices.length,
      points: nextPoints.length,
    },
  };

  const baseValue = zoneSnapshotItem.snapshot.values[0];
  const makeValue = (key: string, value: number, unit: string) => ({
    ...baseValue,
    key,
    state: 'PRESENT',
    value,
    valueType: 'NUMBER',
    unit,
    freshness: 'FRESH',
    quality: 'GOOD',
  });
  zoneSnapshotItem.snapshot.values.push(makeValue('banquet_room_temperature', 25.4, 'Cel'));
  zoneSnapshotItem.snapshot.values.sort((left: { key: string }, right: { key: string }) => left.key.localeCompare(right.key));
  gateway.snapshots.items.push({
    requestId: `assets-0-0-${moduleDeviceId}`,
    deviceId: moduleDeviceId,
    status: 'OK',
    snapshot: {
      ...zoneSnapshotItem.snapshot,
      deviceId: moduleDeviceId,
      values: [
        makeValue('t14', 33.8, 'Cel'),
        makeValue('t11', 31.2, 'Cel'),
        makeValue('t51', 31.2, 'Cel'),
        makeValue('t12', 30.7, 'Cel'),
        makeValue('t55', 30.7, 'Cel'),
        makeValue('v1', 3550, 'mV'),
        makeValue('rssi1', -62, 'dBm'),
      ].sort((left, right) => left.key.localeCompare(right.key)),
    },
  });

  await page.goto(`/operations/realtime?site=${SITE_ID}`);
  const loops = page.getByRole('table', { name: '水系统与室内外环境' });
  const indoorRow = loops.getByRole('row', { name: /室内/ });
  await expect(indoorRow).toContainText('24.1');
  await expect(indoorRow).toContainText('宴会厅');
  await expect(indoorRow).toContainText('25.4');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  if (process.env.PR1_LANE1_SCREENSHOT) {
    await page.screenshot({ path: process.env.PR1_LANE1_SCREENSHOT, fullPage: true });
  }

  await page.goto(`/operations/systems-devices?site=${SITE_ID}`);
  await page.getByText('无线测温集中器 (UID 20)').first().click();
  const inspector = page.getByRole('dialog');
  await expect(inspector).toContainText('无线测温集中器 (UID 20)');
  await expect(inspector).toContainText('33.8');
  await expect(inspector).toContainText('31.2');
  await expect(inspector).toContainText('30.7');
  await expect(inspector).toContainText('3550');
  await expect(inspector).toContainText('mV');
  await expect(inspector).toContainText('-62');
  await expect(inspector).toContainText('dBm');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);

  if (process.env.PR1_LANE10_SCREENSHOT) {
    await page.screenshot({ path: process.env.PR1_LANE10_SCREENSHOT, fullPage: true });
  }
});
