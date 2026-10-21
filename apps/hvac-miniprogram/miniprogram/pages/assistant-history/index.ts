import { assistantErrorText } from '../../lib/agent';
import { presentHistory } from '../../lib/assistant';
import { chooseConversation } from '../../lib/assistant-choice';
import { loadConversations } from '../../lib/conversations';
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
      if (site === null) {
        this.setData({ loading: false, failed: '这个账号还看不到任何站点，请管理员在网页「系统管理」里分配站点。' });
        return;
      }
      const { sessions, todayStart } = await loadConversations(site);
      this.setData({ loading: false, conversations: presentHistory(sessions, todayStart) });
    } catch (error) {
      this.setData({ loading: false, failed: assistantErrorText(error) });
    }
  },

  open(event: WechatMiniprogram.TouchEvent<Record<string, never>, Record<string, never>, { id: string }>) {
    chooseConversation(event.currentTarget.dataset.id);
    wx.navigateBack();
  },
});
