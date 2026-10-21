import assert from 'node:assert/strict';
import test from 'node:test';
import { createGateway, type ChunkedTransport, type Transport } from '../miniprogram/lib/gateway.ts';
import { bindWeChat, readWeChat, removeWeChat } from '../miniprogram/lib/wechat.ts';

// 我的 → 微信 (#664, docs/specs/miniprogram-wechat.md): whether this account has WeChat bound,
// binding it with the code from wx.login, and removing it.

const noStreams: ChunkedTransport = () => { throw new Error('not used'); };

/** A Gateway answering each "METHOD path" with a status and body, recording what was sent. */
function gatewayAnswering(answers: Record<string, { statusCode: number; data?: unknown }>, sent: { method: string; url: string; data?: unknown }[] = []) {
  const transport: Transport = async (request) => {
    sent.push({ method: request.method, url: request.url.replace('https://hvac.example.test', ''), data: request.data });
    const answer = answers[`${request.method} ${request.url.replace('https://hvac.example.test', '')}`];
    if (answer === undefined) throw new Error(`unexpected ${request.method} ${request.url}`);
    return { statusCode: answer.statusCode, data: answer.data ?? '' };
  };
  return createGateway({ origin: 'https://hvac.example.test', transport, chunked: noStreams, session: { get: () => 's', set: () => {}, clear: () => {} }, onSignedOut: () => {} });
}

test('the WeChat row says whether this account has WeChat bound and what tapping it does', async () => {
  const unbound = await readWeChat(gatewayAnswering({ 'GET /api/v1/me/wechat': { statusCode: 200, data: { bound: false } } }));
  assert.deepEqual(unbound, { value: '没绑定', action: 'bind', hint: '微信一键登录和告警提醒要先绑定微信' });
  const bound = await readWeChat(gatewayAnswering({ 'GET /api/v1/me/wechat': { statusCode: 200, data: { bound: true, boundAt: '2026-10-21T08:00:00Z' } } }));
  assert.deepEqual(bound, { value: '已绑定', action: 'remove', hint: '' });
  const unreadable = await readWeChat(gatewayAnswering({ 'GET /api/v1/me/wechat': { statusCode: 503, data: { code: 'IDENTITY_UNAVAILABLE' } } }));
  assert.deepEqual(unreadable, { value: '读不到', action: null, hint: '' });
});

test('binding sends only the code from wx.login, and a refusal says in plain words what to do', async () => {
  const sent: { method: string; url: string; data?: unknown }[] = [];
  const gateway = gatewayAnswering({ 'POST /api/v1/me/wechat': { statusCode: 204 } }, sent);
  assert.deepEqual(await bindWeChat(gateway, async () => 'login-code'), { bound: true, message: '已绑定微信' });
  assert.deepEqual(sent, [{ method: 'POST', url: '/api/v1/me/wechat', data: { code: 'login-code' } }]);

  const refusals: Record<string, string> = {
    WECHAT_ALREADY_BOUND: '这个微信已经绑定了另一个账号，先在那个账号里解除。',
    WECHAT_CODE_INVALID: '微信这次没有确认成功，请再点一次。',
    WECHAT_NOT_CONFIGURED: '服务器还没有开通微信功能，请联系管理员。',
    IDENTITY_UNAVAILABLE: '暂时绑定不了，请稍后再试。',
  };
  for (const [code, message] of Object.entries(refusals)) {
    const refused = gatewayAnswering({ 'POST /api/v1/me/wechat': { statusCode: code === 'WECHAT_ALREADY_BOUND' ? 409 : 400, data: { code } } });
    assert.deepEqual(await bindWeChat(refused, async () => 'login-code'), { bound: false, message });
  }
  // wx.login itself failing sends nothing.
  const untouched: { method: string; url: string }[] = [];
  assert.deepEqual(await bindWeChat(gatewayAnswering({}, untouched), async () => { throw new Error('login:fail'); }), { bound: false, message: '微信这次没有确认成功，请再点一次。' });
  assert.deepEqual(untouched, []);
});

test('removing WeChat asks the platform to remove it', async () => {
  const sent: { method: string; url: string }[] = [];
  assert.deepEqual(await removeWeChat(gatewayAnswering({ 'DELETE /api/v1/me/wechat': { statusCode: 204 } }, sent)), { bound: false, message: '已解除绑定' });
  assert.deepEqual(sent.map((request) => `${request.method} ${request.url}`), ['DELETE /api/v1/me/wechat']);
  assert.deepEqual(await removeWeChat(gatewayAnswering({ 'DELETE /api/v1/me/wechat': { statusCode: 503, data: { code: 'IDENTITY_UNAVAILABLE' } } })), { bound: true, message: '暂时解除不了，请稍后再试。' });
});
