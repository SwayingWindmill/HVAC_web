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
  // The owner's price: peak hours at 1.20 and flat hours at 0.72 元/kWh, 100 kWh of 13:00 unpriced.
  await expect(energy.getByRole('tab', { name: /今日电费（估算）/ })).toContainText('745元');
  await energy.getByRole('tab', { name: /今日综合能效/ }).click();
  await expect(page).toHaveURL(/metric=cop/);
  await expect(energy.getByRole('tab', { name: /今日综合能效/ })).toHaveAttribute('aria-selected', 'true');

  const plant = page.getByRole('region', { name: '冷站实时' });
  await expect(plant).toContainText('211.9kW');
  await expect(plant).toContainText('瞬时制冷量864kW');
  await expect(plant.getByRole('region', { name: '主要设备' })).toContainText('4 / 4 台运行');
  // The gateway is how the plant reaches the platform, not plant equipment.
  await expect(plant).toContainText('8 / 8 在线');
  await expect(plant).toContainText('24.1°C');

  await expect(page.getByRole('region', { name: '近 7 天逐时用电' })).toContainText('6,433kWh');

  // Alarms, findings and work orders wait in one list, most severe first.
  const queue = page.getByRole('region', { name: '需要处理' });
  await expect(queue.getByRole('tab', { name: /告警/ })).toContainText('1');
  await expect(queue.getByRole('tab', { name: /诊断/ })).toContainText('1');
  await expect(queue.getByRole('tab', { name: /工单/ })).toContainText('2');
  const alarm = queue.getByRole('link').filter({ hasText: '冷冻水回水温度过低' });
  await expect(alarm).toContainText('未确认');
  const finding = queue.getByRole('link').filter({ hasText: '冷冻水低温差' });
  await expect(finding).toContainText('BTU-METER-01');
  await expect(finding).toContainText('3.4 K');
  await queue.getByRole('tab', { name: /工单/ }).click();
  await expect(queue.getByRole('listitem')).toHaveCount(2);
  await expect(queue).toContainText('布水器季度检查');
  await queue.getByRole('tab', { name: /全部/ }).click();

  // Verified savings come from the measure in verification; the recommendation stays an estimate beside them.
  const saving = page.getByRole('region', { name: '节能成效' });
  await expect(saving).toContainText('还没有在统计的节能措施');
  await expect(saving).toContainText('51 元/天');
  await expect(saving).toContainText('预计节电 60 kWh/天');
  await expect(saving).toContainText('冷冻水供水 7.1 → 8.1 °C');

  await alarm.click();
  await expect(page).toHaveURL(/\/operations\/alarms\?.*inspect=/);
  await expect(page.getByRole('region', { name: '告警详情' })).toContainText('冷冻水回水温度过低');
});
