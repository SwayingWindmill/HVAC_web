import { SITE_ID } from './gateway';
import { expect, test } from './test';

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
