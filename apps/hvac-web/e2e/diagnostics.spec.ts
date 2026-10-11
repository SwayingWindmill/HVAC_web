import { SITE_ID } from './gateway';
import { expect, test } from './test';

const BTU_METER_DEVICE = '018f3e00-4000-7000-8000-000000000006';
const BTU_METER_ASSET = '01910000-0000-7000-8000-00000000000a';

test('a finding states its measurement against the threshold and opens its causes and actions', async ({ page }) => {
  await page.goto(`/operations/diagnostics?site=${SITE_ID}`);
  const findings = page.getByRole('table', { name: '诊断发现列表' });
  const row = findings.getByRole('row').filter({ hasText: '冷冻水低温差' });
  await expect(row).toHaveCount(1);
  await expect(row).toContainText('BTU-METER-01');
  await expect(row).toContainText('3.4 / 3.5 K');
  await expect(row).toContainText('轻微偏离');

  await row.getByRole('button', { name: '冷冻水低温差' }).click();
  await expect(page).toHaveURL(/inspect=/);
  const sheet = page.getByRole('dialog');
  await expect(sheet).toContainText('测量值3.38 K');
  await expect(sheet).toContainText('阈值3.5 K');
  await expect(sheet).toContainText('降低冷冻水泵频率');
});

test('running the diagnosis checks the last 15 minutes of the loop BTU meter and records what it finds', async ({ page, gateway }) => {
  await page.goto(`/operations/diagnostics?site=${SITE_ID}`);
  await page.getByRole('button', { name: '立即诊断' }).click();

  await expect(page.getByRole('region', { name: '冷冻水低温差' })).toContainText('发现问题');
  await expect(page.getByRole('table', { name: '诊断发现列表' }).getByRole('row').filter({ hasText: '冷冻水低温差' })).toHaveCount(2);
  const [run] = gateway.writes.filter((write) => write.path === `/api/v1/sites/${SITE_ID}/fdd/evaluate/low-delta-t`);
  expect(run.body).toMatchObject({ deviceId: BTU_METER_DEVICE, assetId: BTU_METER_ASSET, ruleRevisionId: 'chilled-water-low-delta-t/v1', minimumDeltaTC: 3.5 });
  expect(Date.parse(run.body!.evaluationTo) - Date.parse(run.body!.evaluationFrom)).toBe(15 * 60_000);
});
