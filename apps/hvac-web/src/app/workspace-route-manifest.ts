import { matchSurface, surfacePath, type SurfaceId } from './surface-catalog';
import {
  SURFACE_WORKSPACE_PLACEMENTS,
  type WorkspaceId,
} from './workspace-catalog';

export interface WorkspaceRouteManifestEntry {
  readonly workspaceId: WorkspaceId;
  readonly portfolioEntrySurfaceId?: SurfaceId;
  readonly siteEntrySurfaceId?: SurfaceId;
  readonly platformEntrySurfaceId?: SurfaceId;
}

/**
 * Runtime entrypoints for the 10-workspace navigation.
 *
 * The 36 Surface routes remain canonical deep links during the consolidation.
 * This manifest chooses one existing Surface route as the workspace entrypoint,
 * so the shell can cut over to 10 navigation items without inventing placeholder
 * routes or breaking durable links.
 */
export const WORKSPACE_ROUTE_MANIFEST = [
  { workspaceId: 'overview', portfolioEntrySurfaceId: '01', siteEntrySurfaceId: '03' },
  { workspaceId: 'operations', siteEntrySurfaceId: '04' },
  { workspaceId: 'devices', siteEntrySurfaceId: '06' },
  { workspaceId: 'issues', siteEntrySurfaceId: '09' },
  { workspaceId: 'work', siteEntrySurfaceId: '11' },
  { workspaceId: 'performance', siteEntrySurfaceId: '14' },
  { workspaceId: 'improvements', siteEntrySurfaceId: '21' },
  { workspaceId: 'automation', siteEntrySurfaceId: '26' },
  { workspaceId: 'reports', siteEntrySurfaceId: '29' },
  { workspaceId: 'settings', siteEntrySurfaceId: '31', platformEntrySurfaceId: '35' },
] as const satisfies readonly WorkspaceRouteManifestEntry[];

export const WORKSPACE_ROUTE_MANIFEST_IS_COMPLETE:
  Exclude<WorkspaceId, (typeof WORKSPACE_ROUTE_MANIFEST)[number]['workspaceId']> extends never ? true : never = true;

const PRIMARY_WORKSPACE_BY_SURFACE = new Map<SurfaceId, WorkspaceId>(
  SURFACE_WORKSPACE_PLACEMENTS.map((placement) => [placement.surfaceId, placement.workspaceIds[0]]),
);

export function workspaceEntryPath(
  workspaceId: WorkspaceId,
  options: { readonly siteId?: string } = {},
): string | undefined {
  const entry = WORKSPACE_ROUTE_MANIFEST.find((candidate) => candidate.workspaceId === workspaceId);
  if (!entry) return undefined;

  if (options.siteId && 'siteEntrySurfaceId' in entry && entry.siteEntrySurfaceId) {
    return surfacePath(entry.siteEntrySurfaceId, { siteId: options.siteId });
  }

  if ('portfolioEntrySurfaceId' in entry && entry.portfolioEntrySurfaceId) {
    return surfacePath(entry.portfolioEntrySurfaceId);
  }

  if ('platformEntrySurfaceId' in entry && entry.platformEntrySurfaceId) {
    return surfacePath(entry.platformEntrySurfaceId);
  }

  return undefined;
}

export function matchWorkspace(pathname: string): WorkspaceId | undefined {
  const surface = matchSurface(pathname);
  if (!surface) return undefined;
  return PRIMARY_WORKSPACE_BY_SURFACE.get(surface.id);
}
