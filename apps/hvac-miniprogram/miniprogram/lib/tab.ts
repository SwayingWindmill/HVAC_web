// The bottom tabs (app.json tabBar lists the same pages), and how a tab page marks itself selected in
// the custom tab bar from onShow, as WeChat's custom tabBar guide has each tab page do.

export interface Tab {
  readonly pagePath: string;
  readonly text: string;
  readonly icon: string;
  readonly activeIcon: string;
  /** The assistant stands out as a raised round button in the middle (the design's 助手 tab). */
  readonly raised: boolean;
  /** The tab that carries the count of unread notifications. */
  readonly showsUnread: boolean;
}

export const TABS: readonly Tab[] = [
  { pagePath: '/pages/overview/index', text: '总览', icon: '/assets/tab/overview.svg', activeIcon: '/assets/tab/overview-active.svg', raised: false, showsUnread: false },
  { pagePath: '/pages/run/index', text: '运行', icon: '/assets/tab/run.svg', activeIcon: '/assets/tab/run-active.svg', raised: false, showsUnread: false },
  { pagePath: '/pages/assistant/index', text: '助手', icon: '/assets/tab/assistant.svg', activeIcon: '/assets/tab/assistant.svg', raised: true, showsUnread: false },
  { pagePath: '/pages/notifications/index', text: '通知', icon: '/assets/tab/notifications.svg', activeIcon: '/assets/tab/notifications-active.svg', raised: false, showsUnread: true },
  { pagePath: '/pages/me/index', text: '我的', icon: '/assets/tab/me.svg', activeIcon: '/assets/tab/me-active.svg', raised: false, showsUnread: false },
];

// Each tab page has its own tab bar; the last unread count read is shown on all of them.
let unread = 0;

const tabBarOf = (page: WechatMiniprogram.Page.TrivialInstance) => (typeof page.getTabBar === 'function' ? page.getTabBar() : null);

/** The badge text: none for nothing unread, 99+ beyond two digits. */
const badge = (count: number) => (count === 0 ? '' : count > 99 ? '99+' : String(count));

export function selectTab(page: WechatMiniprogram.Page.TrivialInstance) {
  tabBarOf(page)?.setData({ selected: TABS.findIndex((tab) => tab.pagePath === `/${page.route}`), badge: badge(unread) });
}

/** Records how many notifications are unread on this Site and shows it on the 通知 tab. */
export function showUnread(page: WechatMiniprogram.Page.TrivialInstance, count: number) {
  unread = count;
  tabBarOf(page)?.setData({ badge: badge(count) });
}
