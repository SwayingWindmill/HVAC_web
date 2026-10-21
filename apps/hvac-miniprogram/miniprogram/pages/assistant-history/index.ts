import { assistantErrorText, listSessions } from '../../lib/agent';
import { presentHistory } from '../../lib/assistant';
import { chooseConversation } from '../../lib/assistant-choice';
import { siteTodayStart } from '../../lib/energy';
import { gateway } from '../../lib/platform';
import { currentSite } from '../../lib/site';

Page({
  data: {
    loading: true,
    failed: '',
    conversations: [] as ReturnType<typeof presentHistory>,
  },

  async onLoad() {
    try {
      const site = await currentSite();
      if (site === null) throw new Error('No Site is visible to this account');
      const [sessions, start] = await Promise.all([listSessions(gateway, site.id), siteTodayStart(site, new Date()).catch(() => null)]);
      this.setData({ loading: false, conversations: presentHistory(sessions, start) });
    } catch (error) {
      this.setData({ loading: false, failed: assistantErrorText(error) });
    }
  },

  open(event: WechatMiniprogram.TouchEvent<Record<string, never>, Record<string, never>, { id: string }>) {
    chooseConversation(event.currentTarget.dataset.id);
    wx.navigateBack();
  },
});
