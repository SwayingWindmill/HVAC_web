// 我的 → 微信 (#664, docs/specs/miniprogram-wechat.md): the signed-in account's WeChat. Binding
// sends the one-time code from wx.login; the platform trades it for this person's WeChat and
// binds it to the account the mini program is signed in as.

import type { Gateway, GatewayError } from './gateway';

const PATH = '/api/v1/me/wechat';

export interface WeChatRow {
  readonly value: string;
  /** What tapping the row does; none while the binding cannot be read. */
  readonly action: 'bind' | 'remove' | null;
  readonly hint: string;
}

export interface WeChatChange {
  readonly bound: boolean;
  readonly message: string;
}

const CODE_NOT_CONFIRMED = '微信这次没有确认成功，请再点一次。';
const BIND_REFUSALS: Record<string, string> = {
  WECHAT_ALREADY_BOUND: '这个微信已经绑定了另一个账号，先在那个账号里解除。',
  WECHAT_CODE_INVALID: CODE_NOT_CONFIRMED,
  WECHAT_NOT_CONFIGURED: '服务器还没有开通微信功能，请联系管理员。',
};

export async function readWeChat(gateway: Gateway): Promise<WeChatRow> {
  try {
    const binding = await gateway.get<{ bound: boolean }>(PATH);
    return binding.bound
      ? { value: '已绑定', action: 'remove', hint: '' }
      : { value: '没绑定', action: 'bind', hint: '微信一键登录和告警提醒要先绑定微信' };
  } catch {
    return { value: '读不到', action: null, hint: '' };
  }
}

/** `login` is wx.login, answering its one-time code. */
export async function bindWeChat(gateway: Gateway, login: () => Promise<string>): Promise<WeChatChange> {
  let code: string;
  try {
    code = await login();
  } catch {
    return { bound: false, message: CODE_NOT_CONFIRMED };
  }
  try {
    await gateway.post(PATH, { code });
    return { bound: true, message: '已绑定微信' };
  } catch (error) {
    return { bound: false, message: BIND_REFUSALS[(error as GatewayError).code] ?? '暂时绑定不了，请稍后再试。' };
  }
}

export async function removeWeChat(gateway: Gateway): Promise<WeChatChange> {
  try {
    await gateway.remove(PATH);
    return { bound: false, message: '已解除绑定' };
  } catch {
    return { bound: true, message: '暂时解除不了，请稍后再试。' };
  }
}
