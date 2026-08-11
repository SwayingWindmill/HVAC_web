import { isUUIDv7 } from '../site-routing.ts';
import type { RealAssetsEquipmentRow } from './model.ts';

export const REAL_ASSETS_DETAIL_HISTORY_MARKER = 'real-assets-equipment-detail:v1';

export type RealAssetsDetailPathState =
  | { readonly state: 'list' }
  | { readonly state: 'detail'; readonly equipmentId: string }
  | { readonly state: 'outside' };

export type RealAssetsDetailResolution =
  | { readonly state: 'closed' }
  | { readonly state: 'not-visible' }
  | { readonly state: 'visible'; readonly row: RealAssetsEquipmentRow };

export interface RealAssetsDetailHistoryState {
  readonly marker: typeof REAL_ASSETS_DETAIL_HISTORY_MARKER;
  readonly siteId: string;
  readonly equipmentId: string;
}

export function realAssetsListPath(siteId: string): string {
  if (!isUUIDv7(siteId)) throw new Error('Real Assets list path requires a Registry Site UUIDv7.');
  return `/sites/${siteId}/assets`;
}

export function realAssetsEquipmentPath(siteId: string, equipmentId: string): string {
  if (!isUUIDv7(equipmentId)) throw new Error('Real Assets detail path requires a Registry Equipment UUIDv7.');
  return `${realAssetsListPath(siteId)}/${equipmentId}`;
}

export function parseRealAssetsDetailPath(pathname: string, siteId: string): RealAssetsDetailPathState {
  const listPath = realAssetsListPath(siteId);
  const normalized = pathname.length > 1 && pathname.endsWith('/') ? pathname.slice(0, -1) : pathname;
  if (normalized === listPath) return { state: 'list' };
  const prefix = `${listPath}/`;
  if (!normalized.startsWith(prefix)) return { state: 'outside' };
  const equipmentId = normalized.slice(prefix.length);
  if (!equipmentId || equipmentId.includes('/')) return { state: 'outside' };
  return { state: 'detail', equipmentId };
}

export function resolveRealAssetsDetail(
  rows: readonly RealAssetsEquipmentRow[],
  requestedEquipmentId: string | null | undefined,
): RealAssetsDetailResolution {
  if (!requestedEquipmentId) return { state: 'closed' };
  if (!isUUIDv7(requestedEquipmentId)) return { state: 'not-visible' };
  const row = rows.find((candidate) => candidate.equipment.id === requestedEquipmentId);
  return row ? { state: 'visible', row } : { state: 'not-visible' };
}

export function isRealAssetsDetailHistoryState(
  value: unknown,
  siteId: string,
  equipmentId: string,
): value is RealAssetsDetailHistoryState {
  if (!value || typeof value !== 'object') return false;
  const candidate = value as Partial<RealAssetsDetailHistoryState>;
  return candidate.marker === REAL_ASSETS_DETAIL_HISTORY_MARKER
    && candidate.siteId === siteId
    && candidate.equipmentId === equipmentId;
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
