import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useLocation } from '@tanstack/react-router';
import { ChevronDown, PanelLeft } from 'lucide-react';
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible';
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarMenu,
  SidebarMenuBadge,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarMenuSub,
  SidebarMenuSubButton,
  SidebarMenuSubItem,
  SidebarSeparator,
  useSidebar,
} from '@/components/ui/sidebar';
import { useAppTheme, type ThemeMode } from '@/app/ThemeGate';
import { activeAlarmsQuery } from '@/features/alarms/alarm-api';
import { openWorkOrdersQuery } from '@/features/work-orders/work-order-queries';
import { useScope } from '@/hooks/use-scope';
import { cn } from '@/lib/utils';
import {
  APP_NAVIGATION_CONFIG,
  isNavActive,
  isSinglePageGroup,
  type AppNavigationGroup,
  type AppNavigationItem,
} from './app-navigation';

interface AppSidebarProps {
  readonly className?: string;
  readonly onNavigate: (target: string) => void;
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

/** Alarms waiting on someone are urgent; everything else is a plain count. */
const URGENT_COUNTS = new Set(['operations-alarms']);

function CountBadge({ count, urgent }: { readonly count: number; readonly urgent: boolean }) {
  return (
    <SidebarMenuBadge
      aria-label={`${count} 项待处理`}
      className={cn(
        'top-1/2! h-[18px] min-w-[18px] -translate-y-1/2 rounded-full px-1.5 font-mono text-[11px] font-semibold',
        urgent ? 'bg-destructive-soft text-destructive-text!' : 'bg-secondary text-secondary-foreground!',
      )}
    >
      {count > 99 ? '99+' : count}
    </SidebarMenuBadge>
  );
}

function useLink(onNavigate: (target: string) => void) {
  const { currentScope } = useScope();
  const { setOpenMobile } = useSidebar();
  const siteQuery = currentScope ? `?site=${currentScope.siteId}` : '';
  return (item: AppNavigationItem) => {
    const href = `${item.path}${siteQuery}`;
    return {
      href,
      onClick: (event: React.MouseEvent) => {
        event.preventDefault();
        setOpenMobile(false);
        onNavigate(href);
      },
    };
  };
}

function SinglePageItem({ group, count, currentPath, link, trailing }: {
  readonly group: AppNavigationGroup;
  readonly count: number | undefined;
  readonly currentPath: string;
  readonly link: ReturnType<typeof useLink>;
  readonly trailing?: React.ReactNode;
}) {
  const item = group.items[0];
  const active = isNavActive(item.path, currentPath);
  const Icon = group.icon;
  return (
    <SidebarMenuItem>
      <SidebarMenuButton asChild isActive={active} tooltip={group.label}>
        <a {...link(item)} aria-current={active ? 'page' : undefined}>
          <Icon aria-hidden="true" />
          <span>{group.label}</span>
          {trailing}
        </a>
      </SidebarMenuButton>
      {count ? <CountBadge count={count} urgent={URGENT_COUNTS.has(item.id)} /> : null}
    </SidebarMenuItem>
  );
}

function GroupItem({ group, counts, currentPath, link }: {
  readonly group: AppNavigationGroup;
  readonly counts: Readonly<Record<string, number | undefined>>;
  readonly currentPath: string;
  readonly link: ReturnType<typeof useLink>;
}) {
  const { state } = useSidebar();
  const items = group.items.filter((item) => item.sidebar !== false);
  const activeItem = items.find((item) => isNavActive(item.path, currentPath));
  const [open, setOpen] = useState(Boolean(activeItem));
  const expanded = open || Boolean(activeItem);
  const Icon = group.icon;
  const groupCount = items.reduce((sum, item) => sum + (counts[item.id] ?? 0), 0);
  const groupUrgent = items.some((item) => URGENT_COUNTS.has(item.id) && (counts[item.id] ?? 0) > 0);

  // With the sidebar collapsed to icons the children are hidden, so the group icon goes straight to its page.
  if (state === 'collapsed') {
    const target = activeItem ?? items[0];
    return (
      <SidebarMenuItem>
        <SidebarMenuButton asChild isActive={Boolean(activeItem)} tooltip={group.label}>
          <a {...link(target)}>
            <Icon aria-hidden="true" />
            <span>{group.label}</span>
          </a>
        </SidebarMenuButton>
      </SidebarMenuItem>
    );
  }

  return (
    <Collapsible asChild open={expanded} onOpenChange={setOpen}>
      <SidebarMenuItem>
        <CollapsibleTrigger asChild>
          <SidebarMenuButton
            tooltip={group.label}
            className={cn(activeItem && 'font-semibold text-foreground')}
            aria-label={`${group.label}，${expanded ? '收起' : '展开'}`}
          >
            <Icon aria-hidden="true" />
            <span>{group.label}</span>
            <ChevronDown
              aria-hidden="true"
              className={cn('ml-auto size-3.5! text-subtle-foreground transition-transform duration-200', !expanded && '-rotate-90')}
            />
          </SidebarMenuButton>
        </CollapsibleTrigger>
        {!expanded && groupCount > 0 ? <CountBadge count={groupCount} urgent={groupUrgent} /> : null}
        <CollapsibleContent>
          <SidebarMenuSub>
            {items.map((item) => {
              const active = item === activeItem;
              const count = counts[item.id];
              return (
                <SidebarMenuSubItem key={item.id} className="relative">
                  <SidebarMenuSubButton asChild isActive={active}>
                    <a {...link(item)} aria-current={active ? 'page' : undefined}>
                      <span>{item.title}</span>
                    </a>
                  </SidebarMenuSubButton>
                  {count ? <CountBadge count={count} urgent={URGENT_COUNTS.has(item.id)} /> : null}
                </SidebarMenuSubItem>
              );
            })}
          </SidebarMenuSub>
        </CollapsibleContent>
      </SidebarMenuItem>
    </Collapsible>
  );
}

const THEME_OPTIONS: readonly { readonly mode: ThemeMode; readonly label: string }[] = [
  { mode: 'system', label: '系统' },
  { mode: 'light', label: '浅色' },
  { mode: 'dark', label: '深色' },
];

function SidebarControls() {
  const { mode, setMode } = useAppTheme();
  const { state, toggleSidebar } = useSidebar();
  const collapsed = state === 'collapsed';
  return (
    <div className={cn('flex items-center gap-2 border-t border-divider pt-2.5', collapsed ? 'justify-center' : 'justify-between pl-1')}>
      {collapsed ? null : (
        <div role="radiogroup" aria-label="外观" className="inline-flex rounded-full border border-divider bg-muted p-0.5">
          {THEME_OPTIONS.map((option) => (
            <button
              key={option.mode}
              type="button"
              role="radio"
              aria-checked={mode === option.mode}
              onClick={() => setMode(option.mode)}
              className={cn(
                'h-[22px] rounded-full px-2 text-[11px] font-medium text-muted-foreground transition-colors duration-100 hover:text-foreground',
                mode === option.mode && 'bg-primary-soft text-primary hover:text-primary',
              )}
            >
              {option.label}
            </button>
          ))}
        </div>
      )}
      <button
        type="button"
        onClick={toggleSidebar}
        aria-label={collapsed ? '展开侧栏' : '收起侧栏'}
        className="inline-flex size-7 items-center justify-center rounded-md text-subtle-foreground transition-colors hover:bg-accent hover:text-foreground"
      >
        <PanelLeft className="size-[15px]" aria-hidden="true" />
      </button>
    </div>
  );
}

export function AppSidebar({ className, onNavigate }: AppSidebarProps) {
  const location = useLocation();
  const currentPath = location.pathname;
  const { currentScope } = useScope();
  const counts = useNavCounts(currentScope?.siteId);
  const link = useLink(onNavigate);
  const groups = (section: AppNavigationGroup['section']) => APP_NAVIGATION_CONFIG.filter((group) => group.section === section);

  return (
    <Sidebar collapsible="icon" className={className}>
      <SidebarContent className="gap-0 px-2.5 pt-3.5">
        <SidebarGroup className="p-0">
          <SidebarMenu className="gap-0.5">
            {groups('main').map((group) =>
              isSinglePageGroup(group)
                ? <SinglePageItem key={group.id} group={group} count={counts[group.items[0].id]} currentPath={currentPath} link={link} />
                : <GroupItem key={group.id} group={group} counts={counts} currentPath={currentPath} link={link} />,
            )}
          </SidebarMenu>
        </SidebarGroup>
        <SidebarSeparator className="mx-1.5 my-3 bg-divider" />
        <SidebarGroup className="p-0">
          <SidebarMenu>
            {groups('control').map((group) => (
              <SinglePageItem
                key={group.id}
                group={group}
                count={undefined}
                currentPath={currentPath}
                link={link}
                trailing={(
                  <span className="ml-auto rounded-full border border-border px-[7px] text-[11px] leading-[18px] font-medium text-muted-foreground group-data-[collapsible=icon]:hidden">
                    需审批
                  </span>
                )}
              />
            ))}
          </SidebarMenu>
        </SidebarGroup>
      </SidebarContent>
      <SidebarFooter className="gap-1 px-2.5 pb-2.5">
        <SidebarMenu className="gap-0.5">
          {groups('platform').flatMap((group) => group.items.filter((item) => item.sidebar !== false)).map((item) => {
            const active = isNavActive(item.path, currentPath);
            const Icon = item.icon;
            return (
              <SidebarMenuItem key={item.id}>
                <SidebarMenuButton asChild isActive={active} tooltip={item.title} className={cn(!active && 'h-[30px] font-normal text-muted-foreground')}>
                  <a {...link(item)} aria-current={active ? 'page' : undefined}>
                    <Icon aria-hidden="true" className="size-[15px]!" />
                    <span>{item.title}</span>
                  </a>
                </SidebarMenuButton>
              </SidebarMenuItem>
            );
          })}
        </SidebarMenu>
        <SidebarControls />
      </SidebarFooter>
    </Sidebar>
  );
}
