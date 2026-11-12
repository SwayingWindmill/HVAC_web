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
  await expect(energy.getByRole('tab', { name: /今日电费（估算）/ })).toContainText('另有 100 kWh 没有电价，未计入');
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

  await expect(page.getByTestId('data-gap-banner')).toHaveCount(0);

  await alarm.click();
  await expect(page).toHaveURL(/\/operations\/alarms\?.*inspect=/);
  await expect(page.getByRole('region', { name: '告警详情' })).toContainText('冷冻水回水温度过低');
});

test('the overview shows synthesized sub-meter electricity and a data-gap banner linking to meter verification when a sub-meter stops reporting', async ({ page, gateway }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const recordedEnergy = JSON.parse(readFileSync(new URL('./fixtures/energy-series.json', import.meta.url), 'utf8'));
  gateway.dataGaps = [
    {
      alarmId: '01a15000-0000-7000-8000-000000000001',
      deviceId: '01a14000-4000-7000-8000-000000000004',
      deviceName: 'D 机组电表',
      pointId: '01a14000-5000-7000-8000-000000000011',
      pointCode: 'active_energy_total',
      pointName: '有功总电能',
      since: '2026-11-08T08:13:00Z',
      affectedFigures: ['空调用电', '综合能效', '估算电费'],
      excludesDeviceFromTotal: true,
    },
  ];
  gateway.energySeriesHandler = (query) => {
    if (query.energyType !== 'electricity' || query.granularity !== 'hour' || query.breakdown === 'device') {
      return undefined;
    }
    const DAY_MS = 86_400_000;
    const recordedDay = Date.parse('2026-10-09T16:00:00Z');
    const from = Date.parse(query.from);
    const to = Date.parse(query.to);
    const points = [];
    for (let day = from; day < to; day += DAY_MS) {
      const shift = day - recordedDay;
      for (const point of recordedEnergy.electricity.points) {
        points.push({
          ...point,
          periodStart: new Date(Date.parse(point.periodStart) + shift).toISOString(),
          periodEnd: new Date(Date.parse(point.periodEnd) + shift).toISOString(),
          incomplete: true,
          missingMeters: ['D 机组电表'],
        });
      }
    }
    return {
      body: {
        ...recordedEnergy.electricity,
        points,
        metadata: {
          ...recordedEnergy.electricity.metadata,
          synthesized: true,
        },
      },
    };
  };

  await page.goto(`/overview?site=${SITE_ID}`);

  const banner = page.getByTestId('data-gap-banner');
  await expect(banner).toBeVisible();
  await expect(banner).toContainText('D 机组电表 11/8 16:13 起没有读数，今日空调用电不含这台');

  const energy = page.getByRole('region', { name: '今日用能' });
  await expect(energy.getByRole('tab', { name: /今日空调用电/ })).toContainText('918.9kWh');
  await expect(energy.getByRole('tab', { name: /今日空调用电/ })).toContainText('不完整，缺 D 机组电表');

  await banner.getByRole('link', { name: '电表数据核对' }).click();
  await expect(page).toHaveURL(new RegExp(`/settings/integrations\\?.*site=${SITE_ID}.*view=meters|/settings/integrations\\?.*view=meters.*site=${SITE_ID}`));
  await expect(page.getByRole('heading', { name: '集成管理' })).toBeVisible();
  await expect(page.getByRole('tab', { name: '电表数据核对' })).toHaveAttribute('aria-selected', 'true');
  await expect(page.getByRole('row').filter({ hasText: 'METER-HVAC-TOTAL' })).toBeVisible();
});
