// Marks a tab page selected in the custom tab bar, as WeChat's custom tabBar guide has each tab
// page do in onShow.
export function selectTab(page: WechatMiniprogram.Page.TrivialInstance, index: number) {
  const tabBar = typeof page.getTabBar === 'function' ? page.getTabBar() : null;
  tabBar?.setData({ selected: index });
}
