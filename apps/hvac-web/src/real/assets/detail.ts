import { isUUIDv7 } from '../site-routing.ts';
import type { RealAssetsDeviceRow } from './model.ts';

export const REAL_ASSETS_DETAIL_HISTORY_MARKER = 'real-assets-device-detail:v1';

export type RealAssetsDetailPathState =
  | { readonly state: 'list' }
  | { readonly state: 'detail'; readonly deviceId: string }
  | { readonly state: 'outside' };

export type RealAssetsDetailResolution =
  | { readonly state: 'closed' }
  | { readonly state: 'not-visible' }
  | { readonly state: 'visible'; readonly row: RealAssetsDeviceRow };

export interface RealAssetsDetailHistoryState {
  readonly marker: typeof REAL_ASSETS_DETAIL_HISTORY_MARKER;
  readonly siteId: string;
  readonly deviceId: string;
}

export function realAssetsListPath(siteId: string): string {
  if (!isUUIDv7(siteId)) throw new Error('Real Assets list path requires a Registry Site UUIDv7.');
  return `/sites/${siteId}/assets`;
}

export function realAssetsDevicePath(siteId: string, deviceId: string): string {
  if (!isUUIDv7(deviceId)) throw new Error('Real Assets detail path requires a Registry Device UUIDv7.');
  return `${realAssetsListPath(siteId)}/${deviceId}`;
}

export function parseRealAssetsDetailPath(pathname: string, siteId: string): RealAssetsDetailPathState {
  const listPath = realAssetsListPath(siteId);
  const normalized = pathname.length > 1 && pathname.endsWith('/') ? pathname.slice(0, -1) : pathname;
  if (normalized === listPath) return { state: 'list' };
  const prefix = `${listPath}/`;
  if (!normalized.startsWith(prefix)) return { state: 'outside' };
  const deviceId = normalized.slice(prefix.length);
  if (!deviceId || deviceId.includes('/')) return { state: 'outside' };
  return { state: 'detail', deviceId };
}

export function resolveRealAssetsDetail(
  rows: readonly RealAssetsDeviceRow[],
  requestedDeviceId: string | null | undefined,
): RealAssetsDetailResolution {
  if (!requestedDeviceId) return { state: 'closed' };
  if (!isUUIDv7(requestedDeviceId)) return { state: 'not-visible' };
  const row = rows.find((candidate) => candidate.device.id === requestedDeviceId);
  return row ? { state: 'visible', row } : { state: 'not-visible' };
}

export function isRealAssetsDetailHistoryState(
  value: unknown,
  siteId: string,
  deviceId: string,
): value is RealAssetsDetailHistoryState {
  if (!value || typeof value !== 'object') return false;
  const candidate = value as Partial<RealAssetsDetailHistoryState>;
  return candidate.marker === REAL_ASSETS_DETAIL_HISTORY_MARKER
    && candidate.siteId === siteId
    && candidate.deviceId === deviceId;
}

export async function writeRealAssetsClipboard(
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
