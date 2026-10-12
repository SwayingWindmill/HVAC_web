import { useMemo, useState } from 'react';
import { PageHeader } from '@/blocks/page-header';
import { Main } from '@/components/layout/Main';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { getRouteApi, useNavigate } from '@tanstack/react-router';
import { ChevronRight, Plus, RefreshCw } from 'lucide-react';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { MetricCard, MetricGrid, MetricValue } from '@/blocks/metric-card';
import type { WorkOrder } from '@/api/work-orders';
import { formatTime, personLabel } from '@/lib/operator-format';
import { CreateWorkOrderDialog } from '../CreateWorkOrderDialog';
import { TableEmpty } from '@/components/data-table';
import { OPEN_STATUSES, PriorityBadge, sortWorkOrdersForOperators, WorkOrderStatusBadge } from '../work-order-presentation';
import { listView, openWorkOrdersQuery, workOrderKeys } from '../work-order-queries';
import { WorkOrderInspector } from './WorkOrderInspector';

export type WorkCenterView = 'open' | 'mine' | 'done' | 'all';

const VIEW_LABELS: Readonly<Record<WorkCenterView, string>> = {
  open: '进行中',
  mine: '我负责的',
  done: '已结束',
  all: '全部',
};

const REFRESH_MS = 15_000;

const siteRoute = getRouteApi('/_app/_site');
const pageRoute = getRouteApi('/_app/_site/operations/work-center');

export function WorkCenterWorkspace() {
  const { site, principal } = siteRoute.useRouteContext();
  const search = pageRoute.useSearch();
  const navigate = useNavigate({ from: '/operations/work-center' });
  const queryClient = useQueryClient();
  const [creating, setCreating] = useState(false);
  const view = search.view ?? 'open';
  const myId = principal.principalId;
  const canCreate = principal.authorization.capabilities.includes('work-order.create');

  const statuses = view === 'done' ? ['COMPLETED', 'CANCELLED'] as const : view === 'all' ? [] : OPEN_STATUSES;
  const assigneeId = view === 'mine' ? myId : undefined;
  const open = useQuery(openWorkOrdersQuery(site.id));
  const list = useQuery({
    queryKey: workOrderKeys.view(site.id, statuses, assigneeId),
    queryFn: ({ signal }) => listView(site.id, statuses, assigneeId, signal),
    refetchInterval: REFRESH_MS,
  });

  const workOrders = useMemo(() => sortWorkOrdersForOperators(list.data ?? []), [list.data]);
  const openItems = open.data ?? [];
  const amount = (predicate: (workOrder: WorkOrder) => boolean) => (open.data ? String(openItems.filter(predicate).length) : null);
  const overdue = (workOrder: WorkOrder) => Boolean(workOrder.dueAt) && Date.parse(workOrder.dueAt!) < Date.now();
  const setSearch = (patch: { view?: WorkCenterView; inspect?: string }) =>
    void navigate({ search: (previous) => ({ ...previous, ...patch }) });

  return (
    <Main>
      <PageHeader
        title="工单"
        description={`${site.displayName} · 优先级高的在前，其次最近创建`}
        actions={(
          <div className="flex gap-2">
            <Button variant="outline" size="sm" onClick={() => void queryClient.invalidateQueries({ queryKey: workOrderKeys.all(site.id) })}>
              <RefreshCw aria-hidden="true" data-icon="inline-start" />刷新
            </Button>
            {canCreate ? (
              <Button size="sm" onClick={() => setCreating(true)}>
                <Plus aria-hidden="true" data-icon="inline-start" />新建工单
              </Button>
            ) : null}
          </div>
        )}
      />

      <MetricGrid ariaLabel="工单概况">
        <MetricCard
          label="待处理"
          value={<MetricValue value={amount((workOrder) => workOrder.status === 'OPEN')} unit="张" />}
          lead={`其中 ${amount((workOrder) => workOrder.status === 'OPEN' && !workOrder.assigneeId) ?? '—'} 张未指派`}
          detail="已创建、尚未开始处理"
        />
        <MetricCard
          label="处理中"
          value={<MetricValue value={amount((workOrder) => workOrder.status === 'IN_PROGRESS')} unit="张" />}
          lead={`其中 ${amount((workOrder) => workOrder.status === 'IN_PROGRESS' && overdue(workOrder)) ?? '—'} 张已超过截止时间`}
          detail="已开始、尚未完成"
        />
        <MetricCard
          label="受阻"
          value={<MetricValue value={amount((workOrder) => workOrder.status === 'BLOCKED')} unit="张" />}
          lead="等待备件、许可或其他工单"
          detail="恢复后回到处理中"
        />
        <MetricCard
          label="我负责的"
          value={<MetricValue value={amount((workOrder) => workOrder.assigneeId === myId)} unit="张" />}
          lead="指派给我、尚未结束"
          detail="在「我负责的」中查看"
        />
      </MetricGrid>

      <Tabs value={view} onValueChange={(value) => setSearch({ view: value as WorkCenterView })}>
        <TabsList>
          {(Object.keys(VIEW_LABELS) as WorkCenterView[]).map((key) => (
            <TabsTrigger key={key} value={key}>{VIEW_LABELS[key]}</TabsTrigger>
          ))}
        </TabsList>
      </Tabs>

      {list.isError ? (
        <Alert variant="destructive">
          <AlertTitle>工单暂不可读</AlertTitle>
          <AlertDescription>{list.error.message}</AlertDescription>
        </Alert>
      ) : null}

      <Card className="py-0">
        <CardContent className="p-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-20 pl-4">优先级</TableHead>
                <TableHead>工单</TableHead>
                <TableHead className="w-24">状态</TableHead>
                <TableHead className="w-24">负责人</TableHead>
                <TableHead className="w-28">检查项</TableHead>
                <TableHead className="w-28">截止</TableHead>
                <TableHead className="w-28">创建</TableHead>
                <TableHead className="w-8" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.isPending ? (
                <TableRow><TableCell colSpan={8}><Skeleton className="h-24" /></TableCell></TableRow>
              ) : workOrders.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={8} className="p-0">
                    <TableEmpty>{view === 'open' ? '当前没有进行中的工单' : '没有符合条件的工单'}</TableEmpty>
                  </TableCell>
                </TableRow>
              ) : workOrders.map((workOrder) => (
                <TableRow
                  key={workOrder.workOrderId}
                  className="cursor-pointer"
                  data-state={search.inspect === workOrder.workOrderId ? 'selected' : undefined}
                  onClick={() => setSearch({ inspect: workOrder.workOrderId })}
                >
                  <TableCell className="pl-4"><PriorityBadge priority={workOrder.priority} /></TableCell>
                  <TableCell>
                    <p className="font-medium">{workOrder.title}</p>
                    {workOrder.sourceReferences.some((source) => source.domain === 'ALARM') ? (
                      <p className="text-xs text-muted-foreground">来自告警</p>
                    ) : null}
                  </TableCell>
                  <TableCell><WorkOrderStatusBadge status={workOrder.status} /></TableCell>
                  <TableCell>{workOrder.assigneeId ? personLabel(workOrder.assigneeId, myId) : '未指派'}</TableCell>
                  <TableCell className="tabular-nums">
                    {workOrder.tasks.total > 0 ? `${workOrder.tasks.completed} / ${workOrder.tasks.total}` : '—'}
                  </TableCell>
                  <TableCell className="tabular-nums">{formatTime(workOrder.dueAt, site.timezone)}</TableCell>
                  <TableCell className="tabular-nums">{formatTime(workOrder.createdAt, site.timezone)}</TableCell>
                  <TableCell><ChevronRight className="size-4 text-muted-foreground" aria-hidden="true" /></TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      <WorkOrderInspector workOrderId={search.inspect} onClose={() => setSearch({ inspect: undefined })} />

      {canCreate ? (
        <CreateWorkOrderDialog
          open={creating}
          onOpenChange={setCreating}
          title="新建工单"
          initial={{ title: '', priority: 'MEDIUM', description: '' }}
          sourceReferences={[{ domain: 'MANUAL', resourceId: myId, relationship: 'ORIGIN' }]}
          onCreated={(workOrder) => {
            void queryClient.invalidateQueries({ queryKey: workOrderKeys.all(site.id) });
            setSearch({ inspect: workOrder.workOrderId });
          }}
        />
      ) : null}
    </Main>
  );
}
