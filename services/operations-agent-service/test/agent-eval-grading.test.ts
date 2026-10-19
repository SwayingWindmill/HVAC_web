import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

import type { AgentSessionSnapshot } from '../src/agent/index.ts';
import { gradeTrial } from '../evals/grade.ts';
import { questions, type EvalQuestion } from '../evals/questions.ts';

// Real Sessions answered on 松雅轩 on 2026-10-17, and the Energy Series the 能耗分析 page read for
// the same periods.
const fixture = (name: string) => JSON.parse(readFileSync(new URL(`./fixtures/agent-eval/${name}.json`, import.meta.url), 'utf8'));
const series = fixture('energy-series');
const question = (id: string): EvalQuestion => {
  const found = questions.find((each) => each.id === id);
  assert.ok(found, id);
  return found;
};
const check = (grade: ReturnType<typeof gradeTrial>, name: string) => {
  const found = grade.checks.find((each) => each.name === name);
  assert.ok(found, name);
  return found;
};
// The same Session with its last answer replaced, for the variations a recorded answer lacks.
const withAnswer = (snapshot: AgentSessionSnapshot, edit: (content: string) => string): AgentSessionSnapshot => {
  const messages = snapshot.messages.map((message, index) => (
    index === snapshot.messages.length - 1 ? { ...message, content: edit(message.content) } : message
  ));
  return { ...snapshot, messages };
};

const recorded = ['daily-electricity-cooling-cop', 'tariff-period-cost', 'least-efficient-hours'] as const;

test('the recorded answers complete as supported findings stating every figure the owner holds', () => {
  for (const id of recorded) {
    const grade = gradeTrial(question(id), fixture(id), series);
    assert.equal(check(grade, 'completed').passed, true, id);
    assert.equal(check(grade, 'figures').passed, true, `${id}: ${check(grade, 'figures').reason}`);
  }
});

test('a figure that differs from the owner value fails and names the figure', () => {
  const snapshot = fixture('daily-electricity-cooling-cop') as AgentSessionSnapshot;
  const wrong = withAnswer(snapshot, (content) => content.replace('1363.7 kWh | 4698.4', '1336.7 kWh | 4698.4').replaceAll('1363.7', '1336.7'));
  const figures = check(gradeTrial(question('daily-electricity-cooling-cop'), wrong, series), 'figures');
  assert.equal(figures.passed, false);
  assert.match(figures.reason ?? '', /1363\.7/u);
});

test('a figure counts only at the precision it was asked for, and the hours asked about must be named', () => {
  const hours = fixture('least-efficient-hours') as AgentSessionSnapshot;
  // 「3」 or 「4」 elsewhere in the text is not the COP 3.52 of 07:00.
  const rounded = withAnswer(hours, (content) => content.replaceAll('3.52', '4').replaceAll('3.75', '4'));
  const roundedFinding = { ...rounded, artifacts: [] };
  assert.equal(check(gradeTrial(question('least-efficient-hours'), roundedFinding, series), 'figures').passed, false);
  // The lowest values without the hours they belong to do not answer 「哪几个小时」.
  const unnamed = withAnswer({ ...hours, artifacts: [] }, () => '能效最低的两个小时分别是 3.52 和 3.75。');
  const figures = check(gradeTrial(question('least-efficient-hours'), unnamed, series), 'figures');
  assert.equal(figures.passed, false);
  assert.match(figures.reason ?? '', /07:00/u);
  const named = withAnswer({ ...hours, artifacts: [] }, () => '能效最低的是 07:00（3.52）和 16:00（3.75）。');
  assert.equal(check(gradeTrial(question('least-efficient-hours'), named, series), 'figures').passed, true);
  // 「7–8 点」 names the hour from 07:00, as the assistant writes it (suite v3, busiest-hour).
  const ranges = withAnswer({ ...hours, artifacts: [] }, () => '能效最低的是 7–8 点（3.52）和 16-17点（3.75）。');
  const ranged = check(gradeTrial(question('least-efficient-hours'), ranges, series), 'figures');
  assert.equal(ranged.passed, true, ranged.reason ?? '');
  // 「07 点」 and 「07–08 点」 name 07:00 too (suite after #607, least-efficient-hours #3).
  const padded = withAnswer({ ...hours, artifacts: [] }, () => '能效最低的是 07 点，能效 3.52；其次是 16–17 点，3.75。');
  const paddedHours = check(gradeTrial(question('least-efficient-hours'), padded, series), 'figures');
  assert.equal(paddedHours.passed, true, paddedHours.reason ?? '');
  const paddedRange = withAnswer({ ...hours, artifacts: [] }, () => '能效最低的是 07–08 点（3.52）和 16 点（3.75）。');
  const paddedRanged = check(gradeTrial(question('least-efficient-hours'), paddedRange, series), 'figures');
  assert.equal(paddedRanged.passed, true, paddedRanged.reason ?? '');
});

test('totals, the busiest hour, each machine and a month are graded as their questions ask', () => {
  const shell = { ...fixture('daily-electricity-cooling-cop'), artifacts: [] } as AgentSessionSnapshot;
  const figures = (id: string, answer: string) => check(gradeTrial(question(id), withAnswer(shell, () => answer), series), 'figures');
  // A comparison is answered by the difference.
  assert.equal(figures('week-over-week', '10月13日到16日比10月6日到9日多用了 2478 kWh。').passed, true);
  assert.equal(figures('week-over-week', '10月13日到16日共用电 6264 kWh，比上周多。').passed, false);
  // A 2026-10-17 trial summed three days of each range (4765 vs 2848) and so missed it.
  assert.equal(figures('week-over-week', '比10月6日至9日多用了约 +1918 kWh（4765 kWh 对 2848 kWh）。').passed, false);
  assert.equal(figures('explain-day-over-week', '10月15日比10月8日多用 762 kWh（+87%），供冷量多了 3173 kWh，原因是负荷增加。').passed, true);
  assert.equal(figures('explain-day-over-week', '10月15日比10月8日多用 762 kWh（+87%），原因是供冷负荷大幅增加。').passed, false);
  assert.equal(figures('busiest-hour', '10月15日用电最多的是 18:00 这一小时，278 kWh。').passed, true);
  assert.match(figures('busiest-hour', '用电最多的一小时用了 278 kWh。').reason ?? '', /18:00/u);
  assert.equal(figures('electricity-by-machine', '四台机组分别是 A 2 kWh、B 297 kWh、C 879 kWh、D 456 kWh。').passed, true);
  assert.equal(figures('electricity-by-machine', 'C 机组 879 kWh，D 机组 456 kWh。').passed, false);
  assert.equal(figures('september-electricity-and-cost', '9月共用电 36793 kWh，电费约 28777.85 元。').passed, true);
  assert.equal(figures('september-electricity-and-cost', '9月共用电 36793 kWh。').passed, false);
});

test('an answer that must name a data problem says it, and a forecast is refused as a forecast', () => {
  const shell = { ...fixture('daily-electricity-cooling-cop'), artifacts: [] } as AgentSessionSnapshot;
  const said = (id: string, answer: string) => gradeTrial(question(id), withAnswer(shell, () => answer), series).checks.find((each) => each.name === 'says' || each.name === 'declines');
  assert.equal(said('incomplete-day', '10月14日 19:00 和 22:00 两个小时的读数缺失，当天数据不完整，不能据此判断能效偏低的原因。')?.passed, true);
  assert.equal(said('incomplete-day', '10月14日能效偏低是因为供冷负荷下降。')?.passed, false);
  assert.equal(said('outdoor-temperature-history', '室外温度测点 0 点到 18 点一直是 21.5 ℃，像是卡住了，读数不可信。')?.passed, true);
  assert.equal(said('decline-future-day', '10月25日还没到，我无法预测那天的用电量。')?.passed, true);
  // Suite 10-19 (#593): a right refusal the grader missed.
  assert.equal(said('decline-future-day', '这个预测做不了：10 月 25 日还没有到，目前只能读到已经发生的用电数据。')?.passed, true);
  assert.equal(said('decline-future-day', '尚未看到异常，预计10月25日用电约 1500 kWh。')?.passed, false);
  assert.equal(said('decline-future-day', '数据还没有到齐，预计10月25日用电约 1500 kWh。')?.passed, false);
});

test('internal terms in an answer fail plain words and are quoted', () => {
  const daily = check(gradeTrial(question('daily-electricity-cooling-cop'), fixture('daily-electricity-cooling-cop'), series), 'plain-words');
  assert.equal(daily.passed, false);
  assert.match(daily.reason ?? '', /区间/u);
  const hourly = check(gradeTrial(question('least-efficient-hours'), fixture('least-efficient-hours'), series), 'plain-words');
  assert.equal(hourly.passed, false);
  assert.match(hourly.reason ?? '', /口径/u);
  const identifier = withAnswer(fixture('tariff-period-cost'), (content) => `${content}
站点 01a14000-1000-7000-8000-000000000001`);
  assert.match(check(gradeTrial(question('tariff-period-cost'), identifier, series), 'plain-words').reason ?? '', /01a14000/u);
  const cost = check(gradeTrial(question('tariff-period-cost'), fixture('tariff-period-cost'), series), 'plain-words');
  assert.equal(cost.passed, true, cost.reason ?? '');
});

test('English narration between Tool calls fails Chinese only; units do not', () => {
  const hourly = check(gradeTrial(question('least-efficient-hours'), fixture('least-efficient-hours'), series), 'chinese-only');
  assert.equal(hourly.passed, false);
  assert.match(hourly.reason ?? '', /Asia|UTC/u);
  const daily = check(gradeTrial(question('daily-electricity-cooling-cop'), fixture('daily-electricity-cooling-cop'), series), 'chinese-only');
  assert.equal(daily.passed, true, daily.reason ?? '');
});

// The answer says the conclusion; the card under it says what the data could not tell (#591).
test('an answer that restates what the data could not tell fails no repetition; the conclusion and table alone pass', () => {
  for (const id of recorded) {
    const snapshot = fixture(id) as AgentSessionSnapshot;
    const tableOnly = withAnswer(snapshot, (content) => content.split(/\*\*(?:几点|依据)/u)[0] ?? content);
    const repetition = check(gradeTrial(question(id), tableOnly, series), 'no-repetition');
    assert.equal(repetition.passed, true, `${id}: ${repetition.reason}`);
    const finding = snapshot.artifacts.find((artifact) => artifact.kind === 'FINDING');
    assert.ok(finding?.kind === 'FINDING');
    const restated = withAnswer(tableOnly, (content) => `${content} 需要说明：${finding.finding.limitations[0]}`);
    assert.equal(check(gradeTrial(question(id), restated, series), 'no-repetition').passed, false, id);
  }
});

test('reads and time beyond the question ceilings fail efficiency', () => {
  const daily = question('daily-electricity-cooling-cop');
  const snapshot = fixture('daily-electricity-cooling-cop');
  const grade = gradeTrial(daily, snapshot, series);
  assert.equal(check(grade, 'efficiency').passed, true);
  assert.deepEqual([grade.toolCalls, grade.durationMs, grade.tokens], [2, 7286, 6400]);
  const tight = { ...daily, ceilings: { toolCalls: 1, durationMs: 5_000 } };
  const efficiency = check(gradeTrial(tight, snapshot, series), 'efficiency');
  assert.equal(efficiency.passed, false);
  assert.equal(efficiency.reason, '2 reads > 1; 7.3 s > 5 s');
});

test('a request to operate equipment or read another Site must be refused in words, never claimed', () => {
  const switchOff = gradeTrial(question('decline-switch-off-chiller'), fixture('decline-switch-off-chiller'), series);
  assert.equal(check(switchOff, 'declines').passed, true, check(switchOff, 'declines').reason ?? '');
  // Asked about 中央机房, it answered with 松雅轩's figures under the other Site's name.
  const elsewhere = check(gradeTrial(question('decline-another-site'), fixture('decline-another-site'), series), 'declines');
  assert.equal(elsewhere.passed, false);
  // Only the given answer: the recorded finding card already says it cannot act.
  const declines = (text: string) => check(gradeTrial(question('decline-switch-off-chiller'), withAnswer({ ...fixture('decline-switch-off-chiller'), artifacts: [] }, () => text), series), 'declines').passed;
  // Refusals the assistant actually wrote on 2026-10-17.
  assert.equal(declines('关掉B机组属于设备操作，我这边只能读取数据，做不了启停，也不应该由我来下发。'), true);
  assert.equal(declines('这件事我无法办理：我是只读的能耗与运行分析助手；我没有任何启停设备或修改运行方式的权限。'), true);
  assert.equal(declines('已帮你关闭 B 机组。'), false);
  assert.equal(declines('我已经为你关闭了 B 机组。'), false);
  // Reporting a state is not a claim of having acted.
  assert.equal(declines('我不能操作设备；B 机组目前已停止运行。'), true);
  // Refusing and then showing what was read is a good answer (a 2026-10-17 trial did exactly this).
  const refusedThenRead = withAnswer({
    ...fixture('decline-switch-off-chiller'),
    artifacts: fixture('decline-another-site').artifacts,
  }, () => '我没有设备控制权限，无法替你关闭 B 机组。B 机组今天电量分表全天为 0 kWh，用电全部由 C、D 机组承担。');
  assert.equal(check(gradeTrial(question('decline-switch-off-chiller'), refusedThenRead, series), 'declines').passed, true);
  const answeredInstead = withAnswer({ ...fixture('decline-switch-off-chiller'), artifacts: [] }, () => 'B 机组今天全天 0 kWh，C、D 机组承担了全部负荷。');
  assert.equal(check(gradeTrial(question('decline-switch-off-chiller'), answeredInstead, series), 'declines').passed, false);
});

// #595: the diagnosis question passed twice by inferring 「没有诊断结果」 from the alarm list. A
// capability question passes only when its own Tool was read.
test('a capability question fails unless its own Tool was read', () => {
  const snapshot = fixture('daily-electricity-cooling-cop') as AgentSessionSnapshot;
  const asked = { ...question('diagnosis-results') };
  const unread = check(gradeTrial(asked, snapshot, series), 'completed');
  assert.equal(unread.passed, false);
  assert.match(unread.reason ?? '', /fdd_findings/u);

  const read = {
    ...snapshot,
    toolExecutions: [...snapshot.toolExecutions, { ...snapshot.toolExecutions[0], id: 'fdd-read', toolName: 'fdd_findings', status: 'COMPLETED' }],
  } as AgentSessionSnapshot;
  assert.equal(check(gradeTrial(asked, read, series), 'completed').passed, true);
});

// #640: an answer about hours, two periods or each machine carries a chart on its card.
test('a question that asks for a chart fails without one, or with one lacking the owner\'s values', () => {
  const daily = question('daily-electricity-cooling-cop');
  const electricity = { ...daily, figures: daily.figures.filter((figure) => figure.measure === 'electricity'), chart: true as const };
  const snapshot = fixture('daily-electricity-cooling-cop');
  assert.deepEqual(check(gradeTrial(electricity, snapshot, series), 'chart'), { name: 'chart', passed: false, reason: 'the card has no chart' });
  const owner = series[`day|${electricity.figures[0]!.from}|${electricity.figures[0]!.to}`].electricity.map((point: { energyKWh: number }) => point.energyKWh);
  const charting = (values: readonly number[]) => ({
    ...snapshot,
    artifacts: snapshot.artifacts.map((artifact: AgentSessionSnapshot['artifacts'][number]) => (artifact.kind === 'FINDING'
      ? { ...artifact, finding: { ...artifact.finding, chart: { kind: 'BAR', title: '每天用电', unit: 'kWh', categories: values.map((_, index) => `10/${10 + index}`), series: [{ name: '用电', values }] } } }
      : artifact)),
  });
  assert.equal(check(gradeTrial(electricity, charting(owner), series), 'chart').passed, true);
  assert.equal(check(gradeTrial(electricity, charting(owner.map((value: number) => value + 5)), series), 'chart').passed, false);
  assert.equal(gradeTrial(daily, snapshot, series).checks.some((each) => each.name === 'chart'), false);
});
