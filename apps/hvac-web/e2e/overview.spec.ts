import { SITE_ID } from './gateway';
import { expect, test } from './test';

test('the overview compares today with yesterday, shows the live plant and open work, and names what is not integrated', async ({ page }) => {
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
  await expect(plant).toContainText('7 / 7 在线');

  await expect(page.getByRole('region', { name: '近 7 天逐时用电' })).toContainText('6,433kWh');

  const alarms = page.getByRole('list').filter({ hasText: '冷冻水回水温度过低' });
  await expect(alarms.getByRole('listitem')).toHaveCount(1);
  await expect(alarms.getByRole('link')).toContainText('未确认');

  const work = page.getByRole('list').filter({ hasText: '布水器季度检查' });
  await expect(work.getByRole('listitem')).toHaveCount(2);

  await expect(page.getByRole('region', { name: '节能成效' })).toContainText('未接入');

  await alarms.getByRole('link').click();
  await expect(page).toHaveURL(/\/operations\/alarms\?.*inspect=/);
  await expect(page.getByRole('dialog')).toContainText('冷冻水回水温度过低');
});
