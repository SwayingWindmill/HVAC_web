import { clearAssistantChoice } from '../../lib/assistant-choice';
import { gateway, requireSession, signOutLocally, weChatLogin } from '../../lib/platform';
import { clearPlantCache } from '../../lib/plant-load';
import { clearSites, currentSite } from '../../lib/site';
import { clearTabBadge, selectTab } from '../../lib/tab';
import {
  readTenantState,
  switchActiveTenant,
  type PrincipalTenantResponse,
  type TenantMembership,
} from '../../lib/tenant';
import { bindWeChat, readWeChat, removeWeChat, type WeChatRow } from '../../lib/wechat';

interface Principal extends PrincipalTenantResponse {
  readonly principal: { readonly displayName: string; readonly email: string };
}

Page({
  data: {
    name: '',
    email: '',
    siteName: '',
    tenants: [] as readonly TenantMembership[],
    activeTenantId: null as string | null,
    tenantName: '',
    tenantIndex: 0,
    canSwitchTenant: false,
    switchingTenant: false,
    wechat: { value: '', action: null, hint: '' } as WeChatRow,
    changingWeChat: false,
    signingOut: false,
  },

  async onShow() {
    if (!requireSession()) return;
    selectTab(this);
    const [principal, site] = await Promise.allSettled([gateway.get<Principal>('/api/v1/principal'), currentSite()]);
    const tenantState = principal.status === 'fulfilled'
      ? readTenantState(principal.value)
      : { activeTenantId: null, activeTenantName: '', activeIndex: 0, tenants: [], canSwitch: false };
    this.setData({
      wechat: await readWeChat(gateway),
      name: principal.status === 'fulfilled' ? principal.value.principal.displayName : '—',
      email: principal.status === 'fulfilled' ? principal.value.principal.email : '',
      siteName: site.status === 'fulfilled' && site.value !== null ? site.value.displayName : '—',
      tenants: tenantState.tenants,
      activeTenantId: tenantState.activeTenantId,
      tenantName: tenantState.activeTenantName,
      tenantIndex: tenantState.activeIndex,
      canSwitchTenant: tenantState.canSwitch,
    });
  },

  async switchTenant(event: WechatMiniprogram.PickerChange) {
    if (this.data.switchingTenant) return;
    const index = Number(event.detail.value);
    const target = this.data.tenants[index];
    if (!target || target.tenantId === this.data.activeTenantId) return;

    this.setData({ switchingTenant: true });
    const result = await switchActiveTenant(gateway, target.tenantId, () => {
      clearSites();
      clearPlantCache();
      clearAssistantChoice();
      clearTabBadge();
    });
    if (!result.switched || !result.state) {
      this.setData({ switchingTenant: false });
      wx.showToast({ title: result.message, icon: 'none' });
      return;
    }

    let siteName = '—';
    let siteFailed = false;
    try {
      const nextSite = await currentSite(true);
      siteName = nextSite !== null ? nextSite.displayName : '暂无可用站点';
    } catch {
      siteFailed = true;
    }

    this.setData({
      switchingTenant: false,
      tenants: result.state.tenants,
      activeTenantId: result.state.activeTenantId,
      tenantName: result.state.activeTenantName,
      tenantIndex: result.state.activeIndex,
      canSwitchTenant: result.state.canSwitch,
      siteName,
    });
    wx.showToast({ title: siteFailed ? '组织已切换，但站点加载失败' : result.message, icon: 'none' });
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
    const { confirm } = await wx.showModal({
      title: '退出当前账号？',
      content: '退出后需重新输入密码或使用微信一键登录。',
      confirmText: '退出登录',
      confirmColor: '#d92d20',
    });
    if (!confirm) return;
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
