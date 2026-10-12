import { useQuery } from '@tanstack/react-query';
import { useLocation } from '@tanstack/react-router';
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuBadge,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarRail,
  useSidebar,
} from '@/components/ui/sidebar';
import { activeAlarmsQuery } from '@/features/alarms/alarm-api';
import { openWorkOrdersQuery } from '@/features/work-orders/work-order-queries';
import { useScope } from '@/hooks/use-scope';
import { NavUser } from './NavUser';
import { ScopeSwitcher } from './ScopeSwitcher';
import {
  APP_NAVIGATION_CONFIG,
  isNavActive,
  type AppNavigationItem,
} from './app-navigation';

interface AppSidebarProps {
  readonly principalName: string;
  readonly principalRole: string;
  readonly onNavigate: (target: string) => void;
  readonly onLogout: () => void;
}

/** What waits on the operator: active alarms and open work orders, from the same queries their pages use. */
function useNavCounts(siteId: string | undefined): Readonly<Record<string, number | undefined>> {
  const alarms = useQuery({ ...activeAlarmsQuery(siteId ?? ''), enabled: Boolean(siteId) });
  const workOrders = useQuery({ ...openWorkOrdersQuery(siteId ?? ''), enabled: Boolean(siteId) });
  return {
    'operations-alarms': alarms.data?.items.length,
    'operations-work-center': workOrders.data?.length,
  };
}

function NavItem({
  item,
  count,
  currentPath,
  siteQuery,
  onNavigate,
}: {
  readonly item: AppNavigationItem;
  readonly count: number | undefined;
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
      {count ? <SidebarMenuBadge aria-label={`${count} 项待处理`} className="tabular-nums">{count > 99 ? '99+' : count}</SidebarMenuBadge> : null}
    </SidebarMenuItem>
  );
}

export function AppSidebar({
  principalName,
  principalRole,
  onNavigate,
  onLogout,
}: AppSidebarProps) {
  const location = useLocation();
  const currentPath = location.pathname;
  const { currentScope } = useScope();
  const siteQuery = currentScope ? `?site=${currentScope.siteId}` : '';
  const counts = useNavCounts(currentScope?.siteId);

  return (
    <Sidebar collapsible="icon" variant="inset">
      <SidebarHeader>
        <ScopeSwitcher />
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
                    count={counts[item.id]}
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
