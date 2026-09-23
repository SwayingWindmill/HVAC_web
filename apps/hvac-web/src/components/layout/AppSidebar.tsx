import { Check, ChevronsUpDown } from 'lucide-react';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import {
  Sidebar,
  SidebarContent,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarRail,
  useSidebar,
} from '@/components/ui/sidebar';
import { navigationEntryIsActive, type AppNavigationEntry, type AppNavigationGroup } from './app-navigation';

interface SiteOption {
  readonly value: string;
  readonly label: string;
}

interface AppSidebarProps {
  readonly title: string;
  readonly pathname: string;
  readonly groups: readonly AppNavigationGroup[];
  readonly siteId?: string;
  readonly siteLabel: string;
  readonly siteOptions: readonly SiteOption[];
  readonly onSiteChange: (siteId: string) => void;
  readonly onNavigate: (target: string) => void;
}

function NavigationLink({
  entry,
  pathname,
  onNavigate,
}: {
  readonly entry: AppNavigationEntry;
  readonly pathname: string;
  readonly onNavigate: (target: string) => void;
}) {
  const active = navigationEntryIsActive(entry, pathname);
  const Icon = entry.icon;
  const { setOpenMobile } = useSidebar();

  return (
    <SidebarMenuItem>
      <SidebarMenuButton asChild isActive={active} tooltip={entry.label}>
        <a
          href={entry.path}
          aria-current={active ? 'page' : undefined}
          onClick={(event) => {
            event.preventDefault();
            setOpenMobile(false);
            onNavigate(entry.path);
          }}
        >
          <Icon aria-hidden="true" />
          <span>{entry.label}</span>
        </a>
      </SidebarMenuButton>
    </SidebarMenuItem>
  );
}

export function AppSidebar({
  title,
  pathname,
  groups,
  siteId,
  siteLabel,
  siteOptions,
  onSiteChange,
  onNavigate,
}: AppSidebarProps) {
  return (
    <Sidebar collapsible="icon" variant="inset">
      <SidebarHeader>
        <SidebarMenu>
          <SidebarMenuItem>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <SidebarMenuButton size="lg" className="data-[state=open]:bg-sidebar-accent data-[state=open]:text-sidebar-accent-foreground">
                  <span className="grid size-8 shrink-0 place-items-center rounded-md border border-sidebar-border bg-background">
                    <img src="/quanlaihe-mark.svg" alt="" width="20" height="20" />
                  </span>
                  <span className="min-w-0 flex-1 text-left leading-tight group-data-[collapsible=icon]:hidden">
                    <strong className="block truncate text-sm font-semibold">{title}</strong>
                    <small className="block truncate text-xs font-normal text-muted-foreground">{siteLabel}</small>
                  </span>
                  <ChevronsUpDown className="ml-auto size-4 text-muted-foreground group-data-[collapsible=icon]:hidden" aria-hidden="true" />
                </SidebarMenuButton>
              </DropdownMenuTrigger>
              <DropdownMenuContent side="right" align="start" className="w-64">
                <div className="px-2 py-1.5 text-xs font-medium text-muted-foreground">切换站点</div>
                <DropdownMenuSeparator />
                <DropdownMenuGroup>
                  {siteOptions.map((site) => (
                    <DropdownMenuItem key={site.value} onSelect={() => onSiteChange(site.value)}>
                      <span className="min-w-0 flex-1 truncate">{site.label}</span>
                      {site.value === siteId ? <Check className="size-4" aria-hidden="true" /> : null}
                    </DropdownMenuItem>
                  ))}
                </DropdownMenuGroup>
              </DropdownMenuContent>
            </DropdownMenu>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarHeader>

      <SidebarContent>
        <nav aria-label="主导航" className="contents">
          {groups.map((group) => (
            <SidebarGroup key={group.id}>
              <SidebarGroupLabel>{group.label}</SidebarGroupLabel>
              <SidebarGroupContent>
                <SidebarMenu>
                  {group.items.map((entry) => (
                    <NavigationLink key={entry.id} entry={entry} pathname={pathname} onNavigate={onNavigate} />
                  ))}
                </SidebarMenu>
              </SidebarGroupContent>
            </SidebarGroup>
          ))}
        </nav>
      </SidebarContent>
      <SidebarRail />
    </Sidebar>
  );
}
