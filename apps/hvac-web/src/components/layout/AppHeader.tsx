import { useEffect, useRef, useState } from 'react';
import { useLocation, useMatches } from '@tanstack/react-router';
import { Bell, Moon, Search, Sun } from 'lucide-react';
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
import { CommandSearch } from './CommandSearch';
import { ScopeSwitcher } from './ScopeSwitcher';
import { APP_NAVIGATION_CONFIG } from './app-navigation';
import { useShellNotifications } from './use-shell-notifications';

interface AppHeaderProps {
  readonly principalSubject: string;
  readonly themeMode: 'light' | 'dark';
  readonly onThemeToggle: () => void;
  readonly onNavigate: (target: string) => void;
}

function resolveBreadcrumbs(pathname: string, routeTitle: string | undefined) {
  for (const group of APP_NAVIGATION_CONFIG) {
    for (const item of group.items) {
      if (pathname === item.path || pathname.startsWith(`${item.path}/`)) {
        return { group: group.label, page: item.title };
      }
    }
  }
  return { group: undefined, page: routeTitle };
}

export function AppHeader({
  principalSubject,
  themeMode,
  onThemeToggle,
  onNavigate,
}: AppHeaderProps) {
  const [commandOpen, setCommandOpen] = useState(false);
  const commandTriggerRef = useRef<HTMLButtonElement>(null);
  const location = useLocation();

  const routeTitle = useMatches({
    select: (matches) => [...matches].reverse().find((match) => match.staticData?.title)?.staticData.title,
  });
  const breadcrumbs = resolveBreadcrumbs(location.pathname, routeTitle);
  const { count: unreadNotifications } = useShellNotifications(true, principalSubject);

  useEffect(() => {
    const handleShortcut = (event: globalThis.KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLocaleLowerCase() === 'k') {
        event.preventDefault();
        setCommandOpen((current) => !current);
      }
    };
    window.addEventListener('keydown', handleShortcut);
    return () => window.removeEventListener('keydown', handleShortcut);
  }, []);

  return (
    <>
      <header className="sticky top-0 z-30 flex h-12 shrink-0 items-center justify-between gap-2 rounded-t-[inherit] border-b bg-background px-3 sm:px-4 lg:px-6">
        {/* Left: SidebarTrigger + Scope Switcher + Breadcrumb */}
        <div className="flex min-w-0 items-center gap-2.5">
          <SidebarTrigger className="-ml-1" />
          <Separator orientation="vertical" className="h-4" />

          {/* Scope Switcher: URL-driven scope selector */}
          <ScopeSwitcher />

          <Separator orientation="vertical" className="hidden h-4 md:block" />

          {/* Breadcrumb: pure navigational path */}
          {breadcrumbs.page ? (
            <Breadcrumb className="hidden lg:block">
              <BreadcrumbList className="text-xs">
                {breadcrumbs.group ? (
                  <>
                    <BreadcrumbItem>
                      <span className="text-muted-foreground">{breadcrumbs.group}</span>
                    </BreadcrumbItem>
                    <BreadcrumbSeparator />
                  </>
                ) : null}
                <BreadcrumbItem>
                  <BreadcrumbPage className="font-medium text-foreground">
                    {breadcrumbs.page}
                  </BreadcrumbPage>
                </BreadcrumbItem>
              </BreadcrumbList>
            </Breadcrumb>
          ) : null}
        </div>

        {/* Right: Search + My Work + Theme + User Menu */}
        <div className="flex items-center gap-1.5 sm:gap-2">
          {/* Global Search Button */}
          <Button
            ref={commandTriggerRef}
            variant="outline"
            size="sm"
            onClick={() => setCommandOpen(true)}
            className="hidden h-8 w-52 justify-between border-border/80 bg-background/60 text-xs text-muted-foreground sm:inline-flex md:w-60"
          >
            <div className="flex items-center gap-1.5">
              <Search className="size-3.5" />
              <span>跳转到页面...</span>
            </div>
            <Kbd>⌘K</Kbd>
          </Button>

          <Button
            variant="ghost"
            size="icon"
            onClick={() => setCommandOpen(true)}
            className="sm:hidden"
            aria-label="搜索"
          >
            <Search className="size-4" />
          </Button>

          {/* Theme Toggle */}
          <Button
            variant="ghost"
            size="icon"
            onClick={onThemeToggle}
            className="size-8"
            aria-label={themeMode === 'dark' ? '切换浅色' : '切换深色'}
          >
            {themeMode === 'dark' ? (
              <Sun className="size-4" />
            ) : (
              <Moon className="size-4" />
            )}
          </Button>

          {/* Notification Button */}
          <Button
            variant="ghost"
            size="icon"
            className="relative size-8"
            aria-label={unreadNotifications > 0 ? `通知中心，${unreadNotifications} 条未读` : '通知中心'}
            onClick={() => onNavigate('/notifications')}
          >
            <Bell className="size-4" />
            {unreadNotifications > 0 ? (
              <span className="absolute right-1 top-1 size-1.5 rounded-full bg-destructive" />
            ) : null}
          </Button>
        </div>
      </header>

      {/* Global Command Palette */}
      <CommandSearch
        open={commandOpen}
        onOpenChange={setCommandOpen}
        onNavigate={onNavigate}
      />
    </>
  );
}
