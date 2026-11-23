import { useEffect, useState } from 'react';
import { Bell, Search } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Kbd } from '@/components/ui/kbd';
import { CommandSearch } from './CommandSearch';
import { NavUser } from './NavUser';
import { ScopeSwitcher } from './ScopeSwitcher';
import { useWaitingAlarmCount } from '@/features/alarms/use-waiting-alarm-count';
import { ControlModeChip } from '@/features/control/components/ControlModeChip';
import { BrandMark } from '@/components/brand-mark';
import { cn } from '@/lib/utils';

import { TenantSwitcher } from '@/components/layout/TenantSwitcher';

interface AppHeaderProps {
  /** The Site the operator works in; the bell counts its alarms happening now that nobody acknowledged. */
  readonly siteId?: string;
  /** Remembered workspace Site when currently viewing a platform-wide route. */
  readonly preferredSiteId?: string;
  readonly principalName: string;
  readonly principalRole: string;
  readonly csrfToken: string;
  readonly onNavigate: (target: string) => void;
  readonly onLogout: () => void;
  /** Present on Site pages, where the assistant has a Site to answer about. */
  readonly assistant?: { readonly open: boolean; readonly onToggle: () => void };
}

function siteTarget(path: string, siteId?: string): string {
  return siteId ? `${path}?site=${encodeURIComponent(siteId)}` : path;
}

/** The top bar across the whole window: product mark and Site on the left, search, notifications and the user on the right. */
export function AppHeader({
  siteId,
  preferredSiteId,
  principalName,
  principalRole,
  csrfToken,
  onNavigate,
  onLogout,
  assistant,
}: AppHeaderProps) {
  const [commandOpen, setCommandOpen] = useState(false);
  const waiting = useWaitingAlarmCount(siteId);
  const linkSiteId = siteId ?? preferredSiteId;
  const overviewHref = siteTarget('/overview', linkSiteId);
  const notificationsHref = siteTarget('/notifications', linkSiteId);

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
      <header className="sticky top-0 z-40 flex h-(--header-height) w-full shrink-0 items-center gap-2 border-b bg-card pr-4 pl-[18px] print:hidden">
        <a
          href={overviewHref}
          aria-label="泉来禾智慧能源，回到总览"
          onClick={(event) => {
            event.preventDefault();
            onNavigate(overviewHref);
          }}
          className="flex shrink-0 rounded-[7px] outline-none focus-visible:ring-3 focus-visible:ring-ring-soft"
        >
          <BrandMark className="size-7" />
        </a>
        <TenantSwitcher />
        <span className="mx-0.5 text-xl font-light text-border" aria-hidden="true">/</span>
        <ScopeSwitcher />
        {siteId ? <ControlModeChip siteId={siteId} /> : null}

        <div className="ml-auto flex items-center gap-2">
          <Button
            variant="outline"
            onClick={() => setCommandOpen(true)}
            className="w-[280px] justify-start gap-2 bg-muted px-2.5 font-normal text-subtle-foreground shadow-none hover:bg-muted hover:text-muted-foreground"
          >
            <Search className="size-3.5" aria-hidden="true" />
            <span className="flex-1 text-left">搜索页面与设备</span>
            <Kbd>Ctrl K</Kbd>
          </Button>
          {assistant !== undefined ? (
            <Button
              variant="outline"
              className={cn('gap-1.5', assistant.open && 'border-input-hover bg-accent text-foreground')}
              aria-pressed={assistant.open}
              onClick={assistant.onToggle}
            >
              <BrandMark className="size-3.5 text-primary" />
              AI 助手
            </Button>
          ) : null}
          <Button
            variant="outline"
            size="icon"
            className="relative text-muted-foreground"
            aria-label={waiting > 0 ? `通知，${waiting} 条告警待确认` : '通知'}
            onClick={() => onNavigate(notificationsHref)}
          >
            <Bell className="size-[15px]" />
            {waiting > 0 ? (
              <span className="absolute top-1.5 right-[7px] size-[7px] rounded-full bg-primary ring-2 ring-card" aria-hidden="true" />
            ) : null}
          </Button>
          <NavUser name={principalName} role={principalRole} csrfToken={csrfToken} onNavigate={onNavigate} onLogout={onLogout} />
        </div>
      </header>

      <CommandSearch open={commandOpen} onOpenChange={setCommandOpen} onNavigate={onNavigate} />
    </>
  );
}
