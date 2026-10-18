import { SITE_ID, principal } from './gateway';
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

test('opening system management from a Site page leaves the Site scope', async ({ page }) => {
  await page.goto(`/operations/alarms?site=${SITE_ID}`);
  await expect(page.getByTestId('real-shell-site')).toHaveText('中央机房');

  await page.getByRole('button', { name: new RegExp(principal.principal.displayName) }).click();
  await page.getByRole('menuitem', { name: '系统管理', exact: true }).click();
  await expect(page).toHaveURL(/\/system$/);
  await expect(page.getByTestId('real-shell-site')).toHaveText('平台范围');
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

  // Away from its pages the 运维 group is folded; open it to reach 告警.
  await page.getByRole('button', { name: '运维，展开', exact: true }).click();
  await page.getByRole('link', { name: '告警', exact: true }).click();
  await expect(page.getByRole('alertdialog')).toContainText('站点与设备登记管理');
  await page.getByRole('alertdialog').getByRole('button', { name: '继续编辑', exact: true }).click();
  await expect(page).toHaveURL(/\/system\?tab=registry/);

  await page.getByRole('tab', { name: '系统概览', exact: true }).click();
  await page.getByRole('alertdialog').getByRole('button', { name: '丢弃并离开', exact: true }).click();
  await expect(page).not.toHaveURL(/tab=registry/);
  await expect(page.getByRole('region', { name: '系统概况' })).toBeVisible();
});

// #597: the operations lead reads, per day, how the assistant's answers went and which steps
// failed that day, every step named as the panel names it.
test('the AI 助手 tab shows each day of answers and that day\'s failed steps in plain words', async ({ page }) => {
  await page.route('**/api/v1/principal', (route) => route.fulfill({ json: {
    ...principal,
    authorization: { ...principal.authorization, capabilities: [...principal.authorization.capabilities, 'audit.read'] },
  } }));
  await page.goto('/system?tab=assistant');
  const usage = page.getByRole('tabpanel');
  await expect(usage).toContainText('最近 14 天回答 50 次，其中 3 次没答完');

  const days = usage.getByRole('table', { name: '每天的回答' });
  await expect(days.getByRole('row', { name: /10月18日/ })).toContainText('出错 1');
  await expect(days.getByRole('row', { name: /10月17日/ })).toContainText('被停下 1');
  const steps = usage.getByRole('table', { name: '各个步骤' });
  await expect(steps).toContainText('写结论');
  await days.getByRole('button', { name: '10月16日' }).click();
  await expect(usage).toContainText('10月16日的步骤');
  await expect(steps.getByRole('row', { name: /读取当前读数/ })).toHaveText(/读取当前读数\s*16\s*2/);
  await expect(steps.getByRole('row', { name: /读取告警/ })).toHaveText(/读取告警\s*8\s*0/);
  await days.getByRole('button', { name: '10月18日' }).click();
  await expect(steps.getByRole('row', { name: /读取告警/ })).toHaveText(/读取告警\s*7\s*1/);
  await expect(usage).not.toContainText(/alarm_list|telemetry_get_current|investigation_complete/);
});

test('the AI 助手 tab says so when the account cannot read records', async ({ page }) => {
  await page.goto('/system?tab=assistant');
  await expect(page.getByRole('tabpanel')).toContainText('当前账号没有查看操作记录的权限');
});
