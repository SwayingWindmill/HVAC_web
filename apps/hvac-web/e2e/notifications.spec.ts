import { expect, test } from './test';

test('an unread notification opens beside the inbox with its alarm as it stands now, and reading it clears the bell', async ({ page, gateway }) => {
  const [unread] = gateway.notifications;
  await page.goto('/notifications');

  const inbox = page.getByRole('list', { name: '通知列表' });
  await expect(inbox.getByRole('button', { name: unread.subject })).toHaveAttribute('aria-current', 'true');
  const pane = page.getByRole('region', { name: '通知内容' });
  await expect(pane).toContainText('未读');
  await expect(pane).toContainText('来源告警当前状态');
  await expect(pane.getByRole('link').filter({ hasText: '持续' })).toHaveAttribute('href', new RegExp(`inspect=${unread.alarmId}`));
  await expect(page.getByRole('button', { name: '通知中心，1 条未读' })).toBeVisible();

  await pane.getByRole('button', { name: '标记已读', exact: true }).click();
  await expect(page.getByText('没有未读通知')).toBeVisible();
  expect(gateway.writes.map((write) => write.path)).toContain(`/api/v1/notifications/inbox/${unread.inboxItemId}/read`);
  await expect(page.getByRole('button', { name: '通知中心', exact: true })).toBeVisible();

  await page.getByRole('radio', { name: /^全部/ }).click();
  await expect(page.getByRole('list', { name: '通知列表' }).getByRole('button')).toHaveCount(gateway.notifications.length);
});
