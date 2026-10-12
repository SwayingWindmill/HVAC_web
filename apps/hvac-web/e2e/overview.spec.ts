import { readFileSync } from 'node:fs';
import { SITE_ID } from './gateway';
import { expect, test } from './test';

test('the overview compares today with yesterday, shows the live plant and open work, and names what is not integrated', async ({ page, gateway }) => {
  gateway.optimization = JSON.parse(readFileSync(new URL('./fixtures/optimization-latest.json', import.meta.url), 'utf8'));
  await page.goto(`/overview?site=${SITE_ID}`);

  const energy = page.getByRole('region', { name: '今日用能' });
  await expect(energy.getByRole('tab', { name: /今日空调用电/ })).toHaveAttribute('aria-selected', 'true');
  await expect(energy.getByRole('tab', { name: /今日空调用电/ })).toContainText('918.9kWh');
  await expect(energy.getByRole('tab', { name: /今日综合能效/ })).toContainText('3.48COP');
  await energy.getByRole('tab', { name: /今日综合能效/ }).click();
  await expect(page).toHaveURL(/metric=cop/);
  await expect(energy.getByRole('tab', { name: /今日综合能效/ })).toHaveAttribute('aria-selected', 'true');

  const plant = page.getByRole('region', { name: '冷站实时' });
  await expect(plant).toContainText('211.9kW');
  await expect(plant).toContainText('瞬时制冷量864kW');
  await expect(plant.getByRole('region', { name: '主要设备' })).toContainText('4 / 4 台运行');
  // The gateway is how the plant reaches the platform, not plant equipment.
  await expect(plant).toContainText('8 / 8 在线');
  await expect(plant).toContainText('室内温度24.1°C');

  await expect(page.getByRole('region', { name: '近 7 天逐时用电' })).toContainText('6,433kWh');

  const alarms = page.getByRole('list').filter({ hasText: '冷冻水回水温度过低' });
  await expect(alarms.getByRole('listitem')).toHaveCount(1);
  await expect(alarms.getByRole('link')).toContainText('未确认');

  const findings = page.getByRole('list').filter({ hasText: '冷冻水低温差' });
  await expect(findings.getByRole('listitem')).toHaveCount(1);
  await expect(findings).toContainText('BTU-METER-01');
  await expect(findings).toContainText('3.4 K');

  const work = page.getByRole('list').filter({ hasText: '布水器季度检查' });
  await expect(work.getByRole('listitem')).toHaveCount(2);

  // Savings are the latest recommendation's expectation until a baseline model measures them.
  const saving = page.getByRole('region', { name: '节能机会' });
  await expect(saving).toContainText('51元/天 · 预计');
  await expect(saving).toContainText('预计日节电60 kWh');
  await expect(saving).toContainText('7.1 → 8.1 °C');
  await expect(saving).toContainText('实测节能量需要基线模型核算，尚未接入');

  await alarms.getByRole('link').click();
  await expect(page).toHaveURL(/\/operations\/alarms\?.*inspect=/);
  await expect(page.getByRole('dialog')).toContainText('冷冻水回水温度过低');
});
