import assert from 'node:assert/strict';
import test from 'node:test';
import { createGateway, type ChunkedTransport, type Transport } from '../miniprogram/lib/gateway.ts';
import { ALARM_TEMPLATE_ID, DAILY_TEMPLATE_ID, addAlarmReminder, addDailyReminder, readAlarmReminders, readDailyReminders } from '../miniprogram/lib/reminders.ts';

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
  assert.deepEqual(await readAlarmReminders(gatewayAnswering({ ...bound, 'GET /api/v1/me/wechat/reminders': { statusCode: 200, data: { alarm: 3, daily: 0 } } })),
    { text: '还能收 3 条（紧急和重要的新告警）', canAdd: true });
  assert.deepEqual(await readAlarmReminders(gatewayAnswering({ ...bound, 'GET /api/v1/me/wechat/reminders': { statusCode: 200, data: { alarm: 0, daily: 0 } } })),
    { text: '紧急和重要的新告警发到微信', canAdd: true });
  assert.deepEqual(await readAlarmReminders(gatewayAnswering({ 'GET /api/v1/me/wechat': { statusCode: 200, data: { bound: false } } })),
    { text: '先在「我的」里绑定微信，才能收告警提醒', canAdd: false });
  assert.deepEqual(await readAlarmReminders(gatewayAnswering({ ...bound, 'GET /api/v1/me/wechat/reminders': { statusCode: 503, data: { code: 'NOTIFICATION_UNAVAILABLE' } } })),
    { text: '告警提醒的次数暂时读不到', canAdd: false });
});

test('one more reminder is added only when the person allows it in WeChat', async () => {
  const sent: { method: string; url: string; data?: unknown }[] = [];
  const gateway = gatewayAnswering({ 'POST /api/v1/me/wechat/reminders': { statusCode: 200, data: { alarm: 4, daily: 0 } } }, sent);
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

// 总览 → 每日运行结论: a summary of yesterday's energy each morning, for the Site the person was on
// when they last allowed one.
const songyaxuan = { id: '01a14000-1000-7000-8000-000000000001', displayName: '松雅轩' };
const otherSite = '01a14000-1000-7000-8000-000000000002';
const siteName = async (id: string) => (id === otherSite ? '二号站点' : null);

test('the daily summary row says how many mornings are left and for which Site', async () => {
  const answering = (data: unknown) => gatewayAnswering({ ...bound, 'GET /api/v1/me/wechat/reminders': { statusCode: 200, data } });
  assert.deepEqual(await readDailyReminders(answering({ alarm: 0, daily: 3, dailySiteId: songyaxuan.id }), songyaxuan, siteName),
    { text: '还能收 3 天（松雅轩）', canAdd: true });
  assert.deepEqual(await readDailyReminders(answering({ alarm: 0, daily: 2, dailySiteId: otherSite }), songyaxuan, siteName),
    { text: '现在发的是二号站点；点「多收一天」改成本站点', canAdd: true });
  assert.deepEqual(await readDailyReminders(answering({ alarm: 0, daily: 0 }), songyaxuan, siteName),
    { text: '每天 8 点把昨天的用电、用冷和能效发到微信：点「多收一天」，在微信里允许', canAdd: true });
  assert.deepEqual(await readDailyReminders(gatewayAnswering({ 'GET /api/v1/me/wechat': { statusCode: 200, data: { bound: false } } }), songyaxuan, siteName),
    { text: '先在「我的」里绑定微信，才能收每日结论', canAdd: false });
  assert.deepEqual(await readDailyReminders(gatewayAnswering({ ...bound, 'GET /api/v1/me/wechat/reminders': { statusCode: 503, data: { code: 'NOTIFICATION_UNAVAILABLE' } } }), songyaxuan, siteName),
    { text: '每日结论的次数暂时读不到', canAdd: false });
});

test('one more morning is added for this Site only when the person allows it in WeChat', async () => {
  const sent: { method: string; url: string; data?: unknown }[] = [];
  const gateway = gatewayAnswering({ 'POST /api/v1/me/wechat/reminders': { statusCode: 200, data: { alarm: 0, daily: 1, dailySiteId: songyaxuan.id } } }, sent);
  const asked: string[][] = [];
  const allow = async (templates: string[]) => { asked.push(templates); return { [DAILY_TEMPLATE_ID]: 'accept' }; };
  assert.deepEqual(await addDailyReminder(gateway, allow, songyaxuan.id), { added: true, message: '还能收 1 天' });
  assert.deepEqual(asked, [[DAILY_TEMPLATE_ID]]);
  assert.deepEqual(sent, [{ method: 'POST', url: '/api/v1/me/wechat/reminders', data: { kind: 'DAILY', siteId: songyaxuan.id } }]);
  const untouched: { method: string; url: string }[] = [];
  assert.deepEqual(await addDailyReminder(gatewayAnswering({}, untouched), async () => ({ [DAILY_TEMPLATE_ID]: 'reject' }), songyaxuan.id), { added: false, message: '没有允许，这次不加' });
  assert.deepEqual(untouched, []);
});
