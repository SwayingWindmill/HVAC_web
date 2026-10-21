// The bottom tab bar (WeChat's custom tabBar), drawn by the design system's 小程序 rules. Each tab
// page marks itself selected in onShow (lib/tab.ts selectTab).

import { TABS } from '../lib/tab';

Component({
  data: { tabs: TABS, selected: 0, badge: '' },
  lifetimes: {
    // Each tab page gets its own tab bar; it starts on its own page, also when the devtools re-create it.
    attached() {
      const pages = getCurrentPages();
      const route = pages[pages.length - 1]?.route;
      this.setData({ selected: Math.max(0, TABS.findIndex((tab) => tab.pagePath === `/${route}`)) });
    },
  },
  methods: {
    switchTab(event: WechatMiniprogram.TouchEvent<Record<string, never>, Record<string, never>, { index: number }>) {
      const tab = TABS[event.currentTarget.dataset.index];
      if (tab !== undefined) wx.switchTab({ url: tab.pagePath });
    },
  },
});
