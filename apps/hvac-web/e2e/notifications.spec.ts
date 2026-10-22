import { SITE_ID } from './gateway';
import { expect, test } from './test';

const ACTIVE_ALARM = '01a0ec45-7ab5-7e9c-aa96-927ee5e050f2';

test('通知 lists the alarms happening now above the ones cleared lately, and the bell counts those nobody acknowledged', async ({ page }) => {
  await page.goto(`/notifications?site=${SITE_ID}`);

  const list = page.getByRole('list', { name: '告警列表' });
  await expect(list.getByText(/^正在发生/)).toBeVisible();
  await expect(list.getByText(/^最近恢复/)).toBeVisible();
  const rows = list.getByRole('button');
  await expect(rows).toHaveCount(2);
  await expect(rows.first()).toContainText('冷冻水回水温度过低');
  await expect(rows.first()).toContainText('未确认');
  await expect(rows.nth(1)).toContainText('已确认');
  // The first alarm opens beside the list without a click.
  await expect(page.getByRole('region', { name: '告警详情' })).toContainText('活动 · 未确认');
  await expect(page.getByRole('button', { name: '通知，1 条告警没人确认' })).toBeVisible();

  await page.getByRole('radio', { name: '只看未确认' }).click();
  await expect(page).toHaveURL(/unack=true/);
  await expect(rows).toHaveCount(1);
  await expect(list.getByText(/^最近恢复/)).toHaveCount(0);
});

test('acknowledging an alarm from 通知 clears it from the bell', async ({ page, gateway }) => {
  await page.goto(`/notifications?site=${SITE_ID}&inspect=${ACTIVE_ALARM}`);
  const detail = page.getByRole('region', { name: '告警详情' });
  await detail.getByRole('button', { name: '确认告警' }).click();
  await page.getByRole('textbox', { name: '确认说明' }).fill('已到现场查看');
  await page.getByRole('button', { name: '确认', exact: true }).click();

  await expect(detail).toContainText('活动 · 已确认');
  await expect(page.getByRole('button', { name: '通知', exact: true })).toBeVisible();
  expect(gateway.writes.map((write) => write.path)).toContain(`/api/v1/alarms/${ACTIVE_ALARM}/ack`);
});
