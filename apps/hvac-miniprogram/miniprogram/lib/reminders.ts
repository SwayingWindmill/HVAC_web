// WeChat reminders (#664, docs/specs/miniprogram-wechat.md): 通知 → 微信提醒 for new urgent and
// important alarms, and 总览 → 每日运行结论 for yesterday's energy each morning. WeChat lets the
// platform send a person one subscription message for each time they allow it in WeChat's dialog;
// the platform counts how many are left. A WeChat bound in 我的 is needed first.

import type { Gateway } from './gateway';

/** 微信公众平台 → 订阅消息 → 我的模板: 监控告警通知 and 昨日能效数据查询提醒. */
export const ALARM_TEMPLATE_ID = 'E1fGHBeTTIC0bT2xd6Tb0S61UyvL5krxDZz12wkzkdE';
export const DAILY_TEMPLATE_ID = 'mc5IwpxxRkoDZUTyq7vWaPyVtrqlamTccAXaJ3Xkgsc';

const PATH = '/api/v1/me/wechat/reminders';

interface Reminders {
  readonly alarm: number;
  readonly daily: number;
  /** The Site the daily summary is for; absent until one is allowed. */
  readonly dailySiteId?: string;
}

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

/** The reminders left, or null when WeChat is not bound. */
async function remindersLeft(gateway: Gateway): Promise<Reminders | null> {
  const binding = await gateway.get<{ bound: boolean }>('/api/v1/me/wechat');
  return binding.bound ? gateway.get<Reminders>(PATH) : null;
}

export async function readAlarmReminders(gateway: Gateway): Promise<ReminderRow> {
  try {
    const reminders = await remindersLeft(gateway);
    if (reminders === null) return { text: '先在「我的」里绑定微信，才能收告警提醒', canAdd: false };
    return reminders.alarm === 0
      ? { text: '还收不到告警提醒：点「多收一条」，在微信里允许', canAdd: true }
      : { text: `还能收到 ${reminders.alarm} 条紧急和重要告警的提醒`, canAdd: true };
  } catch {
    return { text: '告警提醒的次数暂时读不到', canAdd: false };
  }
}

/** `siteName` names another Site the person can see; the summary goes for the Site chosen last. */
export async function readDailyReminders(gateway: Gateway, site: { readonly id: string; readonly displayName: string }, siteName: (id: string) => Promise<string | null>): Promise<ReminderRow> {
  try {
    const reminders = await remindersLeft(gateway);
    if (reminders === null) return { text: '先在「我的」里绑定微信，才能收每日结论', canAdd: false };
    if (reminders.daily === 0) return { text: '每天 8 点把昨天的用电、用冷和能效发到微信', canAdd: true };
    if (reminders.dailySiteId !== site.id) {
      const other = reminders.dailySiteId === undefined ? null : await siteName(reminders.dailySiteId);
      return { text: `现在发的是${other ?? '另一个站点'}；点「多收一天」改成本站点`, canAdd: true };
    }
    return { text: `还能收 ${reminders.daily} 天（${site.displayName}）`, canAdd: true };
  } catch {
    return { text: '每日结论的次数暂时读不到', canAdd: false };
  }
}

/** Asks WeChat first (it must follow the tap directly), and counts the reminder only when allowed. */
async function addReminder(gateway: Gateway, ask: AskWeChat, template: string, request: object, left: (reminders: Reminders) => string): Promise<ReminderChange> {
  let answer: Record<string, string>;
  try {
    answer = await ask([template]);
  } catch {
    return { added: false, message: '微信没有弹出订阅窗口，请再点一次' };
  }
  if (answer[template] !== 'accept') return { added: false, message: '没有允许，这次不加' };
  try {
    return { added: true, message: left(await gateway.post<Reminders>(PATH, request)) };
  } catch {
    return { added: false, message: '平台暂时记不上，请稍后再点一次' };
  }
}

export const addAlarmReminder = (gateway: Gateway, ask: AskWeChat) =>
  addReminder(gateway, ask, ALARM_TEMPLATE_ID, { kind: 'ALARM' }, (reminders) => `还能收到 ${reminders.alarm} 条`);

/** One more morning's summary, for this Site from now on. */
export const addDailyReminder = (gateway: Gateway, ask: AskWeChat, siteId: string) =>
  addReminder(gateway, ask, DAILY_TEMPLATE_ID, { kind: 'DAILY', siteId }, (reminders) => `还能收 ${reminders.daily} 天`);
