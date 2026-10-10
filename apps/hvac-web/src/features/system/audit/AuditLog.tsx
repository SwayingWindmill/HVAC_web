import { useMemo, useState } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import { ScrollText } from 'lucide-react';
import { PlatformApiError } from '@/api/generated/platformGateway.gen';
import { DataTableBlock } from '@/blocks/data-table';
import { DataTable, type DataTableFeatures } from '@/components/data-table';
import { StatusBadge } from '@/components/status-badge';
import { Input } from '@/components/ui/input';
import { useDataTable } from '@/hooks/use-data-table';
import { EmptyGovernanceState } from '../EmptyGovernanceState';
import { presentAuditRecord, type AuditRow, type AuditViewer } from './model';
import { useAuditSearch } from './query';

const AUDIT_SEARCH_LIMIT = 100;
const auditTimeFormat = new Intl.DateTimeFormat('zh-CN', { dateStyle: 'short', timeStyle: 'medium' });

const columns: Array<ColumnDef<DataTableFeatures, AuditRow>> = [
  { id: 'occurredAt', header: '时间', cell: ({ row }) => <span className="tabular-nums">{auditTimeFormat.format(new Date(row.original.occurredAt))}</span> },
  { id: 'actor', header: '操作人', cell: ({ row }) => row.original.actor },
  { id: 'action', header: '动作', cell: ({ row }) => row.original.action },
  { id: 'resource', header: '对象', cell: ({ row }) => row.original.resource },
  { id: 'outcome', header: '结果', cell: ({ row }) => <StatusBadge tone={row.original.tone} label={row.original.outcome} /> },
];

interface AuditLogProps {
  readonly viewer: AuditViewer;
  readonly canRead: boolean;
  readonly active: boolean;
}

// The Tenant's newest audit records. Review builds have no Audit Ledger and never query it.
export function AuditLog({ viewer, canRead, active }: AuditLogProps) {
  const query = useAuditSearch({ limit: AUDIT_SEARCH_LIMIT }, canRead && active && !__HVAC_WEB_FRONTEND_REVIEW__);
  const [search, setSearch] = useState('');
  const rows = useMemo<AuditRow[]>(() => {
    const presented = (query.data ?? []).map((record) => presentAuditRecord(record, viewer));
    const term = search.trim();
    return term ? presented.filter((row) => [row.actor, row.action, row.resource].some((value) => value.includes(term))) : presented;
  }, [query.data, search, viewer]);
  const table = useDataTable({ key: 'system-management-audit', data: rows, columns, paginate: false, getRowId: (row) => row.key });
  const forbidden = !canRead || (query.error instanceof PlatformApiError && query.error.problem.status === 403);

  const ready = !forbidden && !__HVAC_WEB_FRONTEND_REVIEW__ && query.isSuccess;
  return (
    <DataTableBlock
      title={<span className="flex items-center gap-2"><ScrollText className="size-4" />审计日志</span>}
      description={`本租户最近 ${AUDIT_SEARCH_LIMIT} 条`}
      controls={ready ? <Input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="搜索操作人、动作或对象" className="max-w-72" aria-label="搜索审计记录" /> : undefined}
    >
      {forbidden ? <EmptyGovernanceState description="当前账号没有审计查询权限" />
        : __HVAC_WEB_FRONTEND_REVIEW__ ? <EmptyGovernanceState description="评审构建不连接审计服务" />
        : query.isPending ? <EmptyGovernanceState description="正在读取审计记录" />
        : query.isError ? <EmptyGovernanceState description="审计服务暂时不可用，未显示任何替代记录" />
        : rows.length ? <DataTable table={table} tableAriaLabel="审计日志" />
        : <EmptyGovernanceState description={search ? '没有匹配的审计记录' : '本租户还没有审计记录'} />}
    </DataTableBlock>
  );
}
