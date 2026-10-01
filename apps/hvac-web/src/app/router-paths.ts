const UUID_V7_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

export function isUUIDv7(value: string): boolean {
  return UUID_V7_PATTERN.test(value);
}

/** Site-scoped workspaces carry the Site in the `site` search parameter. */
export function siteIdFromLocation(search: string): string | undefined {
  const siteId = new URLSearchParams(search).get('site');
  return siteId && isUUIDv7(siteId) ? siteId : undefined;
}

/** The same workspace re-targeted at another Site. Object selections are dropped. */
export function workspaceLocationForSite(pathname: string, siteId: string): string {
  if (!isUUIDv7(siteId)) throw new Error('Site identity must be a Registry UUIDv7.');
  return `${pathname}?site=${siteId}`;
}
