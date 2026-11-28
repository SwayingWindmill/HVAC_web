import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useLocation, useRouter } from '@tanstack/react-router';
import { ChevronDown, PanelLeft } from 'lucide-react';
import { preloadPathChunks } from '@/app/router-paths';
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
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
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { useAppTheme, type ThemeMode } from '@/app/ThemeGate';
import { useShellSnapshot } from '@/app/ShellRuntimeContext';
import { activeAlarmsQuery } from '@/features/alarms/alarm-api';
import { siteCommandsQuery } from '@/features/control/command-api';
import { needsPerson } from '@/features/control/command-presentation';
import { openWorkOrdersQuery } from '@/features/work-orders/work-order-queries';
import { useScope } from '@/hooks/use-scope';
import { cn } from '@/lib/utils';
import {
  APP_NAVIGATION_CONFIG,
  filterNavigationGroups,
  isNavActive,
  isSinglePageGroup,
  navCountQueryFlags,
  navHref,
  type AppNavigationGroup,
  type AppNavigationItem,
} from './app-navigation';

interface AppSidebarProps {
  readonly className?: string;
  readonly onNavigate: (target: string) => void;
}

/** What waits on the operator: active alarms, open work orders and commands awaiting approval or a site check, from the same queries their pages use. */
function useNavCounts(
  siteId: string | undefined,
  capabilities: readonly string[] | undefined,
): { readonly counts: Readonly<Record<string, number | undefined>>; readonly hasWaitingAlarms: boolean } {
  const flags = navCountQueryFlags(siteId, capabilities);
  const alarms = useQuery({ ...activeAlarmsQuery(siteId ?? ''), enabled: flags.alarms });
  const workOrders = useQuery({ ...openWorkOrdersQuery(siteId ?? ''), enabled: flags.workOrders });
  const commands = useQuery({ ...siteCommandsQuery(siteId ?? ''), enabled: flags.commands });
  const hasWaitingAlarms = flags.alarms ? (alarms.data?.items.some((alarm) => !alarm.acknowledgement) ?? false) : false;
  return {
    counts: {
      'operations-alarms': flags.alarms ? alarms.data?.items.length : undefined,
      'operations-work-center': flags.workOrders ? workOrders.data?.length : undefined,
      'operations-control': flags.commands ? commands.data?.filter(needsPerson).length : undefined,
    },
    hasWaitingAlarms,
  };
}

type CountBadgeTone = 'urgent' | 'warning' | 'neutral';

/** Red only when alarms are unacknowledged; amber for pending control commands; neutral otherwise. */
function countBadgeTone(itemId: string, hasWaitingAlarms: boolean): CountBadgeTone {
  if (itemId === 'operations-alarms') return hasWaitingAlarms ? 'urgent' : 'neutral';
  if (itemId === 'operations-control') return 'warning';
  return 'neutral';
}

function toneClasses(tone: CountBadgeTone): string {
  if (tone === 'urgent') return 'bg-destructive-soft text-destructive-text!';
  if (tone === 'warning') return 'bg-warning-soft text-warning-text!';
  return 'bg-secondary text-secondary-foreground!';
}

function CountBadge({ count, tone }: { readonly count: number; readonly tone: CountBadgeTone }) {
  return (
    <SidebarMenuBadge
      aria-label={`${count} 项待处理`}
      className={cn(
        'top-1/2! h-[18px] min-w-[18px] -translate-y-1/2 rounded-full px-1.5 font-mono text-[11px] font-semibold',
        toneClasses(tone),
      )}
    >
      {count > 99 ? '99+' : count}
    </SidebarMenuBadge>
  );
}

function useLink(onNavigate: (target: string) => void) {
  const router = useRouter();
  const { currentScope } = useScope();
  const snapshot = useShellSnapshot();
  const { setOpenMobile } = useSidebar();
  const siteId = currentScope?.siteId ?? snapshot.lastSiteId;
  return (item: AppNavigationItem) => {
    const href = navHref(item, siteId);
    const preload = () => preloadPathChunks(router, href);
    return {
      href,
      onMouseEnter: preload,
      onFocus: preload,
      onClick: (event: React.MouseEvent) => {
        if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0) return;
        event.preventDefault();
        setOpenMobile(false);
        onNavigate(href);
      },
    };
  };
}

function SinglePageItem({ group, count, hasWaitingAlarms, currentPath, link }: {
  readonly group: AppNavigationGroup;
  readonly count: number | undefined;
  readonly hasWaitingAlarms: boolean;
  readonly currentPath: string;
  readonly link: ReturnType<typeof useLink>;
}) {
  const item = group.items[0];
  const active = isNavActive(item.path, currentPath);
  const Icon = item.icon;
  return (
    <SidebarMenuItem>
      <SidebarMenuButton asChild isActive={active} tooltip={item.title}>
        <a {...link(item)} aria-current={active ? 'page' : undefined}>
          <Icon aria-hidden="true" />
          <span>{item.title}</span>
        </a>
      </SidebarMenuButton>
      {count ? <CountBadge count={count} tone={countBadgeTone(item.id, hasWaitingAlarms)} /> : null}
    </SidebarMenuItem>
  );
}

function GroupItem({ group, counts, hasWaitingAlarms, currentPath, link }: {
  readonly group: AppNavigationGroup;
  readonly counts: Readonly<Record<string, number | undefined>>;
  readonly hasWaitingAlarms: boolean;
  readonly currentPath: string;
  readonly link: ReturnType<typeof useLink>;
}) {
  const { state } = useSidebar();
  const items = group.items.filter((item) => item.sidebar !== false);
  const activeItem = items.find((item) => isNavActive(item.path, currentPath));
  const hasActive = Boolean(activeItem);
  const [prevHasActive, setPrevHasActive] = useState(hasActive);
  const [open, setOpen] = useState(hasActive);
  if (hasActive !== prevHasActive) {
    setPrevHasActive(hasActive);
    if (hasActive) setOpen(true);
  }
  const [flyoutOpen, setFlyoutOpen] = useState(false);
  const Icon = group.icon;
  const groupCount = items.reduce((sum, item) => sum + (counts[item.id] ?? 0), 0);
  const groupUrgent = items.some((item) => countBadgeTone(item.id, hasWaitingAlarms) === 'urgent' && (counts[item.id] ?? 0) > 0);

  if (state === 'collapsed') {
    return (
      <SidebarMenuItem>
        <Popover open={flyoutOpen} onOpenChange={setFlyoutOpen}>
          <PopoverTrigger asChild>
            <SidebarMenuButton isActive={hasActive} tooltip={group.label} aria-label={group.label} className="relative">
              <Icon aria-hidden="true" />
              <span>{group.label}</span>
              {groupCount > 0 ? (
                <span
                  aria-hidden="true"
                  className={cn(
                    'absolute top-1.5 right-1.5 size-1.5 rounded-full ring-2 ring-sidebar',
                    groupUrgent ? 'bg-destructive' : 'bg-primary',
                  )}
                />
              ) : null}
            </SidebarMenuButton>
          </PopoverTrigger>
          <PopoverContent side="right" align="start" sideOffset={8} className="w-48 p-1.5">
            <p className="px-2 py-1 text-xs font-medium text-muted-foreground">{group.label}</p>
            <div className="flex flex-col gap-0.5">
              {items.map((item) => {
                const active = item === activeItem;
                const count = counts[item.id];
                const itemProps = link(item);
                return (
                  <a
                    key={item.id}
                    href={itemProps.href}
                    aria-current={active ? 'page' : undefined}
                    onMouseEnter={itemProps.onMouseEnter}
                    onFocus={itemProps.onFocus}
                    onClick={(event) => {
                      itemProps.onClick(event);
                      setFlyoutOpen(false);
                    }}
                    className={cn(
                      'flex items-center justify-between rounded-md px-2 py-1.5 text-sm transition-colors hover:bg-accent hover:text-accent-foreground',
                      active && 'bg-primary-soft font-medium text-primary',
                    )}
                  >
                    <span>{item.title}</span>
                    {count ? (
                      <span className={cn('inline-flex h-[18px] min-w-[18px] items-center justify-center rounded-full px-1.5 font-mono text-[11px] font-semibold', toneClasses(countBadgeTone(item.id, hasWaitingAlarms)))}>
                        {count > 99 ? '99+' : count}
                      </span>
                    ) : null}
                  </a>
                );
              })}
            </div>
          </PopoverContent>
        </Popover>
      </SidebarMenuItem>
    );
  }

  return (
    <Collapsible asChild open={open} onOpenChange={setOpen}>
      <SidebarMenuItem>
        <CollapsibleTrigger asChild>
          <SidebarMenuButton
            tooltip={group.label}
            className={cn(activeItem && 'font-semibold text-foreground')}
            aria-label={`${group.label}，${open ? '收起' : '展开'}`}
          >
            <Icon aria-hidden="true" />
            <span className="flex-1 truncate">{group.label}</span>
            {!open && groupCount > 0 ? (
              <span className={cn('inline-flex h-[18px] min-w-[18px] shrink-0 items-center justify-center rounded-full px-1.5 font-mono text-[11px] font-semibold', toneClasses(groupUrgent ? 'urgent' : 'neutral'))}>
                {groupCount > 99 ? '99+' : groupCount}
              </span>
            ) : null}
            <ChevronDown
              aria-hidden="true"
              className={cn('size-3.5! shrink-0 text-subtle-foreground transition-transform duration-200', !open && '-rotate-90')}
            />
          </SidebarMenuButton>
        </CollapsibleTrigger>
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
                  {count ? <CountBadge count={count} tone={countBadgeTone(item.id, hasWaitingAlarms)} /> : null}
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
      <Tooltip>
        <TooltipTrigger asChild>
          <button
            type="button"
            onClick={toggleSidebar}
            aria-label={collapsed ? '展开侧栏' : '收起侧栏'}
            className="inline-flex size-7 items-center justify-center rounded-md text-subtle-foreground transition-colors hover:bg-accent hover:text-foreground"
          >
            <PanelLeft className="size-[15px]" aria-hidden="true" />
          </button>
        </TooltipTrigger>
        <TooltipContent side="right">
          {collapsed ? '展开侧栏 (Ctrl+B)' : '收起侧栏 (Ctrl+B)'}
        </TooltipContent>
      </Tooltip>
    </div>
  );
}

export function AppSidebar({ className, onNavigate }: AppSidebarProps) {
  const location = useLocation();
  const currentPath = location.pathname;
  const { currentScope } = useScope();
  const snapshot = useShellSnapshot();
  const capabilities = snapshot.principal?.authorization.capabilities;
  const { counts, hasWaitingAlarms } = useNavCounts(currentScope?.siteId, capabilities);
  const link = useLink(onNavigate);
  const visibleGroups = filterNavigationGroups(APP_NAVIGATION_CONFIG, capabilities, { sidebarOnly: true });
  const groups = (section: AppNavigationGroup['section']) => visibleGroups.filter((group) => group.section === section);
  const mainGroups = groups('main');
  const controlGroups = groups('control');
  const platformItems = groups('platform').flatMap((group) => group.items);

  return (
    <Sidebar collapsible="icon" className={className}>
      <SidebarContent className="gap-0 px-2.5 pt-3.5">
        {mainGroups.length > 0 ? (
          <SidebarGroup className="p-0">
            <SidebarMenu className="gap-0.5">
              {mainGroups.map((group) =>
                isSinglePageGroup(group)
                  ? <SinglePageItem key={group.id} group={group} count={counts[group.items[0].id]} hasWaitingAlarms={hasWaitingAlarms} currentPath={currentPath} link={link} />
                  : <GroupItem key={group.id} group={group} counts={counts} hasWaitingAlarms={hasWaitingAlarms} currentPath={currentPath} link={link} />,
              )}
            </SidebarMenu>
          </SidebarGroup>
        ) : null}
        {mainGroups.length > 0 && controlGroups.length > 0 ? <SidebarSeparator className="mx-1.5 my-3 bg-divider" /> : null}
        {controlGroups.length > 0 ? (
          <SidebarGroup className="p-0">
            <SidebarMenu>
              {controlGroups.map((group) => (
                <SinglePageItem
                  key={group.id}
                  group={group}
                  count={counts[group.items[0].id]}
                  hasWaitingAlarms={hasWaitingAlarms}
                  currentPath={currentPath}
                  link={link}
                />
              ))}
            </SidebarMenu>
          </SidebarGroup>
        ) : null}
      </SidebarContent>
      <SidebarFooter className="gap-1 px-2.5 pb-2.5">
        {platformItems.length > 0 ? (
          <SidebarMenu className="gap-0.5">
            {platformItems.map((item) => {
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
        ) : null}
        <SidebarControls />
      </SidebarFooter>
    </Sidebar>
  );
}
