import { SITE_ID } from './gateway';
import { expect, test } from './test';

test('an operator sets the chilled water setpoint from the device row, confirmed before it is sent', async ({ page, gateway }) => {
  await page.goto(`/operations/control?site=${SITE_ID}`);

  await page.getByRole('button', { name: '调整 CHILLER-01 的设定值' }).click();
  const settings = page.getByRole('dialog', { name: '调整 CHILLER-01 的设定值' });
  const setpointInput = settings.getByRole('textbox', { name: '冷冻水出水设定温度目标值' });
  await setpointInput.fill('7.5');

  // Confirming an IME candidate with Enter (including Safari's post-compositionend keyCode 229) must not submit.
  await setpointInput.evaluate((element) => {
    element.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter', keyCode: 229, isComposing: true, bubbles: true, cancelable: true }));
    element.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter', keyCode: 229, isComposing: false, bubbles: true, cancelable: true }));
  });
  await expect(page.getByRole('alertdialog')).toHaveCount(0);

  await setpointInput.press('Enter');
  const confirm = page.getByRole('alertdialog');
  await expect(confirm).toContainText('调整 CHILLER-01 的冷冻水出水设定温度？');
  await expect(confirm).toContainText('7.5 °C');
  await expect(confirm).toContainText('可设范围');
  await expect(confirm.getByRole('textbox', { name: '动态口令' })).toHaveCount(0);
  await confirm.getByRole('button', { name: '确认下发' }).click();

  const log = page.getByRole('list', { name: '指令记录' });
  await expect(log.getByRole('listitem').first()).toContainText('CHILLER-01 · 冷冻水出水设定温度 → 7.5 °C');
  await expect(log.getByRole('listitem').first()).toContainText('已执行');
  const submitted = gateway.writes.find((write) => write.path === '/api/v1/commands');
  expect(submitted?.body).toEqual({ assetId: expect.any(String), commandPointId: expect.any(String), parameters: { setpointC: 7.5 } });
  expect(submitted?.csrfToken).toBeTruthy();
});

test('stopping a pump asks for the one-time code and a site check, and approving it asks to verify identity again', async ({ page, gateway }) => {
  await page.goto(`/operations/control?site=${SITE_ID}`);

  await page.getByRole('group', { name: 'CHWP-01' }).getByRole('button', { name: '停止' }).click();
  const confirm = page.getByRole('alertdialog', { name: '停止 CHWP-01？' });
  const submit = confirm.getByRole('button', { name: '提交停止' });
  await expect(submit).toBeDisabled();
  await confirm.getByRole('textbox', { name: '动态口令' }).fill('12a3456');
  await expect(confirm.getByRole('textbox', { name: '动态口令' })).toHaveValue('123456');
  await expect(submit).toBeDisabled();
  await confirm.getByRole('checkbox', { name: '我已和现场确认可以停' }).check();
  await submit.click();

  const submitted = gateway.writes.find((write) => write.path === '/api/v1/commands');
  expect(submitted?.body).toMatchObject({ parameters: {}, verificationCode: '123456' });
  const log = page.getByRole('list', { name: '指令记录' });
  const stop = log.getByRole('listitem').filter({ hasText: '停止冷冻水泵' });
  await expect(stop).toContainText('待审批 · 高风险 · 需一人审批 · 0/1');
  await expect(page.getByRole('group', { name: 'CHWP-01' })).toContainText('待审批');

  // The session has no fresh two-step sign-in: the approval is refused and the page offers to verify.
  await stop.getByRole('button', { name: '批准' }).click();
  const stepUp = page.getByRole('dialog', { name: '批准前要再验证一次身份' });
  await expect(stepUp).toContainText('CHWP-01 · 停止冷冻水泵');
  await stepUp.getByRole('button', { name: '取消' }).click();

  gateway.stepUpFresh = true;
  await stop.getByRole('button', { name: '批准' }).click();
  await expect(stop).toContainText('已执行');

  await stop.getByRole('button', { name: /停止冷冻水泵/ }).click();
  await expect(page).toHaveURL(new RegExp(`inspect=${gateway.commands.find((command) => command.capability === 'STOP')!.commandId}`));
  const sheet = page.getByRole('dialog');
  await expect(sheet).toContainText('需一人审批 · 1/1');
  await expect(sheet.getByRole('listitem')).toHaveText([/已提交/, /待审批/, /已批准/, /已执行/]);
});

test('switching to manual control withdraws the pending command, pauses every device and shows in the top bar', async ({ page, gateway }) => {
  await page.goto(`/operations/control?site=${SITE_ID}`);

  await page.getByRole('group', { name: 'CT-01' }).getByRole('button', { name: '停止' }).click();
  const confirm = page.getByRole('alertdialog');
  await confirm.getByRole('textbox', { name: '动态口令' }).fill('654321');
  await confirm.getByRole('checkbox').check();
  await confirm.getByRole('button', { name: '提交停止' }).click();
  await expect(page.getByRole('list', { name: '指令记录' }).getByRole('listitem').first()).toContainText('待审批');

  const bar = page.getByRole('region', { name: '控制方式' });
  await expect(bar).toContainText('远程自动控制');
  await bar.getByRole('button', { name: '人工接管' }).click();
  const dialog = page.getByRole('dialog', { name: '切换到人工接管？' });
  await expect(dialog).toContainText('切换后会撤回 1 条还没下发的指令');
  await expect(dialog).toContainText('CT-01 · 停止冷却塔');
  const switchButton = dialog.getByRole('button', { name: '切换到人工接管' });
  await expect(switchButton).toBeDisabled();
  await dialog.getByRole('textbox', { name: '原因' }).fill('夜班人工巡检');
  await switchButton.click();

  await expect(page.getByText('已切换到人工接管，撤回了 1 条指令')).toBeVisible();
  const write = gateway.writes.find((entry) => entry.path === `/api/v1/sites/${SITE_ID}/control-mode`);
  expect(write?.body).toEqual({ controlMode: 'MANUAL_OVERRIDE', reason: '夜班人工巡检', expectedRevision: 1 });
  await expect(bar).toContainText('人工接管');
  await expect(bar).toContainText('原因：夜班人工巡检');
  await expect(page.getByRole('banner').getByRole('link', { name: '人工接管' })).toBeVisible();
  await expect(page.getByRole('group', { name: 'CHWP-01' }).getByRole('button', { name: '停止' })).toBeDisabled();
  await expect(page.getByRole('list', { name: '指令记录' }).getByRole('listitem').first()).toContainText('已撤回');

  // Safe exit waits for a command nobody has confirmed on site.
  gateway.commands[0].status = 'OUTCOME_UNKNOWN';
  await page.getByRole('button', { name: '刷新' }).click();
  await bar.getByRole('button', { name: '安全退出' }).click();
  const exit = page.getByRole('dialog', { name: '切换到安全退出？' });
  await expect(exit).toContainText('现在不能安全退出');
  await exit.getByRole('textbox', { name: '原因' }).fill('交回现场');
  await expect(exit.getByRole('button', { name: '切换到安全退出' })).toBeDisabled();
});

test('locking a device withdraws its pending command and greys its controls until unlocked', async ({ page, gateway }) => {
  await page.goto(`/operations/control?site=${SITE_ID}`);

  const pump = page.getByRole('group', { name: 'CWP-01' });
  await pump.getByRole('button', { name: '停止' }).click();
  const confirm = page.getByRole('alertdialog');
  await confirm.getByRole('textbox', { name: '动态口令' }).fill('111222');
  await confirm.getByRole('checkbox').check();
  await confirm.getByRole('button', { name: '提交停止' }).click();
  await expect(pump).toContainText('待审批');

  await pump.getByRole('button', { name: '锁定 CWP-01' }).click();
  const dialog = page.getByRole('dialog', { name: '锁定 CWP-01？' });
  await dialog.getByText('检修锁定', { exact: true }).click();
  await expect(dialog).toContainText('锁定后会撤回这台设备 1 条还没下发的指令');
  await dialog.getByRole('textbox', { name: '原因' }).fill('轴承异响，等维保');
  await dialog.getByRole('button', { name: '检修锁定' }).click();

  await expect(page.getByText('已检修锁定 CWP-01，撤回了 1 条指令')).toBeVisible();
  expect(gateway.writes.find((entry) => entry.path.endsWith('/control-lock'))?.body).toEqual({ lockMode: 'MAINTENANCE_LOCK', reason: '轴承异响，等维保' });
  await expect(pump).toContainText('检修锁定');
  await expect(pump.getByRole('button', { name: '停止' })).toBeDisabled();
  await expect(page.getByRole('region', { name: '控制方式' })).toContainText('锁定设备1 台');

  await pump.getByRole('button', { name: '解除锁定 CWP-01' }).click();
  const unlock = page.getByRole('dialog', { name: '解除 CWP-01 的锁定？' });
  await expect(unlock).toContainText('轴承异响，等维保');
  await unlock.getByRole('textbox', { name: '原因' }).fill('维保完成');
  await unlock.getByRole('button', { name: '解除锁定' }).click();
  await expect(pump.getByRole('button', { name: '停止' })).toBeEnabled();
});

test('during a maintenance window only stopping and resetting stay available', async ({ page, gateway }) => {
  gateway.controlState = {
    ...gateway.controlState,
    activeMaintenanceWindow: { id: '01a19000-0000-7000-8000-0000000000aa', title: '冷却塔清洗', startDate: '2026-11-15', endDate: '2026-11-17', reason: '' },
  };
  await page.goto(`/operations/control?site=${SITE_ID}`);

  await expect(page.getByRole('region', { name: '控制方式' })).toContainText('检修窗口「冷却塔清洗」2026-11-15 至 2026-11-17，只能停机和复位');
  await expect(page.getByRole('button', { name: '调整 CHILLER-01 的设定值' })).toBeDisabled();
  await expect(page.getByRole('group', { name: 'CHWP-01' }).getByRole('button', { name: '停止' })).toBeEnabled();
});

test('the appearance switch keeps document color-scheme and meta[name="color-scheme"] in sync across system, light and dark', async ({ page }) => {
  await page.goto(`/operations/control?site=${SITE_ID}`);

  const appearance = page.getByRole('radiogroup', { name: '外观' });
  await expect(page.locator('meta[name="color-scheme"]')).toHaveAttribute('content', 'light dark');

  await appearance.getByRole('radio', { name: '深色' }).click();
  await expect(page.locator('meta[name="color-scheme"]')).toHaveAttribute('content', 'dark');
  await expect.poll(() => page.evaluate(() => getComputedStyle(document.documentElement).colorScheme)).toBe('dark');

  await appearance.getByRole('radio', { name: '浅色' }).click();
  await expect(page.locator('meta[name="color-scheme"]')).toHaveAttribute('content', 'light');
  await expect.poll(() => page.evaluate(() => getComputedStyle(document.documentElement).colorScheme)).toBe('light');

  await appearance.getByRole('radio', { name: '系统' }).click();
  await expect(page.locator('meta[name="color-scheme"]')).toHaveAttribute('content', 'light dark');
});
