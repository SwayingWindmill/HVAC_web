import { fetchSiteAutoControlLease, presentAutoControlLease, type AutoControlLeaseBanner } from '../../lib/control';
import { energySeries, siteTodayStart } from '../../lib/energy';
import type { Plant } from '../../lib/plant';
import { loadPlant } from '../../lib/plant-load';
import { gateway, registerSignOutHandler, requireSession } from '../../lib/platform';
import { whileShown } from '../../lib/refresh';
import { hourlyCop, presentDevices, presentRealtime, type DeviceFilter } from '../../lib/run';
import { siteClock } from '../../lib/today';
import { currentSite, siteSelectionVersion, type Site } from '../../lib/site';
import { selectTab } from '../../lib/tab';

// The web polls current values every 30 seconds when its push stream is not open; so does the phone.
const refresher = whileShown(30_000);

// The last plant read, so switching filters needs no request.
let plant: Plant | null = null;
let lastSiteId: string | null = null;
let lastSiteVersion = -1;
registerSignOutHandler(() => {
  lastSiteId = null;
  lastSiteVersion = -1;
  plant = null;
});

type Chart = ReturnType<typeof hourlyCop>;

/** Today's hourly efficiency; the note says why there is none. */
async function loadChart(site: Site, dayStart: PromiseSettledResult<string | null>, now: Date): Promise<{ chart: Chart | null; note: string }> {
  if (dayStart.status === 'rejected') return { chart: null, note: '今天的能效暂时读不到。' };
  const start = dayStart.value;
  if (start === null) return { chart: null, note: '这个站点今天和昨天都没有用电读数。' };
  const window = { from: start, to: now.toISOString() };
  try {
    const [electricity, cooling] = await Promise.all([energySeries(site, 'electricity', 'hour', window), energySeries(site, 'cooling', 'hour', window)]);
    if (electricity.length === 0 && cooling.length === 0) {
      return { chart: null, note: '这个站点今天还没有用电读数。' };
    }
    return { chart: hourlyCop(electricity, cooling, start, now), note: '' };
  } catch {
    return { chart: null, note: '今天的能效暂时读不到。' };
  }
}

type Segment = 'realtime' | 'devices';

Page({
  data: {
    siteName: '',
    updatedAt: '',
    segment: 'realtime' as Segment,
    filter: 'all' as DeviceFilter,
    loading: true,
    noSite: false,
    failed: false,
    refreshError: '',
    realtime: null as ReturnType<typeof presentRealtime> | null,
    devices: null as ReturnType<typeof presentDevices> | null,
    deviceCount: 0,
    chart: null as Chart | null,
    chartNote: '',
    autoLeaseBanner: null as AutoControlLeaseBanner | null,
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
    await this.refresh({ rereadEquipment: true });
    wx.stopPullDownRefresh();
  },

  /** `rereadEquipment` also re-reads the Sites and their registered equipment, which only change when an administrator edits them. */
  async refresh({ rereadEquipment = false } = {}) {
    const version = siteSelectionVersion();
    if (lastSiteId !== null && version !== lastSiteVersion) {
      lastSiteId = null;
      plant = null;
      this.setData({ loading: true, noSite: false, failed: false, refreshError: '', siteName: '', updatedAt: '', realtime: null, devices: null, deviceCount: 0, chart: null, chartNote: '', autoLeaseBanner: null });
    }
    try {
      const site = await currentSite(rereadEquipment);
      lastSiteVersion = siteSelectionVersion();
      if (site === null) {
        lastSiteId = null;
        plant = null;
        this.setData({ loading: false, noSite: true, failed: false, refreshError: '', siteName: '', updatedAt: '', realtime: null, devices: null, deviceCount: 0, chart: null, chartNote: '', autoLeaseBanner: null });
        return;
      }
      if (site.id !== lastSiteId) {
        lastSiteId = site.id;
        plant = null;
        this.setData({ loading: true, noSite: false, failed: false, refreshError: '', siteName: site.displayName, updatedAt: '', realtime: null, devices: null, deviceCount: 0, chart: null, chartNote: '', autoLeaseBanner: null });
      }
      const now = new Date();
      const [plantRead, dayStart, leaseRead] = await Promise.allSettled([
        loadPlant(site, rereadEquipment),
        siteTodayStart(site, now),
        fetchSiteAutoControlLease(gateway, site.id),
      ]);
      if (plantRead.status === 'rejected') {
        if (this.data.realtime !== null) {
          this.setData({ loading: false, refreshError: '上次刷新失败，当前显示稍早数据，下拉可重试' });
          return;
        }
        plant = null;
        this.setData({ siteName: site.displayName, loading: false, noSite: false, failed: true, refreshError: '', autoLeaseBanner: null });
        return;
      }
      plant = plantRead.value;
      const { chart, note } = await loadChart(site, dayStart, now);
      const start = dayStart.status === 'fulfilled' ? dayStart.value : null;
      const leaseState = leaseRead.status === 'fulfilled' ? leaseRead.value : null;
      const devices = presentDevices(plant, this.data.filter);
      this.setData({
        siteName: site.displayName,
        // Site clock time needs the Site's midnight; without one the time is left out rather than guessed.
        updatedAt: plant.latestSampleAt !== null && start !== null ? siteClock(plant.latestSampleAt, start) : '',
        loading: false,
        noSite: false,
        failed: false,
        refreshError: '',
        realtime: presentRealtime(plant),
        devices,
        // The 设备 tab counts what its list shows: 全部, without the gateway's modules.
        deviceCount: devices.filters[0].count,
        chart,
        chartNote: note,
        autoLeaseBanner: presentAutoControlLease(leaseState, now, site.timezone),
      });
    } catch {
      if (this.data.realtime !== null) {
        this.setData({ loading: false, refreshError: '上次刷新失败，当前显示稍早数据，下拉可重试' });
        return;
      }
      // The Site list itself could not be read; signed-out failures already went to the sign-in page.
      this.setData({ loading: false, failed: true, refreshError: '' });
    }
  },

  chooseSegment(event: WechatMiniprogram.TouchEvent<Record<string, never>, Record<string, never>, { segment: Segment }>) {
    this.setData({ segment: event.currentTarget.dataset.segment });
  },

  chooseFilter(event: WechatMiniprogram.TouchEvent<Record<string, never>, Record<string, never>, { filter: DeviceFilter }>) {
    const filter = event.currentTarget.dataset.filter;
    this.setData({ filter, devices: plant === null ? null : presentDevices(plant, filter) });
  },

  openDevice(event: WechatMiniprogram.TouchEvent<Record<string, never>, Record<string, never>, { id: string }>) {
    wx.navigateTo({ url: `/pages/device/index?id=${event.currentTarget.dataset.id}` });
  },
});
