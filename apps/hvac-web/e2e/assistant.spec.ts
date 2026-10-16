import { readFileSync } from 'node:fs';
import { SITE_ID } from './gateway';
import { expect, test } from './test';

const recordedAt = JSON.parse(readFileSync(new URL('./fixtures/energy-series.json', import.meta.url), 'utf8')).electricity.metadata.dataWatermark;

test('解释 on the busiest hour opens the assistant with the question, and the answer arrives with its steps, finding and follow-ups', async ({ page, gateway }) => {
  await page.clock.setFixedTime(new Date(recordedAt));
  await page.goto(`/energy-analysis/consumption?site=${SITE_ID}`);

  await page.getByRole('region', { name: '期间用能' }).getByRole('button', { name: '解释 11:00' }).click();
  const panel = page.getByRole('complementary', { name: 'AI 助手' });
  const composer = panel.getByRole('textbox', { name: '向助手提问' });
  // The recording replays one day, so the hour matches yesterday's and the question asks whether it is normal.
  await expect(composer).toHaveValue('2026年10月10日 11:00–12:00 的空调用电 247 kWh，用电正常吗？');
  await panel.getByRole('button', { name: '发送' }).click();

  const finding = panel.getByTestId('assistant-finding');
  await expect(finding).toContainText('多用的电主要来自 2 号机组开机');
  await expect(finding).toContainText('多用电+18kWh');
  await expect(finding).toContainText('平台没有室外温度历史');
  await expect(finding.getByRole('link', { name: '能耗分析' })).toHaveAttribute('href', `/energy-analysis/consumption?site=${SITE_ID}`);
  await expect(panel).toContainText('这一小时比前一天同一时段多用电 18 kWh，主要来自 2 号机组。');

  // The steps fold to one line; opened, each read has its plain name.
  await panel.getByRole('button', { name: '查询了 2 项数据' }).click();
  await expect(panel.getByRole('listitem')).toHaveText(['对比两个时段', '读取各设备用电']);

  const created = gateway.writes.find((write) => write.method === 'POST' && write.path.endsWith('/operations/agent-sessions'));
  expect(created?.body).toEqual({ message: '2026年10月10日 11:00–12:00 的空调用电 247 kWh，用电正常吗？' });
  expect(created?.csrfToken).toBeTruthy();

  // A follow-up continues the same conversation at the Session's current revision.
  await panel.getByTestId('assistant-follow-ups').getByRole('button', { name: '2 号机组为什么开机？' }).click();
  await expect(panel.getByTestId('assistant-finding')).toHaveCount(2);
  const asked = gateway.writes.find((write) => write.path.endsWith(':run'));
  expect(asked?.body).toEqual({ expectedRevision: 2, message: '2 号机组为什么开机？' });

  // With the panel already open, 解释 on a detail row hands over that hour's question.
  const details = page.getByRole('region', { name: '能耗明细' });
  await details.getByRole('tab', { name: '分时段明细' }).click();
  await details.getByRole('button', { name: '解释 10:00' }).click();
  await expect(composer).toHaveValue('2026年10月10日 10:00–11:00 的空调用电 178 kWh，用电正常吗？');
});

test('without an Operations Agent the panel says the assistant is not connected', async ({ page, gateway }) => {
  gateway.assistant.unavailable = true;
  await page.goto(`/energy-analysis/consumption?site=${SITE_ID}`);
  await page.getByRole('button', { name: 'AI 助手' }).click();
  await expect(page.getByTestId('assistant-error')).toHaveText('这个环境还没有接入 AI 助手。');
});
