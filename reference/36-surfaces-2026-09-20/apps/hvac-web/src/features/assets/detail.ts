import { isUUIDv7, type AssetsDetailTarget } from '../../app/router-paths.ts';
import type { AssetsAssetRow, AssetsDeviceRow } from './model.ts';

export const REAL_ASSETS_DETAIL_HISTORY_MARKER = 'real-assets-asset-detail:v1';

export type AssetsDetailPathState =
  | { readonly state: 'list' }
  | { readonly state: 'detail'; readonly target: AssetsDetailTarget }
  | { readonly state: 'outside' };

export type AssetsDetailResolution =
  | { readonly state: 'closed' }
  | { readonly state: 'not-visible' }
  | { readonly state: 'visible'; readonly kind: 'asset'; readonly target: Extract<AssetsDetailTarget, { kind: 'asset' }>; readonly row: AssetsAssetRow }
  | { readonly state: 'visible'; readonly kind: 'device'; readonly target: Extract<AssetsDetailTarget, { kind: 'device' }>; readonly row: AssetsDeviceRow };

export interface AssetsDetailHistoryState {
  readonly marker: typeof REAL_ASSETS_DETAIL_HISTORY_MARKER;
  readonly siteId: string;
  readonly assetId: string;
}

export function assetsListPath(siteId: string): string {
  if (!isUUIDv7(siteId)) throw new Error('Assets list path requires a Registry Site UUIDv7.');
  return `/sites/${siteId}/assets`;
}

export function assetsAssetPath(siteId: string, assetId: string): string {
  if (!isUUIDv7(assetId)) throw new Error('Assets detail path requires a Registry Asset UUIDv7.');
  return `${assetsListPath(siteId)}/asset/${assetId}`;
}

export function assetsDevicePath(siteId: string, deviceId: string): string {
  if (!isUUIDv7(deviceId)) throw new Error('Assets detail path requires a Registry Device UUIDv7.');
  return `${assetsListPath(siteId)}/device/${deviceId}`;
}

export function parseAssetsDetailPath(pathname: string, siteId: string): AssetsDetailPathState {
  const listPath = assetsListPath(siteId);
  const normalized = pathname.length > 1 && pathname.endsWith('/') ? pathname.slice(0, -1) : pathname;
  if (normalized === listPath) return { state: 'list' };
  const prefix = `${listPath}/`;
  if (!normalized.startsWith(prefix)) return { state: 'outside' };
  const segments = normalized.slice(prefix.length).split('/');
  if (segments.length !== 2) return { state: 'outside' };
  const [kind, id] = segments;
  if (!id || (kind !== 'asset' && kind !== 'device')) return { state: 'outside' };
  return { state: 'detail', target: { kind, id } };
}

export function resolveAssetsDetail(
  assetRows: readonly AssetsAssetRow[],
  deviceRows: readonly AssetsDeviceRow[],
  requestedTarget: AssetsDetailTarget | null | undefined,
): AssetsDetailResolution {
  if (!requestedTarget) return { state: 'closed' };
  if (!isUUIDv7(requestedTarget.id)) return { state: 'not-visible' };
  if (requestedTarget.kind === 'asset') {
    const row = assetRows.find((candidate) => candidate.asset.id === requestedTarget.id);
    return row ? { state: 'visible', kind: 'asset', target: requestedTarget, row } : { state: 'not-visible' };
  }
  const row = deviceRows.find((candidate) => candidate.device.id === requestedTarget.id);
  return row ? { state: 'visible', kind: 'device', target: requestedTarget, row } : { state: 'not-visible' };
}

export function isAssetsDetailHistoryState(
  value: unknown,
  siteId: string,
  assetId: string,
): value is AssetsDetailHistoryState {
  if (!value || typeof value !== 'object') return false;
  const candidate = value as Partial<AssetsDetailHistoryState>;
  return candidate.marker === REAL_ASSETS_DETAIL_HISTORY_MARKER
    && candidate.siteId === siteId
    && candidate.assetId === assetId;
}

export async function writeAssetsClipboard(
  writeText: ((value: string) => Promise<void>) | undefined,
  value: string,
): Promise<boolean> {
  if (!writeText) return false;
  try {
    await writeText(value);
    return true;
  } catch {
    return false;
  }
}
