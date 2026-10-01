import { useEffect, useRef, useState } from 'react';
import { useLocation } from '@tanstack/react-router';
import {
  Bell,
  ChevronDown,
  LogOut,
  Moon,
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
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { Kbd } from '@/components/ui/kbd';
import { Separator } from '@/components/ui/separator';
import { SidebarTrigger } from '@/components/ui/sidebar';
import { CommandSearch } from './CommandSearch';
import { MyWorkDrawer } from './MyWorkDrawer';
import { ScopeSwitcher } from './ScopeSwitcher';
import { APP_NAVIGATION_CONFIG } from './app-navigation';

interface AppHeaderProps {
  readonly principalName?: string;
  readonly principalRole?: string;
  readonly themeMode: 'light' | 'dark';
  readonly onThemeToggle: () => void;
  readonly onNavigate: (target: string) => void;
  readonly onLogout?: () => void;
}

function resolveBreadcrumbs(pathname: string) {
  for (const group of APP_NAVIGATION_CONFIG) {
    for (const item of group.items) {
      if (item.path === pathname || (item.path !== '/overview' && pathname.startsWith(item.path))) {
        return {
          group: group.label.split(' ')[0], // e.g. "运行管理"
          page: item.title, // e.g. "系统与设备"
        };
      }
    }
  }
  return {
    group: '智慧能源',
    page: pathname === '/overview' || pathname === '/' ? '总览看板' : '工作台',
  };
}

export function AppHeader({
  principalName = '张工 (能源总监)',
  principalRole = '系统主管',
  themeMode,
  onThemeToggle,
  onNavigate,
  onLogout,
}: AppHeaderProps) {
  const [commandOpen, setCommandOpen] = useState(false);
  const commandTriggerRef = useRef<HTMLButtonElement>(null);
  const location = useLocation();

  const breadcrumbs = resolveBreadcrumbs(location.pathname);

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
      <header className="sticky top-0 z-30 flex h-14 shrink-0 items-center justify-between border-b border-border/60 bg-background/95 px-3 backdrop-blur supports-[backdrop-filter]:bg-background/80 sm:px-4">
        {/* Left: SidebarTrigger + Scope Switcher + Breadcrumb */}
        <div className="flex min-w-0 items-center gap-2.5">
          <SidebarTrigger className="-ml-1" />
          <Separator orientation="vertical" className="h-4" />

          {/* Scope Switcher: URL-driven scope selector */}
          <ScopeSwitcher />

          <Separator orientation="vertical" className="hidden h-4 md:block" />

          {/* Breadcrumb: pure navigational path */}
          <Breadcrumb className="hidden lg:block">
            <BreadcrumbList className="text-xs">
              <BreadcrumbItem>
                <span className="text-muted-foreground">{breadcrumbs.group}</span>
              </BreadcrumbItem>
              <BreadcrumbSeparator />
              <BreadcrumbItem>
                <BreadcrumbPage className="font-medium text-foreground">
                  {breadcrumbs.page}
                </BreadcrumbPage>
              </BreadcrumbItem>
            </BreadcrumbList>
          </Breadcrumb>
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
              <span>搜索功能、设备、机会...</span>
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

          {/* My Work Quick Access Drawer */}
          <MyWorkDrawer onNavigate={onNavigate} />

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
            aria-label="通知中心"
            onClick={() => onNavigate('/operations/alarms')}
          >
            <Bell className="size-4" />
            <span className="absolute right-1 top-1 size-1.5 rounded-full bg-rose-500" />
          </Button>

          <Separator orientation="vertical" className="h-4" />

          {/* User Profile Dropdown */}
          <DropdownMenu modal={false}>
            <DropdownMenuTrigger asChild>
              <Button
                variant="ghost"
                className="h-8 gap-2 px-1.5 hover:bg-accent/80"
                aria-label="用户账户菜单"
              >
                <Avatar className="size-6 border border-border/60">
                  <AvatarFallback className="bg-primary/10 text-[11px] font-semibold text-primary">
                    张
                  </AvatarFallback>
                </Avatar>
                <div className="hidden min-w-0 text-left md:block">
                  <span className="block truncate text-xs font-medium leading-none text-foreground">
                    {principalName}
                  </span>
                  <span className="block truncate text-[10px] leading-tight text-muted-foreground">
                    {principalRole}
                  </span>
                </div>
                <ChevronDown className="hidden size-3 text-muted-foreground md:block" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-52">
              <div className="px-2.5 py-1.5">
                <p className="text-xs font-medium text-foreground">{principalName}</p>
                <p className="text-[11px] text-muted-foreground">{principalRole}</p>
              </div>
              <DropdownMenuSeparator />
              <DropdownMenuGroup>
                <DropdownMenuItem
                  className="cursor-pointer gap-2 text-xs"
                  onSelect={() => onNavigate('/settings')}
                >
                  <ShieldCheck className="size-3.5 text-muted-foreground" />
                  <span>权限与组织</span>
                </DropdownMenuItem>
                <DropdownMenuItem
                  className="cursor-pointer gap-2 text-xs"
                  onSelect={() => onNavigate('/settings')}
                >
                  <Settings2 className="size-3.5 text-muted-foreground" />
                  <span>平台配置</span>
                </DropdownMenuItem>
              </DropdownMenuGroup>
              <DropdownMenuSeparator />
              <DropdownMenuItem
                variant="destructive"
                className="cursor-pointer gap-2 text-xs"
                onSelect={() => onLogout?.()}
              >
                <LogOut className="size-3.5" />
                <span>退出登录</span>
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
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
