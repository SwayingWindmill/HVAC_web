import { acknowledgeAlarm, getAlarm } from '../../lib/alarm-api';
import { explainAlarmQuestion, presentAlarm, type Alarm } from '../../lib/alarms';
import { askAssistantLater } from '../../lib/assistant-choice';
import { siteTodayStart } from '../../lib/energy';
import { GatewayError, NETWORK_TEXT } from '../../lib/gateway';
import { siteDeviceName } from '../../lib/plant-load';
import { gateway } from '../../lib/platform';
import { siteById } from '../../lib/site';

// The alarm this page shows, from the page query; not displayed, so kept out of setData.
let alarmId = '';
let shown: { alarm: Alarm; deviceName: string | null; todayStart: string | null } | null = null;
// The same key for retries of the same acknowledgement; a new one once the note changes or the platform answers.
let acknowledgementKey = '';

const newKey = () => `ack-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;

/**
 * The device's name from the Site's registered equipment (`name`), and what the 设备 line shows
 * (`shown`), which also says when the name could not be read. An alarm about the whole Site has neither.
 */
async function deviceOf(alarm: Alarm, site: Parameters<typeof siteDeviceName>[0]): Promise<{ name: string | null; shown: string | null }> {
  if (alarm.deviceId === undefined) return { name: null, shown: null };
  try {
    const name = await siteDeviceName(site, alarm.deviceId);
    return { name, shown: name ?? '没有登记的设备' };
  } catch {
    return { name: null, shown: '设备名称暂时读不到' };
  }
}

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
      const [alarm, me] = await Promise.all([
        getAlarm(gateway, alarmId),
        gateway.get<{ principalId: string }>('/api/v1/principal').then((principal) => principal.principalId),
      ]);
      // The alarm's own Site dates it and names its device, whichever Site the mini program shows.
      const site = await siteById(alarm.siteId);
      const [device, todayStart] = site === null ? [{ name: null, shown: null }, null] : await Promise.all([
        deviceOf(alarm, site),
        // The Site's midnight only dates the alarm; without it its times are left out.
        siteTodayStart(site, new Date()).catch(() => null),
      ]);
      shown = { alarm, deviceName: device.name, todayStart };
      this.setData({ loading: false, failed: '', view: presentAlarm(alarm, { deviceName: device.shown, now: new Date(), todayStart, me }) });
    } catch (error) {
      this.setData({
        loading: false,
        failed: error instanceof GatewayError && error.status === 404 ? '这条告警找不到了。' : '这条告警暂时读不到，下拉刷新再试。',
      });
    }
  },

  onComment(event: WechatMiniprogram.Input) {
    acknowledgementKey = newKey();
    this.setData({ comment: event.detail.value });
  },

  async acknowledge() {
    if (this.data.acknowledging) return;
    this.setData({ acknowledging: true, error: '' });
    try {
      await acknowledgeAlarm(gateway, alarmId, this.data.comment, acknowledgementKey);
      acknowledgementKey = newKey();
      this.setData({ comment: '', acknowledging: false });
      wx.showToast({ title: '已确认', icon: 'success' });
      await this.load();
    } catch (error) {
      const code = error instanceof GatewayError ? error.code : '';
      const status = error instanceof GatewayError ? error.status : 0;
      // A retry after a network failure keeps the key, so it counts once; anything the platform answered starts afresh.
      if (code !== 'NETWORK_UNAVAILABLE') acknowledgementKey = newKey();
      if (status === 409) {
        // Someone acknowledged it first, or it changed meanwhile: show it as it stands now.
        this.setData({ acknowledging: false });
        await this.load();
        this.setData({ error: '这条告警刚刚有了变化，已重新读取。' });
        return;
      }
      this.setData({
        acknowledging: false,
        error: code === 'NETWORK_UNAVAILABLE' ? NETWORK_TEXT : code === 'FORBIDDEN' ? '这个账号不能确认告警，请找管理员开通。' : '这次没能确认，请稍后再试。',
      });
    }
  },

  askAssistant() {
    if (shown === null) return;
    askAssistantLater(explainAlarmQuestion(shown.alarm, shown.deviceName, shown.todayStart));
    wx.switchTab({ url: '/pages/assistant/index' });
  },
});
