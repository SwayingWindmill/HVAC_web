import { principal } from './gateway';
import { expect, test } from './test';

test('asset and point registration opens from the system overview', async ({ page }) => {
  await page.goto('/system');
  await page.getByRole('tab', { name: '资产与点位', exact: true }).click();

  await expect(page).toHaveURL(/tab=registry/);
  await expect(page.getByRole('tabpanel')).toContainText('资源与绑定');
});

test('automation rules open from the system overview', async ({ page }) => {
  await page.goto('/system');
  await page.getByRole('tab', { name: '自动化规则', exact: true }).click();

  await expect(page).toHaveURL(/tab=rules/);
  // The recorded operator does not maintain rules.
  await expect(page.getByRole('tabpanel')).toContainText('当前为只读状态');
});

test('leaving system management with an unsaved registry import asks before dropping it', async ({ page }) => {
  await page.route('**/api/v1/principal', (route) => route.fulfill({ json: {
    ...principal, authorization: { ...principal.authorization, capabilities: [...principal.authorization.capabilities, 'registry.import'] },
  } }));
  await page.goto('/system?tab=registry');
  await page.getByRole('tab', { name: '导入与导出', exact: true }).click();
  const source = page.getByRole('textbox', { name: '登记导入 JSON', exact: true });
  await source.fill('{"draft": true}');
  await expect(page.getByText('有未保存修改')).toBeVisible();

  // Switching the page's tab unmounts the form, so it is guarded like leaving the page.
  await page.getByRole('tab', { name: '系统概览', exact: true }).click();
  const confirmation = page.getByRole('alertdialog');
  await expect(confirmation).toContainText('离开会丢弃未保存内容');
  await expect(confirmation).toContainText('站点与设备登记管理');
  await confirmation.getByRole('button', { name: '继续编辑', exact: true }).click();
  await expect(page).toHaveURL(/tab=registry/);
  await expect(source).toHaveValue('{"draft": true}');

  await page.getByRole('tab', { name: '系统概览', exact: true }).click();
  await page.getByRole('alertdialog').getByRole('button', { name: '丢弃并离开', exact: true }).click();
  await expect(page).not.toHaveURL(/tab=registry/);
  await expect(page.getByRole('region', { name: '系统概况' })).toBeVisible();
});
