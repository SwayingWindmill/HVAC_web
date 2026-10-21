import { activeAlarms, clearedAlarms } from '../../lib/alarm-api';
import { presentAlarmRows, type Alarm } from '../../lib/alarms';
import { siteTodayStart } from '../../lib/energy';
import { gateway } from '../../lib/platform';
import { whileShown } from '../../lib/refresh';
import { currentSite } from '../../lib/site';

const refresher = whileShown(30_000);

const EMPTY = {
  ACTIVE: { all: '现在没有正在发生的告警。', unacknowledged: '正在发生的告警都有人确认了。' },
  CLEARED: { all: '最近没有恢复的告警。', unacknowledged: '最近恢复的告警都有人确认过。' },
} as const;

type Condition = 'ACTIVE' | 'CLEARED';

let lists: Record<Condition, { alarms: Alarm[]; complete: boolean }> | null = null;
let todayStart: string | null = null;

Page({
  data: {
    loading: true,
    failed: '',
    siteName: '',
    condition: 'ACTIVE' as Condition,
    unacknowledgedOnly: false,
    counts: { ACTIVE: '', CLEARED: '' },
    rows: [] as ReturnType<typeof presentAlarmRows>,
    empty: '',
    /** The list could not hold every alarm. */
    partial: false,
  },

  onShow() {
    this.refresh();
    refresher.start(() => this.refresh());
  },

  onHide: refresher.stop,

  onUnload: refresher.stop,

  async onPullDownRefresh() {
    await this.refresh(true);
    wx.stopPullDownRefresh();
  },

  async refresh(rereadSites = false) {
    try {
      const site = await currentSite(rereadSites);
      if (site === null) {
        this.setData({ loading: false, failed: '这个账号还看不到任何站点，请管理员在网页「系统管理」里分配站点。' });
        return;
      }
      const [active, cleared, start] = await Promise.all([
        activeAlarms(gateway, site.id),
        clearedAlarms(gateway, site.id),
        // The Site's midnight only dates the alarms; without it they list without times.
        siteTodayStart(site, new Date()).catch(() => null),
      ]);
      lists = { ACTIVE: active, CLEARED: cleared };
      todayStart = start;
      this.setData({
        loading: false,
        failed: '',
        siteName: site.displayName,
        counts: { ACTIVE: `${active.alarms.length}${active.complete ? '' : '+'}`, CLEARED: `${cleared.alarms.length}${cleared.complete ? '' : '+'}` },
      });
      this.render();
    } catch {
      this.setData({ loading: false, failed: '告警暂时读不到，下拉刷新再试。' });
    }
  },

  render() {
    if (lists === null) return;
    const list = lists[this.data.condition];
    const { condition, unacknowledgedOnly } = this.data;
    this.setData({
      rows: presentAlarmRows(list.alarms, { unacknowledgedOnly }, todayStart),
      empty: EMPTY[condition][unacknowledgedOnly ? 'unacknowledged' : 'all'],
      partial: !list.complete,
    });
  },

  chooseCondition(event: WechatMiniprogram.TouchEvent<Record<string, never>, Record<string, never>, { condition: Condition }>) {
    this.setData({ condition: event.currentTarget.dataset.condition });
    this.render();
  },

  toggleUnacknowledged(event: WechatMiniprogram.SwitchChange) {
    this.setData({ unacknowledgedOnly: event.detail.value });
    this.render();
  },

  open(event: WechatMiniprogram.TouchEvent<Record<string, never>, Record<string, never>, { id: string }>) {
    wx.navigateTo({ url: `/pages/alarm/index?id=${event.currentTarget.dataset.id}` });
  },
});
