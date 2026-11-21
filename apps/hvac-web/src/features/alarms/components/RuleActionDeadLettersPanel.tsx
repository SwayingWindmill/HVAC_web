import { useMemo } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import type { ColumnDef } from '@tanstack/react-table';
import { RefreshCw, RotateCcw } from 'lucide-react';
import { toast } from 'sonner';
import { DataTableBlock } from '@/blocks/data-table';
import { PanelCount } from '@/blocks/panel';
import { DataTable, type DataTableFeatures } from '@/components/data-table';
import { Alert, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { useDataTable } from '@/hooks/use-data-table';
import { formatTime } from '@/lib/operator-format';
import {
  AlarmRequestError,
  alarmKeys,
  alarmRuleDeadLettersQuery,
  replayAlarmRuleDeadLetter,
  type AlarmRuleDeadLetter,
} from '../alarm-api';
import { describeActionKind, describeActionTrigger, describeDeadLetterError } from '../alarm-operations';

const HANZI_PATTERN = /[\u4e00-\u9fff]/;

function safeActionTargetLabel(row: AlarmRuleDeadLetter): string {
  const ref = row.targetRef.trim();
  if (row.actionKind === 'WEBHOOK') {
    try {
      const host = new URL(ref).host;
      return host ? `外部回调（${host}）` : '外部回调接口';
    } catch {
      return '外部回调接口';
    }
  }
  if (row.actionKind === 'PROTECTION_COMMAND') {
    return '关联设备保护指令';
  }
  return HANZI_PATTERN.test(ref) ? ref : '自动运维工单';
}

/** Rule-engine outbox dead-letter inspection and manual replay panel for operators. */
export function RuleActionDeadLettersPanel({
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
  const deadLetters = useQuery(alarmRuleDeadLettersQuery(siteId));
  const replay = useMutation({
    mutationFn: (outboxId: string) => replayAlarmRuleDeadLetter(siteId, outboxId, csrfToken),
    onSuccess: (result) => {
      toast.success(
        result.status === 'DELIVERED'
          ? '该规则动作此前已完成投递'
          : '已重新提交到规则动作投递队列',
      );
      void queryClient.invalidateQueries({ queryKey: alarmKeys.ruleDeadLetters(siteId) });
    },
    onError: (error) => {
      toast.error(error instanceof AlarmRequestError ? error.message : '重投规则动作失败，请稍后重试');
    },
  });

  const activeOutboxId = replay.isPending ? (replay.variables ?? null) : null;
  const items = deadLetters.data ?? [];

  const columns = useMemo<Array<ColumnDef<DataTableFeatures, AlarmRuleDeadLetter>>>(() => [
    {
      id: 'kind',
      header: '动作类型',
      cell: ({ row }) => (
        <div className="flex flex-col gap-0.5 text-xs">
          <span className="font-medium text-foreground">{describeActionKind(row.original.actionKind)}</span>
          <span className="text-muted-foreground">{describeActionTrigger(row.original.triggerKind)}</span>
        </div>
      ),
    },
    {
      id: 'target',
      header: '目标摘要',
      cell: ({ row }) => <span className="text-xs text-foreground">{safeActionTargetLabel(row.original)}</span>,
    },
    {
      id: 'failure',
      header: '重试与失败原因',
      cell: ({ row }) => (
        <div className="flex flex-col gap-0.5 text-xs">
          <span className="text-foreground">{describeDeadLetterError(row.original.lastErrorCode)}</span>
          <span className="text-muted-foreground">已重试 {row.original.attempts} 次 · 动作序号 {row.original.actionIndex + 1}</span>
        </div>
      ),
    },
    {
      id: 'deadLetteredAt',
      header: '进入死信时间',
      cell: ({ row }) => (
        <span className="font-mono text-xs whitespace-nowrap text-muted-foreground">
          {formatTime(row.original.deadLetteredAt, timeZone)}
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
              <RefreshCw data-icon="inline-start" className="animate-spin" />
            ) : (
              <RotateCcw data-icon="inline-start" />
            )}
            {isRowPending ? '重投中…' : '重新投递'}
          </Button>
        );
      },
    },
  ], [activeOutboxId, canManage, replay, timeZone]);

  const table = useDataTable({
    key: `alarm-rule-dlq-${siteId}`,
    data: items,
    columns,
    paginate: false,
    getRowId: (item) => item.outboxId,
  });

  return (
    <DataTableBlock
      title="规则动作投递死信"
      description="外部回调、运维工单或保护联动多次重试失败后进入此列表"
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
          <AlertTitle>规则动作死信暂时读取失败，请稍后刷新</AlertTitle>
        </Alert>
      ) : deadLetters.isPending ? (
        <div className="flex flex-col gap-2">
          <Skeleton className="h-12" />
          <Skeleton className="h-12" />
        </div>
      ) : (
        <DataTable
          table={table}
          tableAriaLabel="规则动作投递死信"
          empty="当前没有待重投的规则联动动作死信"
          getCellProps={(cell) => ({
            className: cell.column.id === 'failure' || cell.column.id === 'target' ? 'whitespace-normal align-top' : 'align-top',
          })}
        />
      )}
    </DataTableBlock>
  );
}
