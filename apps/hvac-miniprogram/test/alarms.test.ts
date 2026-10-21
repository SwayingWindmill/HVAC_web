import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { presentAlarm, presentAlarmRows, presentInbox, type Alarm, type InboxItem } from '../miniprogram/lib/alarms.ts';

// Recorded for the web's browser tests: a major alarm still active and unacknowledged (raised
// 9/29 16:26 at the Site), and the same kind raised and cleared on 10/1 after someone acknowledged it.
const [active, cleared] = JSON.parse(readFileSync(new URL('../../hvac-web/e2e/fixtures/alarms.json', import.meta.url), 'utf8')) as Alarm[];
const SITE = '018f3e00-1000-7000-8000-000000000001';
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

test('notifications list this Site\'s alarm news by day, newest first, with what happened and what is still unread', () => {
  const item = (inboxItemId: string, alarm: Alarm, sourceAction: InboxItem['sourceAction'], createdAt: string, status: InboxItem['status'], siteId = SITE): InboxItem => ({
    inboxItemId, intentId: inboxItemId, tenantId: alarm.tenantId, siteId, principalId: ME, alarmId: alarm.alarmId,
    incidentCorrelationId: alarm.incidentCorrelationId, sourceAction, severity: alarm.currentSeverity,
    subject: alarm.title, body: sourceAction === 'CLEARED' ? '回水温度回到正常，这条告警自动恢复了。' : alarm.summary, status, createdAt,
  });
  const inbox = [
    item('a', active, 'CREATED', active.firstOccurredAt, 'UNREAD'),
    item('b', cleared, 'CREATED', cleared.firstOccurredAt, 'READ'),
    item('c', cleared, 'CLEARED', cleared.clearedAt!, 'UNREAD'),
    item('elsewhere', active, 'CREATED', active.firstOccurredAt, 'UNREAD', '018f3e00-1000-7000-8000-000000000002'),
  ];
  const all = presentInbox(inbox, SITE, todayStart, 'all');
  assert.equal(all.unread, 2);
  assert.deepEqual(all.groups.map((group) => [group.day, group.items.map((entry) => [entry.id, entry.tag, entry.time, entry.unread])]), [
    ['今天', [['c', '已恢复', '17:23', true], ['b', '重要 · 新告警', '17:11', false]]],
    ['9 月 29 日', [['a', '重要 · 新告警', '16:26', true]]],
  ]);
  assert.deepEqual(all.groups[0].items[0], {
    id: 'c', alarmId: cleared.alarmId, unread: true, recovered: true, level: 'major', tag: '已恢复', time: '17:23',
    subject: '冷冻水回水温度过低', body: '回水温度回到正常，这条告警自动恢复了。',
  });
  assert.deepEqual(presentInbox(inbox, SITE, todayStart, 'unread').groups.flatMap((group) => group.items.map((entry) => entry.id)), ['c', 'a']);
  assert.equal(presentInbox(inbox.map((entry) => ({ ...entry, status: 'READ' as const })), SITE, todayStart, 'unread').empty, '没有未读的通知。');
});
