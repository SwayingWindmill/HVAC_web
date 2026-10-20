import { GatewayError } from '../../lib/gateway';
import { gateway, requireSession } from '../../lib/platform';
import { currentSite, type Site } from '../../lib/site';
import { compareWithYesterday, sameHoursWindows, summarize, todayStart, type HourPoint, type Window } from '../../lib/today';
import { monthToDate, savingsCard, type Measure, type SavingsTotals } from '../../lib/savings';
import { presentOverview, type OverviewInputs, type Recommendation } from '../../lib/overview';
import { selectTab } from '../../lib/tab';

const REFRESH_MS = 60_000;
const DAY_MS = 86_400_000;

let timer: number | undefined;

// The overview refreshes only while it is on screen.
const stopRefreshing = () => {
  if (timer !== undefined) clearInterval(timer);
  timer = undefined;
};

const series = (site: Site, energyType: 'electricity' | 'cooling', granularity: 'hour' | 'day', window: Window) =>
  gateway.post<{ points: HourPoint[] }>('/api/v1/analytics/energy-series', {
    tenantId: site.tenantId, siteId: site.id, energyType, granularity, timezone: site.timezone,
    from: window.from, to: window.to, qualityPolicy: 'VALID_ONLY',
  }).then((response) => response.points);

/** Today against yesterday up to the same time of day, read the way the web overview reads it. */
async function loadEnergy(site: Site, start: string, now: Date): Promise<OverviewInputs['energy']> {
  const windows = sameHoursWindows(start, now);
  const [todayElectricity, todayCooling, yesterdayElectricity, yesterdayCooling] = await Promise.all([
    series(site, 'electricity', 'hour', windows.today),
    series(site, 'cooling', 'hour', windows.today),
    series(site, 'electricity', 'hour', windows.yesterday),
    series(site, 'cooling', 'hour', windows.yesterday),
  ]);
  const today = summarize(todayElectricity, todayCooling);
  return { status: 'ok', today, change: compareWithYesterday(today, summarize(yesterdayElectricity, yesterdayCooling)) };
}

async function loadSavings(site: Site, start: string): Promise<OverviewInputs['savings']> {
  const month = monthToDate(start);
  const [summary, measures] = await Promise.all([
    gateway.get<{ totals: SavingsTotals }>(`/api/v1/sites/${site.id}/savings/summary?from=${month.from}&to=${month.to}`),
    gateway.get<{ items: Measure[] }>(`/api/v1/sites/${site.id}/savings/measures`),
  ]);
  return { status: 'ok', card: savingsCard(summary.totals, measures.items) };
}

async function loadRecommendation(site: Site): Promise<OverviewInputs['recommendation']> {
  try {
    return { status: 'ok', latest: await gateway.get<Recommendation>(`/api/v1/sites/${site.id}/optimization/recommendations/latest`) };
  } catch (error) {
    // No recommendation published yet.
    if (error instanceof GatewayError && error.status === 404) return { status: 'ok', latest: null };
    throw error;
  }
}

const settled = <T>(result: PromiseSettledResult<T> | null, failed: T): T => (result?.status === 'fulfilled' ? result.value : failed);

async function load(site: Site): Promise<OverviewInputs> {
  const now = new Date();
  const [days, recommendation, alarms] = await Promise.allSettled([
    series(site, 'electricity', 'day', { from: new Date(now.getTime() - 2 * DAY_MS).toISOString(), to: now.toISOString() }),
    loadRecommendation(site),
    gateway.get<{ data: unknown[]; meta: { hasMore: boolean } }>(`/api/v1/alarms?siteId=${site.id}&condition=ACTIVE&limit=100`),
  ]);
  // Where today began at the Site, as the Energy owner cut the day; it dates today and this month.
  const start = days.status === 'fulfilled' ? todayStart(days.value, now) : null;
  const [energy, savings] = start === null ? [null, null] : await Promise.allSettled([loadEnergy(site, start, now), loadSavings(site, start)]);
  return {
    savings: settled<OverviewInputs['savings']>(savings, { status: 'error' }),
    recommendation: settled<OverviewInputs['recommendation']>(recommendation, { status: 'error' }),
    energy: settled<OverviewInputs['energy']>(energy, { status: 'error' }),
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
    if (!requireSession()) return;
    selectTab(this, 0);
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
