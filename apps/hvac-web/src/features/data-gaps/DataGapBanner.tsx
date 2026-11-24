import { useQuery } from '@tanstack/react-query';
import { TriangleAlert } from 'lucide-react';
import { useShellSnapshot } from '@/app/ShellRuntimeContext';
import { PanelLink } from '@/blocks/panel';
import { cn } from '@/lib/utils';
import { summarizeDataGaps, type DataGapBannerContext } from './model';
import { siteDataGapsQuery } from './query';

export interface DataGapBannerProps {
  readonly siteId: string;
  readonly timeZone: string;
  readonly context?: DataGapBannerContext;
  readonly className?: string;
}

export function DataGapBanner({
  siteId,
  timeZone,
  context = 'general',
  className,
}: DataGapBannerProps) {
  const snapshot = useShellSnapshot();
  const capabilities = snapshot.principal?.authorization.capabilities;
  const canOpenIntegrations = Boolean(
    capabilities?.includes('site.list') && (capabilities?.includes('device.read') || capabilities?.includes('asset.read')),
  );
  const canOpenDevices = Boolean(capabilities?.includes('site.read') && capabilities?.includes('device.list'));
  const query = useQuery(siteDataGapsQuery(siteId));

  if (query.isError) {
    return (
      <div
        role="status"
        aria-label="数据缺口提示"
        data-testid="data-gap-banner-error"
        className={cn(
          'flex flex-wrap items-center justify-between gap-x-4 gap-y-1.5 rounded-lg border border-divider bg-muted/40 px-4 py-2.5 text-xs text-muted-foreground print:hidden',
          className,
        )}
      >
        <span>读数中断状态暂时读不到</span>
        <button
          type="button"
          onClick={() => void query.refetch()}
          className="rounded-sm font-medium text-primary outline-none hover:underline focus-visible:ring-3 focus-visible:ring-ring-soft"
        >
          重试
        </button>
      </div>
    );
  }

  const summary = query.data ? summarizeDataGaps(query.data, timeZone, context) : null;
  if (!summary) {
    return null;
  }

  const hasMeterImpact = (query.data ?? []).some((item) => item.excludesDeviceFromTotal || item.affectedFigures.length === 0);
  const firstGapDeviceId = query.data?.[0]?.deviceId;

  return (
    <div
      role="status"
      aria-label="数据缺口提示"
      data-testid="data-gap-banner"
      className={cn(
        'flex flex-wrap items-center justify-between gap-x-4 gap-y-1.5 rounded-lg border border-warning-soft bg-warning-soft/45 px-4 py-2.5 text-xs text-foreground print:hidden',
        className,
      )}
    >
      <div className="flex min-w-0 items-center gap-2">
        <TriangleAlert className="size-3.5 shrink-0 text-warning-text" aria-hidden="true" />
        <span className="tabular-nums">{summary.message}</span>
      </div>
      <div className="flex shrink-0 items-center gap-3">
        {canOpenIntegrations && hasMeterImpact ? (
          <PanelLink
            to="/settings/integrations"
            search={{ site: siteId, view: 'meters' }}
          >
            电表数据核对
          </PanelLink>
        ) : canOpenDevices ? (
          <PanelLink
            to="/operations/systems-devices"
            search={{ site: siteId, inspect: firstGapDeviceId }}
          >
            查看设备状态
          </PanelLink>
        ) : null}
      </div>
    </div>
  );
}
