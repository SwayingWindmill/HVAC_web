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

test('running the diagnosis names the loop, and FDD reports a condition already found today as ongoing under its rule', async ({ page, gateway }) => {
  await page.goto(`/operations/diagnostics?site=${SITE_ID}`);
  await expect(page.getByRole('region', { name: '冷冻水低温差' })).toContainText('最近 15 分钟温差低于 3.5 K（设计 5 K）时记为问题');
  await expect(page.getByRole('region', { name: '冷冻水低温差' })).toContainText('按第 1 版规则');
  await page.getByRole('button', { name: '立即诊断' }).click();

  const rule = page.getByRole('region', { name: '冷冻水低温差' });
  await expect(rule).toContainText('仍在持续');
  await expect(rule).toContainText('问题仍在持续，24 小时内不重复记录');
  await expect(page.getByRole('table', { name: '诊断发现列表' }).getByRole('row').filter({ hasText: '冷冻水低温差' })).toHaveCount(1);
  const [run] = gateway.writes.filter((write) => write.path === `/api/v1/sites/${SITE_ID}/fdd/evaluate/low-delta-t`);
  // The page names the loop only; the threshold and window are FDD's rule.
  expect(run.body).toEqual({ deviceId: BTU_METER_DEVICE, assetId: BTU_METER_ASSET });
});

test('a revised rule is what the page shows, and an unreadable rule says so', async ({ page, gateway }) => {
  gateway.fddRule = { ...gateway.fddRule!, revisionId: '01910000-0000-7000-8000-860000000002', revision: 2, minimumDeltaTC: 4, windowMinutes: 30 };
  await page.goto(`/operations/diagnostics?site=${SITE_ID}`);
  await expect(page.getByRole('region', { name: '冷冻水低温差' })).toContainText('最近 30 分钟温差低于 4 K');
  await expect(page.getByRole('region', { name: '诊断概况' })).toContainText('第 2 版 · 阈值 4 K · 窗口 30 分钟');

  gateway.fddRule = null;
  await page.reload();
  await expect(page.getByRole('region', { name: '冷冻水低温差' })).toContainText('诊断规则暂不可用');
});
