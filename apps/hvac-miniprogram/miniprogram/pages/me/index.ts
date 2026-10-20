import { gateway, signOutLocally } from '../../lib/platform';
import { currentSite } from '../../lib/site';

interface Principal {
  readonly principal: { readonly displayName: string; readonly email: string };
}

Page({
  data: {
    name: '',
    email: '',
    siteName: '',
    signingOut: false,
  },

  async onShow() {
    const tabBar = typeof this.getTabBar === 'function' ? this.getTabBar() : null;
    tabBar?.setData({ selected: 1 });
    const [principal, site] = await Promise.allSettled([gateway.get<Principal>('/api/v1/principal'), currentSite()]);
    this.setData({
      name: principal.status === 'fulfilled' ? principal.value.principal.displayName : '—',
      email: principal.status === 'fulfilled' ? principal.value.principal.email : '',
      siteName: site.status === 'fulfilled' && site.value !== null ? site.value.displayName : '—',
    });
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
