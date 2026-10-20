// The Site the mini program shows: the one the person picked last, else the first they can see.

import { gateway } from './platform';

export interface Site {
  readonly id: string;
  readonly tenantId: string;
  readonly displayName: string;
  readonly timezone: string;
}

const SITE_KEY = 'hvac.site';

export async function currentSite(): Promise<Site | null> {
  const page = await gateway.get<{ items: Site[] }>('/api/v1/sites?limit=50');
  const remembered = wx.getStorageSync(SITE_KEY) as string;
  return page.items.find((site) => site.id === remembered) ?? page.items[0] ?? null;
}
