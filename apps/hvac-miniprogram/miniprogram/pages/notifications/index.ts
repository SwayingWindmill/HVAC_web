import { activeAlarms, listInbox, markRead } from '../../lib/alarm-api';
import { countText, presentInbox, type InboxItem, type InboxView } from '../../lib/alarms';
import { siteTodayStart } from '../../lib/energy';
import { GatewayError, NETWORK_TEXT } from '../../lib/gateway';
import { gateway, requireSession } from '../../lib/platform';
import { whileShown } from '../../lib/refresh';
import { currentSite, type Site } from '../../lib/site';
import { selectTab, showUnread } from '../../lib/tab';

// The Alarm owner has no stream yet; like the web, the page follows it on a short interval while shown.
const refresher = whileShown(30_000);

let site: Site | null = null;
let inbox: InboxItem[] = [];
let inboxComplete = true;
let todayStart: string | null = null;
let chosenView: InboxView | null = null;

const notificationError = (error: unknown) => (error instanceof GatewayError && error.code === 'NETWORK_UNAVAILABLE'
  ? NETWORK_TEXT
  : '通知暂时读不到，下拉刷新再试。');

Page({
  data: {
    loading: true,
    noSite: false,
    failed: '',
    view: 'unread' as InboxView,
    inbox: null as ReturnType<typeof presentInbox> | null,
    /** Active alarms on the Site: a count, with + when not all were read, or a dash when they could not be read. */
    active: '',
    marking: false,
    /** The inbox held more notifications than were read, so older ones on this Site may be missing. */
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
      const visible = await currentSite(rereadSites);
      if (visible === null) {
        this.setData({ loading: false, noSite: true });
        return;
      }
      site = visible;
      const [items, alarms, start] = await Promise.allSettled([listInbox(gateway), activeAlarms(gateway, visible.id), siteTodayStart(visible, new Date())]);
      if (items.status === 'rejected') {
        this.setData({ loading: false, failed: notificationError(items.reason) });
        return;
      }
      inbox = items.value.items;
      inboxComplete = items.value.complete;
      // The Site's midnight only places notifications on days and clocks; without it they list without times.
      todayStart = start.status === 'fulfilled' ? start.value : null;
      const active = alarms.status === 'fulfilled' ? countText(alarms.value) : '—';
      this.setData({ loading: false, noSite: false, failed: '', active });
      this.render();
    } catch (error) {
      this.setData({ loading: false, failed: notificationError(error) });
    }
  },

  render() {
    if (site === null) return;
    const unread = presentInbox(inbox, site.id, todayStart, 'unread');
    // Opened the first time, the page shows what is unread, or everything when nothing is.
    const view = chosenView ?? (unread.unread > 0 ? 'unread' : 'all');
    this.setData({ view, inbox: view === 'unread' ? unread : presentInbox(inbox, site.id, todayStart, 'all'), partial: !inboxComplete });
    showUnread(this, unread.unread);
  },

  chooseView(event: WechatMiniprogram.TouchEvent<Record<string, never>, Record<string, never>, { view: InboxView }>) {
    chosenView = event.currentTarget.dataset.view;
    this.render();
  },

  /** Marks the notification read, then opens its alarm, so coming back shows it read. */
  async open(event: WechatMiniprogram.TouchEvent<Record<string, never>, Record<string, never>, { id: string; alarm: string; unread: boolean }>) {
    const { id, alarm, unread } = event.currentTarget.dataset;
    if (unread) {
      try {
        const read = await markRead(gateway, id);
        inbox = inbox.map((item) => (item.inboxItemId === id ? read : item));
        this.render();
      } catch {
        wx.showToast({ title: '没能标为已读', icon: 'none' });
      }
    }
    wx.navigateTo({ url: `/pages/alarm/index?id=${alarm}` });
  },

  async markAllRead() {
    if (site === null || this.data.marking) return;
    const siteId = site.id;
    const unread = inbox.filter((item) => item.siteId === siteId && item.status === 'UNREAD');
    if (unread.length === 0) return;
    this.setData({ marking: true });
    const results = await Promise.allSettled(unread.map((item) => markRead(gateway, item.inboxItemId)));
    const read = new Map(results.flatMap((result) => (result.status === 'fulfilled' ? [[result.value.inboxItemId, result.value] as const] : [])));
    inbox = inbox.map((item) => read.get(item.inboxItemId) ?? item);
    this.setData({ marking: false });
    if (read.size < unread.length) wx.showToast({ title: `${unread.length - read.size} 条没能标为已读`, icon: 'none' });
    this.render();
  },

  openAlarms() {
    wx.navigateTo({ url: '/pages/alarms/index' });
  },
});
