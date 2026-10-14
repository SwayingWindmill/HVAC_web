import { readFileSync } from 'node:fs';
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
  await expect(sheet).toContainText('确认实施后，系统用实施前 14 天的逐时用电与供冷量算出对比基线');

  await sheet.getByRole('button', { name: '确认实施' }).click();
  await page.getByLabel('实施时间').fill('2026-10-12T15:17');
  await page.getByRole('button', { name: '确认实施并拟合基线' }).click();

  const [implemented] = gateway.writes.filter((write) => write.path.endsWith('/implement'));
  expect(implemented.body).toEqual({ implementedAt: '2026-10-12T07:17:00.000Z' });
  await expect(sheet).toContainText('对比基线不可靠，暂不统计');
  await expect(sheet).toContainText('对比基线期完整小时不足 72 小时');
  const checks = sheet.getByRole('table', { name: '基线检验' });
  await expect(checks.getByRole('row').filter({ hasText: '数据完整度' })).toContainText('15 / 336（4%）');
  await expect(checks.getByRole('row').filter({ hasText: 'CV(RMSE)' })).toContainText('未拟合');
  await expect(sheet).toContainText('数据不足，未拟合模型');
  await expect(sheet.getByRole('button', { name: '重新拟合基线' })).toBeVisible();

  await page.keyboard.press('Escape');
  const row = page.getByRole('table', { name: '节能措施列表' }).getByRole('row').filter({ hasText: '冷冻水供水温度' });
  await expect(row).toContainText('对比基线不可靠');
  await expect(page.getByRole('region', { name: '节能成效' })).toContainText('1 项对比基线不可靠，暂不统计');
});

test('a measure on the plant meter reports alone, and the owner says so', async ({ page, gateway }) => {
  await page.goto(`/energy-analysis/savings?site=${SITE_ID}`);
  for (const name of ['提高冷冻水供水温度', '冷却塔风机变频']) {
    await page.getByRole('button', { name: '登记措施' }).first().click();
    await page.getByLabel('名称').fill(name);
    await page.getByRole('button', { name: '登记', exact: true }).click();
    const sheet = page.getByRole('dialog');
    await sheet.getByRole('button', { name: '确认实施' }).click();
    await page.getByRole('button', { name: '确认实施并拟合基线' }).click();
    if (name === '冷却塔风机变频') {
      await expect(page.getByRole('dialog', { name: '确认实施' })).toContainText('也不能和单台设备的措施同时统计');
    } else {
      await expect(sheet).toContainText('对比基线不可靠');
      await page.keyboard.press('Escape');
    }
  }
  expect(gateway.measures.filter((measure) => measure.status === 'IMPLEMENTED')).toHaveLength(1);
});

// Each machine's sub-meter is its own boundary: measures on two machines report at once.
test("measures on two machines' sub-meters report side by side", async ({ page, gateway }) => {
  await page.goto(`/energy-analysis/savings?site=${SITE_ID}`);
  for (const [name, machine] of [['冷冻泵降频', 'CHWP-01'], ['冷却塔风机变频', 'CT-01']]) {
    await page.getByRole('button', { name: '登记措施' }).first().click();
    await page.getByLabel('名称').fill(name);
    await page.getByLabel('计量电表').click();
    await page.getByRole('option', { name: `${machine} 电表（IPMVP 选项 B）` }).click();
    await page.getByRole('button', { name: '登记', exact: true }).click();
    const sheet = page.getByRole('dialog');
    await expect(sheet).toContainText(`IPMVP 选项 B · ${machine} 电表`);
    await sheet.getByRole('button', { name: '确认实施' }).click();
    await page.getByRole('button', { name: '确认实施并拟合基线' }).click();
    await expect(sheet).toContainText('对比基线不可靠');
    await page.keyboard.press('Escape');
  }
  const registered = gateway.writes.filter((write) => write.path === MEASURES).map((write) => write.body?.boundaryDeviceId);
  expect(registered.every((id) => typeof id === 'string')).toBe(true);
  expect(new Set(registered).size).toBe(2);
  expect(gateway.measures.filter((measure) => measure.status === 'IMPLEMENTED')).toHaveLength(2);
  const ledger = page.getByRole('table', { name: '节能措施列表' });
  await expect(ledger.getByRole('row', { name: /冷冻泵降频/ })).toContainText('CHWP-01 电表');
  await expect(ledger.getByRole('row', { name: /冷却塔风机变频/ })).toContainText('CT-01 电表');
  await expect(page.getByRole('region', { name: '节能成效' })).toContainText('2 项措施');
});

test('a measure cites the setpoint command it came from, named as the control page names it', async ({ page, gateway }) => {
  await page.goto(`/operations/control?site=${SITE_ID}`);
  const chiller = page.getByRole('rowgroup', { name: 'CHILLER-01' });
  await chiller.getByRole('textbox', { name: '冷冻水出水设定温度目标值' }).fill('8');
  await chiller.getByRole('button', { name: '下发' }).first().click();
  await page.getByRole('alertdialog').getByRole('button', { name: '确认下发' }).click();
  await expect(page.getByRole('list', { name: '指令记录' }).getByRole('listitem').first()).toContainText('已执行');

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

// Recorded on the local stack from replayed history: Site A's setpoint raised to 8 °C on 2026-09-29.
test('a verified measure shows its trusted baseline and what each day saved', async ({ page, gateway }) => {
  const verified = JSON.parse(readFileSync(new URL('./fixtures/savings-measure-verified.json', import.meta.url), 'utf8'));
  gateway.measures.push(verified);
  await page.goto(`/energy-analysis/savings?site=${SITE_ID}&inspect=${verified.id}`);

  const sheet = page.getByRole('dialog');
  await expect(sheet).toContainText('已结束');
  await expect(sheet).toContainText('累计节电373 kWh');
  await expect(sheet).toContainText('节省电费¥353');
  await expect(sheet).toContainText('节电率1.5%');
  const checks = sheet.getByRole('table', { name: '基线检验' });
  await expect(checks.getByRole('row').filter({ hasText: '数据完整度' })).toContainText('336 / 336（100%）');
  await expect(checks.getByLabel('未通过')).toHaveCount(0);
  await expect(sheet.getByLabel('每日节电量')).toBeVisible();
  const days = sheet.getByRole('table', { name: '逐日核算' });
  await expect(days.getByRole('row')).toHaveCount(7);
  await expect(days.getByRole('row').filter({ hasText: '2026-10-02' })).toContainText('81');
  await expect(days.getByRole('row').filter({ hasText: '2026-09-30' })).toContainText('100%');
  await expect(sheet.getByRole('button', { name: '重新拟合基线' })).toHaveCount(0);
});

// Recorded on the local stack from replayed history: Site B's chilled water pump cut from 50 Hz to
// 45 Hz on 2026-09-08 09:00, verified on its sub-meter; moved onto the e2e Site's own CHWP-01.
test("a measure verified on a machine's sub-meter fits that machine against plant cooling", async ({ page, gateway }) => {
  const verified = JSON.parse(readFileSync(new URL('./fixtures/savings-measure-device-verified.json', import.meta.url), 'utf8'));
  gateway.measures.push(verified);
  await page.goto(`/energy-analysis/savings?site=${SITE_ID}&inspect=${verified.id}`);

  const sheet = page.getByRole('dialog');
  await expect(sheet).toContainText('计量电表CHWP-01 电表');
  await expect(sheet).toContainText('IPMVP 选项 B · CHWP-01 电表');
  await expect(sheet).toContainText('设备用电 = 45.0 + 0.000 × 冷站供冷量');
  await expect(sheet).toContainText('累计节电1,646 kWh');
  await expect(sheet).toContainText('节电率27.1%');
  await expect(sheet.getByRole('table', { name: '逐日核算' }).getByRole('row')).toHaveCount(7);
});
