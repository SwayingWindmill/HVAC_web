import { SITE_ID } from './gateway';
import { expect, test } from './test';

test('sidebar opens 历史趋势 with default preset, unit-grouped charts, and Chinese point labels', async ({ page }) => {
  await page.goto(`/operations/realtime?site=${SITE_ID}`);
  await page.getByRole('link', { name: '历史趋势' }).click();
  await expect(page).toHaveURL(/\/operations\/trends/);

  await expect(page.getByRole('heading', { name: '历史趋势' })).toBeVisible();
  await expect(page.getByRole('radio', { name: '冷站功率与供回水' })).toHaveAttribute('aria-checked', 'true');

  // Separate physical unit lanes: kW and °C
  await expect(page.getByRole('region', { name: /功率 \/ 负荷 \(kW\)/ })).toBeVisible();
  await expect(page.getByRole('region', { name: /温度 \/ 温差 \(°C\)/ })).toBeVisible();

  // Chinese domain labels, never raw English source keys
  await expect(page.getByText('中央空调总功率').first()).toBeVisible();
  await expect(page.getByText('供水温度').first()).toBeVisible();
  await expect(page.getByText('chiller leaving chilled water temperature')).toHaveCount(0);
});

test('window presets, 1-minute granularity, previous-day comparison, and URL sharing stay in sync', async ({ page }) => {
  await page.goto(`/operations/trends?site=${SITE_ID}`);

  await page.getByRole('radiogroup', { name: '时间范围' }).getByRole('radio', { name: '1 小时' }).click();
  await expect(page).toHaveURL(/window=1h/);
  await expect(page.getByRole('radiogroup', { name: '聚合粒度' }).getByRole('radio', { name: '1 分钟' })).toHaveAttribute('aria-checked', 'true');

  const compareToggle = page.getByRole('switch', { name: '对比昨日同时段' });
  await compareToggle.click();
  await expect(page).toHaveURL(/compare=prev/);

  await page.getByRole('radio', { name: '水泵流量与温差' }).click();
  await expect(page).toHaveURL(/preset=pump-hydraulics/);
  await expect(page.getByRole('region', { name: /流量 \(m³\/h\)/ })).toBeVisible();
});

test('switching to 统计表 displays latest, average, extremes with timestamps, and coverage ratio', async ({ page }) => {
  await page.goto(`/operations/trends?site=${SITE_ID}&window=1h`);

  await page.getByRole('radio', { name: '统计表' }).click();
  await expect(page).toHaveURL(/view=table/);

  const table = page.getByRole('table', { name: '趋势统计表' });
  await expect(table).toBeVisible();
  await expect(table.getByRole('columnheader', { name: '最新值' })).toBeVisible();
  await expect(table.getByRole('columnheader', { name: '平均值' })).toBeVisible();
  await expect(table.getByRole('columnheader', { name: '最小值' })).toBeVisible();
  await expect(table.getByRole('columnheader', { name: '最大值' })).toBeVisible();
  await expect(table.getByRole('columnheader', { name: '覆盖率' })).toBeVisible();
  await expect(table.getByRole('row', { name: /中央空调总功率/ })).toContainText(/100%/);
});

test('point picker enforces the 8-point cap and disables unchecked points at capacity', async ({ page }) => {
  await page.goto(`/operations/trends?site=${SITE_ID}`);

  await page.getByRole('button', { name: /点位 \(\d+\/8\)/ }).click();
  const dialog = page.getByRole('dialog');
  await expect(dialog).toBeVisible();

  // Select unchecked points until reaching 8/8
  while (true) {
    const statusText = await dialog.getByText(/已选 \d+ \/ 8/).textContent();
    if (statusText?.includes('已选 8 / 8')) break;
    const nextUnchecked = dialog.locator('[role="checkbox"][aria-checked="false"]:not([disabled])').first();
    await nextUnchecked.click();
  }

  await expect(dialog.getByText('已选 8 / 8')).toBeVisible();
  const remainingUnchecked = dialog.locator('[role="checkbox"][aria-checked="false"]');
  await expect(remainingUnchecked.first()).toBeDisabled();
});

test('bottom 设备启停状态 lane renders run/stop segments and updates readout on hover', async ({ page }) => {
  await page.goto(`/operations/trends?site=${SITE_ID}&window=1h`);

  const stateRegion = page.getByRole('region', { name: '设备启停状态' });
  await expect(stateRegion).toBeVisible();
  await expect(stateRegion).toContainText('CHILLER-01');
  await expect(stateRegion.locator('[data-state-kind="RUNNING"]').first()).toBeVisible();
  await expect(stateRegion.locator('[data-state-kind="STOPPED"]').first()).toBeVisible();

  // Hover over the STOPPED segment and verify synchronized cursor readout
  await stateRegion.locator('[data-state-kind="STOPPED"]').first().hover();
  await expect(stateRegion).toContainText(/游标 \d{2}:\d{2}/);
  await expect(stateRegion.getByTestId('state-readout-CHILLER-01')).toHaveText('停止');
});

test('toggling 叠加告警 and 叠加控制指令 renders event rail, collapses dense events, and creates a work order from an alarm event', async ({ page, gateway }) => {
  const nowMs = Date.now();
  const twentyMinAgo = new Date(nowMs - 20 * 60_000).toISOString();
  const nineteenMinAgo = new Date(nowMs - 19 * 60_000 - 30_000).toISOString();

  // Seed a command on CHILLER-01 setpoint 19.5 minutes ago (dense with an alarm 20 minutes ago!)
  gateway.alarms[0].firstOccurredAt = twentyMinAgo;
  gateway.alarms[0].lastOccurredAt = twentyMinAgo;
  gateway.commands.push({
    schemaVersion: 1,
    commandId: 'ec61c2dc-2d2c-4933-8fb7-000000000099',
    siteId: SITE_ID,
    deviceId: '018f3e00-4000-7000-8000-000000000001',
    pointId: '01910000-0000-7000-8000-010000000011',
    capability: 'SETPOINT',
    status: 'SUCCEEDED',
    risk: 'LOW',
    approvalPolicy: 'NONE',
    approvalCount: 0,
    requiredApprovalCount: 0,
    parameters: { value: 7.5 },
    transitions: [{ toStatus: 'SUCCEEDED', reason: 'SUCCEEDED', actorType: 'PRINCIPAL', occurredAt: nineteenMinAgo, version: 1 }],
    createdAt: nineteenMinAgo,
    updatedAt: nineteenMinAgo,
  });

  await page.goto(`/operations/trends?site=${SITE_ID}&window=1h`);

  await page.getByRole('switch', { name: '叠加告警' }).click();
  await page.getByRole('switch', { name: '叠加控制指令' }).click();
  await expect(page).toHaveURL(/overlays=alarms%2Ccommands|overlays=alarms,commands/);

  const rail = page.getByRole('region', { name: '事件时间轴' });
  await expect(rail).toBeVisible();
  // Dense alarm + command within 30s collapse into a 2-event cluster pill
  const clusterBtn = rail.getByRole('button', { name: /2 条事件/ });
  await expect(clusterBtn).toBeVisible();
  await clusterBtn.click();

  const popover = page.getByRole('dialog');
  await expect(popover).toContainText('冷冻水回水温度过低');
  await expect(popover).toContainText('7.5');

  // Create a work order directly from the alarm event card
  await popover.getByRole('button', { name: '创建工单' }).click();
  const woDialog = page.getByRole('dialog', { name: '由告警创建工单' });
  await expect(woDialog).toBeVisible();
  await woDialog.getByLabel('说明').fill('趋势曲线确认回水温度持续偏低，排查旁通阀');
  await woDialog.getByRole('button', { name: '创建工单' }).click();

  const createWrite = gateway.writes.find((w) => w.method === 'POST' && w.path.endsWith('/work-orders'));
  expect(createWrite?.body).toMatchObject({
    sourceReferences: [{ domain: 'ALARM', resourceId: gateway.alarms[0].alarmId, relationship: 'ORIGIN' }],
  });
});

test('denied alarm and command permissions disable overlay switches without blocking trend charts', async ({ page, gateway }) => {
  gateway.alarmsDenied = true;
  gateway.commandsDenied = true;

  await page.goto(`/operations/trends?site=${SITE_ID}&window=1h&overlays=alarms,commands`);

  // Trend charts still render normally
  await expect(page.getByText('功率 / 负荷')).toBeVisible();

  await expect(page.getByRole('switch', { name: '叠加告警' })).toBeDisabled();
  await expect(page.getByText('无告警查看权限')).toBeVisible();
  await expect(page.getByRole('switch', { name: '叠加控制指令' })).toBeDisabled();
  await expect(page.getByText('无指令查看权限')).toBeVisible();
});
