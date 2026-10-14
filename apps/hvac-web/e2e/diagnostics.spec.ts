import { SITE_ID } from './gateway';
import { expect, test } from './test';

const BTU_METER_DEVICE = '018f3e00-4000-7000-8000-000000000006';
const BTU_METER_ASSET = '01910000-0000-7000-8000-00000000000a';

test('a finding states its measurement against the threshold and opens its causes and actions', async ({ page }) => {
  await page.goto(`/operations/diagnostics?site=${SITE_ID}&range=all`);
  const findings = page.getByRole('list', { name: '诊断发现列表' });
  const item = findings.getByRole('button', { name: /冷冻水低温差/ });
  await expect(item).toHaveCount(1);
  await expect(item).toContainText('BTU-METER-01');
  await expect(item).toContainText('3.4 / 3.5 K');
  await expect(item).toContainText('轻微偏离');

  await item.click();
  await expect(page).toHaveURL(/inspect=/);
  await expect(item).toHaveAttribute('aria-current', 'true');
  const detail = page.getByRole('region', { name: '诊断发现详情' });
  await expect(detail).toContainText('测量值3.38 K');
  await expect(detail).toContainText('阈值3.5 K');
  await expect(detail).toContainText('降低冷冻水泵频率');
  await expect(detail.getByRole('link', { name: '去调整水泵' })).toBeVisible();
});

test('running the diagnosis names the loop, and FDD reports a condition already found today as ongoing under its rule', async ({ page, gateway }) => {
  await page.goto(`/operations/diagnostics?site=${SITE_ID}`);
  const rule = page.getByRole('region', { name: '冷冻水低温差' });
  await expect(rule).toContainText('第 1 版');
  await expect(rule.getByRole('button', { name: /最近 15 分钟温差低于 3.5 K（设计 5 K）时记为问题/ })).toBeVisible();
  await page.getByRole('button', { name: '立即诊断' }).click();

  await expect(rule).toContainText('仍在持续');
  await expect(rule).toContainText('问题仍在持续，24 小时内不重复记录');
  const [run] = gateway.writes.filter((write) => write.path === `/api/v1/sites/${SITE_ID}/fdd/evaluate/low-delta-t`);
  // The page names the loop only; the threshold and window are FDD's rule.
  expect(run.body).toEqual({ deviceId: BTU_METER_DEVICE, assetId: BTU_METER_ASSET });
});

test('a revised rule is what the page shows, and an unreadable rule says so', async ({ page, gateway }) => {
  gateway.fddRule = { ...gateway.fddRule!, revisionId: '01910000-0000-7000-8000-860000000002', revision: 2, minimumDeltaTC: 4, windowMinutes: 30 };
  await page.goto(`/operations/diagnostics?site=${SITE_ID}`);
  const rule = page.getByRole('region', { name: '冷冻水低温差' });
  await expect(rule).toContainText('第 2 版');
  await expect(rule).toContainText('30 分钟窗口');
  await expect(rule.getByRole('button', { name: /最近 30 分钟温差低于 4 K/ })).toBeVisible();

  gateway.fddRule = null;
  await page.reload();
  await expect(rule).toContainText('诊断规则暂不可用');
});
