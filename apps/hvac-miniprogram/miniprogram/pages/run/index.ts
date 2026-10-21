import { energySeries, siteTodayStart } from '../../lib/energy';
import type { Plant } from '../../lib/plant';
import { loadPlant } from '../../lib/plant-load';
import { requireSession } from '../../lib/platform';
import { hourlyCop, presentDevices, presentRealtime, siteClock, type DeviceFilter } from '../../lib/run';
import { currentSite, type Site } from '../../lib/site';
import { selectTab } from '../../lib/tab';

// The web polls current values every 30 seconds when its push stream is not open; so does the phone.
const REFRESH_MS = 30_000;

let timer: number | undefined;
// The last plant read, so switching filters needs no request.
let plant: Plant | null = null;

const stopRefreshing = () => {
  if (timer !== undefined) clearInterval(timer);
  timer = undefined;
};

type Chart = ReturnType<typeof hourlyCop>;

/** Today's hourly efficiency; the note says why there is none. */
async function loadChart(site: Site, start: string | null, now: Date): Promise<{ chart: Chart | null; note: string }> {
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
    selectTab(this, 1);
    this.refresh();
    stopRefreshing();
    timer = setInterval(() => this.refresh(), REFRESH_MS);
  },

  onHide: stopRefreshing,

  onUnload: stopRefreshing,

  async onPullDownRefresh() {
    // A pull also re-reads the registered equipment.
    await this.refresh(true);
    wx.stopPullDownRefresh();
  },

  async refresh(fresh = false) {
    try {
      const site = await currentSite();
      if (site === null) {
        this.setData({ loading: false, noSite: true });
        return;
      }
      const now = new Date();
      const [read, start] = await Promise.allSettled([loadPlant(site, fresh), siteTodayStart(site, now)]);
      const today = start.status === 'fulfilled' ? start.value : null;
      if (read.status === 'rejected') {
        plant = null;
        this.setData({ siteName: site.displayName, loading: false, noSite: false, failed: true });
        return;
      }
      plant = read.value;
      const { chart, note } = await loadChart(site, today, now);
      this.setData({
        siteName: site.displayName,
        // Site wall-clock time needs the Site's midnight; without one the time is left out rather than guessed.
        updatedAt: plant.latestSampleAt !== null && today !== null ? siteClock(plant.latestSampleAt, today) : '',
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
