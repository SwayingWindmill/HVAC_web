import { askWeChat, gateway, requireSession } from '../../lib/platform';
import { addDailyReminder, readDailyReminders, type ReminderRow } from '../../lib/reminders';
import { currentSite, siteById } from '../../lib/site';
import { loadPlant } from '../../lib/plant-load';
import { whileShown } from '../../lib/refresh';
import { presentPlantNow } from '../../lib/run';
import { loadOverview, presentOverview, type OverviewCharts } from '../../lib/overview';
import { selectTab, showAttention } from '../../lib/tab';

const refresher = whileShown(60_000);

const siteName = async (id: string) => (await siteById(id))?.displayName ?? null;

const clock = (now: Date) => `${String(now.getHours()).padStart(2, '0')}:${String(now.getMinutes()).padStart(2, '0')}`;

Page({
  data: {
    siteName: '',
    updatedAt: '',
    loading: true,
    noSite: false,
    failed: false,
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
    if (site !== null) this.setData({ daily: await readDailyReminders(gateway, site, siteName), dailySiteId: site.id });
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
    try {
      const site = await currentSite(rereadSites);
      if (site === null) {
        this.setData({ loading: false, noSite: true });
        return;
      }
      const { inputs, plant, waiting, charts } = await loadOverview({ gateway, loadPlant }, site, new Date());
      if (waiting !== null) showAttention(this, waiting);
      this.setData({ siteName: site.displayName, updatedAt: clock(new Date()), loading: false, noSite: false, failed: false, view: presentOverview(inputs), plant, charts });
    } catch {
      // The Site list itself could not be read; signed-out failures already went to the sign-in page.
      this.setData({ loading: false, failed: true });
    }
  },
});
