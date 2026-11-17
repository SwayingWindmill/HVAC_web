import { readFileSync } from 'node:fs';
import { SITE_ID } from './gateway';
import { expect, test } from './test';

const principalFixture = JSON.parse(
  readFileSync(new URL('./fixtures/principal.json', import.meta.url), 'utf8'),
);

const ACTIVE_ALARM = '01a0ec45-7ab5-7e9c-aa96-927ee5e050f2';

const rows = (page: import('@playwright/test').Page) => page.getByRole('list', { name: '告警记录' }).getByRole('button');

test('the default view lists active alarms only', async ({ page }) => {
  await page.goto(`/operations/alarms?site=${SITE_ID}`);
  await expect(rows(page)).toHaveCount(1);
  await expect(rows(page).first()).toContainText('冷冻水回水温度过低');
  await expect(rows(page).first()).toContainText('未确认');
  // The first alarm opens beside the list without a click.
  await expect(page.getByRole('region', { name: '告警详情' })).toContainText('活动 · 未确认');

  await page.getByRole('radio', { name: '已恢复' }).click();
  await expect(page).toHaveURL(/view=cleared/);
  await expect(rows(page).first()).toContainText('已恢复');
});

test('acknowledging records the note and leaves the alarm active', async ({ page, gateway }) => {
  await page.goto(`/operations/alarms?site=${SITE_ID}&inspect=${ACTIVE_ALARM}`);
  const detail = page.getByRole('region', { name: '告警详情' });
  await detail.getByRole('button', { name: '确认告警' }).click();
  await page.getByRole('textbox', { name: '确认说明' }).fill('已到现场查看');
  await page.getByRole('button', { name: '确认', exact: true }).click();

  await expect(detail).toContainText('活动 · 已确认');
  await expect(detail).toContainText('已到现场查看');
  await expect(rows(page).first()).toContainText('已确认');
  const ack = gateway.writes.find((write) => write.path === `/api/v1/alarms/${ACTIVE_ALARM}/ack`);
  expect(ack?.body).toEqual({ comment: '已到现场查看' });
  expect(ack?.csrfToken).toBe('e2e-csrf-token');
});

test('a work order created from an alarm references it as its origin', async ({ page, gateway }) => {
  await page.goto(`/operations/alarms?site=${SITE_ID}&inspect=${ACTIVE_ALARM}`);
  const detail = page.getByRole('region', { name: '告警详情' });
  await detail.getByRole('button', { name: '创建工单', exact: true }).click();
  await page.getByLabel('说明').fill('检查冷冻水泵频率与旁通阀');
  await page.getByRole('dialog', { name: '由告警创建工单' }).getByRole('button', { name: '创建工单' }).click();

  await expect(detail.getByRole('link', { name: /冷冻水回水温度过低/ })).toBeVisible();
  const create = gateway.writes.find((write) => write.method === 'POST' && write.path.endsWith('/work-orders'));
  expect(create?.body).toMatchObject({
    priority: 'HIGH',
    sourceReferences: [{ domain: 'ALARM', resourceId: ACTIVE_ALARM, relationship: 'ORIGIN' }],
  });
});

test('search and severity filters narrow the list and say when nothing matches', async ({ page }) => {
  await page.goto(`/operations/alarms?site=${SITE_ID}&view=all`);
  await expect(rows(page)).toHaveCount(2);

  await page.getByRole('textbox', { name: '搜索告警' }).fill('不存在的告警');
  await expect(page).toHaveURL(/q=/);
  await expect(page.getByText('没有符合条件的告警')).toBeVisible();

  await page.getByRole('button', { name: '清除筛选' }).click();
  await expect(rows(page)).toHaveCount(2);

  await page.getByRole('toolbar').getByRole('button', { name: '严重度' }).click();
  await page.getByRole('option', { name: '紧急' }).click();
  await page.keyboard.press('Escape');
  await expect(page.getByText('没有符合条件的告警')).toBeVisible();
});

test('J and K move through the list', async ({ page }) => {
  await page.goto(`/operations/alarms?site=${SITE_ID}&view=all`);
  await expect(rows(page)).toHaveCount(2);
  await page.keyboard.press('j');
  await expect(rows(page).nth(1)).toHaveAttribute('aria-current', 'true');
  await page.keyboard.press('k');
  await expect(rows(page).nth(0)).toHaveAttribute('aria-current', 'true');
});

// #594: 解释 hands the assistant a question about the alarm in view, with its start in Site time.
test('分析原因 opens the assistant with a question about the alarm in view', async ({ page }) => {
  await page.goto(`/operations/alarms?site=${SITE_ID}&inspect=${ACTIVE_ALARM}`);
  await page.getByRole('region', { name: '告警详情' }).getByRole('button', { name: '分析原因' }).click();
  const panel = page.getByRole('complementary', { name: 'AI 助手' });
  await expect(panel.getByRole('textbox', { name: '向助手提问' })).toHaveValue('2026年9月29日 16:26 开始的告警「冷冻水回水温度过低」是怎么回事？');
});

// #693: the rules the Site evaluates now, read-only, in the operator's words.
test('正在生效的规则 lists each rule with its devices, conditions and severity', async ({ page }) => {
  await page.goto(`/operations/alarms?site=${SITE_ID}`);
  await page.getByRole('tab', { name: /正在生效的规则/ }).click();
  await expect(page).toHaveURL(/tab=rules/);

  const table = page.getByRole('table', { name: '正在生效的规则' });
  const rule = (title: string) => table.getByRole('row').filter({ has: page.getByRole('cell', { name: title, exact: true }) });
  await expect(table.getByRole('row')).toHaveCount(5);
  await expect(rule('冷冻水回水温度过低')).toContainText('CHILLER-01、BTU-METER-01');
  await expect(rule('冷冻水回水温度过低')).toContainText('chiller run state 为运行，并且');
  await expect(rule('冷冻水回水温度过低')).toContainText('btu meter return water temperature 不高于 10.5 °C，持续 5 分钟');
  await expect(rule('冷冻水回水温度过低')).toContainText('btu meter return water temperature 不低于 11.5 °C，或者 CHILLER-01 的 chiller run state 不为运行');
  await expect(rule('冷冻水回水温度过低')).toContainText('重要');
  await expect(rule('1 号冷机供水温度骤升')).toContainText('BTU-METER-01、CHILLER-01');
  await expect(rule('1 号冷机供水温度骤升')).toContainText('在 5 分钟内升高达到 2 °C');
  await expect(rule('1 号冷机供水温度骤升')).toContainText('模板派生（覆盖变化幅度） · 斜率趋势');
  await expect(rule('1 号冷机供水温度骤升')).toContainText('第 1 跳 · 依赖「冷冻水回水温度过低」');
  await expect(rule('CWP-01 读数长时间没有更新')).toContainText('CWP-01 的 4 个读数中任一个超过 30 分钟没有新读数');
  await expect(rule('CWP-01 读数长时间没有更新')).toContainText('CWP-01 的 4 个读数都有新读数');
  await expect(rule('网关上报中断')).toContainText('3 台设备的 5 个读数都超过 15 分钟没有新读数');
  await expect(rule('网关上报中断')).toContainText('3 台设备的 5 个读数中任一个有新读数');
  // Operators see names, never the identifiers or enums behind them.
  await expect(table).not.toContainText(/[0-9a-f]{8}-[0-9a-f]{4}-|RUNNING|NO_DATA|MAJOR|SLOPE|EXPRESSION/);

  const appearance = page.getByRole('radiogroup', { name: '外观' });
  await appearance.getByRole('radio', { name: '浅色' }).click();
  await expect.poll(() => page.evaluate(() => getComputedStyle(document.documentElement).colorScheme)).toBe('light');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);

  await appearance.getByRole('radio', { name: '深色' }).click();
  await expect.poll(() => page.evaluate(() => getComputedStyle(document.documentElement).colorScheme)).toBe('dark');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
});

test('模拟试算 dry-runs a multi-step sampling sequence without mutating alarms', async ({ page, gateway }) => {
  await page.goto(`/operations/alarms?site=${SITE_ID}&tab=rules`);
  const appearance = page.getByRole('radiogroup', { name: '外观' });
  await appearance.getByRole('radio', { name: '浅色' }).click();
  const table = page.getByRole('table', { name: '正在生效的规则' });
  const targetRow = table.getByRole('row').filter({ has: page.getByRole('cell', { name: '1 号冷机供水温度骤升', exact: true }) });
  await targetRow.getByRole('button', { name: '模拟试算' }).click();

  const sheet = page.getByRole('dialog');
  await expect(sheet).toContainText('规则模拟试算：1 号冷机供水温度骤升');
  await expect(sheet).toContainText('模板派生（覆盖变化幅度）');

  const steps = sheet.locator('fieldset');
  await expect(steps).toHaveCount(2);
  await steps.nth(0).getByLabel(/btu meter supply water temperature/).fill('7');
  await steps.nth(1).getByLabel(/btu meter supply water temperature/).fill('10.5');
  await sheet.getByRole('button', { name: '添加采样步' }).click();
  await expect(steps).toHaveCount(3);
  await steps.nth(2).getByLabel(/btu meter supply water temperature/).fill('6.8');

  await sheet.getByRole('button', { name: '运行模拟试算' }).click();
  const resultTable = sheet.getByRole('table', { name: '试算结果明细' });
  await expect(resultTable).toContainText('触发告警');
  await expect(resultTable).toContainText('自动恢复');
  await expect(resultTable).not.toContainText(/[0-9a-f]{8}-[0-9a-f]{4}-|MATCHED|PUBLISH|CLEAR/);

  const simWrite = gateway.writes.find((write) => write.path === '/api/v1/alarm-rules/simulate');
  expect(simWrite?.csrfToken).toBe('e2e-csrf-token');
  expect(simWrite?.body).toMatchObject({
    ruleId: '01910000-b100-7000-8000-00000000000d',
    steps: [
      { readings: [{ pointCode: 'supply_water_temperature', value: 7 }, { pointCode: 'run_state', value: 'RUNNING' }] },
      { readings: [{ pointCode: 'supply_water_temperature', value: 10.5 }, { pointCode: 'run_state', value: 'RUNNING' }] },
      { readings: [{ pointCode: 'supply_water_temperature', value: 6.8 }, { pointCode: 'run_state', value: 'RUNNING' }] },
    ],
  });
});

test('an alarm recovered because its rule was retired says so', async ({ page, gateway }) => {
  const cleared = gateway.alarms.find((alarm) => alarm.condition === 'CLEARED')!;
  cleared.timeline.at(-1).reason = 'RULE_RETIRED';
  await page.goto(`/operations/alarms?site=${SITE_ID}&view=cleared&inspect=${cleared.alarmId}`);
  await expect(page.getByRole('region', { name: '告警详情' })).toContainText('系统 · 规则已撤销');
});

test('查看当时趋势 opens 历史趋势 with the alarm fault window, overlays enabled, and source context banner', async ({ page }) => {
  await page.goto(`/operations/alarms?site=${SITE_ID}&inspect=${ACTIVE_ALARM}`);
  const detail = page.getByRole('region', { name: '告警详情' });
  await detail.getByRole('link', { name: '查看当时趋势' }).click();

  await expect(page).toHaveURL(/\/operations\/trends/);
  await expect(page).toHaveURL(/source=alarm/);
  await expect(page).toHaveURL(/overlays=alarms%2Ccommands|overlays=alarms,commands/);
  await expect(page.getByRole('region', { name: '历史趋势工作台' }).getByRole('status')).toContainText('来自告警：冷冻水回水温度过低');
  await expect(page.getByRole('link', { name: /返回告警/ })).toBeVisible();
});

test('operators can unassign, suppress and unsuppress alarms, and review evaluation snapshots without UUID or raw enum leakage', async ({ page, gateway }) => {
  gateway.principalOverride = {
    ...principalFixture,
    authorization: {
      ...principalFixture.authorization,
      capabilities: [...principalFixture.authorization.capabilities, 'alarm.assign'],
    },
  };
  await page.goto(`/operations/alarms?site=${SITE_ID}&inspect=${ACTIVE_ALARM}`);
  const detail = page.getByRole('region', { name: '告警详情' });

  const evalTable = detail.getByRole('table', { name: '规则评估明细表' });
  await expect(evalTable).toContainText('已命中');
  await expect(evalTable).toContainText('触发告警');
  await expect(evalTable).toContainText('回水温度 10.2 · 功率 372.5');
  await expect(evalTable).not.toContainText(/[0-9a-f]{8}-[0-9a-f]{4}-|MATCHED|PUBLISH/);

  await detail.getByRole('button', { name: '由我处理' }).click();
  await expect(detail).toContainText('我');
  await detail.getByRole('button', { name: '取消分配' }).click();
  await expect(detail).toContainText('未分配');

  await detail.getByRole('button', { name: '抑制通知' }).click();
  await page.getByRole('radio', { name: '4 小时', exact: true }).click();
  await page.getByRole('textbox', { name: '抑制原因' }).fill('冷水机组计划保养');
  await page.getByRole('button', { name: '确认抑制' }).click();

  await expect(detail).toContainText('活动 · 未确认 · 已抑制');
  await expect(detail).toContainText('已抑制通知至');
  await expect(detail).toContainText('原因：冷水机组计划保养');

  await page.getByRole('radio', { name: /已抑制/ }).click();
  await expect(page).toHaveURL(/view=suppressed/);
  await expect(rows(page)).toHaveCount(1);
  await expect(rows(page).first()).toContainText('已抑制');

  await detail.getByRole('button', { name: '解除抑制' }).click();
  await expect(rows(page)).toHaveCount(0);
  await page.getByRole('radio', { name: /^活动/ }).click();
  await expect(detail).toContainText('活动 · 未确认');
  await expect(detail).not.toContainText('已抑制通知至');

  const suppressWrite = gateway.writes.find((write) => write.path === `/api/v1/alarms/${ACTIVE_ALARM}/suppress`);
  expect(suppressWrite?.body).toMatchObject({ reason: '冷水机组计划保养' });
  const unsuppressWrite = gateway.writes.find((write) => write.path === `/api/v1/alarms/${ACTIVE_ALARM}/unsuppress`);
  expect(unsuppressWrite?.body).toMatchObject({ reason: '恢复告警通知' });
});
