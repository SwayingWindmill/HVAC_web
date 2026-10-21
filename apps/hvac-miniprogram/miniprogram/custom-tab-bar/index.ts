// The bottom tab bar (WeChat's custom tabBar), drawn by the design system's 小程序 rules. Each tab
// page marks itself selected in onShow (lib/tab.ts selectTab).

import { TABS } from '../lib/tab';

Component({
  data: { tabs: TABS, selected: 0 },
  methods: {
    switchTab(event: WechatMiniprogram.TouchEvent<Record<string, never>, Record<string, never>, { index: number }>) {
      const tab = TABS[event.currentTarget.dataset.index];
      if (tab !== undefined) wx.switchTab({ url: tab.pagePath });
    },
  },
});
