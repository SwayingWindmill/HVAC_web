import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { explainAlarmQuestion, presentAlarm, presentFeed, type Alarm } from '../miniprogram/lib/alarms.ts';

// Recorded for the web's browser tests: a major alarm still active and unacknowledged (raised
// 9/29 16:26 at the Site), and the same kind raised and cleared on 10/1 after someone acknowledged it.
const [active, cleared] = JSON.parse(readFileSync(new URL('../../hvac-web/e2e/fixtures/alarms.json', import.meta.url), 'utf8')) as Alarm[];
// 1 October at the Site (UTC+8) began at 16:00 UTC the day before; it is now 23:30 there.
const todayStart = '2026-09-30T16:00:00.000Z';
const now = new Date('2026-10-01T15:30:00Z');
const ME = '018f3e00-2000-7000-8000-000000000001';

test('通知 lists what is happening in one card, most severe first, each with when it began and a status to scan down', () => {
  const info = { ...active, alarmId: 'info', currentSeverity: 'INFO' as const, lastOccurredAt: '2026-10-01T15:00:00Z' };
  const critical = { ...active, alarmId: 'critical', currentSeverity: 'CRITICAL' as const, acknowledgement: { acknowledgedAt: '2026-10-01T10:00:00Z', acknowledgedBy: ME } };
  const [happening] = presentFeed([info, active, critical], [], todayStart, { unacknowledgedOnly: false }).sections;
  assert.deepEqual([happening.title, happening.count, happening.caption], ['正在发生', '3', '2 条没人确认']);
  assert.deepEqual(happening.groups.map((group) => group.day), ['']);
  assert.deepEqual(happening.groups[0].rows.map((row) => row.alarmId), ['critical', active.alarmId, 'info']);
  assert.deepEqual(happening.groups[0].rows[1], {
    alarmId: active.alarmId, title: '冷冻水回水温度过低', level: 'major', severity: '重要', when: '9/29 16:26 开始',
    status: { label: '未确认', tone: 'waiting' }, count: '', cleared: false,
  });
  assert.deepEqual(happening.groups[0].rows[0].status, { label: '已确认', tone: 'quiet' });
});

test('what cleared lately is grouped by Site day, newest first, each with the time it cleared', () => {
  const earlier = { ...cleared, alarmId: 'earlier', clearedAt: '2026-09-29T02:00:00Z', acknowledgement: null };
  const [, recovered] = presentFeed([], [earlier, cleared], todayStart, { unacknowledgedOnly: false }).sections;
  assert.deepEqual([recovered.title, recovered.count, recovered.caption, recovered.empty], ['最近恢复', '', '', '']);
  // Cleared rows are greyed; only one no one saw keeps a grey 未确认, never the orange of one happening.
  assert.deepEqual(recovered.groups.map((group) => [group.day, group.rows.map((row) => [row.alarmId, row.when, row.status, row.cleared])]), [
    ['今天', [[cleared.alarmId, '17:23 恢复', null, true]]],
    ['9 月 29 日', [['earlier', '10:00 恢复', { label: '未确认', tone: 'quiet' }, true]]],
  ]);
});

// Regression (#677, 2026-10-21): 最近恢复 listed the same alarm cleared at the same minute over and
// over (a dozen 「…读数长时间没有更新」 cleared at 20:47 when the gateway came back).
test('the same alarm cleared at the same minute on the same day is one row that says how many', () => {
  const at = (id: string, title: string, clearedAt: string, acknowledged: boolean) => ({
    ...cleared, alarmId: id, title, clearedAt, acknowledgement: acknowledged ? cleared.acknowledgement : null,
  });
  const [, recovered] = presentFeed([], [
    at('gw-1', '网关上报中断', '2026-10-01T12:48:10Z', true),
    at('pump', '冷冻水泵读数长时间没有更新', '2026-10-01T12:47:20Z', true),
    at('gw-2', '网关上报中断', '2026-10-01T12:48:40Z', false),
    at('gw-3', '网关上报中断', '2026-10-01T12:48:05Z', true),
    at('gw-other-day', '网关上报中断', '2026-09-30T12:48:00Z', true),
  ], todayStart, { unacknowledgedOnly: false }).sections;
  assert.deepEqual(recovered.groups.map((group) => [group.day, group.rows.map((row) => [row.alarmId, row.title, row.when, row.count, row.status?.label ?? ''])]), [
    // Opens the newest; 未确认 when any of them is.
    ['今天', [['gw-2', '网关上报中断', '20:48 恢复', '共 3 条', '未确认'], ['pump', '冷冻水泵读数长时间没有更新', '20:47 恢复', '', '']]],
    ['昨天', [['gw-other-day', '网关上报中断', '20:48 恢复', '', '']]],
  ]);
});

test('only the unacknowledged ones on request; the badge counts what is happening with no one on it; empty sections say so', () => {
  const feed = presentFeed([active], [cleared], todayStart, { unacknowledgedOnly: true });
  assert.equal(feed.needsAttention, 1);
  assert.deepEqual(feed.sections.map((section) => [section.groups.flatMap((group) => group.rows.map((row) => row.alarmId)), section.empty]), [
    [[active.alarmId], ''],
    [[], '最近恢复的告警都有人确认过。'],
  ]);
  assert.deepEqual(presentFeed([], [], todayStart, { unacknowledgedOnly: false }).sections.map((section) => [section.count, section.empty]), [['0', '现在没有正在发生的告警。'], ['', '最近没有恢复的告警。']]);
  // Without the Site's midnight there is no time to give and no day to group by.
  const timeless = presentFeed([active], [cleared], null, { unacknowledgedOnly: false }).sections;
  assert.deepEqual(timeless.map((section) => section.groups.map((group) => [group.day, group.rows.map((row) => row.when)])), [[['', ['']]], [['', ['']]]]);
});

test('an alarm opens on its severity and state, what happened, the facts an operator needs, and who acknowledged it', () => {
  assert.deepEqual(presentAlarm(active, { deviceName: 'C 机组电表', now, todayStart, me: ME }), {
    level: 'major', severity: '重要', state: '正在发生 · 未确认', title: '冷冻水回水温度过低',
    summary: '冷机运行且负荷不少于 360 kW 时，回水温度持续 5 分钟不高于 10.5°C。',
    facts: [
      { label: '设备', value: 'C 机组电表' },
      { label: '开始', value: '9/29 16:26' },
      { label: '已经持续', value: '2 天 7 小时' },
      { label: '报过', value: '1 次' },
    ],
    acknowledgement: null,
    canAcknowledge: true,
    acknowledgeNote: '确认表示有人在处理，告警不会因此消失；读数恢复后它自己恢复。',
  });
  const done = presentAlarm(cleared, { deviceName: null, now, todayStart, me: ME });
  assert.equal(done.state, '已恢复 · 已确认');
  assert.deepEqual(done.facts, [
    { label: '开始', value: '今天 17:11' },
    { label: '恢复', value: '今天 17:23' },
    { label: '持续了', value: '12 分钟' },
    { label: '报过', value: '1 次' },
  ]);
  assert.deepEqual(done.acknowledgement, { who: '我', when: '今天 17:11', comment: '值班人员已到冷站核查' });
  assert.equal(done.canAcknowledge, false);
  // Without the Site's midnight, times are left out rather than read in the phone's time zone.
  assert.deepEqual(presentAlarm(active, { deviceName: null, now, todayStart: null, me: ME }).facts.map((fact) => fact.label), ['已经持续', '报过']);
});

test('问助手 asks about the alarm as the web\'s 解释 does: when it began at the Site, whether it cleared, and its device', () => {
  assert.equal(explainAlarmQuestion(active, 'C 机组电表', todayStart), '2026年9月29日 16:26 开始的告警「冷冻水回水温度过低」（C 机组电表）是怎么回事？');
  assert.equal(explainAlarmQuestion(cleared, null, todayStart), '2026年10月1日 17:11 开始、已恢复的告警「冷冻水回水温度过低」是怎么回事？');
});

test('acknowledging says what it means: someone is on it while the alarm is happening, someone has seen it once it cleared', () => {
  assert.equal(presentAlarm(active, { deviceName: null, now, todayStart, me: ME }).acknowledgeNote, '确认表示有人在处理，告警不会因此消失；读数恢复后它自己恢复。');
  const unacknowledgedClear = { ...cleared, acknowledgement: null };
  assert.equal(presentAlarm(unacknowledgedClear, { deviceName: null, now, todayStart, me: ME }).acknowledgeNote, '这条告警已经恢复了，确认只是记下有人看过。');
});
