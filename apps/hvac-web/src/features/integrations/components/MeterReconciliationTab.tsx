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
import { useDataTable } from '@/hooks/use-data-table';
import { formatDecimal } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import { describeIntegrationsError } from '../governance-shared-model';
import type {
  MeterReconciliationModel,
  MeterReconciliationRow,
} from '../meter-reconciliation-model';
import { GovernanceBadge } from './GovernanceShared';
import {
  HourlyQualityStrip,
  MeterHourlyReconciliationPanel,
} from './MeterHourlyReconciliationPanel';

export interface MeterReconciliationTabProps {
  readonly model: MeterReconciliationModel | null;
  readonly isAssetLoading: boolean;
  readonly isAssetError: boolean;
  readonly assetError: unknown;
  readonly isEnergyLoading: boolean;
  readonly isEnergyError: boolean;
  readonly energyError: unknown;
  readonly canReadSnapshots: boolean;
  readonly isSnapshotsError: boolean;
  readonly onRetry: () => void;
  readonly onInspectMeter: (deviceCode: string) => void;
}

export function MeterReconciliationTab({
  model,
  isAssetLoading,
  isAssetError,
  assetError,
  isEnergyLoading,
  isEnergyError,
  energyError,
  canReadSnapshots,
  isSnapshotsError,
  onRetry,
  onInspectMeter,
}: MeterReconciliationTabProps) {
  const rows = useMemo(() => [...(model?.rows ?? [])], [model?.rows]);

  const columns = useMemo<ColumnDef<DataTableFeatures, MeterReconciliationRow>[]>(
    () => [
      {
        id: 'meter',
        header: () => '表计与位置',
        cell: ({ row }) => {
          const item = row.original;
          return (
            <div>
              <button
                type="button"
                className="block text-left font-semibold text-foreground outline-none hover:underline focus-visible:underline"
                onClick={() => onInspectMeter(item.deviceCode)}
              >
                {item.displayName}
              </button>
              <div className="mt-0.5 text-xs text-muted-foreground">
                <span>{item.meterRoleLabel}</span>
                <span> · </span>
                <span className="font-mono">{item.deviceCode}</span>
                {item.spaceName ? <span> · {item.spaceName}</span> : null}
              </div>
            </div>
          );
        },
      },
      {
        id: 'hourlyStrip',
        header: () => '24 小时状态条',
        cell: ({ row }) => {
          const item = row.original;
          if (!item.supportsHourlyEnergy) {
            return <span className="text-xs text-muted-foreground">仅实时瞬时量监测</span>;
          }
          if (!item.hasHourlySeries || item.hourlyStrip.length === 0) {
            return (
              <span className="text-xs text-muted-foreground">
                {isEnergyError ? '逐时统计暂不可读' : '暂无逐时电量汇总（—）'}
              </span>
            );
          }
          return (
            <HourlyQualityStrip
              buckets={item.hourlyStrip}
              missingCompletedHours={item.missingCompletedHours}
            />
          );
        },
      },
      {
        id: 'energy24h',
        header: () => '近 24 小时计量',
        cell: ({ row }) => {
          const item = row.original;
          if (!item.hasHourlySeries || item.validEnergyKwh === null) {
            return <span className="text-muted-foreground">—</span>;
          }
          const unitLabel = item.meterRole === 'THERMAL_METER' ? 'kWh 冷量' : 'kWh';
          return (
            <div className="tabular-nums">
              <div className="font-mono text-foreground">
                {formatDecimal(item.validEnergyKwh, 2)} {unitLabel}
              </div>
              {item.suspectEnergyKwh > 0.001 ? (
                <div className="mt-0.5 font-mono text-xs text-warning-text">
                  ▲ 可疑 +{formatDecimal(item.suspectEnergyKwh, 2)} {unitLabel}
                </div>
              ) : (
                <div className="mt-0.5 text-xs text-muted-foreground">无跳变或可疑差值</div>
              )}
            </div>
          );
        },
      },
      {
        id: 'reading',
        header: () => '当前累计读数',
        cell: ({ row }) => {
          const item = row.original;
          return (
            <div className="tabular-nums">
              <div className="font-mono text-foreground">
                {item.primaryReadingValue}
                {item.primaryReadingValue !== '—' && item.primaryReadingUnit
                  ? ` ${item.primaryReadingUnit}`
                  : ''}
              </div>
              <div className="mt-0.5 text-xs text-muted-foreground">{item.counterPolicyLabel}</div>
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
                onClick={() => onInspectMeter(item.deviceCode)}
              >
                核查详情
              </Button>
            </div>
          );
        },
      },
    ],
    [isEnergyError, onInspectMeter],
  );

  const table = useDataTable({
    key: 'integrations.meters',
    data: rows,
    columns,
    pageSize: 10,
    getRowId: (row) => row.deviceId,
  });

  if (isAssetError) {
    return (
      <StateCard
        tone="danger"
        title={<h2 className="text-base font-semibold">无法读取站点计量表计配置</h2>}
        description={describeIntegrationsError(assetError)}
      >
        <Button variant="outline" size="sm" onClick={onRetry}>
          重试
        </Button>
      </StateCard>
    );
  }

  const summary = model?.summary;
  const hourlyBuckets = model?.hourlyBuckets ?? [];

  return (
    <div className="flex flex-col gap-4">
      {!canReadSnapshots ? (
        <Alert>
          <AlertDescription>
            当前账号没有实时读数查看权限，当前累计读数暂不可见，以下展示表计登记与逐时电量核对。
          </AlertDescription>
        </Alert>
      ) : isSnapshotsError ? (
        <Alert>
          <AlertDescription>
            实时读数快照暂时无法读取，当前累计读数显示为 —，稍后可重试刷新。
          </AlertDescription>
        </Alert>
      ) : null}

      <Panel aria-label="电表核对概览">
        {isAssetLoading || !summary ? (
          <div className="grid grid-cols-1 gap-4 p-5 sm:grid-cols-3">
            <Skeleton className="h-16" />
            <Skeleton className="h-16" />
            <Skeleton className="h-16" />
          </div>
        ) : (
          <div className="grid grid-cols-1 divide-y divide-divider sm:grid-cols-3 sm:divide-x sm:divide-y-0">
            <div className="flex flex-col gap-1 px-6 py-4">
              <span className="text-xs font-medium text-muted-foreground">已接入表计</span>
              <div className="flex items-baseline gap-1.5">
                <span className="font-mono text-2xl font-semibold tabular-nums">
                  {summary.totalMeters}
                </span>
                <span className="text-xs text-muted-foreground">台</span>
              </div>
              <span className="text-xs text-muted-foreground">
                含总电表、总冷量表、流量计与设备电表
              </span>
            </div>

            <div className="flex flex-col gap-1 px-6 py-4">
              <span className="text-xs font-medium text-muted-foreground">有效计量时段</span>
              <div className="flex items-baseline gap-1.5">
                <span className="font-mono text-2xl font-semibold tabular-nums">
                  {summary.completedHours > 0
                    ? `${summary.validHours} / ${summary.completedHours}`
                    : '—'}
                </span>
                {summary.completedHours > 0 ? (
                  <span className="text-xs text-muted-foreground">小时</span>
                ) : null}
              </div>
              <span className="text-xs text-muted-foreground">
                {summary.completedHours > 0
                  ? `可疑 ${summary.suspectHours} 小时 · 缺数据 ${summary.missingHours} 小时`
                  : '等待近 24 小时逐时电量统计'}
              </span>
            </div>

            <div className="flex flex-col gap-1 px-6 py-4">
              <span className="flex items-center gap-1 text-xs font-medium text-muted-foreground">
                待现场确认
                <InfoTip>
                  同时对比有效口径与含可疑样本口径；可疑跳变电量仅提示现场核查，平台保留原始累计采集记录，不在网页端手工覆盖历史读数
                </InfoTip>
              </span>
              <div className="flex items-baseline gap-1.5">
                <span
                  className={cn(
                    'font-mono text-2xl font-semibold tabular-nums',
                    summary.attentionMeters > 0 ? 'text-warning-text' : 'text-foreground',
                  )}
                >
                  {summary.attentionMeters}
                </span>
                <span className="text-xs text-muted-foreground">台</span>
              </div>
              <span className="text-xs text-muted-foreground">
                {summary.attentionMeters > 0
                  ? '存在可疑差值、缺段或通信延迟'
                  : '各计量表计近 24 小时读数连续'}
              </span>
            </div>
          </div>
        )}
      </Panel>

      <MeterHourlyReconciliationPanel
        summary={summary}
        hourlyBuckets={hourlyBuckets}
        isEnergyLoading={isEnergyLoading}
        isEnergyError={isEnergyError}
        energyError={energyError}
      />

      <DataTableBlock
        title="计量表计"
        description="核对总电表、总冷量表、流量计与设备电表的通信状态、逐时质量条与累计示数；点击核查详情打开侧边抽屉"
      >
        {isAssetLoading ? (
          <Skeleton className="h-48 w-full rounded-xl" />
        ) : (
          <DataTable
            table={table}
            tableAriaLabel="计量表计"
            empty={
              <div className="py-10 text-center text-sm text-muted-foreground">
                当前站点还没有登记计量表计或累计电表点位
              </div>
            }
            getHeaderCellProps={(header) => ({
              className:
                header.id === 'meter'
                  ? 'pl-5'
                  : header.id === 'energy24h' || header.id === 'reading'
                    ? 'text-right'
                    : header.id === 'actions'
                      ? 'pr-5 text-right'
                      : undefined,
            })}
            getCellProps={(cell) => ({
              className:
                cell.column.id === 'meter'
                  ? 'pl-5 align-top'
                  : cell.column.id === 'energy24h' || cell.column.id === 'reading'
                    ? 'text-right align-top whitespace-nowrap'
                    : cell.column.id === 'actions'
                      ? 'pr-5 text-right align-top whitespace-nowrap'
                      : 'align-top',
            })}
            footer={
              rows.length > 0 ? <DataTablePagination table={table} totalRows={rows.length} /> : null
            }
          />
        )}
      </DataTableBlock>
    </div>
  );
}
