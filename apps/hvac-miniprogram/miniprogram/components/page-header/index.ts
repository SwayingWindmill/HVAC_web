// A page's large title on the page itself, lined up with WeChat's capsule button: the window uses
// navigationStyle "custom", so the header keeps clear of the status bar and the capsule.

Component({
  options: { virtualHost: true },
  properties: {
    title: { type: String, value: '' },
  },
  data: { top: 0, height: 32, right: 96 },
  lifetimes: {
    attached() {
      const capsule = wx.getMenuButtonBoundingClientRect();
      const { windowWidth } = wx.getWindowInfo();
      this.setData({ top: capsule.top, height: capsule.height, right: windowWidth - capsule.left + 8 });
    },
  },
});
