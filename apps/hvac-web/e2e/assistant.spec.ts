import { readFileSync } from 'node:fs';
import { SITE_ID } from './gateway';
import { expect, test } from './test';

const recordedAt = JSON.parse(readFileSync(new URL('./fixtures/energy-series.json', import.meta.url), 'utf8')).electricity.metadata.dataWatermark;

test('解释 on the busiest hour opens the assistant with the question, and the answer arrives with its steps, finding and follow-ups', async ({ page, gateway }) => {
  await page.clock.setFixedTime(new Date(recordedAt));
  await page.goto(`/energy-analysis/consumption?site=${SITE_ID}`);

  await page.getByRole('region', { name: '期间用能' }).getByRole('button', { name: '分析 11:00' }).click();
  const panel = page.getByRole('complementary', { name: 'AI 助手' });
  const composer = panel.getByRole('textbox', { name: '向助手提问' });
  // The recording replays one day, so the hour matches yesterday's and the question asks whether it is normal.
  await expect(composer).toHaveValue('2026年10月10日 11:00–12:00 的空调用电 247 kWh，用电正常吗？');
  await panel.getByRole('button', { name: '发送' }).click();

  const finding = panel.getByTestId('assistant-finding');
  await expect(finding).toContainText('有数据依据');
  await expect(finding).toContainText('多用电+18kWh');
  await expect(finding).toContainText('平台没有室外温度历史');
  // The card charts the read the Agent named (#640).
  await expect(finding.getByRole('figure', { name: '10月9日–10月10日 11:00 用电' })).toBeVisible();
  await expect(finding.getByRole('link', { name: '能耗分析' })).toHaveAttribute('href', `/energy-analysis/consumption?site=${SITE_ID}`);
  await expect(panel).toContainText('这一小时比前一天同一时段多用电 18 kWh，主要来自 2 号机组。');
  // The conclusion is said once, in the answer; the card does not repeat it.
  await expect(finding).not.toContainText('主要来自 2 号机组');

  // The steps fold to one line; opened, each read has its plain name.
  await panel.getByRole('button', { name: '查询了 2 项数据' }).click();
  await expect(panel.getByRole('listitem')).toHaveText(['对比两个时段', '读取各设备用电']);

  const created = gateway.writes.find((write) => write.method === 'POST' && write.path.endsWith('/operations/agent-sessions'));
  expect(created?.body).toEqual({ message: '2026年10月10日 11:00–12:00 的空调用电 247 kWh，用电正常吗？' });
  expect(created?.csrfToken).toBeTruthy();

  // A follow-up under the last answer (#638) continues the same conversation at the Session's current revision.
  const answers = panel.locator('[data-role="assistant"]');
  gateway.assistant.reply = { ...gateway.assistant.reply, finding: { ...gateway.assistant.reply.finding, recommendedNext: ['2 号机组明天几点开机？'] } };
  await answers.last().getByTestId('assistant-follow-ups').getByRole('button', { name: '2 号机组为什么开机？' }).click();
  await expect(panel.getByTestId('assistant-finding')).toHaveCount(2);
  const asked = gateway.writes.find((write) => write.path.endsWith(':run'));
  expect(asked?.body).toEqual({ expectedRevision: 2, message: '2 号机组为什么开机？' });
  // Only the last answer offers follow-ups; the first answer's are gone.
  await expect(answers.first().getByTestId('assistant-follow-ups')).toHaveCount(0);
  await expect(answers.last().getByTestId('assistant-follow-ups').getByRole('button')).toHaveText(['2 号机组明天几点开机？']);

  // With the panel already open, 分析原因 on a detail row hands over that hour's question.
  const details = page.getByRole('region', { name: '能耗明细' });
  await details.getByRole('tab', { name: '逐时明细' }).click();
  await details.getByRole('button', { name: '分析 10:00' }).click();
  await expect(composer).toHaveValue('2026年10月10日 10:00–11:00 的空调用电 178 kWh，用电正常吗？');
});

test('without an Operations Agent the panel says the assistant is not connected', async ({ page, gateway }) => {
  gateway.assistant.unavailable = true;
  await page.goto(`/energy-analysis/consumption?site=${SITE_ID}`);
  await page.getByRole('button', { name: 'AI 助手' }).click();
  await expect(page.getByTestId('assistant-error')).toHaveText('这个环境还没有接入 AI 助手。');
});

test('opening the assistant folds the navigation to icons so the page keeps its width, and closing it unfolds it again', async ({ page }) => {
  await page.goto(`/energy-analysis/consumption?site=${SITE_ID}`);
  const navigation = page.locator('[data-slot="sidebar"][data-state]');
  await expect(navigation).toHaveAttribute('data-state', 'expanded');

  await page.getByRole('button', { name: 'AI 助手' }).click();
  await expect(navigation).toHaveAttribute('data-state', 'collapsed');
  // 1440 less the folded navigation and the 420px panel.
  await expect.poll(() => page.locator('#content').evaluate((element) => element.getBoundingClientRect().width)).toBeGreaterThan(940);

  await page.getByRole('complementary', { name: 'AI 助手' }).getByRole('button', { name: '收起助手' }).click();
  await expect(navigation).toHaveAttribute('data-state', 'expanded');
});

// #639: 历史 lists my conversations on the Site, newest first; an old one opens with its answers and continues.
test('历史 lists my conversations newest first, and an older one opens with its answers and continues', async ({ page, gateway }) => {
  await page.goto(`/energy-analysis/consumption?site=${SITE_ID}`);
  await page.getByRole('button', { name: 'AI 助手' }).click();
  const panel = page.getByRole('complementary', { name: 'AI 助手' });
  const composer = panel.getByRole('textbox', { name: '向助手提问' });
  const askNew = async (question: string) => {
    await panel.getByRole('button', { name: '新对话' }).click();
    await composer.fill(question);
    await panel.getByRole('button', { name: '发送' }).click();
    await expect(panel.getByTestId('assistant-finding')).toHaveCount(1);
  };
  await askNew('10月15日用了多少电？');
  await askNew('10月16日用了多少电？');

  await panel.getByRole('button', { name: '历史对话' }).click();
  const history = panel.getByRole('list', { name: '历史对话' });
  await expect(history.getByRole('listitem')).toHaveCount(2);
  await expect(history.getByRole('listitem').first()).toContainText('10月16日用了多少电？');
  await expect(history.getByRole('listitem').first()).toContainText('答完');
  await expect(history.getByRole('listitem').last()).toContainText('10月15日用了多少电？');

  await history.getByRole('button', { name: '10月15日用了多少电？' }).click();
  await expect(history).toHaveCount(0);
  await expect(panel.locator('[data-role="user"]')).toHaveText(['10月15日用了多少电？']);
  await expect(panel.getByTestId('assistant-finding')).toHaveCount(1);
  // Choosing the conversation already open closes the list as well.
  await panel.getByRole('button', { name: '历史对话' }).click();
  await history.getByRole('button', { name: '10月15日用了多少电？' }).click();
  await expect(history).toHaveCount(0);

  await composer.fill('那 C 机组呢？');
  await panel.getByRole('button', { name: '发送' }).click();
  await expect(panel.getByTestId('assistant-finding')).toHaveCount(2);
  const first = gateway.assistant.sessions.find((session) => session.messages[0].content === '10月15日用了多少电？');
  const continued = gateway.writes.filter((write) => write.path.endsWith(':run')).at(-1);
  expect(continued?.path).toContain(`/${first!.sessionId}:run`);
  expect(continued?.body).toEqual({ expectedRevision: 2, message: '那 C 机组呢？' });
});

// #669: an assistant answer containing a pipe table without its GFM divider row still renders as a <table>.
test('pipe table without a GFM divider row still renders as a table in the assistant panel', async ({ page, gateway }) => {
  const consoleErrors: string[] = [];
  const pageErrors: string[] = [];
  page.on('console', (message) => {
    if (message.type() === 'error') consoleErrors.push(message.text());
  });
  page.on('pageerror', (error) => {
    pageErrors.push(error.message);
  });

  gateway.assistant.reply = {
    tools: ['energy_compare_periods'],
    text: [
      '**今天（10 月 21 日）到 10:41 为止的电费是 230.09 元，比昨天全天的 1423.48 元少，不过今天还没过完，不能直接和昨天比。**\n\n',
      '| 日期 | 用电量 | 估算电费 |\n',
      '| 10 月 20 日（全天） | 1691 kWh | 1423.48 元 |\n',
      '| 10 月 21 日（到 10:41） | 403 kWh | 230.09 元 |',
    ],
    finding: {
      outcome: 'SUPPORTED_FINDING',
      keyFigures: [
        { label: '今日电费', value: '230.09', unit: '元' },
        { label: '昨日全天', value: '1423.48', unit: '元' },
      ],
      evidenceRefs: [],
      limitations: ['今天尚未过完，仅统计到 10:41。'],
      recommendedNext: [],
    },
  };

  await page.goto(`/energy-analysis/consumption?site=${SITE_ID}`);
  await page.getByRole('button', { name: 'AI 助手' }).click();
  const panel = page.getByRole('complementary', { name: 'AI 助手' });
  const composer = panel.getByRole('textbox', { name: '向助手提问' });
  await composer.fill('今天电费比昨天多还是少？');
  await panel.getByRole('button', { name: '发送' }).click();

  await expect(panel.getByTestId('assistant-finding')).toHaveCount(1);
  const answer = panel.locator('[data-role="assistant"]').last();
  await expect(answer).toContainText('今天（10 月 21 日）到 10:41 为止的电费是 230.09 元');

  const tables = answer.locator('table');
  await expect(tables).toHaveCount(1);
  const headers = tables.locator('thead tr th');
  await expect(headers).toHaveCount(3);
  await expect(headers).toHaveText(['日期', '用电量', '估算电费']);
  const rows = tables.locator('tbody tr');
  await expect(rows).toHaveCount(2);
  await expect(rows.nth(0)).toContainText('10 月 20 日（全天）');
  await expect(rows.nth(0)).toContainText('1691 kWh');
  await expect(rows.nth(0)).toContainText('1423.48 元');
  await expect(rows.nth(1)).toContainText('10 月 21 日（到 10:41）');
  await expect(rows.nth(1)).toContainText('403 kWh');
  await expect(rows.nth(1)).toContainText('230.09 元');

  if (process.env.ISSUE_669_SCREENSHOT) {
    await page.screenshot({ path: process.env.ISSUE_669_SCREENSHOT });
  }

  expect(consoleErrors).toEqual([]);
  expect(pageErrors).toEqual([]);
});
