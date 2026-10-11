import { SITE_ID } from './gateway';
import { expect, test } from './test';

test('an operator sets the chilled water setpoint and approves stopping a pump, each confirmed before it is sent', async ({ page, gateway }) => {
  await page.goto(`/operations/control?site=${SITE_ID}`);

  const chiller = page.getByRole('region', { name: 'CHILLER-01' });
  await chiller.getByRole('textbox', { name: '冷冻水出水设定温度目标值' }).fill('7.5');
  await chiller.getByRole('button', { name: '下发' }).first().click();
  const confirm = page.getByRole('alertdialog');
  await expect(confirm).toContainText('向 CHILLER-01 下发指令？');
  await expect(confirm).toContainText('7.5 °C');
  await confirm.getByRole('button', { name: '确认下发' }).click();

  const log = page.getByRole('list').filter({ hasText: '冷冻水出水设定温度' });
  await expect(log.getByRole('listitem').first()).toContainText('CHILLER-01 · 冷冻水出水设定温度 → 7.5 °C');
  await expect(log.getByRole('listitem').first()).toContainText('已执行');
  const submitted = gateway.writes.find((write) => write.path === '/api/v1/commands');
  expect(submitted?.body).toEqual({
    assetId: expect.any(String), commandPointId: expect.any(String), parameters: { setpointC: 7.5 },
  });
  expect(submitted?.csrfToken).toBeTruthy();

  await page.getByRole('region', { name: 'CHWP-01' }).getByRole('button', { name: '停止' }).click();
  await page.getByRole('alertdialog').getByRole('button', { name: '确认下发' }).click();
  const stop = log.getByRole('listitem').filter({ hasText: '停止冷冻水泵' });
  await expect(stop).toContainText('待审批');
  await expect(page.getByRole('region', { name: '指令概况' })).toContainText('待审批1');
  await stop.getByRole('button', { name: '批准' }).click();
  await expect(stop).toContainText('已执行');

  await stop.getByRole('button', { name: /停止冷冻水泵/ }).click();
  await expect(page).toHaveURL(/inspect=command-2/);
  const sheet = page.getByRole('dialog');
  await expect(sheet).toContainText('需一人审批 · 1/1');
  await expect(sheet.getByRole('listitem')).toHaveText([/已提交/, /待审批/, /已批准/, /已执行/]);
});
