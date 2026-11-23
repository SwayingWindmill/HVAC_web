import { SITE_ID, principal } from './gateway';
import { expect, test } from './test';

const UUID_RE = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/i;
const RAW_ENUM_RE = /\b(?:TEMPLATE_ROLLOUT|PRESERVED_SITE_OVERRIDE|CLOUD_AND_EDGE|APPLIED|SKIPPED)\b/;

test('alarm rule baseline templates rollout preserves overrides and single-site reset works at 1440x900 light and dark', async ({ page, gateway }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const consoleErrors: string[] = [];
  const pageErrors: string[] = [];
  page.on('console', (msg) => {
    if (msg.type() === 'error') consoleErrors.push(msg.text());
  });
  page.on('pageerror', (err) => pageErrors.push(err.message));

  gateway.principalOverride = {
    ...principal,
    authorization: {
      ...principal.authorization,
      capabilities: [...new Set([...principal.authorization.capabilities, 'schedule.manage'])],
    },
  };

  await page.goto(`/system?tab=governance&section=alarm-templates&site=${SITE_ID}`);
  await expect(page.getByText('跨站点告警规则基准模板库')).toBeVisible();
  await expect(page.getByRole('heading', { name: '冷机供水温度骤升基准模板' })).toBeVisible();

  await expect.poll(() => page.evaluate(() => getComputedStyle(document.documentElement).colorScheme)).toBe('light');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  const initialBody = await page.locator('body').innerText();
  expect(initialBody).not.toMatch(UUID_RE);
  expect(initialBody).not.toMatch(RAW_ENUM_RE);

  // Create a brand new template and publish v1
  await page.getByRole('button', { name: '新建基准模板' }).click();
  const createDialog = page.getByRole('dialog');
  await createDialog.getByLabel('模板唯一编码').fill('chw-supply-high');
  await createDialog.getByLabel('模板展示名称').fill('冷冻水供水温度超高基准');
  await createDialog.getByRole('button', { name: '创建并发布 v1' }).click();
  await expect(page.getByRole('heading', { name: '冷冻水供水温度超高基准' })).toBeVisible();

  // Select pre-seeded chiller-slope-rise template and publish v2 with CRITICAL + CLOUD_AND_EDGE
  await page.getByRole('button', { name: /冷机供水温度骤升基准模板/ }).click();
  await page.getByRole('button', { name: '发布新版本' }).click();
  const releaseDialog = page.getByRole('dialog');
  await releaseDialog.getByRole('combobox', { name: '默认严重程度' }).click();
  await page.getByRole('option', { name: '紧急' }).click();
  await releaseDialog.getByRole('combobox', { name: '默认执行位置' }).click();
  await page.getByRole('option', { name: '云边协同' }).click();
  await releaseDialog.getByLabel('版本变更说明').fill('冷机骤升规则升级至紧急并开启云边协同');
  await releaseDialog.getByRole('button', { name: '发布新基准版本' }).click();
  await expect(page.getByLabel('模板发布版本列表')).toContainText('v2 · 云边协同');

  // Open diff preview & rollout dialog -> verify preserved override conflict highlight -> execute rollout
  const slopeItem = page.getByRole('listitem').filter({ hasText: 'chiller-slope-rise' });
  await slopeItem.getByRole('button', { name: '差异预检与下发' }).click();
  const rolloutDialog = page.getByRole('dialog');
  await expect(rolloutDialog).toContainText('自动保留站点覆写 1 处');
  await expect(rolloutDialog).toContainText('冲突高亮 1 项');
  await expect(rolloutDialog).toContainText('冲突字段「触发条件」：站点覆写 2 °C / 5 分钟 vs 模板基线 3 °C / 5 分钟');
  await expect(rolloutDialog).toContainText('保留站点值');
  await rolloutDialog.getByRole('button', { name: '确认受控下发' }).click();
  await expect(rolloutDialog).toContainText('下发批次已完成：已同步 1 个站点（保留 1 处覆写）');
  await rolloutDialog.getByRole('button', { name: '关闭' }).first().click();

  // Switch to Dark mode and verify single-site rule kept override while taking new severity, then reset to template defaults
  await page.getByRole('radiogroup', { name: '外观' }).getByRole('radio', { name: '深色' }).click();
  await expect.poll(() => page.evaluate(() => getComputedStyle(document.documentElement).colorScheme)).toBe('dark');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);

  await page.goto(`/operations/alarms?site=${SITE_ID}&tab=rules`);
  await expect(page.getByRole('table', { name: '正在生效的规则' })).toContainText('1 号冷机供水温度骤升');
  const slopeRuleRow = page.getByRole('table', { name: '正在生效的规则' })
    .getByRole('row')
    .filter({ has: page.getByRole('cell', { name: '1 号冷机供水温度骤升' }) });
  await expect(slopeRuleRow).toContainText('紧急');
  await expect(slopeRuleRow).toContainText('在 5 分钟内升高达到 2 °C');
  await expect(slopeRuleRow).toContainText('云边协同');

  await page.getByRole('button', { name: '模板偏离核对' }).click();
  const driftSheet = page.getByRole('dialog');
  await expect(driftSheet).toContainText('1 号冷机供水温度骤升');
  await expect(driftSheet).toContainText('触发条件');
  await expect(driftSheet).toContainText('2 °C / 5 分钟');
  await expect(driftSheet).toContainText('3 °C / 5 分钟');
  await driftSheet.getByRole('button', { name: '重置为模板默认值' }).click();
  await expect(driftSheet).toContainText('当前站点模板派生规则均与所属基准模板保持一致，无偏离项');
  await page.keyboard.press('Escape');

  await expect(slopeRuleRow).toContainText('在 5 分钟内升高达到 3 °C');
  expect(consoleErrors).toEqual([]);
  expect(pageErrors).toEqual([]);
});

test('read-only principal cannot rollout templates or reset site rule drift (403 FORBIDDEN)', async ({ page, gateway }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const consoleErrors: string[] = [];
  const pageErrors: string[] = [];
  page.on('console', (msg) => {
    if (msg.type() === 'error') consoleErrors.push(msg.text());
  });
  page.on('pageerror', (err) => pageErrors.push(err.message));

  const readOnlyCaps = (principal.authorization.capabilities as string[]).filter((c) => c !== 'schedule.manage');
  gateway.principalOverride = {
    ...principal,
    authorization: {
      ...principal.authorization,
      capabilities: [...new Set([...readOnlyCaps, 'audit.read'])],
    },
  };

  await page.goto(`/system?tab=governance&section=alarm-templates&site=${SITE_ID}`);
  await expect(page.getByText('跨站点告警规则基准模板库')).toBeVisible();
  await expect(page.getByRole('button', { name: '新建基准模板' })).toBeDisabled();
  await expect(page.getByRole('button', { name: '发布新版本' })).toBeDisabled();

  await page.goto(`/operations/alarms?site=${SITE_ID}&tab=rules`);
  await page.getByRole('button', { name: '模板偏离核对' }).click();
  await expect(page.getByRole('button', { name: '重置为模板默认值' })).toBeDisabled();
  await page.keyboard.press('Escape');

  // Assert zero unexpected console errors BEFORE intentional 403 RBAC checks
  expect(consoleErrors).toEqual([]);
  expect(pageErrors).toEqual([]);

  const csrfToken = String(principal.session.csrfToken);
  const rolloutForbidden = await page.evaluate(async ({ token, siteId }) => {
    const res = await fetch('/api/v1/alarm-rule-templates/01930000-0000-7000-8000-000000000001/rollout', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': token },
      body: JSON.stringify({ version: 1, siteIds: [siteId] }),
    });
    return { status: res.status, body: await res.json() };
  }, { token: csrfToken, siteId: SITE_ID });
  expect(rolloutForbidden.status).toBe(403);
  expect(rolloutForbidden.body.code).toBe('FORBIDDEN');

  const resetForbidden = await page.evaluate(async ({ token, siteId }) => {
    const res = await fetch(`/api/v1/alarm-rules/01910000-b100-7000-8000-00000000000d/reset-template?siteId=${siteId}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': token },
      body: JSON.stringify({}),
    });
    return { status: res.status, body: await res.json() };
  }, { token: csrfToken, siteId: SITE_ID });
  expect(resetForbidden.status).toBe(403);
  expect(resetForbidden.body.code).toBe('FORBIDDEN');
});
