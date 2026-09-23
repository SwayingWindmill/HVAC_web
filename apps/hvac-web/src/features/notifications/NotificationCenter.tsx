import { useEffect, useMemo, useState } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import { Check, RefreshCw } from 'lucide-react';
import { DataTableBlock } from '@/blocks/data-table';
import {
  listNotifications,
  markNotificationRead,
  notificationErrorMessage,
  type NotificationInboxItem,
} from '@/api/notifications';
import type { ShellSnapshot } from '@/app/shell-runtime';
import { siteRoute } from '@/app/router-paths';
import { Main } from '@/components/layout/Main';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Sheet, SheetContent, SheetHeader, SheetTitle } from '@/components/ui/sheet';
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { DataTable, DataTablePagination, type DataTableFeatures } from '@/components/data-table';
import { StatusBadge } from '@/components/status-badge';
import { useDataTable } from '@/hooks/use-data-table';

interface NotificationCenterProps {
  snapshot: ShellSnapshot;
}

const severityLabel: Record<NotificationInboxItem['severity'], string> = {
  INFO: '提示',
  WARNING: '警告',
  MINOR: '一般',
  MAJOR: '重要',
  CRITICAL: '紧急',
};

function statusLabel(status: NotificationInboxItem['status']): string {
  if (status === 'UNREAD') return '未读';
  if (status === 'READ') return '已读';
  return '已确认';
}

export function NotificationCenter({ snapshot }: NotificationCenterProps) {
  const principal = snapshot.principal!;
  const [items, setItems] = useState<NotificationInboxItem[]>([]);
  const [filter, setFilter] = useState<'unread' | 'all'>('unread');
  const [loading, setLoading] = useState(true);
  const [failure, setFailure] = useState<string | null>(null);
  const [selected, setSelected] = useState<NotificationInboxItem | null>(null);
  const [markingId, setMarkingId] = useState<string | null>(null);
  const [reloadKey, setReloadKey] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setFailure(null);
    listNotifications(controller.signal)
      .then((notifications) => {
        setItems([...notifications].sort((left, right) => Date.parse(right.createdAt) - Date.parse(left.createdAt)));
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted) setFailure(notificationErrorMessage(error));
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [reloadKey]);

  const siteById = useMemo(
    () => new Map((snapshot.sites?.items ?? []).map((site) => [site.id, site])),
    [snapshot.sites?.items],
  );
  const unreadCount = items.filter((item) => item.status === 'UNREAD').length;
  const visibleItems = filter === 'unread' ? items.filter((item) => item.status === 'UNREAD') : items;

  const markRead = async (item: NotificationInboxItem) => {
    if (item.status !== 'UNREAD' || markingId) return;
    setMarkingId(item.inboxItemId);
    setFailure(null);
    try {
      const updated = await markNotificationRead(item.inboxItemId, principal.session.csrfToken);
      setItems((current) => current.map((candidate) => candidate.inboxItemId === updated.inboxItemId ? updated : candidate));
      setSelected((current) => current?.inboxItemId === updated.inboxItemId ? updated : current);
    } catch (error: unknown) {
      setFailure(notificationErrorMessage(error));
    } finally {
      setMarkingId(null);
    }
  };

  const notificationColumns = useMemo<Array<ColumnDef<DataTableFeatures, NotificationInboxItem>>>(() => [
    {
      id: 'select',
      header: ({ table }) => (
        <Checkbox
          checked={table.getIsAllPageRowsSelected() ? true : table.getIsSomePageRowsSelected() ? 'indeterminate' : false}
          onCheckedChange={(checked) => table.toggleAllPageRowsSelected(Boolean(checked))}
          aria-label="全选通知"
        />
      ),
      cell: ({ row }) => (
        <Checkbox
          checked={row.getIsSelected()}
          onCheckedChange={(checked) => row.toggleSelected(Boolean(checked))}
          aria-label={`选择 ${row.original.subject}`}
          onClick={(event) => event.stopPropagation()}
        />
      ),
      enableSorting: false,
      enableHiding: false,
    },
    {
      id: 'status',
      header: '状态',
      cell: ({ row }) => <StatusBadge tone={row.original.status === 'UNREAD' ? 'in-progress' : 'neutral'} label={statusLabel(row.original.status)} />,
    },
    {
      id: 'severity',
      header: '级别',
      cell: ({ row }) => (
        <StatusBadge
          tone={row.original.severity === 'CRITICAL' ? 'destructive' : row.original.severity === 'MAJOR' || row.original.severity === 'WARNING' ? 'warning' : 'info'}
          label={severityLabel[row.original.severity]}
        />
      ),
    },
    {
      id: 'message',
      header: '通知',
      cell: ({ row }) => (
        <div>
          <strong className={row.original.status === 'UNREAD' ? 'block text-xs font-semibold text-foreground' : 'block text-xs font-medium text-foreground'}>{row.original.subject}</strong>
          <span className="mt-0.5 block max-w-xl truncate text-[11px] text-muted-foreground">{row.original.body}</span>
        </div>
      ),
    },
    { id: 'site', header: '站点', cell: ({ row }) => <span className="text-muted-foreground">{siteById.get(row.original.siteId)?.displayName ?? '站点信息不可用'}</span> },
    { id: 'createdAt', header: '时间', cell: ({ row }) => <span className="font-mono text-muted-foreground tabular-nums">{new Date(row.original.createdAt).toLocaleString()}</span> },
    {
      id: 'actions',
      header: '操作',
      cell: ({ row }) => row.original.status === 'UNREAD' ? (
        <Button
          variant="ghost"
          size="sm"
          className="h-7 gap-1 text-xs"
          disabled={markingId === row.original.inboxItemId}
          onClick={(event) => {
            event.stopPropagation();
            void markRead(row.original);
          }}
        >
          <Check className="size-3" />已读
        </Button>
      ) : null,
      enableSorting: false,
    },
  ], [markingId, siteById, principal.session.csrfToken]);

  const notificationTable = useDataTable({
    key: 'notification-center',
    data: [...visibleItems],
    columns: notificationColumns,
    pageSize: 10,
    getRowId: (row) => row.inboxItemId,
  });

  const selectedSite = selected ? siteById.get(selected.siteId) : undefined;

  return (
    <section data-testid="real-route-notifications" data-route-state="READY" data-business-state="POPULATED">
      <Main>
        <div className="space-y-4">
          <div className="flex justify-end gap-2">
            <Badge variant={unreadCount > 0 ? 'default' : 'outline'}>{unreadCount} 未读</Badge>
            <Button variant="outline" onClick={() => setReloadKey((value) => value + 1)} disabled={loading}><RefreshCw className={loading ? 'animate-spin' : undefined} />刷新</Button>
          </div>
          <Alert>
            <AlertTitle>通知与告警生命周期相互独立</AlertTitle>
            <AlertDescription>这里只展示服务器投递给当前用户的通知。将通知标记为已读，不会改变告警确认、恢复或工单状态。</AlertDescription>
          </Alert>
          {failure ? <Alert variant="destructive"><AlertTitle>通知读取失败</AlertTitle><AlertDescription>{failure}</AlertDescription></Alert> : null}
          <DataTableBlock>
            {loading ? (
              <div className="grid min-h-36 place-items-center rounded-md border text-sm text-muted-foreground">正在读取通知…</div>
            ) : (
              <DataTable
                table={notificationTable}
                tableAriaLabel="通知"
                empty={filter === 'unread' ? '当前没有未读通知' : '当前没有通知'}
                getHeaderRowProps={() => ({ className: 'bg-muted/20' })}
                getHeaderCellProps={(header) => ({
                  className:
                    header.id === 'select' ? 'w-12 text-center' :
                    header.id === 'status' || header.id === 'severity' ? 'w-28 text-center' :
                    header.id === 'site' ? 'w-44' :
                    header.id === 'createdAt' ? 'w-48' :
                    header.id === 'actions' ? 'w-24 text-center' :
                    undefined,
                })}
                getRowProps={(row) => ({
                  className: 'cursor-pointer hover:bg-muted/40',
                  onClick: () => setSelected(row.original),
                })}
                getCellProps={(cell) => ({
                  className:
                    cell.column.id === 'select' || cell.column.id === 'status' || cell.column.id === 'severity' || cell.column.id === 'actions'
                      ? 'text-center'
                      : undefined,
                })}
                footer={<DataTablePagination table={notificationTable} totalRows={visibleItems.length} />}
              >
                <div className="flex justify-end">
                  <Select
                    value={filter}
                    onValueChange={(val) => {
                      setFilter(val as 'unread' | 'all');
                      notificationTable.setPageIndex(0);
                    }}
                  >
                    <SelectTrigger className="w-36" aria-label="筛选通知状态">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent><SelectGroup>
                      <SelectItem value="unread">未读 ({unreadCount})</SelectItem>
                      <SelectItem value="all">全部 ({items.length})</SelectItem>
                    </SelectGroup></SelectContent>
                  </Select>
                </div>
              </DataTable>
            )}
          </DataTableBlock>
        </div>
      </Main>

      <Sheet open={Boolean(selected)} onOpenChange={(open) => { if (!open) setSelected(null); }}>
        <SheetContent className="w-full overflow-y-auto sm:max-w-xl!">
          <SheetHeader>
            <SheetTitle>{selected?.subject ?? '通知详情'}</SheetTitle>
          </SheetHeader>
          {selected ? (
            <div className="space-y-5 px-4 pb-6">
              <p className="text-sm leading-6">{selected.body}</p>
              <dl className="grid overflow-hidden rounded-md border">
                {[
                  ['状态', statusLabel(selected.status)],
                  ['级别', severityLabel[selected.severity]],
                  ['时间', new Date(selected.createdAt).toLocaleString()],
                  ['站点', selectedSite?.displayName ?? '站点信息不可用'],
                ].map(([label, value], index) => (
                  <div key={label} className={`grid grid-cols-[96px_minmax(0,1fr)] gap-3 px-3 py-2.5 ${index > 0 ? 'border-t' : ''}`}>
                    <dt className="text-sm text-muted-foreground">{label}</dt>
                    <dd className="text-sm font-medium">{value}</dd>
                  </div>
                ))}
              </dl>
              {selected.status === 'UNREAD' ? <Button className="w-full" disabled={markingId === selected.inboxItemId} onClick={() => void markRead(selected)}><Check />标记已读</Button> : null}
              {selectedSite ? <Button variant="outline" className="w-full" asChild><a href={siteRoute(selectedSite, 'alarms')}>查看该站点告警</a></Button> : null}
            </div>
          ) : null}
        </SheetContent>
      </Sheet>
    </section>
  );
}
