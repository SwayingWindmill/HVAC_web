import type { LucideIcon } from 'lucide-react';
import {
  Activity,
  ChartNoAxesCombined,
  ClipboardList,
  FileText,
  LayoutDashboard,
  Lightbulb,
  ServerCog,
  Settings2,
  ShieldAlert,
  Workflow,
} from 'lucide-react';
import {
  WORKSPACE_CATALOG,
  type WorkspaceId,
} from '@/app/workspace-catalog';
import {
  matchWorkspace,
  workspaceEntryPath,
} from '@/app/workspace-route-manifest';

export interface AppNavigationEntry {
  readonly id: string;
  readonly workspaceId: WorkspaceId;
  readonly label: string;
  readonly path: string;
  readonly group: 'workspace';
  readonly icon: LucideIcon;
}

export interface AppNavigationGroup {
  readonly id: 'workspace';
  readonly label: string;
  readonly items: readonly AppNavigationEntry[];
}

const ICONS: Readonly<Record<WorkspaceId, LucideIcon>> = {
  overview: LayoutDashboard,
  operations: Activity,
  devices: ServerCog,
  issues: ShieldAlert,
  work: ClipboardList,
  performance: ChartNoAxesCombined,
  improvements: Lightbulb,
  automation: Workflow,
  reports: FileText,
  settings: Settings2,
};

export function buildAppNavigation({
  siteId,
}: {
  readonly siteId?: string;
  readonly capabilities: ReadonlySet<string>;
}): {
  readonly entries: readonly AppNavigationEntry[];
  readonly groups: readonly AppNavigationGroup[];
} {
  const entries = WORKSPACE_CATALOG.flatMap<AppNavigationEntry>((workspace) => {
    const path = workspaceEntryPath(workspace.id, { siteId });
    if (!path) return [];
    return [{
      id: `workspace-${workspace.id}`,
      workspaceId: workspace.id,
      label: workspace.label,
      path,
      group: 'workspace',
      icon: ICONS[workspace.id],
    }];
  });

  return {
    entries,
    groups: [{
      id: 'workspace',
      label: '工作区',
      items: entries,
    }],
  };
}

export function navigationEntryIsActive(entry: AppNavigationEntry, pathname: string): boolean {
  return matchWorkspace(pathname) === entry.workspaceId;
}
