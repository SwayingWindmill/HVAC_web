import { energySeries, siteTodayStart } from '../../lib/energy';
import type { Plant } from '../../lib/plant';
import { loadPlant } from '../../lib/plant-load';
import { requireSession } from '../../lib/platform';
import { whileShown } from '../../lib/refresh';
import { hourlyCop, presentDevices, presentRealtime, type DeviceFilter } from '../../lib/run';
import { siteClock } from '../../lib/today';
import { currentSite, type Site } from '../../lib/site';
import { selectTab } from '../../lib/tab';

// The web polls current values every 30 seconds when its push stream is not open; so does the phone.
const refresher = whileShown(30_000);

// The last plant read, so switching filters needs no request.
let plant: Plant | null = null;

type Chart = ReturnType<typeof hourlyCop>;

/** Today's hourly efficiency; the note says why there is none. */
async function loadChart(site: Site, dayStart: PromiseSettledResult<string | null>, now: Date): Promise<{ chart: Chart | null; note: string }> {
  if (dayStart.status === 'rejected') return { chart: null, note: '今天的能效暂时读不到。' };
  const start = dayStart.value;
  if (start === null) return { chart: null, note: '这个站点今天和昨天都没有用电读数。' };
  const window = { from: start, to: now.toISOString() };
  try {
    const [electricity, cooling] = await Promise.all([energySeries(site, 'electricity', 'hour', window), energySeries(site, 'cooling', 'hour', window)]);
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
    realtime: null as ReturnType<typeof presentRealtime> | null,
    devices: null as ReturnType<typeof presentDevices> | null,
    deviceCount: 0,
    chart: null as Chart | null,
    chartNote: '',
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

  /** `rereadEquipment` also re-reads the registered equipment, which only changes when an administrator edits it. */
  async refresh({ rereadEquipment = false } = {}) {
    try {
      const site = await currentSite();
      if (site === null) {
        this.setData({ loading: false, noSite: true });
        return;
      }
      const now = new Date();
      const [plantRead, dayStart] = await Promise.allSettled([loadPlant(site, rereadEquipment), siteTodayStart(site, now)]);
      if (plantRead.status === 'rejected') {
        plant = null;
        this.setData({ siteName: site.displayName, loading: false, noSite: false, failed: true });
        return;
      }
      plant = plantRead.value;
      const { chart, note } = await loadChart(site, dayStart, now);
      const start = dayStart.status === 'fulfilled' ? dayStart.value : null;
      this.setData({
        siteName: site.displayName,
        // Site clock time needs the Site's midnight; without one the time is left out rather than guessed.
        updatedAt: plant.latestSampleAt !== null && start !== null ? siteClock(plant.latestSampleAt, start) : '',
        loading: false,
        noSite: false,
        failed: false,
        realtime: presentRealtime(plant),
        devices: presentDevices(plant, this.data.filter),
        deviceCount: plant.devices.length,
        chart,
        chartNote: note,
      });
    } catch {
      // The Site list itself could not be read; signed-out failures already went to the sign-in page.
      this.setData({ loading: false, failed: true });
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
