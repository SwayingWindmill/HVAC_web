import { gateway } from '../../lib/platform';
import { currentSite, type Site } from '../../lib/site';
import { againstYesterday, todayStart as startOfToday, type HourPoint } from '../../lib/today';
import { monthToDate, savingsCard, type Measure, type SavingsTotals } from '../../lib/savings';
import { presentOverview, type OverviewInputs } from '../../lib/overview';

const REFRESH_MS = 60_000;
const TWO_DAYS_MS = 2 * 86_400_000;

let timer: number | undefined;

// The overview refreshes only while it is on screen.
const stopRefreshing = () => {
  if (timer !== undefined) clearInterval(timer);
  timer = undefined;
};

const series = (site: Site, energyType: 'electricity' | 'cooling', granularity: 'hour' | 'day', now: Date) =>
  gateway.post<{ points: HourPoint[] }>('/api/v1/analytics/energy-series', {
    tenantId: site.tenantId, siteId: site.id, energyType, granularity, timezone: site.timezone,
    from: new Date(now.getTime() - TWO_DAYS_MS).toISOString(), to: now.toISOString(), qualityPolicy: 'VALID_ONLY',
  }).then((response) => response.points);

async function load(site: Site): Promise<OverviewInputs> {
  const now = new Date();
  const [days, hourlyElectricity, hourlyCooling, alarms] = await Promise.allSettled([
    series(site, 'electricity', 'day', now),
    series(site, 'electricity', 'hour', now),
    series(site, 'cooling', 'hour', now),
    gateway.get<{ data: unknown[]; meta: { hasMore: boolean } }>(`/api/v1/alarms?siteId=${site.id}&condition=ACTIVE&limit=100`),
  ]);
  // Today's Site day as the owner cut it.
  const todayStart = days.status === 'fulfilled' ? startOfToday(days.value, now) : null;

  let energy: OverviewInputs['energy'] = { status: 'error' };
  if (todayStart !== null && hourlyElectricity.status === 'fulfilled' && hourlyCooling.status === 'fulfilled') {
    energy = { status: 'ok', ...againstYesterday(hourlyElectricity.value, hourlyCooling.value, todayStart, now) };
  }

  let savings: OverviewInputs['savings'] = { status: 'error' };
  if (todayStart !== null) {
    const month = monthToDate(todayStart);
    const [summary, measures] = await Promise.allSettled([
      gateway.get<{ totals: SavingsTotals }>(`/api/v1/sites/${site.id}/savings/summary?from=${month.from}&to=${month.to}`),
      gateway.get<{ items: Measure[] }>(`/api/v1/sites/${site.id}/savings/measures`),
    ]);
    if (summary.status === 'fulfilled' && measures.status === 'fulfilled') {
      savings = { status: 'ok', card: savingsCard(summary.value.totals, measures.value.items) };
    }
  }

  return {
    savings,
    energy,
    alarms: alarms.status === 'fulfilled' ? { status: 'ok', active: alarms.value.data.length, complete: !alarms.value.meta.hasMore } : { status: 'error' },
  };
}

const clock = (now: Date) => `${String(now.getHours()).padStart(2, '0')}:${String(now.getMinutes()).padStart(2, '0')}`;

Page({
  data: {
    siteName: '',
    updatedAt: '',
    loading: true,
    noSite: false,
    failed: false,
    view: null as ReturnType<typeof presentOverview> | null,
  },

  onShow() {
    const tabBar = typeof this.getTabBar === 'function' ? this.getTabBar() : null;
    tabBar?.setData({ selected: 0 });
    this.refresh();
    stopRefreshing();
    timer = setInterval(() => this.refresh(), REFRESH_MS);
  },

  onHide: stopRefreshing,

  onUnload: stopRefreshing,

  async onPullDownRefresh() {
    await this.refresh();
    wx.stopPullDownRefresh();
  },

  async refresh() {
    try {
      const site = await currentSite();
      if (site === null) {
        this.setData({ loading: false, noSite: true });
        return;
      }
      const view = presentOverview(await load(site));
      this.setData({ siteName: site.displayName, updatedAt: clock(new Date()), loading: false, noSite: false, failed: false, view });
    } catch {
      // The Site list itself could not be read; signed-out failures already went to the sign-in page.
      this.setData({ loading: false, failed: true });
    }
  },
});
