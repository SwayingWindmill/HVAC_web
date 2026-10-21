import { GatewayError } from '../../lib/gateway';
import { gateway, requireSession } from '../../lib/platform';
import { currentSite, type Site } from '../../lib/site';
import { energySeries, recentDays } from '../../lib/energy';
import { loadPlant } from '../../lib/plant-load';
import { whileShown } from '../../lib/refresh';
import { presentPlantNow } from '../../lib/run';
import { compareWithYesterday, sameHoursWindows, summarize, todayStart } from '../../lib/today';
import { monthToDate, savingsCard, type Measure, type SavingsTotals } from '../../lib/savings';
import { presentOverview, type OverviewInputs, type Recommendation } from '../../lib/overview';
import { listInbox } from '../../lib/alarm-api';
import { unreadOn } from '../../lib/alarms';
import { selectTab, showUnread } from '../../lib/tab';

const refresher = whileShown(60_000);

/** Today against yesterday up to the same time of day, read the way the web overview reads it. */
async function loadEnergy(site: Site, start: string, now: Date): Promise<OverviewInputs['energy']> {
  const windows = sameHoursWindows(start, now);
  const [todayElectricity, todayCooling, yesterdayElectricity, yesterdayCooling] = await Promise.all([
    energySeries(site, 'electricity', 'hour', windows.today),
    energySeries(site, 'cooling', 'hour', windows.today),
    energySeries(site, 'electricity', 'hour', windows.yesterday),
    energySeries(site, 'cooling', 'hour', windows.yesterday),
  ]);
  const today = summarize(todayElectricity, todayCooling);
  return { status: 'ok', today, change: compareWithYesterday(today, summarize(yesterdayElectricity, yesterdayCooling)) };
}

const NOTHING_SAVED: SavingsTotals = { days: 0, avoidedKWh: 0, avoidedCost: 0, plantKWh: 0 };

/** This month's savings; without a Site day to date the month, the measures alone say why there is none. */
async function loadSavings(site: Site, start: string | null): Promise<OverviewInputs['savings']> {
  const month = start === null ? null : monthToDate(start);
  const [totals, measures] = await Promise.all([
    month === null ? NOTHING_SAVED
      : gateway.get<{ totals: SavingsTotals }>(`/api/v1/sites/${site.id}/savings/summary?from=${month.from}&to=${month.to}`).then((summary) => summary.totals),
    gateway.get<{ items: Measure[] }>(`/api/v1/sites/${site.id}/savings/measures`),
  ]);
  return { status: 'ok', card: savingsCard(totals, measures.items) };
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

const settled = <T>(result: PromiseSettledResult<T>, failed: T): T => (result.status === 'fulfilled' ? result.value : failed);

async function load(site: Site) {
  const now = new Date();
  const [days, recommendation, alarms, plant] = await Promise.allSettled([
    recentDays(site, now),
    loadRecommendation(site),
    gateway.get<{ data: unknown[]; meta: { hasMore: boolean } }>(`/api/v1/alarms?siteId=${site.id}&condition=ACTIVE&limit=100`),
    loadPlant(site),
  ]);
  // Where today began at the Site, as the Energy owner cut the day; it dates today and this month.
  // No electricity reading in the last two days leaves no day to compare.
  const start = days.status === 'fulfilled' ? todayStart(days.value, now) : null;
  const [energy, savings] = await Promise.allSettled([
    start === null ? Promise.resolve<OverviewInputs['energy']>(days.status === 'fulfilled' ? { status: 'empty' } : { status: 'error' }) : loadEnergy(site, start, now),
    loadSavings(site, start),
  ]);
  const inputs: OverviewInputs = {
    savings: settled<OverviewInputs['savings']>(savings, { status: 'error' }),
    recommendation: settled<OverviewInputs['recommendation']>(recommendation, { status: 'error' }),
    energy: settled<OverviewInputs['energy']>(energy, { status: 'error' }),
    alarms: alarms.status === 'fulfilled' ? { status: 'ok', active: alarms.value.data.length, complete: !alarms.value.meta.hasMore } : { status: 'error' },
  };
  // The plant card stays off a Site without registered devices, and says so when it cannot be read.
  return { inputs, plant: plant.status === 'fulfilled' ? { failed: false, now: presentPlantNow(plant.value) } : { failed: true, now: null } };
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
    plant: { failed: false, now: null } as { failed: boolean; now: ReturnType<typeof presentPlantNow> },
  },

  onShow() {
    if (!requireSession()) return;
    selectTab(this);
    this.refresh();
    refresher.start(() => this.refresh());
  },

  onHide: refresher.stop,

  onUnload: refresher.stop,

  openAlarms() {
    wx.navigateTo({ url: '/pages/alarms/index' });
  },

  openRun() {
    wx.switchTab({ url: '/pages/run/index' });
  },

  openSites() {
    wx.navigateTo({ url: '/pages/sites/index' });
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
      const [{ inputs, plant }, inbox] = await Promise.all([load(site), listInbox(gateway).catch(() => null)]);
      // The 通知 tab shows how many of this Site's notifications are unread; a failed read leaves the last count.
      if (inbox !== null) showUnread(this, unreadOn(inbox, site.id));
      this.setData({ siteName: site.displayName, updatedAt: clock(new Date()), loading: false, noSite: false, failed: false, view: presentOverview(inputs), plant });
    } catch {
      // The Site list itself could not be read; signed-out failures already went to the sign-in page.
      this.setData({ loading: false, failed: true });
    }
  },
});
