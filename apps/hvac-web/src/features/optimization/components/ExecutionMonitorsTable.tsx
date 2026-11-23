import { Link } from '@tanstack/react-router';
import type { ColumnDef } from '@tanstack/react-table';
import { useMemo, useState } from 'react';
import { DataTableBlock } from '@/blocks/data-table';
import { Panel } from '@/blocks/panel';
import { DataTable, type DataTableFeatures } from '@/components/data-table';
import { StatusBadge } from '@/components/status-badge';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { useDataTable } from '@/hooks/use-data-table';
import { formatCompactTime, formatDecimal } from '@/lib/operator-format';
import { isOneOf } from '@/lib/typed';
import type { OptimizationExecutionMonitor } from '../auto-control-api';
import {
  filterExecutionMonitors,
  formatAutoControlCapability,
  formatAutoControlTripReason,
  formatGuardrailVerdict,
  formatMonitorDecisionTrace,
  formatMonitorExecutionMode,
  formatMonitorLoopLabel,
  formatMonitorNumericValue,
  MONITOR_STATUS_PRESENTATION,
  resolveMonitorExecutionMode,
  resolveMonitorUnit,
  type ExecutionMonitorFilter,
} from '../auto-control-model';

const FILTER_OPTIONS: readonly { readonly value: ExecutionMonitorFilter; readonly label: string }[] = [
  { value: 'all', label: '全部' },
  { value: 'active', label: '观察中' },
  { value: 'tripped', label: '异常回退' },
  { value: 'shadow', label: '影子' },
];

const FILTER_VALUES = ['all', 'active', 'tripped', 'shadow'] as const satisfies readonly ExecutionMonitorFilter[];

export function ExecutionMonitorsTable({
  siteId,
  timeZone,
  monitors,
  isPending,
  isError,
  onRetry,
}: {
  readonly siteId: string;
  readonly timeZone: string;
  readonly monitors: readonly OptimizationExecutionMonitor[];
  readonly isPending: boolean;
  readonly isError: boolean;
  readonly onRetry: () => void;
}) {
  const [filter, setFilter] = useState<ExecutionMonitorFilter>('all');

  const counts = useMemo(
    () => ({
      all: monitors.length,
      active: filterExecutionMonitors(monitors, 'active').length,
      tripped: filterExecutionMonitors(monitors, 'tripped').length,
      shadow: filterExecutionMonitors(monitors, 'shadow').length,
    }),
    [monitors],
  );

  const filteredMonitors = useMemo(
    () => filterExecutionMonitors(monitors, filter),
    [monitors, filter],
  );

  const columns = useMemo<Array<ColumnDef<DataTableFeatures, OptimizationExecutionMonitor>>>(() => [
    {
      id: 'window',
      header: '观察窗口',
      cell: ({ row }) => (
        <div className="flex flex-col gap-1 font-mono text-xs tabular-nums">
          <span className="text-foreground">
            {formatCompactTime(row.original.startedAt, timeZone)} 至 {formatCompactTime(row.original.windowEndsAt, timeZone)}
          </span>
          <div className="flex flex-wrap items-center gap-1.5 font-sans">
            <Badge variant="secondary">
              {formatMonitorLoopLabel(row.original.loopKey, row.original.targetCapability)}
            </Badge>
            <span className="text-muted-foreground">{formatAutoControlCapability(row.original.targetCapability)}</span>
          </div>
        </div>
      ),
    },
    {
      id: 'temperatures',
      header: '设定值变化',
      cell: ({ row }) => {
        const {
          baselineSupplyTempC,
          appliedSupplyTempC,
          restoreSupplyTempC,
          rawRecommendedValue,
          clampedValue,
        } = row.original;
        const unit = resolveMonitorUnit(row.original);
        const rawVal = typeof rawRecommendedValue === 'number' ? rawRecommendedValue : undefined;
        const clampedVal = typeof clampedValue === 'number' ? clampedValue : undefined;
        const hasClampedDiff =
          rawVal !== undefined
          && clampedVal !== undefined
          && Math.abs(rawVal - clampedVal) >= 0.05;
        const showClampedOnly =
          !hasClampedDiff
          && clampedVal !== undefined
          && (appliedSupplyTempC === undefined || Math.abs(clampedVal - appliedSupplyTempC) >= 0.05);
        const showRawOnly =
          !hasClampedDiff
          && !showClampedOnly
          && rawVal !== undefined
          && (appliedSupplyTempC === undefined || Math.abs(rawVal - appliedSupplyTempC) >= 0.05);
        return (
          <div className="flex flex-col gap-0.5 text-xs tabular-nums">
            <span className="font-medium whitespace-nowrap text-foreground">
              {formatMonitorNumericValue(baselineSupplyTempC, unit)} → {formatMonitorNumericValue(appliedSupplyTempC, unit)}
            </span>
            <span className="text-muted-foreground">
              回退目标 {formatMonitorNumericValue(restoreSupplyTempC, unit)}
            </span>
            {hasClampedDiff ? (
              <span className="text-warning-text">
                原建议 {formatMonitorNumericValue(rawVal, unit)}，限幅为 {formatMonitorNumericValue(clampedVal, unit)}
              </span>
            ) : showClampedOnly ? (
              <span className="text-muted-foreground">
                限幅目标 {formatMonitorNumericValue(clampedVal, unit)}
              </span>
            ) : showRawOnly ? (
              <span className="text-muted-foreground">
                原始建议 {formatMonitorNumericValue(rawVal, unit)}
              </span>
            ) : null}
          </div>
        );
      },
    },
    {
      id: 'limits',
      header: '租约安全边界',
      cell: ({ row }) => {
        const unit = resolveMonitorUnit(row.original);
        const loopSubject = unit === 'Hz' ? '频率' : unit === '%' ? '转速' : '供水';
        const suffix = unit === '%' ? '%' : unit === 'Hz' ? ' Hz' : ' °C';
        const minVal = row.original.supplyTempMinC;
        const maxText = `${formatDecimal(row.original.supplyTempMaxC, 1)}${suffix}`;
        return (
          <span className="text-xs text-muted-foreground tabular-nums">
            {minVal !== undefined
              ? `${loopSubject} ${formatDecimal(minVal, 1)} 至 ${maxText}`
              : `${loopSubject} ≤ ${maxText}`}
            {' · '}
            室内 ≤ {formatDecimal(row.original.zoneTempMaxC, 1)} °C
          </span>
        );
      },
    },
    {
      id: 'status',
      header: '监护状态',
      cell: ({ row }) => {
        const presentation = MONITOR_STATUS_PRESENTATION[row.original.status];
        const mode = resolveMonitorExecutionMode(row.original);
        return (
          <div className="flex flex-wrap items-center gap-1.5">
            <StatusBadge tone={presentation.tone} label={presentation.label} />
            <Badge variant={mode === 'SHADOW' ? 'default' : 'secondary'}>
              {formatMonitorExecutionMode(mode)}
            </Badge>
          </div>
        );
      },
    },
    {
      id: 'reason',
      header: '巡检与退出原因',
      cell: ({ row }) => {
        const verdictLabel = formatGuardrailVerdict(row.original.guardrailVerdict);
        const traceInfo = formatMonitorDecisionTrace(row.original);
        const primary = row.original.tripReason ? (
          <span className="text-xs text-destructive-text">
            {formatAutoControlTripReason(row.original.tripReason)}
          </span>
        ) : row.original.status === 'PENDING_SUBMIT' ? (
          <span className="text-xs text-muted-foreground">等待写入控制指令</span>
        ) : row.original.status === 'SHADOW_RECORDED' ? (
          <span className="text-xs text-muted-foreground">影子模式仅记录推荐轨迹，不下发设备</span>
        ) : row.original.status === 'OBSERVING' ? (
          <span className="text-xs text-muted-foreground">
            {row.original.lastCheckedAt
              ? `最近巡检 ${formatCompactTime(row.original.lastCheckedAt, timeZone)}`
              : '等待首次巡检'}
          </span>
        ) : row.original.status === 'VERIFIED' ? (
          <span className="text-xs text-muted-foreground">30 分钟内温度与功率均在安全范围内</span>
        ) : row.original.status === 'MANUAL_OVERRIDE' ? (
          <span className="text-xs text-warning-text">检测到现场或人工调参，AI 进入退避保护</span>
        ) : (
          <span className="text-xs text-muted-foreground">已记录保护处置结果</span>
        );
        if (!verdictLabel && !traceInfo) return primary;
        return (
          <div className="flex flex-col gap-0.5">
            {primary}
            {verdictLabel ? (
              <span className="text-xs text-muted-foreground">护栏：{verdictLabel}</span>
            ) : null}
            {traceInfo ? (
              <span className="text-xs text-muted-foreground" title={traceInfo.title}>
                {traceInfo.label}
              </span>
            ) : null}
          </div>
        );
      },
    },
    {
      id: 'actions',
      header: '关联指令',
      cell: ({ row }) => (
        <div className="flex flex-col items-end gap-1">
          {row.original.commandIntentId ? (
            <Link
              to="/operations/control"
              search={{ site: siteId, inspect: row.original.commandIntentId }}
              className="text-xs font-medium text-primary hover:underline"
            >
              查看下发指令 ›
            </Link>
          ) : (
            <span className="text-xs text-muted-foreground">无下行指令</span>
          )}
          {row.original.rollbackCommandIntentId ? (
            <Link
              to="/operations/control"
              search={{ site: siteId, inspect: row.original.rollbackCommandIntentId }}
              className="text-xs font-medium text-warning-text hover:underline"
            >
              查看回退指令 ›
            </Link>
          ) : null}
        </div>
      ),
    },
  ], [siteId, timeZone]);

  const table = useDataTable({
    key: `execution-monitors-${siteId}-${filter}`,
    data: filteredMonitors,
    columns,
    pageSize: 10,
    getRowId: (row) => row.id,
  });

  return (
    <DataTableBlock
      title="30 分钟执行监护"
      className="w-full min-w-0"
      actions={
        monitors.length > 0 ? (
          <ToggleGroup
            type="single"
            value={filter}
            onValueChange={(next) => {
              if (isOneOf(FILTER_VALUES, next)) setFilter(next);
            }}
            aria-label="监护记录筛选"
            className="inline-flex h-8 items-center gap-0.5 rounded-lg bg-secondary p-0.5"
          >
            {FILTER_OPTIONS.map((option) => {
              const count = counts[option.value];
              return (
                <ToggleGroupItem
                  key={option.value}
                  value={option.value}
                  className="h-7 rounded-md px-2.5 text-xs font-medium text-secondary-foreground hover:bg-transparent hover:text-foreground data-[state=on]:bg-card data-[state=on]:text-foreground data-[state=on]:shadow-xs"
                >
                  <span>{option.label}</span>
                  {count > 0 ? (
                    <span className="ml-1 font-mono text-[11px] text-subtle-foreground tabular-nums">
                      {count}
                    </span>
                  ) : null}
                </ToggleGroupItem>
              );
            })}
          </ToggleGroup>
        ) : undefined
      }
    >
      {isError ? (
        <Panel aria-label="30 分钟执行监护">
          <div className="flex flex-col items-start gap-2 px-5 py-6">
            <p className="text-sm font-semibold">执行监护记录暂时读不到</p>
            <Button variant="outline" size="sm" onClick={onRetry}>
              重试
            </Button>
          </div>
        </Panel>
      ) : isPending ? (
        <Skeleton className="h-36 rounded-xl" />
      ) : (
        <DataTable
          table={table}
          tableAriaLabel="30 分钟执行监护"
          empty={monitors.length === 0 ? '暂无执行监护记录' : '当前筛选条件下没有执行监护记录'}
          getHeaderCellProps={(header) => ({
            className:
              header.id === 'window'
                ? 'pl-5'
                : header.id === 'actions'
                  ? 'pr-5 text-right'
                  : undefined,
          })}
          getCellProps={(cell) => ({
            className:
              cell.column.id === 'window'
                ? 'pl-5 align-top whitespace-nowrap'
                : cell.column.id === 'actions'
                  ? 'pr-5 text-right align-top whitespace-nowrap'
                  : cell.column.id === 'temperatures'
                    ? 'align-top min-w-36 whitespace-normal'
                    : cell.column.id === 'limits'
                      ? 'align-top min-w-36 whitespace-normal'
                      : cell.column.id === 'reason'
                        ? 'align-top min-w-44 whitespace-normal'
                        : 'align-top whitespace-nowrap',
          })}
        />
      )}
    </DataTableBlock>
  );
}
