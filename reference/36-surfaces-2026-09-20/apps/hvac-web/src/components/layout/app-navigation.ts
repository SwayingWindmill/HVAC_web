import type { LucideIcon } from 'lucide-react';
import {
  Activity,
  BellRing,
  Building2,
  ChartNoAxesCombined,
  ClipboardCheck,
  ClipboardList,
  Database,
  FileText,
  Gauge,
  LayoutDashboard,
  Lightbulb,
  Network,
  PlugZap,
  ServerCog,
  Settings2,
  ShieldAlert,
  ShieldCheck,
  SlidersHorizontal,
  Sparkles,
  Wrench,
  Workflow,
} from 'lucide-react';
import {
  SURFACE_CATALOG,
  SURFACE_GROUP_LABELS,
  WIREFRAME_READY_SURFACE_IDS,
  matchSurface,
  surfacePath,
  type SurfaceGroup,
  type SurfaceId,
  type SurfaceScope,
} from '@/app/surface-catalog';

export interface AppNavigationEntry {
  readonly id: string;
  readonly surfaceId: SurfaceId;
  readonly label: string;
  readonly path: string;
  readonly group: SurfaceGroup;
  readonly scope: SurfaceScope;
  readonly icon: LucideIcon;
  readonly degraded: boolean;
}

export interface AppNavigationGroup {
  readonly id: SurfaceGroup;
  readonly label: string;
  readonly items: readonly AppNavigationEntry[];
}

const GROUP_ORDER: readonly SurfaceGroup[] = [
  'overview',
  'operations',
  'events',
  'energy',
  'optimization',
  'management',
  'system',
];

const ICONS: Partial<Record<SurfaceId, LucideIcon>> = {
  '01': Building2,
  '02': ChartNoAxesCombined,
  '03': LayoutDashboard,
  '04': Activity,
  '05': ChartNoAxesCombined,
  '06': ServerCog,
  '07': ServerCog,
  '08': Gauge,
  '09': ShieldAlert,
  '10': Wrench,
  '11': ClipboardList,
  '12': ClipboardList,
  '13': ClipboardCheck,
  '14': ChartNoAxesCombined,
  '15': Gauge,
  '16': Gauge,
  '17': ClipboardCheck,
  '18': FileText,
  '19': FileText,
  '20': Activity,
  '21': Lightbulb,
  '22': Sparkles,
  '23': ClipboardCheck,
  '24': ChartNoAxesCombined,
  '25': SlidersHorizontal,
  '26': Workflow,
  '27': Workflow,
  '28': ClipboardList,
  '29': FileText,
  '30': ClipboardCheck,
  '31': Database,
  '32': Network,
  '33': BellRing,
  '34': PlugZap,
  '35': Settings2,
  '36': ShieldCheck,
};

const CAPABILITY_REQUIREMENTS: Partial<Record<SurfaceId, readonly string[]>> = {
  '03': ['site.read'],
};

export function buildAppNavigation({
  siteId,
  capabilities,
}: {
  readonly siteId?: string;
  readonly capabilities: ReadonlySet<string>;
}): {
  readonly entries: readonly AppNavigationEntry[];
  readonly groups: readonly AppNavigationGroup[];
} {
  const entries = SURFACE_CATALOG
    .filter((surface) => WIREFRAME_READY_SURFACE_IDS.has(surface.id))
    .filter((surface) => surface.navigation === 'default')
    .filter((surface) => surface.scope !== 'site' || Boolean(siteId))
    .filter((surface) => (CAPABILITY_REQUIREMENTS[surface.id] ?? []).every((capability) => capabilities.has(capability)))
    .map<AppNavigationEntry>((surface) => ({
      id: `surface-${surface.id}`,
      surfaceId: surface.id,
      label: surface.navLabel,
      path: surfacePath(surface.id, siteId ? { siteId } : {}),
      group: surface.group,
      scope: surface.scope,
      icon: ICONS[surface.id] ?? LayoutDashboard,
      degraded: false,
    }));

  const groups = GROUP_ORDER
    .map<AppNavigationGroup>((id) => ({
      id,
      label: SURFACE_GROUP_LABELS[id],
      items: entries.filter((entry) => entry.group === id),
    }))
    .filter((group) => group.items.length > 0);

  return { entries, groups };
}

export function navigationEntryIsActive(entry: AppNavigationEntry, pathname: string): boolean {
  return matchSurface(pathname)?.id === entry.surfaceId;
}
