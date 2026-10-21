// The bottom tabs (app.json tabBar lists the same pages), and how a tab page marks itself selected in
// the custom tab bar from onShow, as WeChat's custom tabBar guide has each tab page do.

export interface Tab {
  readonly pagePath: string;
  readonly text: string;
  readonly icon: string;
  readonly activeIcon: string;
  /** The assistant stands out as a raised round button (the design's 助手 tab). */
  readonly raised: boolean;
}

export const TABS: readonly Tab[] = [
  { pagePath: '/pages/overview/index', text: '总览', icon: '/assets/tab/overview.svg', activeIcon: '/assets/tab/overview-active.svg', raised: false },
  { pagePath: '/pages/run/index', text: '运行', icon: '/assets/tab/run.svg', activeIcon: '/assets/tab/run-active.svg', raised: false },
  { pagePath: '/pages/assistant/index', text: '助手', icon: '/assets/tab/assistant.svg', activeIcon: '/assets/tab/assistant.svg', raised: true },
  { pagePath: '/pages/me/index', text: '我的', icon: '/assets/tab/me.svg', activeIcon: '/assets/tab/me-active.svg', raised: false },
];

export function selectTab(page: WechatMiniprogram.Page.TrivialInstance) {
  const tabBar = typeof page.getTabBar === 'function' ? page.getTabBar() : null;
  tabBar?.setData({ selected: TABS.findIndex((tab) => tab.pagePath === `/${page.route}`) });
}
