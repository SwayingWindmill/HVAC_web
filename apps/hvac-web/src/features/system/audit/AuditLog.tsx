import { useMemo, useState } from 'react';
import { Search } from 'lucide-react';
import { PlatformApiError } from '@/api/generated/platformGateway.gen';
import { Panel } from '@/blocks/panel';
import { StatusBadge } from '@/components/status-badge';
import { InputGroup, InputGroupAddon, InputGroupInput } from '@/components/ui/input-group';
import { Skeleton } from '@/components/ui/skeleton';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { presentAuditRecord, type AuditRow, type AuditViewer } from './model';
import { useAuditSearch } from './query';

const AUDIT_SEARCH_LIMIT = 100;
const auditTimeFormat = new Intl.DateTimeFormat('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false });

interface AuditLogProps {
  readonly viewer: AuditViewer;
  readonly canRead: boolean;
  readonly active: boolean;
}

function Notice({ children }: { readonly children: string }) {
  return <p className="px-5 py-10 text-center text-sm text-muted-foreground">{children}</p>;
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
  const forbidden = !canRead || (query.error instanceof PlatformApiError && query.error.problem.status === 403);
  const ready = !forbidden && !__HVAC_WEB_FRONTEND_REVIEW__ && query.isSuccess;

  return (
    <Panel aria-label="操作记录">
      <div className="flex h-12 items-center gap-3 border-b border-divider px-4">
        <InputGroup className="h-8 w-72">
          <InputGroupAddon><Search /></InputGroupAddon>
          <InputGroupInput aria-label="搜索操作记录" placeholder="搜索操作人、操作或对象" value={search} disabled={!ready} onChange={(event) => setSearch(event.target.value)} />
        </InputGroup>
        <span className="ml-auto text-xs text-muted-foreground">本组织最近 {AUDIT_SEARCH_LIMIT} 条</span>
      </div>
      {forbidden ? <Notice>当前账号没有查看操作记录的权限</Notice>
        : __HVAC_WEB_FRONTEND_REVIEW__ ? <Notice>评审构建不连接操作记录服务</Notice>
        : query.isPending ? <div className="flex flex-col gap-2 p-4"><Skeleton className="h-9" /><Skeleton className="h-9" /><Skeleton className="h-9" /></div>
        : query.isError ? <Notice>操作记录暂时读不到，没有显示任何替代记录</Notice>
        : rows.length === 0 ? <Notice>{search ? '没有匹配的操作记录' : '本组织还没有操作记录'}</Notice>
        : (
          <Table aria-label="操作记录">
            <TableHeader>
              <TableRow>
                <TableHead className="pl-5">时间</TableHead>
                <TableHead>操作人</TableHead>
                <TableHead>操作</TableHead>
                <TableHead>对象</TableHead>
                <TableHead className="pr-5">结果</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((row) => (
                <TableRow key={row.key} className="hover:bg-transparent">
                  <TableCell className="pl-5 font-mono text-xs">{auditTimeFormat.format(new Date(row.occurredAt))}</TableCell>
                  <TableCell>{row.actor}</TableCell>
                  <TableCell className="font-medium">{row.action}</TableCell>
                  <TableCell className="text-muted-foreground">{row.resource}</TableCell>
                  <TableCell className="pr-5"><StatusBadge tone={row.tone} label={row.outcome} /></TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
    </Panel>
  );
}
