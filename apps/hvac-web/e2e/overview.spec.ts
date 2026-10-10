import { SITE_ID } from './gateway';
import { expect, test } from './test';

test('the overview combines live plant state, today energy and open work, and names what is not integrated', async ({ page }) => {
  await page.goto(`/overview?site=${SITE_ID}`);

  const plant = page.getByRole('region', { name: '冷站概况' });
  await expect(plant).toContainText('冷站实时功率211.9kW瞬时制冷量 864 kW');
  await expect(plant).toContainText('今日空调用电918.9kWh');
  await expect(plant).toContainText('今日冷站综合能效3.48COP');
  await expect(plant).toContainText('主要设备运行4 / 4台运行7 / 7 在线没有故障或离线设备');

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
