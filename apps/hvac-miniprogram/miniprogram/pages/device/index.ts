import { electricityByDevice, siteTodayStart } from '../../lib/energy';
import { loadPlant } from '../../lib/plant-load';
import { requireSession } from '../../lib/platform';
import { whileShown } from '../../lib/refresh';
import { presentDevice, type DeviceToday } from '../../lib/run';
import { currentSite, type Site } from '../../lib/site';

const refresher = whileShown(30_000);

// The device this page shows, from the page query; not displayed, so kept out of setData.
let deviceId = '';

const ELECTRICITY_UNREADABLE = '今天的用电暂时读不到。';

/** Today's electricity of the Site and of this device's own meter; a failed read leaves the chart out with a note. */
async function loadToday(site: Site, dayStart: PromiseSettledResult<string | null>, now: Date): Promise<{ today: DeviceToday | null; note: string }> {
  if (dayStart.status === 'rejected') return { today: null, note: ELECTRICITY_UNREADABLE };
  const start = dayStart.value;
  // A Site without electricity readings today or yesterday has no day to chart.
  if (start === null) return { today: null, note: '' };
  try {
    const series = await electricityByDevice(site, { from: start, to: now.toISOString() });
    const own = series.devices?.find((device) => device.deviceId === deviceId);
    return { today: { start, now, site: series.points, device: own?.points ?? null }, note: '' };
  } catch {
    return { today: { start, now, site: [], device: null }, note: ELECTRICITY_UNREADABLE };
  }
}

Page({
  data: {
    loading: true,
    failed: '',
    view: null as ReturnType<typeof presentDevice> | null,
    energyNote: '',
  },

  onLoad(query: Record<string, string | undefined>) {
    deviceId = query.id ?? '';
  },

  onShow() {
    if (!requireSession()) return;
    this.refresh();
    refresher.start(() => this.refresh());
  },

  onHide: refresher.stop,

  onUnload: refresher.stop,

  async onPullDownRefresh() {
    await this.refresh({ rereadEquipment: true });
    wx.stopPullDownRefresh();
  },

  async refresh({ rereadEquipment = false } = {}) {
    try {
      const site = await currentSite();
      if (site === null) {
        this.setData({ loading: false, failed: '这个账号还看不到任何站点，请管理员在网页「系统管理」里分配站点。' });
        return;
      }
      const now = new Date();
      const [plant, dayStart] = await Promise.all([loadPlant(site, rereadEquipment), Promise.allSettled([siteTodayStart(site, now)])]);
      const device = plant.devices.find((candidate) => candidate.deviceId === deviceId);
      if (device === undefined) {
        this.setData({ loading: false, failed: '这台设备已不在站点的设备清单里。' });
        return;
      }
      const { today, note } = await loadToday(site, dayStart[0], now);
      wx.setNavigationBarTitle({ title: device.name });
      this.setData({ loading: false, failed: '', view: presentDevice(device, today), energyNote: note });
    } catch {
      this.setData({ loading: false, failed: '这台设备的读数暂时读不到，下拉刷新再试。' });
    }
  },
});
