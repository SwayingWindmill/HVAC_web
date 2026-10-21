import { activeAlarms, clearedAlarms } from '../../lib/alarm-api';
import { presentFeed, type Alarm } from '../../lib/alarms';
import { siteTodayStart } from '../../lib/energy';
import { GatewayError, NETWORK_TEXT } from '../../lib/gateway';
import { gateway, requireSession } from '../../lib/platform';
import { whileShown } from '../../lib/refresh';
import { currentSite } from '../../lib/site';
import { selectTab, showAttention } from '../../lib/tab';

// The Alarm owner has no stream yet; like the web, the page follows it on a short interval while shown.
const refresher = whileShown(30_000);

let read: { active: Alarm[]; cleared: Alarm[]; complete: boolean; todayStart: string | null } | null = null;

Page({
  data: {
    loading: true,
    noSite: false,
    failed: '',
    siteName: '',
    unacknowledgedOnly: false,
    feed: null as ReturnType<typeof presentFeed> | null,
    /** There were more active alarms than were read. */
    partial: false,
  },

  onShow() {
    if (!requireSession()) return;
    selectTab(this);
    this.refresh();
    refresher.start(() => this.refresh());
  },

  onHide: refresher.stop,

  onUnload: refresher.stop,

  async onPullDownRefresh() {
    await this.refresh(true);
    wx.stopPullDownRefresh();
  },

  async refresh(rereadSites = false) {
    try {
      const site = await currentSite(rereadSites);
      if (site === null) {
        this.setData({ loading: false, noSite: true });
        return;
      }
      const [active, cleared, todayStart] = await Promise.all([
        activeAlarms(gateway, site.id),
        clearedAlarms(gateway, site.id),
        // The Site's midnight only dates the alarms; without it they list without times.
        siteTodayStart(site, new Date()).catch(() => null),
      ]);
      read = { active: active.alarms, cleared: cleared.alarms, complete: active.complete, todayStart };
      this.setData({ loading: false, noSite: false, failed: '', siteName: site.displayName });
      this.render();
    } catch (error) {
      this.setData({
        loading: false,
        failed: error instanceof GatewayError && error.code === 'NETWORK_UNAVAILABLE' ? NETWORK_TEXT : '告警暂时读不到，下拉刷新再试。',
      });
    }
  },

  render() {
    if (read === null) return;
    const feed = presentFeed(read.active, read.cleared, read.todayStart, { unacknowledgedOnly: this.data.unacknowledgedOnly });
    this.setData({ feed, partial: !read.complete });
    showAttention(this, feed.needsAttention);
  },

  toggleUnacknowledged(event: WechatMiniprogram.SwitchChange) {
    this.setData({ unacknowledgedOnly: event.detail.value });
    this.render();
  },

  open(event: WechatMiniprogram.TouchEvent<Record<string, never>, Record<string, never>, { id: string }>) {
    wx.navigateTo({ url: `/pages/alarm/index?id=${event.currentTarget.dataset.id}` });
  },
});
