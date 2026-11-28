import { askWeChat, gateway, registerSignOutHandler, requireSession } from '../../lib/platform';
import { addDailyReminder, readDailyReminders, type ReminderRow } from '../../lib/reminders';
import { currentSite, siteById, siteSelectionVersion } from '../../lib/site';
import { loadPlant } from '../../lib/plant-load';
import { whileShown } from '../../lib/refresh';
import { presentPlantNow } from '../../lib/run';
import { loadOverview, presentOverview, type OverviewCharts } from '../../lib/overview';
import { selectTab, showAttention } from '../../lib/tab';

const refresher = whileShown(60_000);
let lastSiteId: string | null = null;
let lastSiteVersion = -1;
registerSignOutHandler(() => {
  lastSiteId = null;
  lastSiteVersion = -1;
});

const siteName = async (id: string) => (await siteById(id))?.displayName ?? null;

const clock = (now: Date) => `${String(now.getHours()).padStart(2, '0')}:${String(now.getMinutes()).padStart(2, '0')}`;

Page({
  data: {
    siteName: '',
    updatedAt: '',
    loading: true,
    noSite: false,
    failed: false,
    refreshError: '',
    view: null as ReturnType<typeof presentOverview> | null,
    plant: { failed: false, now: null } as { failed: boolean; now: ReturnType<typeof presentPlantNow> },
    charts: null as OverviewCharts | null,
    daily: null as ReminderRow | null,
    /** The Site 多收一天 adds a morning for: read with the row, so the tap opens WeChat's dialog at once. */
    dailySiteId: '',
    addingDaily: false,
  },

  onShow() {
    if (!requireSession()) return;
    selectTab(this);
    this.refresh();
    this.readDaily();
    refresher.start(() => this.refresh());
  },

  onHide: refresher.stop,

  onUnload: refresher.stop,

  openAlarms() {
    wx.switchTab({ url: '/pages/notifications/index' });
  },

  openRun() {
    wx.switchTab({ url: '/pages/run/index' });
  },

  openSites() {
    wx.navigateTo({ url: '/pages/sites/index' });
  },

  async readDaily() {
    const site = await currentSite().catch(() => null);
    if (site === null) {
      this.setData({ daily: null, dailySiteId: '' });
      return;
    }
    this.setData({ daily: await readDailyReminders(gateway, site, siteName), dailySiteId: site.id });
  },

  async addDaily() {
    if (this.data.addingDaily || !this.data.dailySiteId) return;
    // WeChat's dialog opens first, before anything is awaited.
    const adding = addDailyReminder(gateway, askWeChat, this.data.dailySiteId);
    this.setData({ addingDaily: true });
    const change = await adding;
    this.setData({ addingDaily: false });
    wx.showToast({ title: change.message, icon: change.added ? 'success' : 'none' });
    if (change.added) this.readDaily();
  },

  async onPullDownRefresh() {
    // A pull also reads the Site list again.
    await this.refresh(true);
    wx.stopPullDownRefresh();
  },

  async refresh(rereadSites = false) {
    const version = siteSelectionVersion();
    if (lastSiteId !== null && version !== lastSiteVersion) {
      lastSiteId = null;
      this.setData({ loading: true, noSite: false, failed: false, refreshError: '', siteName: '', updatedAt: '', view: null, plant: { failed: false, now: null }, charts: null, daily: null, dailySiteId: '' });
    }
    try {
      const site = await currentSite(rereadSites);
      lastSiteVersion = siteSelectionVersion();
      if (site === null) {
        lastSiteId = null;
        this.setData({ loading: false, noSite: true, failed: false, refreshError: '', siteName: '', updatedAt: '', view: null, plant: { failed: false, now: null }, charts: null });
        return;
      }
      if (site.id !== lastSiteId) {
        lastSiteId = site.id;
        this.setData({ loading: true, noSite: false, failed: false, refreshError: '', siteName: site.displayName, updatedAt: '', view: null, plant: { failed: false, now: null }, charts: null, daily: null, dailySiteId: '' });
      }
      const { inputs, plant, waiting, charts } = await loadOverview({ gateway, loadPlant }, site, new Date());
      if (waiting !== null) showAttention(this, waiting);
      this.setData({ siteName: site.displayName, updatedAt: clock(new Date()), loading: false, noSite: false, failed: false, refreshError: '', view: presentOverview(inputs), plant, charts });
    } catch {
      // Keep the previously loaded view intact during background polling failures so operators don't lose context.
      if (this.data.view !== null) {
        this.setData({ loading: false, refreshError: '上次刷新失败，当前显示稍早数据，下拉可重试' });
        return;
      }
      // The Site list itself could not be read; signed-out failures already went to the sign-in page.
      this.setData({ loading: false, failed: true, refreshError: '' });
    }
  },
});
