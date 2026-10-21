import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { explainAlarmQuestion, presentAlarm, presentAlarmRows, presentFeed, type Alarm } from '../miniprogram/lib/alarms.ts';

// Recorded for the web's browser tests: a major alarm still active and unacknowledged (raised
// 9/29 16:26 at the Site), and the same kind raised and cleared on 10/1 after someone acknowledged it.
const [active, cleared] = JSON.parse(readFileSync(new URL('../../hvac-web/e2e/fixtures/alarms.json', import.meta.url), 'utf8')) as Alarm[];
// 1 October at the Site (UTC+8) began at 16:00 UTC the day before; it is now 23:30 there.
const todayStart = '2026-09-30T16:00:00.000Z';
const now = new Date('2026-10-01T15:30:00Z');
const ME = '018f3e00-2000-7000-8000-000000000001';

test('the alarm list puts the most severe first and says when each began or cleared and whether someone is on it', () => {
  assert.deepEqual(presentAlarmRows([active], { unacknowledgedOnly: false }, todayStart), [{
    alarmId: active.alarmId, title: '冷冻水回水温度过低', level: 'major', severity: '重要', detail: '9/29 16:26 开始 · 未确认',
  }]);
  assert.deepEqual(presentAlarmRows([cleared], { unacknowledgedOnly: false }, todayStart).map((row) => row.detail), ['今天 17:23 恢复 · 已确认']);
  assert.deepEqual(presentAlarmRows([cleared, active], { unacknowledgedOnly: true }, todayStart).map((row) => row.alarmId), [active.alarmId]);
  const info = { ...active, alarmId: 'info', currentSeverity: 'INFO' as const, lastOccurredAt: '2026-10-01T15:00:00Z' };
  const critical = { ...active, alarmId: 'critical', currentSeverity: 'CRITICAL' as const };
  assert.deepEqual(presentAlarmRows([info, active, critical], { unacknowledgedOnly: false }, todayStart).map((row) => row.alarmId), ['critical', active.alarmId, 'info']);
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

test('the 通知 page lists the alarms themselves: what is happening, most severe first, then what cleared lately', () => {
  const minor = { ...active, alarmId: 'minor', currentSeverity: 'MINOR' as const, acknowledgement: { acknowledgedAt: '2026-10-01T10:00:00Z', acknowledgedBy: ME } };
  const feed = presentFeed([minor, active], [cleared], todayStart, { unacknowledgedOnly: false });
  assert.deepEqual(feed.sections.map((section) => [section.title, section.caption, section.rows.map((row) => row.alarmId), section.empty]), [
    ['正在发生', '2 条 · 1 条没人确认', [active.alarmId, 'minor'], ''],
    ['最近恢复', '', [cleared.alarmId], ''],
  ]);
  // The tab's badge counts what needs someone: happening and not yet acknowledged.
  assert.equal(feed.needsAttention, 1);

  const unacknowledged = presentFeed([minor, active], [cleared], todayStart, { unacknowledgedOnly: true });
  assert.deepEqual(unacknowledged.sections.map((section) => [section.rows.map((row) => row.alarmId), section.empty]), [
    [[active.alarmId], ''],
    [[], '最近恢复的告警都有人确认过。'],
  ]);
  assert.deepEqual(presentFeed([], [], todayStart, { unacknowledgedOnly: false }).sections.map((section) => section.empty), ['现在没有正在发生的告警。', '最近没有恢复的告警。']);
});

test('without the Site\'s midnight an alarm row says whether it is happening, never a bare 开始', () => {
  // Equally severe, the more recent first: the one cleared on 10/1, then the one active since 9/29.
  assert.deepEqual(presentAlarmRows([active, cleared], { unacknowledgedOnly: false }, null).map((row) => row.detail), ['已恢复 · 已确认', '正在发生 · 未确认']);
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
