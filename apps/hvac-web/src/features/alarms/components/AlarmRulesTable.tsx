import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import type { ColumnDef } from '@tanstack/react-table';
import { DataTableBlock } from '@/blocks/data-table';
import { DataTable, type DataTableFeatures } from '@/components/data-table';
import { Skeleton } from '@/components/ui/skeleton';
import { useSiteEquipment } from '@/features/assets/use-device-names';
import { useDataTable } from '@/hooks/use-data-table';
import { describeCondition, describeDevices, describeRaise, siteAlarmRulesQuery, type AlarmRule, type ReadingNames } from '../alarm-rules';
import { SeverityBadge, severityRank } from '../alarm-presentation';

/** The Site's rules in effect, read-only: what each watches, when it raises and when it clears (#693). */
export function AlarmRulesTable({ siteId }: { readonly siteId: string }) {
  const rules = useQuery(siteAlarmRulesQuery(siteId));
  const equipment = useSiteEquipment(siteId);
  const names = useMemo<ReadingNames>(() => ({
    device: (deviceId) => equipment.nameByDevice.get(deviceId) ?? '未登记设备',
    point: (reading) => equipment.pointByReading.get(`${reading.deviceId}/${reading.pointCode}`) ?? { name: '未登记读数' },
  }), [equipment]);
  const sorted = useMemo(() => [...(rules.data ?? [])].sort((left, right) =>
    severityRank(left.severity) - severityRank(right.severity) || left.title.localeCompare(right.title, 'zh-CN')), [rules.data]);

  const columns = useMemo<Array<ColumnDef<DataTableFeatures, AlarmRule>>>(() => [
    { id: 'title', header: '规则', cell: ({ row }) => <span className="font-medium text-foreground">{row.original.title}</span> },
    { id: 'devices', header: '设备', cell: ({ row }) => describeDevices(row.original, names) },
    { id: 'raise', header: '什么时候报警', cell: ({ row }) => describeRaise(row.original, names) },
    { id: 'clear', header: '什么时候恢复', cell: ({ row }) => describeCondition(row.original.clear, names) },
    { id: 'severity', header: '严重程度', cell: ({ row }) => <SeverityBadge severity={row.original.severity} /> },
  ], [names]);
  const table = useDataTable({ key: `alarm-rules-${siteId}`, data: sorted, columns, paginate: false, getRowId: (rule) => rule.ruleId });

  return (
    <DataTableBlock
      description={rules.data ? `站点现在按这 ${rules.data.length} 条规则判断告警。规则随站点的规则文件发布，这里只能查看。` : '站点现在按哪些规则判断告警'}
    >
      {rules.isError ? (
        <p className="text-sm text-destructive-text">规则暂时读不到，稍后点「刷新」再看。</p>
      ) : rules.isPending ? (
        <div className="flex flex-col gap-2"><Skeleton className="h-12" /><Skeleton className="h-12" /><Skeleton className="h-12" /></div>
      ) : (
        <DataTable
          table={table}
          tableAriaLabel="正在生效的规则"
          empty="这个站点还没有发布告警规则"
          getHeaderCellProps={(header) => ({
            className: header.id === 'title' ? 'w-56' : header.id === 'devices' ? 'w-56' : header.id === 'severity' ? 'w-24' : undefined,
          })}
          getCellProps={(cell) => ({ className: cell.column.id === 'raise' || cell.column.id === 'clear' || cell.column.id === 'devices' ? 'whitespace-normal align-top text-muted-foreground' : 'align-top' })}
        />
      )}
    </DataTableBlock>
  );
}
