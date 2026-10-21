// The Site the mini program shows: the one the person picked last, else the first they can see.

import { gateway, sessionToken } from './platform';

export interface Site {
  readonly id: string;
  readonly tenantId: string;
  readonly displayName: string;
  readonly timezone: string;
}

const SITE_KEY = 'hvac.site';

// The Sites a person can see change only when an administrator assigns them, so the list is read
// once per Session and again on the Site list page or a pull-down refresh, not on every page refresh.
let known: { readonly session: string | null; readonly sites: readonly Site[] } | null = null;

/** The Sites this person can see, read now; the pages after it use this list. */
export async function listSites(): Promise<readonly Site[]> {
  const session = sessionToken();
  const page = await gateway.get<{ items: Site[] }>('/api/v1/sites?limit=50');
  known = { session, sites: page.items };
  return page.items;
}

export const rememberedSiteId = () => (wx.getStorageSync(SITE_KEY) as string) || null;

export const rememberSite = (siteId: string) => wx.setStorageSync(SITE_KEY, siteId);

/** The list read for this Session; `fresh` reads it again (a pull-down refresh). */
const visibleSites = async (fresh: boolean) => (!fresh && known !== null && known.session === sessionToken() ? known.sites : listSites());

export async function currentSite(fresh = false): Promise<Site | null> {
  const sites = await visibleSites(fresh);
  const remembered = rememberedSiteId();
  return sites.find((site) => site.id === remembered) ?? sites[0] ?? null;
}

/** A Site this person can see, by id, from the list read for this Session; null when it is not among them. */
export async function siteById(siteId: string): Promise<Site | null> {
  return (await visibleSites(false)).find((site) => site.id === siteId) ?? null;
}
