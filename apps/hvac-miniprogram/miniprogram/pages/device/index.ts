import { electricityByDevice, siteTodayStart } from '../../lib/energy';
import { loadPlant } from '../../lib/plant-load';
import { requireSession } from '../../lib/platform';
import { presentDevice, type DeviceToday } from '../../lib/run';
import { currentSite, type Site } from '../../lib/site';

const REFRESH_MS = 30_000;

let timer: number | undefined;

const stopRefreshing = () => {
  if (timer !== undefined) clearInterval(timer);
  timer = undefined;
};

/** Today's electricity of the Site and of this device's own meter; a failed read leaves the chart out with a note. */
async function loadToday(site: Site, deviceId: string, start: string | null, now: Date): Promise<{ today: DeviceToday | null; note: string }> {
  if (start === null) return { today: null, note: '' };
  try {
    const series = await electricityByDevice(site, { from: start, to: now.toISOString() });
    const own = series.devices?.find((device) => device.deviceId === deviceId);
    return { today: { start, now, site: series.points, device: own?.points ?? null }, note: '' };
  } catch {
    return { today: { start, now, site: [], device: null }, note: '今天的用电暂时读不到。' };
  }
}

Page({
  data: {
    deviceId: '',
    loading: true,
    failed: '',
    view: null as ReturnType<typeof presentDevice> | null,
    energyNote: '',
  },

  onLoad(query: Record<string, string | undefined>) {
    this.setData({ deviceId: query.id ?? '' });
  },

  onShow() {
    if (!requireSession()) return;
    this.refresh();
    stopRefreshing();
    timer = setInterval(() => this.refresh(), REFRESH_MS);
  },

  onHide: stopRefreshing,

  onUnload: stopRefreshing,

  async onPullDownRefresh() {
    await this.refresh(true);
    wx.stopPullDownRefresh();
  },

  async refresh(fresh = false) {
    try {
      const site = await currentSite();
      if (site === null) throw new Error('No Site is visible to this account');
      const now = new Date();
      const [plant, start] = await Promise.all([loadPlant(site, fresh), siteTodayStart(site, now).catch(() => null)]);
      const device = plant.devices.find((candidate) => candidate.deviceId === this.data.deviceId);
      if (device === undefined) {
        this.setData({ loading: false, failed: '这台设备已不在站点的设备清单里。' });
        return;
      }
      const { today, note } = await loadToday(site, device.deviceId, start, now);
      wx.setNavigationBarTitle({ title: device.name });
      this.setData({ loading: false, failed: '', view: presentDevice(device, today), energyNote: note });
    } catch {
      this.setData({ loading: false, failed: '这台设备的读数暂时读不到，下拉刷新再试。' });
    }
  },
});
