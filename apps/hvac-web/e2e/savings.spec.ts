import { SITE_ID } from './gateway';
import { expect, test } from './test';

const MEASURES = `/api/v1/sites/${SITE_ID}/savings/measures`;

test('a measure is registered on the Site clock and implemented, and a baseline too thin to fit says so instead of verifying', async ({ page, gateway }) => {
  await page.goto(`/energy-analysis/savings?site=${SITE_ID}`);
  await expect(page.getByText('还没有节能措施')).toBeVisible();
  await expect(page.getByRole('region', { name: '节能成效' })).toHaveCount(0);

  await page.getByRole('button', { name: '登记措施' }).first().click();
  await page.getByLabel('名称').fill('冷冻水供水温度由 7 °C 提高到 8 °C');
  await page.getByLabel('计划实施时间').fill('2026-10-12T15:17');
  await page.getByLabel('预期年节电量（kWh，可选）').fill('21000');
  await page.getByLabel('依据说明（可选）').fill('10 月 12 日优化建议');
  await page.getByRole('button', { name: '登记', exact: true }).click();

  const [registered] = gateway.writes.filter((write) => write.path === MEASURES);
  expect(registered.csrfToken).toBe('e2e-csrf-token');
  expect(registered.body).toMatchObject({
    name: '冷冻水供水温度由 7 °C 提高到 8 °C', kind: 'SETPOINT_RESET', plannedAt: '2026-10-12T07:17:00.000Z',
    expectedAnnualKWh: 21000, evidence: '10 月 12 日优化建议',
  });
  const sheet = page.getByRole('dialog');
  await expect(sheet).toContainText('计划中');
  await expect(sheet).toContainText('确认实施后，系统用实施前 14 天的逐时用电与供冷量拟合基线并冻结');

  await sheet.getByRole('button', { name: '确认实施' }).click();
  await page.getByLabel('实施时间').fill('2026-10-12T15:17');
  await page.getByRole('button', { name: '确认实施并拟合基线' }).click();

  const [implemented] = gateway.writes.filter((write) => write.path.endsWith('/implement'));
  expect(implemented.body).toEqual({ implementedAt: '2026-10-12T07:17:00.000Z' });
  await expect(sheet).toContainText('基线不可信，暂不核算');
  await expect(sheet).toContainText('基线期完整小时不足 72 小时');
  const checks = sheet.getByRole('table', { name: '基线检验' });
  await expect(checks.getByRole('row').filter({ hasText: '有效小时' })).toContainText('15 / 336（4%）');
  await expect(checks.getByRole('row').filter({ hasText: 'CV(RMSE)' })).toContainText('未拟合');
  await expect(sheet).toContainText('数据不足，未拟合模型');
  await expect(sheet.getByRole('button', { name: '重新拟合基线' })).toBeVisible();

  await page.keyboard.press('Escape');
  const row = page.getByRole('table', { name: '节能措施列表' }).getByRole('row').filter({ hasText: '冷冻水供水温度' });
  await expect(row).toContainText('基线不可信');
  await expect(page.getByRole('region', { name: '节能成效' })).toContainText('基线不可信，暂不核算');
});

test('only one measure reports on a Site at a time, and the owner says so', async ({ page, gateway }) => {
  await page.goto(`/energy-analysis/savings?site=${SITE_ID}`);
  for (const name of ['提高冷冻水供水温度', '冷却塔风机变频']) {
    await page.getByRole('button', { name: '登记措施' }).first().click();
    await page.getByLabel('名称').fill(name);
    await page.getByRole('button', { name: '登记', exact: true }).click();
    const sheet = page.getByRole('dialog');
    await sheet.getByRole('button', { name: '确认实施' }).click();
    await page.getByRole('button', { name: '确认实施并拟合基线' }).click();
    if (name === '冷却塔风机变频') {
      await expect(page.getByRole('dialog', { name: '确认实施' })).toContainText('同一站点同一时间只能有一项措施在核算');
    } else {
      await expect(sheet).toContainText('基线不可信');
      await page.keyboard.press('Escape');
    }
  }
  expect(gateway.measures.filter((measure) => measure.status === 'IMPLEMENTED')).toHaveLength(1);
});

test('a measure cites the setpoint command it came from, named as the control page names it', async ({ page, gateway }) => {
  await page.goto(`/operations/control?site=${SITE_ID}`);
  const chiller = page.getByRole('region', { name: 'CHILLER-01' });
  await chiller.getByRole('textbox', { name: '冷冻水出水设定温度目标值' }).fill('8');
  await chiller.getByRole('button', { name: '下发' }).first().click();
  await page.getByRole('alertdialog').getByRole('button', { name: '确认下发' }).click();
  await expect(page.getByRole('list').filter({ hasText: '冷冻水出水设定温度' }).getByRole('listitem').first()).toContainText('已执行');

  await page.goto(`/energy-analysis/savings?site=${SITE_ID}`);
  await page.getByRole('button', { name: '登记措施' }).first().click();
  await page.getByLabel('名称').fill('冷冻水供水温度由 7 °C 提高到 8 °C');
  await page.getByLabel('依据指令（可选）').click();
  await page.getByRole('option', { name: /CHILLER-01 冷冻水出水设定温度 → 8\.0 °C/ }).click();
  await page.getByRole('button', { name: '登记', exact: true }).click();

  const [registered] = gateway.writes.filter((write) => write.path === MEASURES);
  expect(registered.body?.evidenceCommandId).toBe(gateway.commands[0].commandId);
  const evidence = page.getByRole('dialog').getByRole('link', { name: /CHILLER-01 冷冻水出水设定温度 → 8\.0 °C/ });
  await expect(evidence).toBeVisible();
  await evidence.click();
  await expect(page).toHaveURL(new RegExp(`/operations/control\?.*inspect=${gateway.commands[0].commandId}`));
});
