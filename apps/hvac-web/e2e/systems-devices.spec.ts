import { SITE_ID } from './gateway';
import { expect, test } from './test';

test('a workspace opened without a Site carries the default Site in the URL', async ({ page }) => {
  await page.goto('/operations/systems-devices');
  await expect(page).toHaveURL(new RegExp(`[?&]site=${SITE_ID}`));
  await expect(page.getByRole('heading', { name: '设备', exact: true })).toBeVisible();
});

test('the device wall lists registered equipment and counts only observed points', async ({ page }) => {
  await page.goto(`/operations/systems-devices?site=${SITE_ID}`);
  const chiller = page.getByRole('button', { name: /CHILLER-01/ });
  await expect(chiller).toContainText('运行');
  // Command points are written, never observed; they must not count as missing data.
  await expect(chiller).toContainText('点位 11/11');
  await expect(page.getByRole('button', { name: /WEATHER-STATION-01/ })).toContainText('室外环境');
});

test('the table view keeps the ledger columns', async ({ page }) => {
  await page.goto(`/operations/systems-devices?site=${SITE_ID}`);
  await page.getByRole('radio', { name: '表格' }).click();
  await expect(page).toHaveURL(/layout=table/);
  await expect(page.getByRole('row', { name: /CHILLER-01/ })).toContainText('11 / 11');
});

test('the device inspector shows current readings and the 24-hour trend', async ({ page }) => {
  await page.goto(`/operations/systems-devices?site=${SITE_ID}`);
  await page.getByRole('button', { name: /CHILLER-01/ }).click();
  await expect(page).toHaveURL(/inspect=018f3e00-4000-7000-8000-000000000001/);

  const inspector = page.getByRole('dialog');
  await expect(inspector.getByRole('heading', { name: 'CHILLER-01' })).toBeVisible();
  await expect(inspector).toContainText('运行状态');
  await expect(inspector).not.toContainText('RUNNING');
  await expect(inspector).not.toContainText('启动冷水机');
  await expect(inspector.locator('.recharts-surface')).toBeVisible();
});

test('a work order created from a device names the device as its origin', async ({ page, gateway }) => {
  await page.goto(`/operations/systems-devices?site=${SITE_ID}&inspect=018f3e00-4000-7000-8000-000000000001`);
  const inspector = page.getByRole('dialog');
  await inspector.getByRole('button', { name: '创建工单' }).click();
  await page.getByLabel('标题').fill('CHILLER-01：冷凝器清洗');
  await page.getByLabel('说明').fill('冷凝器逼近温度偏高，安排清洗');
  await page.getByRole('dialog', { name: /创建工单/ }).getByRole('button', { name: '创建工单' }).click();

  await expect(inspector).toContainText('CHILLER-01：冷凝器清洗');
  const create = gateway.writes.find((write) => write.method === 'POST' && write.path.endsWith('/work-orders'));
  expect(create?.csrfToken).toBe('e2e-csrf-token');
  expect(create?.body?.sourceReferences).toEqual([{ domain: 'ASSET', resourceId: expect.any(String), relationship: 'ORIGIN' }]);
});

// #596: 解释 hands the assistant a question about the device in view.
test('解释 opens the assistant with a question about the device in view', async ({ page }) => {
  await page.goto(`/operations/systems-devices?site=${SITE_ID}&inspect=018f3e00-4000-7000-8000-000000000001`);
  await page.getByRole('dialog').getByRole('button', { name: '解释' }).click();
  const panel = page.getByRole('complementary', { name: 'AI 助手' });
  await expect(panel.getByRole('textbox', { name: '向助手提问' })).toHaveValue('「CHILLER-01」最近 24 小时运行得怎么样？现在的读数有没有异常？');
});

test('missing hourly buckets break the device inspector 24h curve instead of bridging across an outage', async ({ page, gateway }) => {
  gateway.aggregateHandler = (query) => {
    if (query.granularity !== 'HOUR' || query.deviceId !== '018f3e00-4000-7000-8000-000000000001') return undefined;
    const fromMs = Date.parse(query.from);
    const hourMs = 3_600_000;
    const key = query.keys[0];
    const indices = [0, 1, 2, 3, 8, 9, 10, 11];
    const buckets = indices.map((index) => ({
      telemetryKey: key,
      pointId: '01910000-0000-7000-8000-00000000001b',
      pointRevision: 1,
      pointType: 'TELEMETRY',
      unit: '°C',
      periodStart: new Date(fromMs + index * hourMs).toISOString(),
      periodEnd: new Date(fromMs + (index + 1) * hourMs).toISOString(),
      quality: { good: 60, partial: 0, estimated: 0, manual: 0, stale: 0, invalid: 0 },
      completeness: 1,
      gauge: { average: 7.0 + index * 0.1, minimum: 6.8, maximum: 7.6, first: 7.0, last: 7.1, sampleCount: 60 },
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
          granularity: 'HOUR',
          timezone: query.timezone,
          qualityPolicy: query.qualityPolicy,
          projectionWatermark: query.to,
          returnedBuckets: buckets.length,
        },
      },
    };
  };

  await page.goto(`/operations/systems-devices?site=${SITE_ID}&inspect=018f3e00-4000-7000-8000-000000000001`);
  const inspector = page.getByRole('dialog');
  const curve = inspector.locator('path.recharts-line-curve').first();
  await expect(curve).toBeVisible();
  const pathD = (await curve.getAttribute('d')) ?? '';
  expect(pathD.match(/M/g)?.length).toBe(2);
});
