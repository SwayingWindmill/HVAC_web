import { gateway, requireSession, signOutLocally } from '../../lib/platform';
import { currentSite } from '../../lib/site';
import { selectTab } from '../../lib/tab';
import { bindWeChat, readWeChat, removeWeChat, type WeChatRow } from '../../lib/wechat';

const weChatLogin = () => new Promise<string>((resolve, reject) => {
  wx.login({ success: (result) => resolve(result.code), fail: reject });
});

interface Principal {
  readonly principal: { readonly displayName: string; readonly email: string };
}

Page({
  data: {
    name: '',
    email: '',
    siteName: '',
    wechat: { value: '', action: null, hint: '' } as WeChatRow,
    changingWeChat: false,
    signingOut: false,
  },

  async onShow() {
    if (!requireSession()) return;
    selectTab(this);
    const [principal, site] = await Promise.allSettled([gateway.get<Principal>('/api/v1/principal'), currentSite()]);
    this.setData({
      wechat: await readWeChat(gateway),
      name: principal.status === 'fulfilled' ? principal.value.principal.displayName : '—',
      email: principal.status === 'fulfilled' ? principal.value.principal.email : '',
      siteName: site.status === 'fulfilled' && site.value !== null ? site.value.displayName : '—',
    });
  },

  openSites() {
    wx.navigateTo({ url: '/pages/sites/index' });
  },

  async tapWeChat() {
    const { action } = this.data.wechat;
    if (action === null || this.data.changingWeChat) return;
    if (action === 'remove') {
      const { confirm } = await wx.showModal({ title: '解除微信绑定？', content: '解除后不能再用这个微信一键登录，也收不到告警提醒。', confirmText: '解除', confirmColor: '#d92d20' });
      if (!confirm) return;
    }
    this.setData({ changingWeChat: true });
    const change = action === 'bind' ? await bindWeChat(gateway, weChatLogin) : await removeWeChat(gateway);
    const wechat = await readWeChat(gateway);
    this.setData({ wechat, changingWeChat: false });
    const succeeded = change.bound === (action === 'bind');
    if (succeeded) wx.showToast({ title: change.message, icon: 'success' });
    else wx.showModal({ title: action === 'bind' ? '没有绑定成功' : '没有解除成功', content: change.message, showCancel: false });
  },

  async signOut() {
    if (this.data.signingOut) return;
    this.setData({ signingOut: true });
    try {
      // Ends the Session on the platform; the mini program forgets it either way.
      await gateway.post('/api/v1/auth/logout', {});
    } catch {
      // A Session the platform already ended cannot be ended again.
    }
    signOutLocally();
    wx.reLaunch({ url: '/pages/login/index' });
  },
});
