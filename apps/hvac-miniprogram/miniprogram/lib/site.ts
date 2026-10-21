// The Site the mini program shows: the one the person picked last, else the first they can see.

import { gateway } from './platform';

export interface Site {
  readonly id: string;
  readonly tenantId: string;
  readonly displayName: string;
  readonly timezone: string;
}

const SITE_KEY = 'hvac.site';

export const listSites = () => gateway.get<{ items: Site[] }>('/api/v1/sites?limit=50').then((page) => page.items);

export const rememberedSiteId = () => (wx.getStorageSync(SITE_KEY) as string) || null;

export const rememberSite = (siteId: string) => wx.setStorageSync(SITE_KEY, siteId);

export async function currentSite(): Promise<Site | null> {
  const sites = await listSites();
  const remembered = rememberedSiteId();
  return sites.find((site) => site.id === remembered) ?? sites[0] ?? null;
}
