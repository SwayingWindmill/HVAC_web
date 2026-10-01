import { useEffect, useRef, useState } from 'react';
import {
  Bell,
  ChevronDown,
  LogOut,
  Moon,
  RadioTower,
  Search,
  Settings2,
  ShieldCheck,
  Sun,
} from 'lucide-react';
import { Avatar, AvatarFallback } from '@/components/ui/avatar';
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from '@/components/ui/breadcrumb';
import { Button } from '@/components/ui/button';
import { Kbd } from '@/components/ui/kbd';
import { Separator } from '@/components/ui/separator';
import { SidebarTrigger } from '@/components/ui/sidebar';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import type { AppNavigationEntry } from './app-navigation';
import { CommandSearch } from './CommandSearch';

interface AppHeaderProps {
  readonly pageTitle: string;
  readonly pageTitleAsHeading?: boolean;
  readonly scopeLabel?: string;
  readonly navigation: readonly AppNavigationEntry[];
  readonly principalName: string;
  readonly principalRole: string;
  readonly themeMode: 'light' | 'dark';
  readonly submittingLogout: boolean;
  readonly notificationCount: number;
  readonly notificationLabel: string;
  readonly notificationDisabled: boolean;
  readonly realtimeLabel: string;
  readonly realtimeState: string;
  readonly onNavigate: (target: string) => void;
  readonly onNotificationOpen: () => void;
  readonly onThemeToggle: () => void;
  readonly onLogout: () => void;
}

export function AppHeader({
  pageTitle,
  pageTitleAsHeading = true,
  scopeLabel,
  navigation,
  principalName,
  principalRole,
  themeMode,
  submittingLogout,
  notificationCount,
  notificationLabel,
  notificationDisabled,
  realtimeLabel,
  realtimeState,
  onNavigate,
  onNotificationOpen,
  onThemeToggle,
  onLogout,
}: AppHeaderProps) {
  const [commandOpen, setCommandOpen] = useState(false);
  const commandTriggerRef = useRef<HTMLButtonElement>(null);
  const changeCommandOpen = (open: boolean) => {
    setCommandOpen(open);
    if (!open) requestAnimationFrame(() => commandTriggerRef.current?.focus({ preventScroll: true }));
  };

  useEffect(() => {
    const handleShortcut = (event: globalThis.KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLocaleLowerCase() === 'k') {
        event.preventDefault();
        setCommandOpen((current) => {
          const next = !current;
          if (!next) requestAnimationFrame(() => commandTriggerRef.current?.focus({ preventScroll: true }));
          return next;
        });
      }
    };
    window.addEventListener('keydown', handleShortcut);
    return () => window.removeEventListener('keydown', handleShortcut);
  }, []);

  const showScope = Boolean(scopeLabel && scopeLabel !== pageTitle && scopeLabel !== '平台范围');
  const avatarLabel = principalName.trim().slice(0, 1) || '用';

  return (
    <>
      <header className="sticky top-0 z-30 flex h-14 shrink-0 items-center gap-2 border-b bg-background/95 px-3 backdrop-blur supports-[backdrop-filter]:bg-background/80 sm:px-4">
        <SidebarTrigger className="-ml-1" />
        <Separator orientation="vertical" className="mr-2 data-[orientation=vertical]:h-4" />
        <div className="flex min-w-0 items-center gap-3">
          <Breadcrumb>
            <BreadcrumbList className="flex-nowrap">
              {showScope ? (
                <>
                  <BreadcrumbItem className="hidden min-w-0 sm:inline-flex">
                    <span className="max-w-44 truncate">{scopeLabel}</span>
                  </BreadcrumbItem>
                  <BreadcrumbSeparator className="hidden sm:list-item" />
                </>
              ) : null}
              <BreadcrumbItem className="min-w-0">
                <BreadcrumbPage className="truncate font-medium" data-testid="real-shell-surface-identity">
                  {pageTitleAsHeading
                    ? <h1 className="truncate text-sm font-medium">{pageTitle}</h1>
                    : <span className="truncate text-sm font-medium">{pageTitle}</span>}
                </BreadcrumbPage>
              </BreadcrumbItem>
            </BreadcrumbList>
          </Breadcrumb>
          <span className="hidden items-center gap-1.5 text-xs text-muted-foreground xl:inline-flex" data-state={realtimeState} title={realtimeLabel}>
            <RadioTower className="size-3.5" aria-hidden="true" />
            {realtimeLabel}
          </span>
        </div>

        <div className="ml-auto flex items-center gap-1">
          <Button
            ref={commandTriggerRef}
            className="hidden w-56 justify-start gap-2 text-muted-foreground md:inline-flex lg:w-64"
            variant="outline"
            size="sm"
            onClick={() => setCommandOpen(true)}
          >
            <Search className="size-3.5" aria-hidden="true" />
            <span className="flex-1 text-left">搜索页面与功能</span>
            <Kbd>⌘K</Kbd>
          </Button>
          <Button className="md:hidden" variant="ghost" size="icon-sm" aria-label="搜索页面与功能" onClick={() => setCommandOpen(true)}>
            <Search />
          </Button>
          <Button variant="ghost" size="icon-sm" onClick={onThemeToggle} aria-label={themeMode === 'dark' ? '切换为浅色' : '切换为深色'}>
            {themeMode === 'dark' ? <Sun /> : <Moon />}
          </Button>
          <Button
            className="relative"
            variant="ghost"
            size="icon-sm"
            onClick={onNotificationOpen}
            disabled={notificationDisabled}
            aria-label={notificationLabel}
            title={notificationLabel}
          >
            <Bell />
            {notificationCount > 0 ? <span className="absolute right-0.5 top-0.5 size-1.5 rounded-full bg-destructive" aria-hidden="true" /> : null}
          </Button>
          <DropdownMenu modal={false}>
            <DropdownMenuTrigger asChild>
              <Button variant="ghost" className="h-8 gap-2 px-1.5" data-testid="real-account-trigger" aria-label="打开账户菜单">
                <Avatar className="size-6">
                  <AvatarFallback>{avatarLabel}</AvatarFallback>
                </Avatar>
                <span className="hidden min-w-0 max-w-28 text-left lg:block">
                  <strong className="block truncate text-xs font-medium leading-4">{principalName}</strong>
                  <small className="block truncate text-[10px] leading-3 text-muted-foreground">{principalRole}</small>
                </span>
                <ChevronDown className="hidden size-3 text-muted-foreground lg:block" aria-hidden="true" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-48">
              <div className="px-2 py-1.5">
                <strong className="block truncate text-xs font-medium">{principalName}</strong>
                <small className="block truncate text-[11px] text-muted-foreground">{principalRole}</small>
              </div>
              <DropdownMenuSeparator />
              <DropdownMenuItem onSelect={() => onNavigate('/settings/access')} data-testid="real-account-access">
                <ShieldCheck />
                <span>用户与权限</span>
              </DropdownMenuItem>
              <DropdownMenuItem onSelect={() => onNavigate('/settings/sites')} data-testid="real-account-settings">
                <Settings2 />
                <span>系统配置</span>
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem className="text-destructive focus:text-destructive" disabled={submittingLogout} onSelect={onLogout}>
                <LogOut />
                <span data-testid="real-logout-button">{submittingLogout ? '正在退出…' : '退出登录'}</span>
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </header>
      <CommandSearch open={commandOpen} entries={navigation} onOpenChange={changeCommandOpen} onNavigate={onNavigate} />
    </>
  );
}
