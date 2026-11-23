import { SITE_ID } from './gateway';
import { expect, test } from './test';

let consoleErrors: string[] = [];
let pageErrors: string[] = [];

test.beforeEach(({ page }) => {
  consoleErrors = [];
  pageErrors = [];
  page.on('console', (message) => {
    if (message.type() === 'error') {
      consoleErrors.push(message.text());
    }
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

test('displays hydraulic loops and parallel branch equipment without horizontal overflow at 1440x900', async ({ page, gateway }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const nowIso = new Date().toISOString();
  const baseAsset = gateway.assetModel.assets.find((asset: { assetType: string }) => asset.assetType === 'CHILLER');
  const chwpAsset = gateway.assetModel.assets.find((asset: { assetType: string }) => asset.assetType === 'CHILLED_WATER_PUMP');
  const cwpAsset = gateway.assetModel.assets.find((asset: { assetType: string }) => asset.assetType === 'COOLING_WATER_PUMP');
  const baseDevice = gateway.assetModel.devices.find((device: { deviceType: string }) => device.deviceType === 'PUMP_CONTROLLER');
  const basePoint = gateway.assetModel.telemetryPoints[0];
  const baseSnap = gateway.snapshots.items.find((item: { status: string }) => item.status === 'OK')?.snapshot;
  expect(baseAsset && chwpAsset && cwpAsset && baseDevice && basePoint && baseSnap).toBeTruthy();

  const valveAssetId = '018f3e00-3000-7000-8000-000000000091';
  const valveDeviceId = '018f3e00-4000-7000-8000-000000000091';
  const dpAssetId = '018f3e00-3000-7000-8000-000000000092';
  const dpDeviceId = '018f3e00-4000-7000-8000-000000000092';

  const nextAssets = gateway.assetModel.assets.map((asset: { id: string }) => {
    if (asset.id === baseAsset.id || asset.id === chwpAsset.id) return { ...asset, waterLoop: 'CHILLED_WATER', branchCode: 'CH-A' };
    if (asset.id === cwpAsset.id) return { ...asset, waterLoop: 'CONDENSER_WATER', branchCode: 'CW-A' };
    return asset;
  });
  nextAssets.push(
    { ...baseAsset, id: valveAssetId, code: 'mv-chw-a', displayName: 'CH-A 电动隔离阀', assetType: 'MOTORIZED_VALVE', waterLoop: 'CHILLED_WATER', branchCode: 'CH-A' },
    { ...baseAsset, id: dpAssetId, code: 'chw-dp-01', displayName: '冷冻总管压差传感器', assetType: 'DIFFERENTIAL_PRESSURE_SENSOR', waterLoop: 'CHILLED_WATER', branchCode: null },
  );

  const nextDevices = [
    ...gateway.assetModel.devices,
    { ...baseDevice, id: valveDeviceId, code: 'mv-chw-a', displayName: 'CH-A 电动隔离阀', deviceType: 'MOTORIZED_VALVE' },
    { ...baseDevice, id: dpDeviceId, code: 'chw-dp-01', displayName: '冷冻总管压差传感器', deviceType: 'DIFFERENTIAL_PRESSURE_SENSOR' },
  ];
  const nextPoints = [
    ...gateway.assetModel.telemetryPoints,
    { ...basePoint, id: '018f3e00-5000-7000-8000-000000000091', reportingDeviceId: valveDeviceId, pointCode: 'valve_open_state', sourceKey: 'valve.open_state', displayName: '阀门状态', pointType: 'STATE', valueType: 'STRING', unit: null, writable: false, status: 'ACTIVE' },
    { ...basePoint, id: '018f3e00-5000-7000-8000-000000000092', reportingDeviceId: valveDeviceId, pointCode: 'valve_position', sourceKey: 'valve.position_feedback', displayName: '阀门开度反馈', pointType: 'TELEMETRY', valueType: 'NUMBER', unit: '%', writable: false, status: 'ACTIVE' },
    { ...basePoint, id: '018f3e00-5000-7000-8000-000000000093', reportingDeviceId: dpDeviceId, pointCode: 'header_dp', sourceKey: 'dp_sensor.differential_pressure', displayName: '总管压差', pointType: 'TELEMETRY', valueType: 'NUMBER', unit: 'kPa', writable: false, status: 'ACTIVE' },
  ];
  const nextRelationships = [
    ...gateway.assetModel.relationships,
    { id: '018f3e00-6400-7000-8000-000000000091', tenantId: baseAsset.tenantId, siteId: baseAsset.siteId, fromType: 'DEVICE', fromId: valveDeviceId, toType: 'ASSET', toId: valveAssetId, role: 'PRIMARY_CONTROLLER', status: 'ACTIVE', validFrom: nowIso, validTo: null, revision: 1, createdAt: nowIso, updatedAt: nowIso },
    { id: '018f3e00-6400-7000-8000-000000000092', tenantId: baseAsset.tenantId, siteId: baseAsset.siteId, fromType: 'DEVICE', fromId: dpDeviceId, toType: 'ASSET', toId: dpAssetId, role: 'MEASURES', status: 'ACTIVE', validFrom: nowIso, validTo: null, revision: 1, createdAt: nowIso, updatedAt: nowIso },
    { id: '018f3e00-6600-7000-8000-000000000093', tenantId: baseAsset.tenantId, siteId: baseAsset.siteId, fromType: 'ASSET', fromId: valveAssetId, toType: 'ASSET', toId: baseAsset.id, role: 'ISOLATES', status: 'ACTIVE', validFrom: nowIso, validTo: null, revision: 1, createdAt: nowIso, updatedAt: nowIso },
  ];

  gateway.assetModel = {
    ...gateway.assetModel,
    assets: nextAssets,
    devices: nextDevices,
    telemetryPoints: nextPoints,
    relationships: nextRelationships,
    counts: { ...gateway.assetModel.counts, assets: nextAssets.length, deviceEndpoints: nextDevices.length, points: nextPoints.length },
  };
  const sampleValue = baseSnap.values[0];
  gateway.snapshots.items.push(
    {
      requestId: 'e2e-valve',
      deviceId: valveDeviceId,
      status: 'OK',
      snapshot: {
        ...baseSnap,
        deviceId: valveDeviceId,
        values: [
          { ...sampleValue, key: 'valve_open_state', value: 'OPEN', valueType: 'STRING', unit: null },
          { ...sampleValue, key: 'valve_position', value: 100, valueType: 'NUMBER', unit: '%' },
        ],
      },
    },
    {
      requestId: 'e2e-dp',
      deviceId: dpDeviceId,
      status: 'OK',
      snapshot: {
        ...baseSnap,
        deviceId: dpDeviceId,
        values: [{ ...sampleValue, key: 'header_dp', value: 86.4, valueType: 'NUMBER', unit: 'kPa' }],
      },
    },
  );

  await page.goto(`/operations/realtime?site=${SITE_ID}`);
  const loops = page.getByRole('table', { name: '水系统与室内外环境' });
  await expect(loops.getByRole('row', { name: /冷冻水/ })).toContainText('1 条并联支路');
  await expect(loops.getByRole('row', { name: /冷冻水/ })).toContainText('86.4');

  const branches = page.getByRole('region', { name: '水力并联支路' });
  await expect(branches).toBeVisible();
  await expect(branches).toContainText('支路 CH-A');
  await expect(branches).toContainText('冷冻水环路');
  await expect(branches).toContainText('CH-A 电动隔离阀');
  await expect(branches).toContainText('全开');
  await expect(branches).toContainText('支路 CW-A');
  await expect(branches).toContainText('冷却水环路');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
});

test('delayed site discovery waits before mounting router without throwing SITE_DISCOVERY_UNAVAILABLE', async ({ page }) => {
  await page.route('**/api/v1/sites*', async (route) => {
    await new Promise<void>((resolveDelay) => {
      setTimeout(resolveDelay, 250);
    });
    await route.fallback();
  });

  await page.goto(`/operations/realtime?site=${SITE_ID}`);
  await expect(page.getByRole('region', { name: '冷站运行' })).toBeVisible();
  await expect(page.getByRole('link', { name: /主要设备/ })).toBeVisible();
});

test('renders 2D hydraulic schematic with live loop readings, inspector sheet, and zero-flow anomaly cue', async ({ page, gateway }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(`/operations/realtime?site=${SITE_ID}`);

  const schematic = page.getByRole('region', { name: '冷站水力回路' });
  await expect(schematic).toBeVisible();
  await expect(schematic).toContainText('CT-01');
  await expect(schematic).toContainText('CWP-01');
  await expect(schematic).toContainText('CHILLER-01');
  await expect(schematic).toContainText('CHWP-01');
  await expect(schematic).toContainText('ZONE-SENSOR-01');
  await expect(schematic).toContainText(/供水\s*7\s*°C/);
  await expect(schematic).toContainText('12.6');
  await expect(schematic).toContainText('132');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);

  await schematic.getByRole('button', { name: /CHILLER-01/ }).click();
  await expect(page).toHaveURL(/inspect=018f3e00-4000-7000-8000-000000000001/);
  const inspector = page.getByRole('dialog');
  await expect(inspector).toContainText('CHILLER-01');
  await inspector.getByRole('button', { name: 'Close' }).click();
  await expect(inspector).toHaveCount(0);
  await expect(page).not.toHaveURL(/inspect=/);

  await page.setViewportSize({ width: 1280, height: 900 });
  await expect(schematic).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);

  const btuSnap = gateway.snapshots.items.find((item: { deviceId: string }) => item.deviceId === '018f3e00-4000-7000-8000-000000000006');
  expect(btuSnap).toBeTruthy();
  const flowPoint = btuSnap.snapshot.values.find((entry: { key: string }) => entry.key === 'flow_rate');
  expect(flowPoint).toBeTruthy();
  flowPoint.value = 0;

  await page.reload();
  await expect(page.getByRole('region', { name: '冷站水力回路' })).toContainText('冷冻水泵运行但流量接近零');
});

test('hovering the realtime trend chart formats numeric timestamps without crashing the page', async ({ page, gateway }) => {
  gateway.aggregateHandler = (query) => {
    if (query.granularity !== 'QUARTER_HOUR' || !query.keys.includes('active_power')) return undefined;
    const fromMs = Date.parse(query.from);
    const quarterMs = 15 * 60_000;
    const buckets = Array.from({ length: 24 }, (_, index) => ({
      telemetryKey: 'active_power',
      pointId: '01910000-0000-7000-8000-000000000035',
      pointRevision: 1,
      pointType: 'TELEMETRY',
      unit: 'kW',
      periodStart: new Date(fromMs + index * quarterMs).toISOString(),
      periodEnd: new Date(fromMs + (index + 1) * quarterMs).toISOString(),
      quality: { good: 60, partial: 0, estimated: 0, stale: 0, invalid: 0 },
      completeness: 1,
      gauge: { average: 210 + index, minimum: 200, maximum: 240, first: 210, last: 215, sampleCount: 60 },
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
  const chart = plant.locator('[data-slot="composed-chart"]');
  await expect(chart.locator('path.recharts-area-curve').first()).toBeVisible();

  const surface = chart.locator('.recharts-wrapper');
  const box = await surface.boundingBox();
  expect(box).toBeTruthy();
  const tooltip = chart.locator('.recharts-tooltip-wrapper');
  for (const ratio of [0.25, 0.5, 0.75]) {
    await page.mouse.move(box!.x + box!.width * ratio, box!.y + box!.height * 0.5, { steps: 3 });
  }
  await expect(tooltip).toBeVisible();
  await expect(tooltip).toContainText(/\b\d{2}:\d{2}\b/);
  await expect(tooltip).toContainText('冷站总功率');
  await expect(page.getByText('页面暂时无法显示')).toHaveCount(0);
});
