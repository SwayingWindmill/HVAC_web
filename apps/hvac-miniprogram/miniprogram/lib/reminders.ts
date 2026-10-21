// 通知 → 微信提醒 (#664, docs/specs/miniprogram-wechat.md). WeChat lets the platform send a person
// one subscription message for each time they allow it in WeChat's dialog; the platform counts how
// many are left. A WeChat bound in 我的 is needed first.

import type { Gateway } from './gateway';

/** 微信公众平台 → 订阅消息 → 我的模板: 监控告警通知. */
export const ALARM_TEMPLATE_ID = 'E1fGHBeTTIC0bT2xd6Tb0S61UyvL5krxDZz12wkzkdE';

const PATH = '/api/v1/me/wechat/reminders';

export interface ReminderRow {
  readonly text: string;
  readonly canAdd: boolean;
}

export interface ReminderChange {
  readonly added: boolean;
  readonly message: string;
}

/** wx.requestSubscribeMessage: each template's answer ('accept', 'reject', ...). */
export type AskWeChat = (templates: string[]) => Promise<Record<string, string>>;

export async function readAlarmReminders(gateway: Gateway): Promise<ReminderRow> {
  try {
    const binding = await gateway.get<{ bound: boolean }>('/api/v1/me/wechat');
    if (!binding.bound) return { text: '先在「我的」里绑定微信，才能收告警提醒', canAdd: false };
    const { alarm } = await gateway.get<{ alarm: number }>(PATH);
    return alarm === 0
      ? { text: '还收不到告警提醒：点「多收一条」，在微信里允许', canAdd: true }
      : { text: `还能收到 ${alarm} 条紧急和重要告警的提醒`, canAdd: true };
  } catch {
    return { text: '告警提醒的次数暂时读不到', canAdd: false };
  }
}

/** Asks WeChat first (it must follow the tap directly), and counts the reminder only when allowed. */
export async function addAlarmReminder(gateway: Gateway, ask: AskWeChat): Promise<ReminderChange> {
  let answer: Record<string, string>;
  try {
    answer = await ask([ALARM_TEMPLATE_ID]);
  } catch {
    return { added: false, message: '微信没有弹出订阅窗口，请再点一次' };
  }
  if (answer[ALARM_TEMPLATE_ID] !== 'accept') return { added: false, message: '没有允许，这次不加' };
  try {
    const { alarm } = await gateway.post<{ alarm: number }>(PATH, { kind: 'ALARM' });
    return { added: true, message: `还能收到 ${alarm} 条` };
  } catch {
    return { added: false, message: '平台暂时记不上，请稍后再点一次' };
  }
}
