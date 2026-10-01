import { useLocation } from '@tanstack/react-router';
import { Zap } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
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
import {
  APP_NAVIGATION_CONFIG,
  isNavActive,
  type AppNavigationItem,
} from './app-navigation';

interface AppSidebarProps {
  readonly title?: string;
  readonly onNavigate: (target: string) => void;
}

function NavItem({
  item,
  currentPath,
  searchStr,
  onNavigate,
}: {
  readonly item: AppNavigationItem;
  readonly currentPath: string;
  readonly searchStr: string;
  readonly onNavigate: (target: string) => void;
}) {
  const active = isNavActive(item.path, currentPath);
  const Icon = item.icon;
  const { setOpenMobile } = useSidebar();

  const targetUrl = `${item.path}${searchStr}`;

  return (
    <SidebarMenuItem>
      <SidebarMenuButton
        asChild
        isActive={active}
        tooltip={item.title}
        className="transition-colors hover:bg-sidebar-accent/80 data-[active=true]:bg-primary/10 data-[active=true]:text-primary data-[active=true]:font-medium"
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
          <Icon className="size-4 shrink-0" aria-hidden="true" />
          <span className="truncate">{item.title}</span>
          {item.badge ? (
            <Badge
              variant={active ? 'default' : 'secondary'}
              className="ml-auto px-1.5 py-0 text-[10px] leading-4"
            >
              {item.badge}
            </Badge>
          ) : null}
        </a>
      </SidebarMenuButton>
    </SidebarMenuItem>
  );
}

export function AppSidebar({
  title = '智慧能源 SaaS 平台',
  onNavigate,
}: AppSidebarProps) {
  const location = useLocation();
  const currentPath = location.pathname;
  const searchStr = location.searchStr;

  return (
    <Sidebar collapsible="icon" variant="inset" className="border-r border-border/60">
      <SidebarHeader className="border-b border-border/50 pb-2">
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton
              size="lg"
              className="data-[state=open]:bg-sidebar-accent data-[state=open]:text-sidebar-accent-foreground cursor-pointer"
              onClick={() => onNavigate(`/overview${searchStr}`)}
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

      <SidebarContent className="px-2 py-2">
        {APP_NAVIGATION_CONFIG.map((group) => (
          <SidebarGroup key={group.id} className="py-1">
            <SidebarGroupLabel className="text-[11px] tracking-wider text-muted-foreground/80 group-data-[collapsible=icon]:hidden">
              {group.label}
            </SidebarGroupLabel>
            <SidebarGroupContent>
              <SidebarMenu>
                {group.items.map((item) => (
                  <NavItem
                    key={item.id}
                    item={item}
                    currentPath={currentPath}
                    searchStr={searchStr}
                    onNavigate={onNavigate}
                  />
                ))}
              </SidebarMenu>
            </SidebarGroupContent>
          </SidebarGroup>
        ))}
      </SidebarContent>

      <SidebarRail />
    </Sidebar>
  );
}
