import { useMemo } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import type { ColumnDef } from '@tanstack/react-table';
import { RefreshCw } from 'lucide-react';
import { notifyError, notifySuccess } from '@/lib/toast';
import { DataTableBlock } from '@/blocks/data-table';
import { FactTile } from '@/blocks/fact-list';
import { Panel, PanelHeader } from '@/blocks/panel';
import { DataTable, type DataTableFeatures } from '@/components/data-table';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { useDataTable } from '@/hooks/use-data-table';
import { formatTime } from '@/lib/operator-format';
import {
  AlarmRequestError,
  alarmKeys,
  reloadSiteAlarmRouting,
  siteAlarmRoutingQuery,
  type SiteAlarmRoutingSummary,
} from '../alarm-api';
import {
  describeGroupBy,
  describeReloadError,
  describeRoutingChannel,
  describeRoutingRouteName,
  routingDigestPrefix,
} from '../alarm-operations';
import { SEVERITY_LABELS, SeverityBadge } from '../alarm-presentation';

type RoutingRouteRow = SiteAlarmRoutingSummary['routes'][number];

function formatRouteSteps(route: RoutingRouteRow): string {
  return route.steps
    .map((step) => {
      const delay = step.afterMinutes === 0 ? '立即' : `${step.afterMinutes} 分钟后`;
      const channels = step.channels.map(describeRoutingChannel).join(' / ');
      return `${delay}：${channels}（${step.recipientCount} 人）`;
    })
    .join(' → ');
}

/** Secret-free inspection and manual hot-reload workbench for Site alarm notification routing. */
export function AlarmRoutingPanel({
  siteId,
  canManage,
  csrfToken,
  timeZone,
}: {
  readonly siteId: string;
  readonly canManage: boolean;
  readonly csrfToken: string;
  readonly timeZone: string;
}) {
  const queryClient = useQueryClient();
  const routing = useQuery(siteAlarmRoutingQuery(siteId));

  const reload = useMutation({
    mutationFn: () => reloadSiteAlarmRouting(siteId, csrfToken),
    onSuccess: (next) => {
      const { changed, ...summary } = next;
      queryClient.setQueryData(alarmKeys.routing(siteId), summary);
      notifySuccess(changed ? '站点通知路由配置已热更新并生效' : '配置文件指纹未变化，继续沿用当前路由');
    },
    onError: (error) => {
      notifyError(error instanceof AlarmRequestError ? error.message : '路由配置热重载失败，已保留原生效配置');
      void queryClient.invalidateQueries({ queryKey: alarmKeys.routing(siteId) });
    },
  });

  const summary = routing.data;
  const routes = summary?.routes ?? [];
  const reloadErrorText = describeReloadError(summary?.lastReloadErrorCode);

  const columns = useMemo<Array<ColumnDef<DataTableFeatures, RoutingRouteRow>>>(() => [
    {
      id: 'name',
      header: '路由通道',
      cell: ({ row }) => <span className="font-medium text-foreground">{describeRoutingRouteName(row.original.name)}</span>,
    },
    {
      id: 'minSeverity',
      header: '触发门槛',
      cell: ({ row }) => <SeverityBadge severity={row.original.minSeverity} />,
    },
    {
      id: 'grouping',
      header: '收敛分组方式',
      cell: ({ row }) => (
        <span className="text-xs text-muted-foreground">
          {row.original.groupWindowSeconds
            ? `每 ${row.original.groupWindowSeconds} 秒分组 · 最多 ${row.original.groupMaxItems ?? 10} 条（${describeGroupBy(row.original.groupBy)}）`
            : '单条即时直发'}
        </span>
      ),
    },
    {
      id: 'policy',
      header: '冷却与确认通知',
      cell: ({ row }) => (
        <span className="text-xs text-muted-foreground">
          冷却 {row.original.cooldownMinutes ?? 0} 分钟 · {row.original.notifyAck ? '发送确认通知' : '跳过确认通知'} · {row.original.notifyRecovery ? '发送恢复通知' : '跳过恢复通知'}
        </span>
      ),
    },
    {
      id: 'steps',
      header: '升级投递通道与人数',
      cell: ({ row }) => <span className="text-xs text-foreground">{formatRouteSteps(row.original)}</span>,
    },
  ], []);

  const table = useDataTable({
    key: `alarm-routing-${siteId}`,
    data: routes,
    columns,
    paginate: false,
    getRowId: (route) => route.name,
  });

  return (
    <div className="flex flex-col gap-5">
      <Panel>
        <PanelHeader
          title="通知路由与降噪配置"
          caption={summary ? `指纹 ${routingDigestPrefix(summary.configDigest)} · 加载于 ${formatTime(summary.loadedAt, timeZone)}` : undefined}
          action={canManage ? (
            <Button
              type="button"
              size="sm"
              variant="outline"
              disabled={reload.isPending || routing.isPending}
              onClick={() => reload.mutate()}
            >
              <RefreshCw data-icon="inline-start" />
              {reload.isPending ? '热重载中…' : '热重载路由配置'}
            </Button>
          ) : undefined}
        />

        <div className="flex flex-col gap-4 p-4">
          {routing.isError ? (
            <Alert variant="destructive">
              <AlertTitle>站点通知路由摘要暂时读取失败，请稍后刷新</AlertTitle>
            </Alert>
          ) : routing.isPending || !summary ? (
            <Skeleton className="h-16" />
          ) : (
            <>
              {reloadErrorText ? (
                <Alert variant="destructive">
                  <AlertTitle>最近一次路由配置重载未通过校验</AlertTitle>
                  <AlertDescription className="text-xs">{reloadErrorText}</AlertDescription>
                </Alert>
              ) : null}

              <dl className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-6">
                <FactTile label="持续延迟确认">{summary.holdMinutes} 分钟</FactTile>
                <FactTile label="通知限流上限">{summary.rateLimitPerHour} 条/小时</FactTile>
                <FactTile label="追发有效期">{summary.maxAgeHours} 小时</FactTile>
                <FactTile label="热重载次数">{summary.reloadCount} 次</FactTile>
                <FactTile label="根因抑制规则">{summary.rootCauseRules?.length ?? summary.suppressWhenRootCause?.length ?? 0} 条</FactTile>
                <FactTile label="夜间免打扰">
                  {summary.quietHours
                    ? `${summary.quietHours.start} 至 ${summary.quietHours.end}（${SEVERITY_LABELS[summary.quietHours.bypassSeverity]}及以上直通）`
                    : '未启用'}
                </FactTile>
              </dl>
            </>
          )}
        </div>
      </Panel>

      {!routing.isError && !routing.isPending && summary ? (
        <DataTableBlock
          title="分级路由与升级路径"
          description="仅展示通道类型、分组窗口与接收人数，不暴露个人手机号或邮箱凭据。紧急告警自动跳过分组窗口即时直发。"
        >
          <DataTable
            table={table}
            tableAriaLabel="分级路由与升级路径"
            empty="当前站点未配置独立通知路由"
            getHeaderCellProps={(header) => ({
              className: header.id === 'name'
                ? 'w-28 whitespace-nowrap'
                : header.id === 'minSeverity'
                  ? 'w-24 whitespace-nowrap'
                  : header.id === 'grouping'
                    ? 'w-64'
                    : header.id === 'policy'
                      ? 'w-64'
                      : undefined,
            })}
            getCellProps={(cell) => ({
              className: cell.column.id === 'name' || cell.column.id === 'minSeverity'
                ? 'whitespace-nowrap align-top'
                : 'whitespace-normal align-top',
            })}
          />
        </DataTableBlock>
      ) : null}
    </div>
  );
}
