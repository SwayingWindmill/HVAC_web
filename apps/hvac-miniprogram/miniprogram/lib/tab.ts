// The bottom tabs (app.json tabBar lists the same pages), and how a tab page marks itself selected in
// the custom tab bar from onShow, as WeChat's custom tabBar guide has each tab page do.

export interface Tab {
  readonly pagePath: string;
  readonly text: string;
  readonly icon: string;
  readonly activeIcon: string;
}

export const TABS: readonly Tab[] = [
  { pagePath: '/pages/overview/index', text: '总览', icon: '/assets/tab/overview.svg', activeIcon: '/assets/tab/overview-active.svg' },
  { pagePath: '/pages/run/index', text: '运行', icon: '/assets/tab/run.svg', activeIcon: '/assets/tab/run-active.svg' },
  { pagePath: '/pages/me/index', text: '我的', icon: '/assets/tab/me.svg', activeIcon: '/assets/tab/me-active.svg' },
];

export function selectTab(page: WechatMiniprogram.Page.TrivialInstance) {
  const tabBar = typeof page.getTabBar === 'function' ? page.getTabBar() : null;
  tabBar?.setData({ selected: TABS.findIndex((tab) => tab.pagePath === `/${page.route}`) });
}
