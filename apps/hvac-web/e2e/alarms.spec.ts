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
  await expect(rule('冷冻水回水温度过低')).toContainText('运行状态为运行，并且');
  await expect(rule('冷冻水回水温度过低')).toContainText('回水温度不高于 10.5 °C，持续 5 分钟');
  await expect(rule('冷冻水回水温度过低')).toContainText('回水温度不低于 11.5 °C，或者 CHILLER-01 的运行状态不为运行');
  await expect(table).not.toContainText(/chiller run state|btu meter/i);
  await expect(rule('冷冻水回水温度过低')).toContainText('重要');
  await expect(rule('1 号冷机供水温度骤升')).toContainText('BTU-METER-01、CHILLER-01');
  await expect(rule('1 号冷机供水温度骤升')).toContainText('在 5 分钟内升高达到 2 °C');
  await expect(rule('1 号冷机供水温度骤升')).toContainText('模板派生（覆盖变化幅度） · 斜率趋势');
  await expect(rule('1 号冷机供水温度骤升')).toContainText('第 1 跳 · 依赖「冷冻水回水温度过低」（需全部上游告警同时触发）');
  await expect(rule('1 号冷机供水温度骤升')).toContainText('联动 2 项动作：创建运维工单、下发保护指令');
  await expect(rule('CWP-01 读数长时间没有更新')).toContainText('CWP-01 的 4 个读数中任一个超过 30 分钟没有新读数');
  await expect(rule('CWP-01 读数长时间没有更新')).toContainText('CWP-01 的 4 个读数都有新读数');
  await expect(rule('网关上报中断')).toContainText('3 台设备的 5 个读数都超过 15 分钟没有新读数');
  await expect(rule('网关上报中断')).toContainText('3 台设备的 5 个读数中任一个有新读数');
  // Operators see names, never the identifiers or enums behind them.
  await expect(table).not.toContainText(/[0-9a-f]{8}-[0-9a-f]{4}-|RUNNING|NO_DATA|MAJOR|SLOPE|EXPRESSION|ALL_ACTIVE|WORK_ORDER|PROTECTION_COMMAND/);
  await expect(page.getByRole('table', { name: '规则动作投递死信' })).toHaveCount(0);
  await expect(page.getByRole('region', { name: '分级路由与升级路径' })).toHaveCount(0);

  const appearance = page.getByRole('radiogroup', { name: '外观' });
  await appearance.getByRole('radio', { name: '浅色' }).click();
  await expect.poll(() => page.evaluate(() => getComputedStyle(document.documentElement).colorScheme)).toBe('light');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);

  await appearance.getByRole('radio', { name: '深色' }).click();
  await expect.poll(() => page.evaluate(() => getComputedStyle(document.documentElement).colorScheme)).toBe('dark');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);

  await page.getByRole('link', { name: '路由与动作死信' }).click();
  await expect(page).toHaveURL(/\/system\?.*tab=governance.*section=alarms/);

  const dlqTable = page.getByRole('table', { name: '规则动作投递死信' });
  await expect(dlqTable).toContainText('回调外部接口');
  await expect(dlqTable).toContainText('触发时');
  await expect(dlqTable).toContainText('网络请求超时或传输中断');
  await dlqTable.getByRole('button', { name: '重新投递' }).click();
  await expect(page.getByText('已重新提交到规则动作投递队列')).toBeVisible();
  await expect(page.getByText('当前没有待重投的规则联动动作死信')).toBeVisible();

  await expect(page.getByText('通知路由与降噪配置')).toBeVisible();
  const routingPanel = page.getByRole('region', { name: '分级路由与升级路径' });
  await expect(routingPanel).toContainText('重要分级通道');
  await expect(routingPanel).not.toContainText(/\b(?:major|warning)\b/);
  await expect(routingPanel).toContainText('企业微信应用 / 邮件（2 人）');
  await expect(routingPanel).not.toContainText(/example\.com|duty-a/);
  await page.getByRole('button', { name: '热重载路由配置' }).click();
  await expect(page.getByText('站点通知路由配置已热更新并生效')).toBeVisible();
  await expect(page.getByText('1 次')).toBeVisible();
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
  await steps.nth(0).getByLabel(/供水温度/).fill('7');
  await steps.nth(1).getByLabel(/供水温度/).fill('10.5');
  await sheet.getByRole('button', { name: '添加采样步' }).click();
  await expect(steps).toHaveCount(3);
  await steps.nth(2).getByLabel(/供水温度/).fill('6.8');

  // With ALL_ACTIVE gate and upstream unchecked, raise stays blocked until upstream is checked.
  await sheet.getByRole('button', { name: '运行模拟试算' }).click();
  const resultTable = sheet.getByRole('table', { name: '试算结果明细' });
  await expect(resultTable).toContainText('前置上游告警未全部激活');

  await sheet.getByRole('checkbox', { name: '冷冻水回水温度过低' }).check();
  await sheet.getByRole('button', { name: '运行模拟试算' }).click();
  await expect(resultTable).toContainText('触发告警');
  await expect(resultTable).toContainText('拟触发 触发时创建运维工单：排查冷机供水温度骤升异常');
  await expect(resultTable).toContainText('拟触发 触发时下发保护指令：保护设定值与联锁指令');
  await expect(resultTable).toContainText('自动恢复');
  await expect(resultTable).not.toContainText(/[0-9a-f]{8}-[0-9a-f]{4}-|MATCHED|PUBLISH|CLEAR|WORK_ORDER|PROTECTION_COMMAND/);

  const simWrite = gateway.writes.filter((write) => write.path === '/api/v1/alarm-rules/simulate').at(-1);
  expect(simWrite?.csrfToken).toBe('e2e-csrf-token');
  expect(simWrite?.body).toMatchObject({
    ruleId: '01910000-b100-7000-8000-00000000000d',
    steps: [
      {
        upstreamActiveRuleIds: ['01910000-b100-7000-8000-00000000000a'],
        readings: [{ pointCode: 'supply_water_temperature', value: 7 }, { pointCode: 'run_state', value: 'RUNNING' }],
      },
      {
        upstreamActiveRuleIds: ['01910000-b100-7000-8000-00000000000a'],
        readings: [{ pointCode: 'supply_water_temperature', value: 10.5 }, { pointCode: 'run_state', value: 'RUNNING' }],
      },
      {
        upstreamActiveRuleIds: ['01910000-b100-7000-8000-00000000000a'],
        readings: [{ pointCode: 'supply_water_temperature', value: 6.8 }, { pointCode: 'run_state', value: 'RUNNING' }],
      },
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

test('operators can edit rules on the DAG canvas, dry-run drafts, diff and hot-publish, and roll back revisions', async ({ page, gateway }) => {
  await page.goto(`/operations/alarms?site=${SITE_ID}&tab=rules`);
  await expect(page.getByText('生效版本 v1', { exact: true })).toBeVisible();

  await page.getByRole('button', { name: '拓扑画布' }).click();
  await page.getByRole('button', { name: /1 号冷机供水温度骤升/ }).click();
  await page.getByRole('button', { name: '编辑规则' }).click();

  const editor = page.getByRole('dialog', { name: '编辑告警规则' });
  await expect(editor).toBeVisible();
  await editor.getByLabel('规则名称').fill('1 号冷机供水温度骤升（已调优）');
  await editor.getByRole('button', { name: '试算草稿' }).click();

  const simSheet = page.getByRole('dialog', { name: /规则草稿模拟试算：1 号冷机供水温度骤升（已调优）/ });
  await expect(simSheet).toBeVisible();
  const draftSteps = simSheet.locator('fieldset');
  await draftSteps.nth(0).getByLabel(/供水温度/).fill('7');
  await draftSteps.nth(1).getByLabel(/供水温度/).fill('10.5');
  await simSheet.getByRole('checkbox', { name: '冷冻水回水温度过低' }).check();
  await simSheet.getByRole('button', { name: '运行模拟试算' }).click();
  await expect(simSheet.getByRole('table', { name: '试算结果明细' })).toContainText('触发告警');
  const draftSimWrite = gateway.writes.filter((w) => w.path === '/api/v1/alarm-rules/simulate').at(-1);
  expect(draftSimWrite?.body).toMatchObject({
    ruleDraft: { title: '1 号冷机供水温度骤升（已调优）' },
  });
  await page.keyboard.press('Escape');

  await expect(page.getByText('未发布草稿：新增 0 条 · 修改 1 条 · 停用 0 条')).toBeVisible();
  await page.getByRole('button', { name: '预览差异并热发布' }).click();

  const diffDialog = page.getByRole('dialog', { name: '确认热发布站点告警规则？' });
  await expect(diffDialog).toContainText('1 号冷机供水温度骤升（已调优）');
  await expect(diffDialog).toContainText('变更字段：规则名称');
  await expect(diffDialog).not.toContainText(/[0-9a-f]{8}-[0-9a-f]{4}-|CHANGED|RETIRED|PROTECTION_COMMAND|WEB_WORKBENCH/);
  await diffDialog.getByLabel('变更说明（选填）').fill('更新规则显示名称并校验试算结果');
  await diffDialog.getByRole('button', { name: '确认热发布' }).click();

  await expect(page.getByText('已热发布规则包至版本 v2（共 4 条生效规则）。')).toBeVisible();
  await expect(page.getByText('生效版本 v2', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: /1 号冷机供水温度骤升（已调优）/ })).toBeVisible();

  await page.getByRole('button', { name: '版本历史' }).click();
  const revSheet = page.getByRole('dialog', { name: '规则版本账本' });
  await expect(revSheet).toContainText('工作台热发布');
  await expect(revSheet).toContainText('当前生效');
  await expect(revSheet).toContainText('更新规则显示名称并校验试算结果');
  await expect(revSheet).not.toContainText(/[0-9a-f]{8}-[0-9a-f]{4}-|WEB_WORKBENCH|CLI_SYNC|ROLLBACK/);

  await revSheet.getByRole('button', { name: '回滚到此版本' }).click();
  await revSheet.getByRole('button', { name: '确认回滚' }).click();

  await expect(page.getByText('已回滚规则包至版本 v3（共 4 条生效规则）。')).toBeVisible();
  await expect(page.getByText('生效版本 v3', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: /1 号冷机供水温度骤升/ })).toBeVisible();

  const publishWrite = gateway.writes.find((w) => w.method === 'PUT' && w.path === '/api/v1/alarm-rules');
  expect(publishWrite?.body).toMatchObject({ expectedRevision: 1, changeNote: '更新规则显示名称并校验试算结果' });
  const rollbackWrite = gateway.writes.find((w) => w.path === '/api/v1/alarm-rules/revisions/1/rollback');
  expect(rollbackWrite?.body).toMatchObject({ expectedRevision: 2 });

  // Retire upstream rule with active alarm -> auto-cleans downstream dependencyGate + shows clear notice -> conflict recovery test.
  await page.getByRole('button', { name: '新建规则' }).click();
  const createEditor = page.getByRole('dialog', { name: '新建告警规则' });
  await createEditor.getByLabel('规则名称').fill('临时压差偏高告警');
  await createEditor.getByRole('button', { name: '保存至草稿' }).click();
  await expect(page.getByText('未发布草稿：新增 1 条 · 修改 0 条 · 停用 0 条')).toBeVisible();
  await page.getByRole('button', { name: '放弃草稿' }).click();
  await expect(page.getByText('未发布草稿：')).toHaveCount(0);

  // Select upstream rule 冷冻水回水温度过低 & retire it cleanly without dangling ALL_ACTIVE 422 error.
  await page.getByRole('button', { name: /冷冻水回水温度过低/ }).click();
  await page.getByRole('button', { name: '停用规则' }).click();
  await expect(page.getByText('未发布草稿：新增 0 条 · 修改 1 条 · 停用 1 条')).toBeVisible();
  await page.getByRole('button', { name: '预览差异并热发布' }).click();
  await expect(diffDialog).toContainText('停用将自动恢复活动告警');
  await expect(diffDialog).toContainText('变更字段：前置依赖规则、联动门控条件');

  // Simulate concurrent revision bump before confirm -> surfaces 409 reload recovery banner cleanly.
  gateway.alarmRuleBundleRevision = 4;
  await diffDialog.getByRole('button', { name: '确认热发布' }).click();
  await expect(diffDialog).toContainText('服务端规则版本已在其他会话中更新');
  await diffDialog.getByRole('button', { name: '重新加载最新版本' }).click();
  await expect(page.getByText('未发布草稿：')).toHaveCount(0);

  // Check 1440px DAG horizontal fit in dark & light themes.
  const appearance = page.getByRole('radiogroup', { name: '外观' });
  await appearance.getByRole('radio', { name: '浅色' }).click();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
  await appearance.getByRole('radio', { name: '深色' }).click();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
});

test('operators can author streaming window rules, compile edge bundles and push to gateway shadow', async ({ page, gateway }) => {
  gateway.principalOverride = {
    ...principalFixture,
    authorization: {
      ...principalFixture.authorization,
      capabilities: [...principalFixture.authorization.capabilities, 'device.write'],
    },
  };
  await page.goto(`/operations/alarms?site=${SITE_ID}&tab=rules`);
  await expect(page.getByText('生效版本 v1', { exact: true })).toBeVisible();

  await page.getByRole('button', { name: '新建规则' }).click();
  const editor = page.getByRole('dialog', { name: '新建告警规则' });
  await expect(editor).toBeVisible();
  await editor.getByLabel('规则名称').fill('1 号冷机供水窗口均值偏高');

  await editor.getByRole('combobox', { name: '规则类型' }).click();
  await page.getByRole('option', { name: '流式窗口', exact: true }).click();

  await editor.getByRole('combobox', { name: '执行位置' }).click();
  await page.getByRole('option', { name: '云边协同执行', exact: true }).click();
  await expect(editor).toContainText('符合云边协同执行要求');

  await editor.getByRole('combobox', { name: '触发报警条件判定类型' }).click();
  await page.getByRole('option', { name: '流式窗口聚合', exact: true }).click();

  await editor.getByLabel('触发报警条件聚合阈值').fill('15');
  await editor.getByLabel('触发报警条件窗口时长秒数').fill('300');
  await editor.getByLabel('触发报警条件最小样本数').fill('3');
  await editor.getByRole('button', { name: '保存至草稿' }).click();
  await expect(editor).toHaveCount(0);

  await expect(page.getByText('1 号冷机供水窗口均值偏高')).toBeVisible();
  await expect(page.getByText('云边协同', { exact: true })).toBeVisible();

  await page.getByRole('button', { name: '预览差异并热发布' }).click();
  const diffDialog = page.getByRole('dialog', { name: '确认热发布站点告警规则？' });
  await diffDialog.getByRole('button', { name: '确认热发布' }).click();
  await expect(page.getByText('已热发布规则包至版本 v2（共 5 条生效规则）。')).toBeVisible();

  await page.getByRole('button', { name: '边缘编译下发' }).click();
  const edgeSheet = page.getByRole('dialog', { name: '云边统一编译与网关下发' });
  await expect(edgeSheet).toBeVisible();
  await expect(edgeSheet).toContainText('1 条');
  await expect(edgeSheet).toContainText('已编入边缘');
  await expect(edgeSheet).toContainText('EG8200-COMMERCIAL-001');

  await edgeSheet.getByRole('button', { name: '下发至网关配置影子' }).click();
  await expect(edgeSheet).toContainText('已下发 1 条边缘规则至网关配置影子');

  const shadowWrite = gateway.writes.find(
    (entry) => entry.method === 'PUT' && entry.path === '/api/v1/gateways/018f3e00-4000-7000-8000-000000000100/config-shadow',
  );
  expect(shadowWrite?.body?.edgeRuleBundle?.rules).toHaveLength(1);
  expect(shadowWrite?.body?.edgeRuleBundle?.rules?.[0]?.raise?.kind).toBe('WINDOW_AGG');
  expect(shadowWrite?.body?.edgeRuleBundle?.edgeBundleDigest).toMatch(/^[a-f0-9]{64}$/);

  await edgeSheet.getByRole('button', { name: '清空边缘规则包' }).click();
  await expect(edgeSheet).toContainText('已清空目标网关配置影子中的边缘规则包');
  const clearWrite = gateway.writes.filter(
    (entry) => entry.method === 'PUT' && entry.path === '/api/v1/gateways/018f3e00-4000-7000-8000-000000000100/config-shadow',
  ).at(-1);
  expect(clearWrite?.body?.edgeRuleBundle).toBeNull();
});

test('keyboard shortcuts ignore IME composition and assistant panel keeps 1440px alarms layout free of horizontal overflow', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(`/operations/alarms?site=${SITE_ID}&view=all`);
  await expect(rows(page)).toHaveCount(2);
  await expect(rows(page).nth(0)).toHaveAttribute('aria-current', 'true');

  // IME composition confirmation (`isComposing` / `keyCode === 229`) must not trigger J/K navigation.
  await page.evaluate(() => {
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'j', code: 'KeyJ', keyCode: 229, isComposing: true, bubbles: true, cancelable: true }));
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'j', code: 'KeyJ', keyCode: 229, isComposing: false, bubbles: true, cancelable: true }));
  });
  await expect(rows(page).nth(0)).toHaveAttribute('aria-current', 'true');

  await page.getByRole('button', { name: 'AI 助手' }).click();
  await expect(page.getByRole('complementary', { name: 'AI 助手' })).toBeVisible();
  await expect
    .poll(() => page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth))
    .toBe(true);
});
