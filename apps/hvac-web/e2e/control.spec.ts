import { SITE_ID } from './gateway';
import { expect, test } from './test';

test('an operator sets the chilled water setpoint and approves stopping a pump, each confirmed before it is sent', async ({ page, gateway }) => {
  await page.goto(`/operations/control?site=${SITE_ID}`);

  const chiller = page.getByRole('rowgroup', { name: 'CHILLER-01' });
  const setpointInput = chiller.getByRole('textbox', { name: '冷冻水出水设定温度目标值' });
  await setpointInput.fill('7.5');

  // Confirming an IME candidate with Enter (including Safari's post-compositionend keyCode 229) must not submit.
  await setpointInput.evaluate((element) => {
    element.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter', keyCode: 229, isComposing: true, bubbles: true, cancelable: true }));
    element.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter', keyCode: 229, isComposing: false, bubbles: true, cancelable: true }));
  });
  await expect(page.getByRole('alertdialog')).toHaveCount(0);

  await setpointInput.press('Enter');
  const confirm = page.getByRole('alertdialog');
  await expect(confirm).toContainText('向 CHILLER-01 下发指令？');
  await expect(confirm).toContainText('7.5 °C');
  await expect(confirm).toContainText('可设范围');
  await confirm.getByRole('button', { name: '确认下发' }).click();

  const log = page.getByRole('list', { name: '指令记录' });
  await expect(log.getByRole('listitem').first()).toContainText('CHILLER-01 · 冷冻水出水设定温度 → 7.5 °C');
  await expect(log.getByRole('listitem').first()).toContainText('已执行');
  const submitted = gateway.writes.find((write) => write.path === '/api/v1/commands');
  expect(submitted?.body).toEqual({
    assetId: expect.any(String), commandPointId: expect.any(String), parameters: { setpointC: 7.5 },
  });
  expect(submitted?.csrfToken).toBeTruthy();

  await page.getByRole('rowgroup', { name: 'CHWP-01' }).getByRole('button', { name: '停止' }).click();
  await page.getByRole('alertdialog').getByRole('button', { name: '确认下发' }).click();
  const stop = log.getByRole('listitem').filter({ hasText: '停止冷冻水泵' });
  await expect(stop).toContainText('待审批');
  await expect(page.getByRole('radio', { name: /^待审批\s*1$/ })).toBeVisible();
  await stop.getByRole('button', { name: '批准' }).click();
  await expect(stop).toContainText('已执行');

  await stop.getByRole('button', { name: /停止冷冻水泵/ }).click();
  await expect(page).toHaveURL(new RegExp(`inspect=${gateway.commands.find((command) => command.capability === 'STOP')!.commandId}`));
  const sheet = page.getByRole('dialog');
  await expect(sheet).toContainText('需一人审批 · 1/1');
  await expect(sheet.getByRole('listitem')).toHaveText([/已提交/, /待审批/, /已批准/, /已执行/]);
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
