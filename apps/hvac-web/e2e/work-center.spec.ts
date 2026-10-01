import { MY_ID, SITE_ID } from './gateway';
import { expect, test } from './test';

const MANUAL_ORDER = '01a0f6dd-3b7f-7c9a-a37a-e8a2f1e71979';
const DEVICE_ORDER = '01a0f7cd-4a64-7602-9a44-51dc14d78d1a';

test('an order is claimed, started and completed with a recorded result', async ({ page, gateway }) => {
  await page.goto(`/operations/work-center?site=${SITE_ID}&inspect=${MANUAL_ORDER}`);
  const inspector = page.getByRole('dialog');
  await expect(inspector.getByRole('heading', { name: '冷却塔 CT-01 布水器季度检查' })).toBeVisible();

  // The owner refuses to start an order nobody is responsible for.
  await expect(inspector.getByRole('button', { name: '开始处理' })).toBeDisabled();
  await inspector.getByRole('button', { name: '由我负责' }).click();
  await expect(inspector).toContainText('负责人我');
  expect(gateway.writes.find((write) => write.path.endsWith(':assign'))?.body).toMatchObject({ assigneeId: MY_ID });

  await inspector.getByRole('button', { name: '开始处理' }).click();
  await expect(inspector).toContainText('处理中');

  await inspector.getByRole('button', { name: '完成' }).click();
  const confirm = inspector.getByRole('button', { name: '确认完成' });
  await expect(confirm).toBeDisabled();
  await inspector.getByPlaceholder(/处理结果/).fill('已清理布水器喷嘴，布水均匀');
  await confirm.click();

  await expect(inspector).toContainText('已完成');
  await expect(inspector).toContainText('已清理布水器喷嘴，布水均匀');
  const complete = gateway.writes.find((write) => write.path.endsWith(':complete'));
  expect(complete?.body?.completionEvidence).toEqual([
    { kind: 'OPERATOR_NOTE', reference: '已清理布水器喷嘴，布水均匀', capturedAt: expect.any(String) },
  ]);
});

test('the open view lists unfinished orders and hides completed ones', async ({ page }) => {
  await page.goto(`/operations/work-center?site=${SITE_ID}`);
  await expect(page.locator('tbody tr')).toHaveCount(2);
  await expect(page.locator('tbody')).not.toContainText('冷冻水回水温度过低');
  await page.getByRole('tab', { name: '已结束' }).click();
  await expect(page.locator('tbody tr')).toHaveCount(1);
  await expect(page.locator('tbody')).toContainText('冷冻水回水温度过低');
});

test('an order raised against equipment links back to the device', async ({ page }) => {
  await page.goto(`/operations/work-center?site=${SITE_ID}&inspect=${DEVICE_ORDER}`);
  await page.getByRole('dialog').getByRole('link', { name: 'CT-01' }).click();
  await expect(page).toHaveURL(/\/operations\/systems-devices\?.*inspect=/);
  await expect(page.getByRole('dialog').getByRole('heading', { name: 'CT-01' })).toBeVisible();
});
