import { SITE_ID } from './gateway';
import { expect, test } from './test';

const ACTIVE_ALARM = '01a0ec45-7ab5-7e9c-aa96-927ee5e050f2';

const rows = (page: import('@playwright/test').Page) => page.getByRole('list', { name: '告警记录' }).getByRole('button');

test('the default view lists active alarms only', async ({ page }) => {
  await page.goto(`/operations/alarms?site=${SITE_ID}`);
  await expect(rows(page)).toHaveCount(1);
  await expect(rows(page).first()).toContainText('冷冻水回水温度过低');
  await expect(rows(page).first()).toContainText('未确认');
  // The first alarm opens beside the list without a click.
  await expect(page.getByRole('region', { name: '告警详情' })).toContainText('活动 · 未确认');

  await page.getByRole('radio', { name: '已恢复' }).click();
  await expect(page).toHaveURL(/view=cleared/);
  await expect(rows(page).first()).toContainText('已恢复');
});

test('acknowledging records the note and leaves the alarm active', async ({ page, gateway }) => {
  await page.goto(`/operations/alarms?site=${SITE_ID}&inspect=${ACTIVE_ALARM}`);
  const detail = page.getByRole('region', { name: '告警详情' });
  await detail.getByRole('button', { name: '确认告警' }).click();
  await page.getByRole('textbox', { name: '确认说明' }).fill('已到现场查看');
  await page.getByRole('button', { name: '确认', exact: true }).click();

  await expect(detail).toContainText('活动 · 已确认');
  await expect(detail).toContainText('已到现场查看');
  await expect(rows(page).first()).toContainText('已确认');
  const ack = gateway.writes.find((write) => write.path === `/api/v1/alarms/${ACTIVE_ALARM}/ack`);
  expect(ack?.body).toEqual({ comment: '已到现场查看' });
  expect(ack?.csrfToken).toBe('e2e-csrf-token');
});

test('a work order created from an alarm references it as its origin', async ({ page, gateway }) => {
  await page.goto(`/operations/alarms?site=${SITE_ID}&inspect=${ACTIVE_ALARM}`);
  const detail = page.getByRole('region', { name: '告警详情' });
  await detail.getByRole('button', { name: '创建工单', exact: true }).click();
  await page.getByLabel('说明').fill('检查冷冻水泵频率与旁通阀');
  await page.getByRole('dialog', { name: '由告警创建工单' }).getByRole('button', { name: '创建工单' }).click();

  await expect(detail.getByRole('link', { name: /冷冻水回水温度过低/ })).toBeVisible();
  const create = gateway.writes.find((write) => write.method === 'POST' && write.path.endsWith('/work-orders'));
  expect(create?.body).toMatchObject({
    priority: 'HIGH',
    sourceReferences: [{ domain: 'ALARM', resourceId: ACTIVE_ALARM, relationship: 'ORIGIN' }],
  });
});

test('search and severity filters narrow the list and say when nothing matches', async ({ page }) => {
  await page.goto(`/operations/alarms?site=${SITE_ID}&view=all`);
  await expect(rows(page)).toHaveCount(2);

  await page.getByRole('textbox', { name: '搜索告警' }).fill('不存在的告警');
  await expect(page).toHaveURL(/q=/);
  await expect(page.getByText('没有符合条件的告警')).toBeVisible();

  await page.getByRole('button', { name: '清除筛选' }).click();
  await expect(rows(page)).toHaveCount(2);

  await page.getByRole('toolbar').getByRole('button', { name: '严重度' }).click();
  await page.getByRole('option', { name: '紧急' }).click();
  await page.keyboard.press('Escape');
  await expect(page.getByText('没有符合条件的告警')).toBeVisible();
});

test('J and K move through the list', async ({ page }) => {
  await page.goto(`/operations/alarms?site=${SITE_ID}&view=all`);
  await expect(rows(page)).toHaveCount(2);
  await page.keyboard.press('j');
  await expect(rows(page).nth(1)).toHaveAttribute('aria-current', 'true');
  await page.keyboard.press('k');
  await expect(rows(page).nth(0)).toHaveAttribute('aria-current', 'true');
});

// #594: 解释 hands the assistant a question about the alarm in view, with its start in Site time.
test('解释 opens the assistant with a question about the alarm in view', async ({ page }) => {
  await page.goto(`/operations/alarms?site=${SITE_ID}&inspect=${ACTIVE_ALARM}`);
  await page.getByRole('region', { name: '告警详情' }).getByRole('button', { name: '解释' }).click();
  const panel = page.getByRole('complementary', { name: 'AI 助手' });
  await expect(panel.getByRole('textbox', { name: '向助手提问' })).toHaveValue('2026年9月29日 16:26 开始的告警「冷冻水回水温度过低」是怎么回事？');
});
