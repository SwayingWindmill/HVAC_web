import { SITE_ID, principal } from './gateway';
import { expect, test } from './test';

const UUID_RE = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/i;
const RAW_ENUM_RE = /\b(?:EDGE_CONTROL|EDGE_FDD|EDGE_APPLY_FAILED|ROLLED_BACK)\b/;

test('edge AI models and hyperparams lifecycle works cleanly at 1440x900 light and dark', async ({ page, gateway }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  gateway.principalOverride = {
    ...principal,
    authorization: {
      ...principal.authorization,
      capabilities: [...new Set([...principal.authorization.capabilities, 'schedule.manage', 'device.write'])],
    },
  };

  await page.goto(`/system?tab=governance&section=edge-models&site=${SITE_ID}`);
  await expect(page.getByText('端侧 AI 模型与控制超参托管')).toBeVisible();
  await expect(page.getByText('冷机 COP 基准参数集')).toBeVisible();

  // Check zero horizontal overflow & clean labels in Light mode
  await expect.poll(() => page.evaluate(() => getComputedStyle(document.documentElement).colorScheme)).toBe('light');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  const bodyText = await page.locator('body').innerText();
  expect(bodyText).not.toMatch(UUID_RE);
  expect(bodyText).not.toMatch(RAW_ENUM_RE);

  // Switch to Dark mode and verify layout & contrast
  await page.getByRole('radiogroup', { name: '外观' }).getByRole('radio', { name: '深色' }).click();
  await expect.poll(() => page.evaluate(() => getComputedStyle(document.documentElement).colorScheme)).toBe('dark');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);

  // Switch back to Light mode and test full register -> physical guardrail reject -> register valid -> approve -> deploy -> rollback
  await page.getByRole('radiogroup', { name: '外观' }).getByRole('radio', { name: '浅色' }).click();
  await expect.poll(() => page.evaluate(() => getComputedStyle(document.documentElement).colorScheme)).toBe('light');

  await page.getByRole('button', { name: '登记候选版本' }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('供水温度下限 (°C)').fill('2.0');
  await dialog.getByRole('button', { name: '保存并计算校验摘要' }).click();
  await expect(dialog.getByRole('alert')).toContainText('超参越出设备安全物理边界');

  await dialog.getByLabel('供水温度下限 (°C)').fill('7.2');
  await dialog.getByLabel('版本说明').fill('冷机 COP 夏季调优候选');
  await dialog.getByRole('button', { name: '保存并计算校验摘要' }).click();
  await expect(page.getByText('冷机 COP 夏季调优候选')).toBeVisible();

  // Structured diff renders new value cleanly
  await expect(page.getByRole('table', { name: '超参差异明细' })).toContainText('7.2 °C');

  // Approve candidate -> deploy to PRODUCTION -> one-click rollback restores 1.0.0
  const candidateRow = page.getByRole('row', { name: /冷机 COP 夏季调优候选/ });
  await candidateRow.getByRole('button', { name: '审批', exact: true }).click();
  await page.getByRole('dialog').getByRole('button', { name: '确认提交审批' }).click();
  await expect(candidateRow).toContainText('已审批');

  await candidateRow.getByRole('button', { name: '下发', exact: true }).click();
  await page.getByRole('dialog').getByLabel('运行模式').click();
  await page.getByRole('option', { name: /正式运行/ }).click();
  const deployDialog = page.getByRole('dialog');
  await expect(deployDialog.getByRole('button', { name: '确认下发' })).toBeDisabled();
  await deployDialog.getByRole('checkbox', { name: '我已确认影子验证结果，确认切换为正式运行版本' }).check();
  await deployDialog.getByRole('button', { name: '确认下发' }).click();
  await expect(page.getByRole('table', { name: '端侧部署记录' })).toContainText('1.2.0');

  const activeDeployRow = page.getByRole('row', { name: /chiller-cop-opt .*1\.2\.0.*正式运行/ });
  await activeDeployRow.getByRole('button', { name: '一键回退' }).click();
  await page.getByRole('dialog').getByRole('button', { name: '立即执行回退' }).click();
  await expect(page.getByText(/最近回退审计：.*回退至 1\.0\.0/)).toBeVisible();
});
