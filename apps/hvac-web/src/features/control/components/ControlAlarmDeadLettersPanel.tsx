import { useMemo } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import type { ColumnDef } from '@tanstack/react-table';
import { RefreshCw, RotateCcw } from 'lucide-react';
import { toast } from 'sonner';
import { DataTableBlock } from '@/blocks/data-table';
import { PanelCount } from '@/blocks/panel';
import { DataTable, type DataTableFeatures } from '@/components/data-table';
import { InfoTip } from '@/components/info-tip';
import { Alert, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { useDataTable } from '@/hooks/use-data-table';
import { formatTime } from '@/lib/operator-format';
import { CommandRequestError } from '../command-api';
import {
  controlAlarmDeadLetterKeys,
  controlAlarmDeadLettersQuery,
  describeControlAlarmError,
  describeControlAlarmEvent,
  replayControlAlarmDeadLetter,
  type ControlAlarmDeadLetter,
} from '../control-state';

/** Inline control-plane alarm outbox dead-letter inspection and replay panel for engineering governance. */
export function ControlAlarmDeadLettersPanel({
  siteId,
  canManage,
  csrfToken,
  deviceNames,
  timeZone,
}: {
  readonly siteId: string;
  readonly canManage: boolean;
  readonly csrfToken: string;
  readonly deviceNames: ReadonlyMap<string, string>;
  readonly timeZone: string;
}) {
  const queryClient = useQueryClient();
  const deadLetters = useQuery(controlAlarmDeadLettersQuery(siteId));

  const replay = useMutation({
    mutationFn: (outboxId: string) => replayControlAlarmDeadLetter(siteId, outboxId, csrfToken),
    onSuccess: (result) => {
      toast.success(result.replayed ? '已重新提交到控制告警投递队列' : '该死信此前已重投，正在队列中处理');
      void queryClient.invalidateQueries({ queryKey: controlAlarmDeadLetterKeys.site(siteId) });
    },
    onError: (error) => {
      toast.error(error instanceof CommandRequestError ? error.message : '死信重投失败，请稍后重试');
    },
  });

  const activeOutboxId = replay.isPending ? (replay.variables ?? null) : null;
  const items = deadLetters.data ?? [];

  const columns = useMemo<Array<ColumnDef<DataTableFeatures, ControlAlarmDeadLetter>>>(() => [
    {
      id: 'device',
      header: '关联设备',
      cell: ({ row }) => (
        <span className="font-medium text-foreground">
          {row.original.deviceId ? (deviceNames.get(row.original.deviceId) ?? '站点级设备') : '全站控制'}
        </span>
      ),
    },
    {
      id: 'eventKind',
      header: '事件类型',
      cell: ({ row }) => describeControlAlarmEvent(row.original.eventKind),
    },
    {
      id: 'reason',
      header: '失败详情',
      cell: ({ row }) => (
        <div className="flex flex-col gap-0.5 text-xs">
          <span className="text-foreground">{describeControlAlarmError(row.original.lastErrorCode)}</span>
          <span className="text-muted-foreground">已重试 {row.original.attempts} 次 · 触发来源：{describeControlAlarmError(row.original.reasonCode)}</span>
        </div>
      ),
    },
    {
      id: 'updatedAt',
      header: '最后失败时间',
      cell: ({ row }) => (
        <span className="font-mono text-xs whitespace-nowrap text-muted-foreground">
          {formatTime(row.original.updatedAt, timeZone)}
        </span>
      ),
    },
    {
      id: 'action',
      header: '操作',
      cell: ({ row }) => {
        const isRowPending = activeOutboxId === row.original.outboxId;
        return (
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={!canManage || activeOutboxId !== null}
            onClick={() => replay.mutate(row.original.outboxId)}
          >
            {isRowPending ? (
              <RefreshCw data-icon="inline-start" className="motion-safe:animate-spin" />
            ) : (
              <RotateCcw data-icon="inline-start" />
            )}
            {isRowPending ? '重投中…' : '重新投递'}
          </Button>
        );
      },
    },
  ], [activeOutboxId, canManage, deviceNames, replay, timeZone]);

  const table = useDataTable({
    key: `control-alarm-dlq-${siteId}`,
    data: items,
    columns,
    paginate: false,
    getRowId: (item) => item.outboxId,
  });

  return (
    <DataTableBlock
      title={(
        <span className="inline-flex items-center gap-1.5">
          控制告警投递死信
          <InfoTip>
            执行结果未知、读回校验超时、安全下发拒收或设备锁定等安全事件若多次同步告警服务失败，会暂存于此队列供确认后重投。
          </InfoTip>
        </span>
      )}
      actions={(
        <div className="flex items-center gap-2">
          {items.length > 0 ? <PanelCount count={items.length} tone="urgent" /> : null}
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={deadLetters.isFetching}
            onClick={() => void deadLetters.refetch()}
          >
            <RefreshCw data-icon="inline-start" />
            刷新
          </Button>
        </div>
      )}
    >
      {deadLetters.isError ? (
        <Alert variant="destructive">
          <AlertTitle>控制告警死信队列暂时读取失败，请稍后刷新</AlertTitle>
        </Alert>
      ) : deadLetters.isPending ? (
        <div className="flex flex-col gap-2">
          <Skeleton className="h-12" />
          <Skeleton className="h-12" />
        </div>
      ) : (
        <DataTable
          table={table}
          tableAriaLabel="控制告警投递死信列表"
          empty="当前没有待重投的控制告警死信记录"
          getHeaderCellProps={(header) => ({
            className: header.id === 'device' || header.id === 'eventKind' || header.id === 'updatedAt' || header.id === 'action'
              ? 'whitespace-nowrap'
              : undefined,
          })}
          getCellProps={(cell) => ({
            className: cell.column.id === 'reason' ? 'whitespace-normal align-top' : 'whitespace-nowrap align-top',
          })}
        />
      )}
    </DataTableBlock>
  );
}
