import { useLocation } from '@tanstack/react-router';
import { Zap } from 'lucide-react';
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
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
import { useScope } from '@/hooks/use-scope';
import { NavUser } from './NavUser';
import {
  APP_NAVIGATION_CONFIG,
  isNavActive,
  type AppNavigationItem,
} from './app-navigation';

interface AppSidebarProps {
  readonly title?: string;
  readonly principalName: string;
  readonly principalRole: string;
  readonly onNavigate: (target: string) => void;
  readonly onLogout: () => void;
}

function NavItem({
  item,
  currentPath,
  siteQuery,
  onNavigate,
}: {
  readonly item: AppNavigationItem;
  readonly currentPath: string;
  readonly siteQuery: string;
  readonly onNavigate: (target: string) => void;
}) {
  const active = isNavActive(item.path, currentPath);
  const Icon = item.icon;
  const { setOpenMobile } = useSidebar();

  const targetUrl = `${item.path}${siteQuery}`;

  return (
    <SidebarMenuItem>
      <SidebarMenuButton
        asChild
        isActive={active}
        tooltip={item.title}
      >
        <a
          href={targetUrl}
          aria-current={active ? 'page' : undefined}
          onClick={(event) => {
            event.preventDefault();
            setOpenMobile(false);
            onNavigate(targetUrl);
          }}
        >
          <Icon aria-hidden="true" />
          <span>{item.title}</span>
        </a>
      </SidebarMenuButton>
    </SidebarMenuItem>
  );
}

export function AppSidebar({
  title = '智慧能源 SaaS 平台',
  principalName,
  principalRole,
  onNavigate,
  onLogout,
}: AppSidebarProps) {
  const location = useLocation();
  const currentPath = location.pathname;
  const { currentScope } = useScope();
  const siteQuery = currentScope ? `?site=${currentScope.siteId}` : '';

  return (
    <Sidebar collapsible="icon" variant="inset">
      <SidebarHeader>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton
              size="lg"
              className="data-[state=open]:bg-sidebar-accent data-[state=open]:text-sidebar-accent-foreground cursor-pointer"
              onClick={() => onNavigate(`/overview${siteQuery}`)}
            >
              <div className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-primary text-primary-foreground shadow-xs">
                <Zap className="size-4.5" />
              </div>
              <div className="flex min-w-0 flex-1 flex-col text-left leading-tight group-data-[collapsible=icon]:hidden">
                <span className="truncate text-sm font-semibold tracking-tight text-foreground">
                  {title}
                </span>
                <span className="truncate text-[11px] text-muted-foreground">
                  Smart Energy Cloud
                </span>
              </div>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarHeader>

      <SidebarContent>
        {APP_NAVIGATION_CONFIG.map((group) => (
          <SidebarGroup key={group.id}>
            <SidebarGroupLabel>
              {group.label}
            </SidebarGroupLabel>
            <SidebarGroupContent>
              <SidebarMenu>
                {group.items.map((item) => (
                  <NavItem
                    key={item.id}
                    item={item}
                    currentPath={currentPath}
                    siteQuery={siteQuery}
                    onNavigate={onNavigate}
                  />
                ))}
              </SidebarMenu>
            </SidebarGroupContent>
          </SidebarGroup>
        ))}
      </SidebarContent>

      <SidebarFooter>
        <NavUser name={principalName} role={principalRole} onNavigate={onNavigate} onLogout={onLogout} />
      </SidebarFooter>
      <SidebarRail />
    </Sidebar>
  );
}
