import assert from 'node:assert/strict';
import test from 'node:test';
import { createGateway, type ChunkedTransport, type Transport } from '../miniprogram/lib/gateway.ts';
import { ALARM_TEMPLATE_ID, addAlarmReminder, readAlarmReminders } from '../miniprogram/lib/reminders.ts';

// 通知 → 微信提醒 (#664, docs/specs/miniprogram-wechat.md): how many more alarm reminders WeChat
// lets the platform send this person, and one more each time they allow it in WeChat's dialog.

const noStreams: ChunkedTransport = () => { throw new Error('not used'); };

function gatewayAnswering(answers: Record<string, { statusCode: number; data?: unknown }>, sent: { method: string; url: string; data?: unknown }[] = []) {
  const transport: Transport = async (request) => {
    const url = request.url.replace('https://hvac.example.test', '');
    sent.push({ method: request.method, url, data: request.data });
    const answer = answers[`${request.method} ${url}`];
    if (answer === undefined) throw new Error(`unexpected ${request.method} ${url}`);
    return { statusCode: answer.statusCode, data: answer.data ?? '' };
  };
  return createGateway({ origin: 'https://hvac.example.test', transport, chunked: noStreams, session: { get: () => 's', set: () => {}, clear: () => {} }, onSignedOut: () => {} });
}

const bound = { 'GET /api/v1/me/wechat': { statusCode: 200, data: { bound: true } } };

test('the reminder row says how many alarm reminders are left, or what to do first', async () => {
  assert.deepEqual(await readAlarmReminders(gatewayAnswering({ ...bound, 'GET /api/v1/me/wechat/reminders': { statusCode: 200, data: { alarm: 3 } } })),
    { text: '还能收到 3 条紧急和重要告警的提醒', canAdd: true });
  assert.deepEqual(await readAlarmReminders(gatewayAnswering({ ...bound, 'GET /api/v1/me/wechat/reminders': { statusCode: 200, data: { alarm: 0 } } })),
    { text: '还收不到告警提醒：点「多收一条」，在微信里允许', canAdd: true });
  assert.deepEqual(await readAlarmReminders(gatewayAnswering({ 'GET /api/v1/me/wechat': { statusCode: 200, data: { bound: false } } })),
    { text: '先在「我的」里绑定微信，才能收告警提醒', canAdd: false });
  assert.deepEqual(await readAlarmReminders(gatewayAnswering({ ...bound, 'GET /api/v1/me/wechat/reminders': { statusCode: 503, data: { code: 'NOTIFICATION_UNAVAILABLE' } } })),
    { text: '告警提醒的次数暂时读不到', canAdd: false });
});

test('one more reminder is added only when the person allows it in WeChat', async () => {
  const sent: { method: string; url: string; data?: unknown }[] = [];
  const gateway = gatewayAnswering({ 'POST /api/v1/me/wechat/reminders': { statusCode: 200, data: { alarm: 4 } } }, sent);
  const asked: string[][] = [];
  const allow = async (templates: string[]) => { asked.push(templates); return { [ALARM_TEMPLATE_ID]: 'accept' }; };
  assert.deepEqual(await addAlarmReminder(gateway, allow), { added: true, message: '还能收到 4 条' });
  assert.deepEqual(asked, [[ALARM_TEMPLATE_ID]]);
  assert.deepEqual(sent, [{ method: 'POST', url: '/api/v1/me/wechat/reminders', data: { kind: 'ALARM' } }]);

  // Refusing, or WeChat failing to ask, adds nothing.
  const untouched: { method: string; url: string }[] = [];
  const quiet = gatewayAnswering({}, untouched);
  assert.deepEqual(await addAlarmReminder(quiet, async () => ({ [ALARM_TEMPLATE_ID]: 'reject' })), { added: false, message: '没有允许，这次不加' });
  assert.deepEqual(await addAlarmReminder(quiet, async () => { throw new Error('requestSubscribeMessage:fail'); }), { added: false, message: '微信没有弹出订阅窗口，请再点一次' });
  assert.deepEqual(untouched, []);

  // Allowed in WeChat but the platform could not count it.
  const refused = gatewayAnswering({ 'POST /api/v1/me/wechat/reminders': { statusCode: 503, data: { code: 'NOTIFICATION_UNAVAILABLE' } } });
  assert.deepEqual(await addAlarmReminder(refused, allow), { added: false, message: '平台暂时记不上，请稍后再点一次' });
});
