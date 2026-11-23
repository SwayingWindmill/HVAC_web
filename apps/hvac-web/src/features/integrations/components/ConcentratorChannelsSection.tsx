import { useId, useMemo, useState } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import { Search, X } from 'lucide-react';
import { DataTableBlock } from '@/blocks/data-table';
import { DataTable, DataTablePagination, type DataTableFeatures } from '@/components/data-table';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Skeleton } from '@/components/ui/skeleton';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { useDataTable } from '@/hooks/use-data-table';
import {
  filterConcentratorChannels,
  formatConcentratorEidDisplay,
  type ConcentratorChannelFilter,
  type ConcentratorChannelRow,
} from '../concentrator-model';
import { GovernanceBadge } from './GovernanceShared';

const CHANNEL_FILTERS: readonly {
  readonly value: ConcentratorChannelFilter;
  readonly label: string;
}[] = [
  { value: 'ALL', label: '全部通道' },
  { value: 'ONLINE', label: '在线' },
  { value: 'OFFLINE', label: '离线' },
  { value: 'LOW_BATTERY', label: '低电压' },
];

export interface ConcentratorChannelsSectionProps {
  readonly rows: readonly ConcentratorChannelRow[];
  readonly loading: boolean;
  readonly onInspectChannel: (channel: number) => void;
}

export function ConcentratorChannelsSection({
  rows,
  loading,
  onInspectChannel,
}: ConcentratorChannelsSectionProps) {
  const searchInputId = useId();
  const [filter, setFilter] = useState<ConcentratorChannelFilter>('ALL');
  const [draftQuery, setDraftQuery] = useState('');
  const [committedQuery, setCommittedQuery] = useState('');

  const filteredRows = useMemo(
    () => [...filterConcentratorChannels(rows, filter, committedQuery)],
    [rows, filter, committedQuery],
  );

  const columns = useMemo<ColumnDef<DataTableFeatures, ConcentratorChannelRow>[]>(
    () => [
      {
        id: 'channel',
        header: () => '通道与探头型号',
        cell: ({ row }) => {
          const item = row.original;
          return (
            <div>
              <button
                type="button"
                className="block font-mono text-left font-semibold text-foreground outline-none hover:underline focus-visible:underline tabular-nums"
                onClick={() => onInspectChannel(item.channel)}
              >
                CH {String(item.channel).padStart(2, '0')} · {item.sensorModel || '未命名型号'}
              </button>
              <div className="mt-0.5 text-xs text-muted-foreground tabular-nums">
                传感器类型代码 {item.sensorType}
              </div>
            </div>
          );
        },
      },
      {
        id: 'eid',
        header: () => '绑定探头 EID',
        cell: ({ row }) => {
          const item = row.original;
          return (
            <div className="font-mono text-xs tabular-nums">
              {item.eid > 0 ? (
                <>
                  <div className="font-medium text-foreground">{item.eid}</div>
                  <div className="text-muted-foreground">
                    0x{item.eid.toString(16).toUpperCase().padStart(8, '0')}
                  </div>
                </>
              ) : (
                <span className="text-muted-foreground">{formatConcentratorEidDisplay(item.eid)}</span>
              )}
            </div>
          );
        },
      },
      {
        id: 'temperature',
        header: () => '实时温度',
        cell: ({ row }) => {
          const item = row.original;
          return (
            <span className="font-mono font-medium text-foreground tabular-nums">
              {item.temperatureC !== null ? `${item.temperatureC.toFixed(1)} °C` : '—'}
            </span>
          );
        },
      },
      {
        id: 'batteryAndRssi',
        header: () => '电池电压与信号强度',
        cell: ({ row }) => {
          const item = row.original;
          return (
            <div className="flex flex-col gap-1">
              <div className="flex items-center gap-2 font-mono text-xs tabular-nums">
                <span className="text-foreground">
                  {item.voltageMv !== null ? `${item.voltageMv} mV` : '— mV'}
                </span>
                <span className="text-muted-foreground">·</span>
                <span className="text-muted-foreground">
                  {item.rssiDbm !== null ? `${item.rssiDbm} dBm` : '— dBm'}
                </span>
              </div>
              <GovernanceBadge status={item.batteryStatus} />
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
              <GovernanceBadge status={item.linkStatus} />
              <Button
                variant="outline"
                size="sm"
                onClick={() => onInspectChannel(item.channel)}
              >
                通道详情
              </Button>
            </div>
          );
        },
      },
    ],
    [onInspectChannel],
  );

  const table = useDataTable({
    key: 'integrations.concentrator-channels',
    data: filteredRows,
    columns,
    pageSize: 25,
    getRowId: (row) => String(row.channel),
  });

  const applySearch = () => {
    table.setPageIndex(0);
    setCommittedQuery(draftQuery);
  };

  const clearSearch = () => {
    table.setPageIndex(0);
    setDraftQuery('');
    setCommittedQuery('');
  };

  return (
    <DataTableBlock
      title="无线探头通道"
      description="展示集中器各通道的实测温度、电池电压与无线信号强度；支持按在线/低电压筛选或搜索通道号与探头 EID"
      actions={
        <ToggleGroup
          type="single"
          size="sm"
          variant="outline"
          value={filter}
          onValueChange={(next) => {
            const matched = CHANNEL_FILTERS.find((item) => item.value === next);
            if (!matched) return;
            table.setPageIndex(0);
            setFilter(matched.value);
          }}
          aria-label="通道状态筛选"
        >
          {CHANNEL_FILTERS.map((item) => (
            <ToggleGroupItem key={item.value} value={item.value} className="text-xs">
              {item.label}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
      }
      controls={
        <div className="flex flex-wrap items-center gap-2">
          <Label htmlFor={searchInputId} className="sr-only">
            搜索通道号、探头 EID 或传感器型号
          </Label>
          <div className="relative w-full max-w-md">
            <Search
              className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground"
              aria-hidden="true"
            />
            <Input
              id={searchInputId}
              value={draftQuery}
              onChange={(event) => {
                const next = event.currentTarget.value;
                setDraftQuery(next);
                if (next === '') {
                  table.setPageIndex(0);
                  setCommittedQuery('');
                }
              }}
              onKeyDown={(event) => {
                if (event.nativeEvent.isComposing || event.keyCode === 229) return;
                if (event.key === 'Enter') {
                  event.preventDefault();
                  applySearch();
                }
              }}
              placeholder="搜通道号、EID（如 50 / 0x00000037）或型号，按 Enter"
              className="pl-8 text-sm"
            />
          </div>
          <Button variant="outline" size="sm" onClick={applySearch}>
            搜索通道
          </Button>
          {draftQuery || committedQuery ? (
            <Button variant="ghost" size="sm" onClick={clearSearch}>
              <X data-icon="inline-start" />
              清空条件
            </Button>
          ) : null}
        </div>
      }
    >
      {loading ? (
        <Skeleton className="h-56 w-full rounded-xl" />
      ) : (
        <DataTable
          table={table}
          tableAriaLabel="无线探头通道"
          empty={
            <div className="py-10 text-center text-sm text-muted-foreground">
              {rows.length === 0
                ? '当前集中器尚未返回有效无线通道数据'
                : '当前筛选或搜索条件下没有匹配的探头通道'}
            </div>
          }
          getHeaderCellProps={(header) => ({
            className:
              header.id === 'channel'
                ? 'pl-5'
                : header.id === 'temperature'
                  ? 'text-right'
                  : header.id === 'actions'
                    ? 'pr-5 text-right'
                    : undefined,
          })}
          getCellProps={(cell) => ({
            className:
              cell.column.id === 'channel'
                ? 'pl-5 align-top'
                : cell.column.id === 'temperature'
                  ? 'text-right align-top whitespace-nowrap'
                  : cell.column.id === 'actions'
                    ? 'pr-5 text-right align-top whitespace-nowrap'
                    : 'align-top',
          })}
          footer={
            filteredRows.length > 0 ? (
              <DataTablePagination
                table={table}
                totalRows={filteredRows.length}
                pageSizeOptions={[10, 25, 50]}
              />
            ) : null
          }
        />
      )}
    </DataTableBlock>
  );
}
