import { listSites, rememberedSiteId, rememberSite } from '../../lib/site';

Page({
  data: {
    loading: true,
    failed: false,
    sites: [] as { id: string; name: string; current: boolean }[],
  },

  async onLoad() {
    try {
      const sites = await listSites();
      const current = rememberedSiteId() ?? sites[0]?.id ?? null;
      this.setData({ loading: false, sites: sites.map((site) => ({ id: site.id, name: site.displayName, current: site.id === current })) });
    } catch {
      this.setData({ loading: false, failed: true });
    }
  },

  choose(event: WechatMiniprogram.TouchEvent<Record<string, never>, Record<string, never>, { id: string }>) {
    rememberSite(event.currentTarget.dataset.id);
    wx.navigateBack();
  },
});
