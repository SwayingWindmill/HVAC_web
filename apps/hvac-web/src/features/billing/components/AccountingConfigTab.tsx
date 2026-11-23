import { useState } from 'react';
import { Button } from '@/components/ui/button';
import { MeterRatioVersionsPanel } from './MeterRatioVersionsPanel';
import { SiteHolidaysPanel } from './SiteHolidaysPanel';

export interface AccountingConfigTabProps {
  readonly siteId: string;
  readonly timezone: string;
  readonly csrfToken: string;
  readonly canManageBilling: boolean;
  readonly now: Date;
}

export function AccountingConfigTab({
  siteId,
  timezone,
  csrfToken,
  canManageBilling,
  now,
}: AccountingConfigTabProps) {
  const [configNotice, setConfigNotice] = useState<string | null>(null);

  return (
    <div className="flex flex-col gap-4">
      {configNotice ? (
        <div
          role="status"
          aria-live="polite"
          data-testid="accounting-config-notice"
          className="flex items-center justify-between gap-3 rounded-lg border border-primary-line bg-primary-soft/60 px-4 py-2.5 text-xs text-foreground"
        >
          <span>{configNotice}</span>
          <Button
            type="button"
            variant="ghost"
            size="xs"
            onClick={() => setConfigNotice(null)}
          >
            关闭提示
          </Button>
        </div>
      ) : null}

      <SiteHolidaysPanel
        siteId={siteId}
        timezone={timezone}
        csrfToken={csrfToken}
        canManageBilling={canManageBilling}
        now={now}
        onNotice={setConfigNotice}
      />

      <MeterRatioVersionsPanel
        siteId={siteId}
        timezone={timezone}
        csrfToken={csrfToken}
        canManageBilling={canManageBilling}
        now={now}
        onNotice={setConfigNotice}
      />
    </div>
  );
}
