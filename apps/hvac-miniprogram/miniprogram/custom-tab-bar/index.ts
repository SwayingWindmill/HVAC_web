// The bottom tab bar (WeChat's custom tabBar), drawn by the design system's 小程序 rules. Each tab
// page marks itself selected in onShow, as WeChat's custom tabBar guide describes.

interface Tab {
  readonly pagePath: string;
  readonly text: string;
  readonly icon: string;
  readonly activeIcon: string;
}

const TABS: readonly Tab[] = [
  { pagePath: '/pages/overview/index', text: '总览', icon: '/assets/tab/overview.svg', activeIcon: '/assets/tab/overview-active.svg' },
  { pagePath: '/pages/run/index', text: '运行', icon: '/assets/tab/run.svg', activeIcon: '/assets/tab/run-active.svg' },
  { pagePath: '/pages/me/index', text: '我的', icon: '/assets/tab/me.svg', activeIcon: '/assets/tab/me-active.svg' },
];

Component({
  data: { tabs: TABS, selected: 0 },
  methods: {
    switchTab(event: WechatMiniprogram.TouchEvent<Record<string, never>, Record<string, never>, { index: number }>) {
      const tab = TABS[event.currentTarget.dataset.index];
      if (tab !== undefined) wx.switchTab({ url: tab.pagePath });
    },
  },
});
