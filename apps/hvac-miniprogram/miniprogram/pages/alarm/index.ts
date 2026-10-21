import { acknowledgeAlarm, getAlarm } from '../../lib/alarm-api';
import { presentAlarm, type Alarm } from '../../lib/alarms';
import { askAssistantLater } from '../../lib/assistant-choice';
import { siteTodayStart } from '../../lib/energy';
import { GatewayError } from '../../lib/gateway';
import { siteDeviceName } from '../../lib/plant-load';
import { gateway } from '../../lib/platform';
import { currentSite } from '../../lib/site';

// The alarm this page shows, from the page query; not displayed, so kept out of setData.
let alarmId = '';
let alarm: Alarm | null = null;
// One acknowledgement per press of the button, however often the request is retried.
let acknowledgementKey = '';

const newKey = () => `ack-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;

const ACKNOWLEDGE_FAILED: Readonly<Record<string, string>> = {
  NETWORK_UNAVAILABLE: '连不上平台，请检查网络后再试。',
  FORBIDDEN: '这个账号不能确认告警，请找管理员开通。',
};

Page({
  data: {
    loading: true,
    failed: '',
    view: null as ReturnType<typeof presentAlarm> | null,
    comment: '',
    acknowledging: false,
    error: '',
  },

  onLoad(query: Record<string, string | undefined>) {
    alarmId = query.id ?? '';
    acknowledgementKey = newKey();
    this.load();
  },

  async onPullDownRefresh() {
    await this.load();
    wx.stopPullDownRefresh();
  },

  async load() {
    try {
      const site = await currentSite();
      if (site === null) {
        this.setData({ loading: false, failed: '这个账号还看不到任何站点，请管理员在网页「系统管理」里分配站点。' });
        return;
      }
      const [found, me, start] = await Promise.all([
        getAlarm(gateway, alarmId),
        gateway.get<{ principalId: string }>('/api/v1/principal').then((principal) => principal.principalId),
        // The Site's midnight only dates the alarm; without it its times are left out.
        siteTodayStart(site, new Date()).catch(() => null),
      ]);
      alarm = found;
      // The device's name comes from the Site's registered equipment; an alarm about the whole Site has none.
      const deviceName = found.deviceId === undefined ? null : await siteDeviceName(site, found.deviceId).catch(() => null);
      this.setData({ loading: false, failed: '', view: presentAlarm(found, { deviceName, now: new Date(), todayStart: start, me }) });
    } catch (error) {
      this.setData({
        loading: false,
        failed: error instanceof GatewayError && error.status === 404 ? '这条告警找不到了。' : '这条告警暂时读不到，下拉刷新再试。',
      });
    }
  },

  onComment(event: WechatMiniprogram.Input) {
    this.setData({ comment: event.detail.value });
  },

  async acknowledge() {
    if (this.data.acknowledging) return;
    this.setData({ acknowledging: true, error: '' });
    try {
      await acknowledgeAlarm(gateway, alarmId, this.data.comment, acknowledgementKey);
      acknowledgementKey = newKey();
      this.setData({ comment: '' });
      wx.showToast({ title: '已确认', icon: 'success' });
      await this.load();
    } catch (error) {
      const code = error instanceof GatewayError ? error.code : '';
      this.setData({ error: ACKNOWLEDGE_FAILED[code] ?? '这次没能确认，请稍后再试。' });
    } finally {
      this.setData({ acknowledging: false });
    }
  },

  askAssistant() {
    if (alarm === null) return;
    askAssistantLater(`「${alarm.title}」这条告警是怎么回事？要怎么处理？`);
    wx.switchTab({ url: '/pages/assistant/index' });
  },
});
