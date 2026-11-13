import { useMemo } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import { DataTableBlock } from '@/blocks/data-table';
import { Panel } from '@/blocks/panel';
import { StateCard } from '@/blocks/state/state-card';
import { DataTable, DataTablePagination, type DataTableFeatures } from '@/components/data-table';
import { InfoTip } from '@/components/info-tip';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { useDataTable } from '@/hooks/use-data-table';
import { cn } from '@/lib/utils';
import {
  describeIntegrationsError,
  filterSensorCalibrationRows,
  type SensorCalibrationFilter,
  type SensorCalibrationModel,
  type SensorCalibrationRow,
} from '../integrations-model';
import { GovernanceBadge } from './GovernanceShared';

const CALIBRATION_FILTERS: readonly {
  readonly value: SensorCalibrationFilter;
  readonly label: string;
}[] = [
  { value: 'ALL', label: '全部点位' },
  { value: 'ATTENTION', label: '待关注' },
  { value: 'PROBE_OFFSET', label: '零点建议' },
  { value: 'CALIBRATION_DUE', label: '定检到期' },
];

function formatShortDate(iso: string): string {
  try {
    return new Intl.DateTimeFormat('zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
    }).format(new Date(iso));
  } catch {
    return iso.slice(0, 10);
  }
}

export interface SensorCalibrationTabProps {
  readonly model: SensorCalibrationModel | null;
  readonly isAssetLoading: boolean;
  readonly isAssetError: boolean;
  readonly assetError: unknown;
  readonly canReadSnapshots: boolean;
  readonly isSnapshotsError: boolean;
  readonly filter: SensorCalibrationFilter;
  readonly onFilterChange: (next: SensorCalibrationFilter) => void;
  readonly onRetry: () => void;
  readonly onInspectSensor: (inspectKey: string) => void;
}

export function SensorCalibrationTab({
  model,
  isAssetLoading,
  isAssetError,
  assetError,
  canReadSnapshots,
  isSnapshotsError,
  filter,
  onFilterChange,
  onRetry,
  onInspectSensor,
}: SensorCalibrationTabProps) {
  const filteredRows = useMemo(
    () => [...filterSensorCalibrationRows(model?.rows ?? [], filter)],
    [model?.rows, filter],
  );

  const columns = useMemo<ColumnDef<DataTableFeatures, SensorCalibrationRow>[]>(
    () => [
      {
        id: 'point',
        header: () => '点位与设备',
        cell: ({ row }) => {
          const item = row.original;
          return (
            <div>
              <button
                type="button"
                className="block text-left font-semibold text-foreground outline-none hover:underline focus-visible:underline"
                onClick={() => onInspectSensor(item.inspectKey)}
              >
                {item.pointLabel}
              </button>
              <div className="mt-0.5 text-xs text-muted-foreground">
                <span>{item.deviceDisplayName}</span>
                <span> · </span>
                <span>{item.channelLabel}</span>
                {item.spaceName ? <span> · {item.spaceName}</span> : null}
              </div>
            </div>
          );
        },
      },
      {
        id: 'reading',
        header: () => '当前读数',
        cell: ({ row }) => {
          const item = row.original;
          return (
            <div className="tabular-nums">
              <div className="font-mono text-foreground">
                {item.currentValue}
                {item.currentValue !== '—' && item.unit ? ` ${item.unit}` : ''}
              </div>
              <div className="mt-0.5 text-xs text-muted-foreground">{item.sampleIntervalLabel}</div>
            </div>
          );
        },
      },
      {
        id: 'diagnostic',
        header: () => '零点建议 / 不确定度',
        cell: ({ row }) => {
          const item = row.original;
          if (item.primaryProbeOffset) {
            return (
              <div className="space-y-0.5">
                <div className="text-xs font-medium text-warning-text">
                  ▲ {item.primaryProbeOffset.summaryLabel}
                </div>
                <div className="text-xs text-muted-foreground tabular-nums">
                  <span>{item.primaryProbeOffset.pairLabel}</span>
                  <span> · </span>
                  <span className="font-mono">
                    σ {item.primaryProbeOffset.stddevK.toFixed(2)} K（{item.primaryProbeOffset.sampleCount} 样本）
                  </span>
                </div>
              </div>
            );
          }
          if (item.measurementSummaryLabel) {
            return (
              <div className="space-y-0.5">
                <div
                  className={cn(
                    'font-mono text-xs tabular-nums',
                    item.measurement?.lowConfidence ? 'font-medium text-warning-text' : 'text-foreground',
                  )}
                >
                  {item.measurement?.lowConfidence ? '▲ ' : ''}
                  {item.measurementSummaryLabel}
                </div>
                {item.diagnosticNotes[0] ? (
                  <div className="text-xs text-muted-foreground">{item.diagnosticNotes[0]}</div>
                ) : null}
              </div>
            );
          }
          if (item.diagnosticNotes.length > 0) {
            return <div className="text-xs text-warning-text">▲ {item.diagnosticNotes[0]}</div>;
          }
          return <span className="text-muted-foreground">—</span>;
        },
      },
      {
        id: 'calibration',
        header: () => '定检到期',
        cell: ({ row }) => {
          const item = row.original;
          if (item.calibration.status === 'UNREGISTERED' || !item.calibration.dueAt) {
            return <span className="text-xs text-muted-foreground">未登记</span>;
          }
          return (
            <div className="tabular-nums">
              <div className="font-mono text-xs">{formatShortDate(item.calibration.dueAt)}</div>
              <div
                className={cn(
                  'mt-0.5 text-xs',
                  item.calibration.status === 'OVERDUE'
                    ? 'font-medium text-destructive-text'
                    : item.calibration.status === 'EXPIRING_SOON'
                      ? 'text-warning-text'
                      : 'text-muted-foreground',
                )}
              >
                {item.calibration.label}
              </div>
            </div>
          );
        },
      },
      {
        id: 'actions',
        header: () => '状态与操作',
        cell: ({ row }) => {
          const item = row.original;
          return (
            <div className="inline-flex items-center justify-end gap-2">
              <GovernanceBadge status={item.status} />
              <Button
                variant="outline"
                size="sm"
                onClick={() => onInspectSensor(item.inspectKey)}
              >
                校准详情
              </Button>
            </div>
          );
        },
      },
    ],
    [onInspectSensor],
  );

  const table = useDataTable({
    key: 'integrations.sensors',
    data: filteredRows,
    columns,
    pageSize: 12,
    getRowId: (row) => row.pointId,
  });

  if (isAssetError) {
    return (
      <StateCard
        tone="danger"
        title={<h2 className="text-base font-semibold">无法读取站点传感器与点位配置</h2>}
        description={describeIntegrationsError(assetError)}
      >
        <Button variant="outline" size="sm" onClick={onRetry}>
          重试
        </Button>
      </StateCard>
    );
  }

  const summary = model?.summary;

  return (
    <div className="flex flex-col gap-4">
      {!canReadSnapshots ? (
        <Alert>
          <AlertDescription>
            当前账号没有实时读数查看权限，实时值、零点偏移建议与测量不确定度暂不可见，以下仅展示传感器与点位登记。
          </AlertDescription>
        </Alert>
      ) : isSnapshotsError ? (
        <Alert>
          <AlertDescription>
            实时读数快照暂时无法读取，当前实测值与探头零点建议暂显示为 —，稍后可重试刷新。
          </AlertDescription>
        </Alert>
      ) : null}

      <Panel aria-label="传感器校准概览">
        {isAssetLoading || !summary ? (
          <div className="grid grid-cols-1 gap-4 p-5 sm:grid-cols-2 lg:grid-cols-4">
            <Skeleton className="h-16" />
            <Skeleton className="h-16" />
            <Skeleton className="h-16" />
            <Skeleton className="h-16" />
          </div>
        ) : (
          <div className="grid grid-cols-1 divide-y divide-divider sm:grid-cols-2 sm:divide-y-0 lg:grid-cols-4 lg:divide-x">
            <div className="flex flex-col gap-1 px-6 py-4">
              <span className="text-xs font-medium text-muted-foreground">已接入数值点位</span>
              <div className="flex items-baseline gap-1.5">
                <span className="font-mono text-2xl font-semibold tabular-nums">
                  {summary.totalPoints}
                </span>
                <span className="text-xs text-muted-foreground">项</span>
              </div>
              <span className="text-xs text-muted-foreground">
                待关注 {summary.attentionPoints} 项
              </span>
            </div>

            <div className="flex flex-col gap-1 px-6 py-4">
              <span className="flex items-center gap-1 text-xs font-medium text-muted-foreground">
                零点偏移建议
                <InfoTip>
                  在冷机停机、仅水泵稳态循环窗口自动评估供回水探头对温差偏差；仅作为现场校准建议展示，默认未采用，不自动改写原始读数
                </InfoTip>
              </span>
              <div className="flex items-baseline gap-1.5">
                <span
                  className={cn(
                    'font-mono text-2xl font-semibold tabular-nums',
                    summary.probeOffsetCount > 0 ? 'text-warning-text' : 'text-foreground',
                  )}
                >
                  {summary.probeOffsetCount}
                </span>
                <span className="text-xs text-muted-foreground">项 · 未采用</span>
              </div>
              <span className="text-xs text-muted-foreground">
                {summary.probeOffsetCount > 0
                  ? '需现场比对温度探头零点后确认'
                  : '当前未检出显著探头零点偏移'}
              </span>
            </div>

            <div className="flex flex-col gap-1 px-6 py-4">
              <span className="text-xs font-medium text-muted-foreground">物理与低可信提醒</span>
              <div className="flex items-baseline gap-1.5">
                <span
                  className={cn(
                    'font-mono text-2xl font-semibold tabular-nums',
                    summary.physicsOrLowConfidenceCount > 0
                      ? 'text-warning-text'
                      : 'text-foreground',
                  )}
                >
                  {summary.physicsOrLowConfidenceCount}
                </span>
                <span className="text-xs text-muted-foreground">项</span>
              </div>
              <span className="text-xs text-muted-foreground">
                小温差低可信、热平衡或铭牌越界校核
              </span>
            </div>

            <div className="flex flex-col gap-1 px-6 py-4">
              <span className="text-xs font-medium text-muted-foreground">定检到期</span>
              <div className="flex items-baseline gap-1.5">
                <span
                  className={cn(
                    'font-mono text-2xl font-semibold tabular-nums',
                    summary.calibrationDueOrExpiringCount > 0
                      ? 'text-warning-text'
                      : 'text-foreground',
                  )}
                >
                  {summary.calibrationDueDisplay}
                </span>
                {summary.calibrationConfiguredCount > 0 ? (
                  <span className="text-xs text-muted-foreground">项</span>
                ) : null}
              </div>
              <span className="text-xs text-muted-foreground">
                {summary.calibrationConfiguredCount > 0
                  ? `已登记定检周期 ${summary.calibrationConfiguredCount} 项`
                  : '当前站点传感器尚未录入定检到期日'}
              </span>
            </div>
          </div>
        )}
      </Panel>

      <DataTableBlock
        title="点位与传感器"
        description="核查温度、压力、流量与功率点位的零点偏移建议、测量不确定度与定检到期状态；点击校准详情打开侧边抽屉"
        actions={
          <ToggleGroup
            type="single"
            size="sm"
            variant="outline"
            value={filter}
            onValueChange={(next) => {
              const matched = CALIBRATION_FILTERS.find((item) => item.value === next);
              if (!matched) return;
              table.setPageIndex(0);
              onFilterChange(matched.value);
            }}
            aria-label="传感器状态筛选"
          >
            {CALIBRATION_FILTERS.map((item) => (
              <ToggleGroupItem key={item.value} value={item.value} className="text-xs">
                {item.label}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
        }
      >
        {isAssetLoading ? (
          <Skeleton className="h-48 w-full rounded-xl" />
        ) : (
          <DataTable
            table={table}
            tableAriaLabel="点位与传感器"
            empty={
              <div className="py-10 text-center text-sm text-muted-foreground">
                {filter === 'ALL'
                  ? '当前站点还没有登记数值类传感器点位'
                  : '当前筛选条件下没有需要处理的点位'}
              </div>
            }
            getHeaderCellProps={(header) => ({
              className:
                header.id === 'point'
                  ? 'pl-5'
                  : header.id === 'reading'
                    ? 'text-right'
                    : header.id === 'actions'
                      ? 'pr-5 text-right'
                      : undefined,
            })}
            getCellProps={(cell) => ({
              className:
                cell.column.id === 'point'
                  ? 'pl-5 align-top'
                  : cell.column.id === 'reading'
                    ? 'text-right align-top whitespace-nowrap'
                    : cell.column.id === 'actions'
                      ? 'pr-5 text-right align-top whitespace-nowrap'
                      : 'align-top',
            })}
            footer={
              filteredRows.length > 0 ? (
                <DataTablePagination table={table} totalRows={filteredRows.length} />
              ) : null
            }
          />
        )}
      </DataTableBlock>
    </div>
  );
}
