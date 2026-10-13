import { useMemo, useState } from 'react';
import { PageHeader } from '@/blocks/page-header';
import { Main } from '@/components/layout/Main';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { getRouteApi, useNavigate } from '@tanstack/react-router';
import { Bell, CalendarClock, Plus, RefreshCw, Server, User } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import type { WorkOrder, WorkOrderStatus } from '@/api/work-orders';
import { useSiteEquipment } from '@/features/assets/use-device-names';
import { formatCompactTime, personLabel } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import { CreateWorkOrderDialog } from '../CreateWorkOrderDialog';
import { OPEN_STATUSES, PriorityBadge, sortWorkOrdersForOperators, STATUS_LABELS } from '../work-order-presentation';
import { listView, openWorkOrdersQuery, workOrderKeys } from '../work-order-queries';
import { StatusShape } from './StatusShape';
import { WorkOrderInspector } from './WorkOrderInspector';

export type WorkCenterView = 'open' | 'mine' | 'done' | 'all';

const VIEW_LABELS: Readonly<Record<WorkCenterView, string>> = {
  open: '进行中',
  mine: '我负责的',
  done: '已结束',
  all: '全部',
};

const COLUMNS: Readonly<Record<WorkCenterView, readonly WorkOrderStatus[]>> = {
  open: OPEN_STATUSES,
  mine: OPEN_STATUSES,
  done: ['COMPLETED', 'CANCELLED'],
  all: [...OPEN_STATUSES, 'COMPLETED', 'CANCELLED'],
};

const REFRESH_MS = 15_000;

const siteRoute = getRouteApi('/_app/_site');
const pageRoute = getRouteApi('/_app/_site/operations/work-center');

function isOverdue(workOrder: WorkOrder, now: number): boolean {
  return Boolean(workOrder.dueAt) && Date.parse(workOrder.dueAt!) < now && OPEN_STATUSES.includes(workOrder.status);
}

function WorkOrderCard({ workOrder, selected, myId, timeZone, sourceLabel, onOpen }: {
  readonly workOrder: WorkOrder;
  readonly selected: boolean;
  readonly myId: string;
  readonly timeZone: string;
  readonly sourceLabel: { readonly icon: typeof Bell; readonly text: string };
  readonly onOpen: () => void;
}) {
  const now = Date.now();
  const SourceIcon = sourceLabel.icon;
  const done = workOrder.status === 'COMPLETED' || workOrder.status === 'CANCELLED';
  return (
    <button
      type="button"
      onClick={onOpen}
      aria-current={selected ? 'true' : undefined}
      className={cn(
        'flex w-full flex-col gap-2 rounded-lg border bg-card p-3 text-left shadow-xs outline-none transition-colors duration-100 hover:border-input-hover focus-visible:ring-3 focus-visible:ring-ring-soft',
        selected && 'border-primary ring-1 ring-primary hover:border-primary',
      )}
    >
      <span className="flex items-center gap-1.5">
        <PriorityBadge priority={workOrder.priority} />
        {isOverdue(workOrder, now) ? <span className="rounded-md bg-destructive-soft px-1.5 text-[11px] leading-5 font-medium text-destructive-text">已超期</span> : null}
      </span>
      <span className={cn('text-sm leading-[19px] font-semibold', done && 'text-muted-foreground')}>{workOrder.title}</span>
      <span className="flex items-center gap-1.5 truncate text-xs text-muted-foreground">
        <SourceIcon className="size-3.5 shrink-0" aria-hidden="true" />
        {sourceLabel.text}
      </span>
      {workOrder.tasks.total > 0 ? (
        <span className="flex items-center gap-2 text-xs text-muted-foreground">
          检查项
          <span className="h-1 flex-1 overflow-hidden rounded-full bg-muted">
            <span className="block h-full rounded-full bg-primary" style={{ width: `${(workOrder.tasks.completed / workOrder.tasks.total) * 100}%` }} />
          </span>
          <span className="tabular-nums">{workOrder.tasks.completed} / {workOrder.tasks.total}</span>
        </span>
      ) : null}
      <span className="flex items-center gap-2 border-t border-divider pt-2 text-xs text-muted-foreground">
        <span className={cn('flex size-5 items-center justify-center rounded-full text-[10px] font-semibold', workOrder.assigneeId ? 'bg-primary-soft text-primary' : 'border border-dashed border-input-hover')}>
          {workOrder.assigneeId ? personLabel(workOrder.assigneeId, myId).slice(0, 1) : <User className="size-3" aria-hidden="true" />}
        </span>
        {workOrder.assigneeId ? personLabel(workOrder.assigneeId, myId) : '未指派'}
        {workOrder.dueAt ? (
          <span className={cn('ml-auto flex items-center gap-1 tabular-nums', isOverdue(workOrder, now) && 'text-destructive-text')}>
            <CalendarClock className="size-3.5" aria-hidden="true" />
            {formatCompactTime(workOrder.dueAt, timeZone)}
          </span>
        ) : null}
      </span>
    </button>
  );
}

export function WorkCenterWorkspace() {
  const { site, principal } = siteRoute.useRouteContext();
  const search = pageRoute.useSearch();
  const navigate = useNavigate({ from: '/operations/work-center' });
  const queryClient = useQueryClient();
  const [creating, setCreating] = useState(false);
  const equipment = useSiteEquipment(site.id);
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
  const now = Date.now();
  const setSearch = (patch: { view?: WorkCenterView; inspect?: string }) =>
    void navigate({ search: (previous) => ({ ...previous, ...patch }) });

  const counts: Partial<Record<WorkCenterView, number>> = open.data ? {
    open: openItems.length,
    mine: openItems.filter((workOrder) => workOrder.assigneeId === myId).length,
  } : {};

  const sourceOf = (workOrder: WorkOrder) => {
    const alarm = workOrder.sourceReferences.find((source) => source.domain === 'ALARM');
    if (alarm) return { icon: Bell, text: '来自告警' };
    const asset = workOrder.sourceReferences.find((source) => source.domain === 'ASSET');
    if (asset) return { icon: Server, text: `来自设备 · ${equipment.deviceByAsset.get(asset.resourceId)?.name ?? '—'}` };
    return { icon: User, text: '人工创建' };
  };

  // What each column asks of the operator, said in its header.
  const hint = (status: WorkOrderStatus, items: readonly WorkOrder[]) => {
    if (status === 'OPEN') {
      const unassigned = items.filter((workOrder) => !workOrder.assigneeId).length;
      return unassigned > 0 ? `${unassigned} 张未指派` : '';
    }
    if (status === 'IN_PROGRESS') {
      const overdue = items.filter((workOrder) => isOverdue(workOrder, now)).length;
      return overdue > 0 ? `${overdue} 张已超期` : '';
    }
    return '';
  };

  return (
    <Main fixed>
      <PageHeader
        title="工单"
        description={site.displayName}
        actions={(
          <>
            <Button variant="outline" onClick={() => void queryClient.invalidateQueries({ queryKey: workOrderKeys.all(site.id) })}>
              <RefreshCw aria-hidden="true" data-icon="inline-start" />刷新
            </Button>
            {canCreate ? (
              <Button onClick={() => setCreating(true)}>
                <Plus aria-hidden="true" data-icon="inline-start" />新建工单
              </Button>
            ) : null}
          </>
        )}
      />

      <Tabs value={view} onValueChange={(value) => setSearch({ view: value as WorkCenterView })}>
        <TabsList variant="line" className="h-9 w-full justify-start gap-5 rounded-none border-b p-0">
          {(Object.keys(VIEW_LABELS) as WorkCenterView[]).map((key) => (
            <TabsTrigger key={key} value={key} className="h-9 flex-none gap-1.5 px-0 after:bottom-[-1px]!">
              {VIEW_LABELS[key]}
              {counts[key] !== undefined ? <span className="font-mono text-[11px] text-subtle-foreground">{counts[key]}</span> : null}
            </TabsTrigger>
          ))}
        </TabsList>
      </Tabs>

      {list.isError ? (
        <p className="rounded-lg border border-destructive-soft bg-destructive-soft/40 px-4 py-3 text-sm text-destructive-text">工单暂时读不到：{list.error.message}</p>
      ) : null}

      <section
        aria-label="工单看板"
        className="grid min-h-[420px] flex-1 gap-3.5"
        style={{ gridTemplateColumns: `repeat(${COLUMNS[view].length}, minmax(0, 1fr))` }}
      >
        {COLUMNS[view].map((status) => {
          const items = workOrders.filter((workOrder) => workOrder.status === status);
          return (
            <div key={status} role="list" aria-label={STATUS_LABELS[status]} className="flex min-h-0 flex-col gap-2 rounded-xl border border-divider bg-muted p-2">
              <div className="flex h-[30px] items-center gap-2 px-1.5 text-sm font-semibold">
                <StatusShape status={status} />
                {STATUS_LABELS[status]}
                <span className="font-mono text-xs font-medium text-subtle-foreground">{list.data ? items.length : ''}</span>
                <span className="ml-auto text-xs font-normal text-muted-foreground">{hint(status, items)}</span>
              </div>
              <div className="flex min-h-0 flex-1 flex-col gap-2 overflow-y-auto">
                {list.isPending ? <><Skeleton className="h-28" /><Skeleton className="h-28" /></> : items.length === 0 ? (
                  <p className="px-2 py-6 text-center text-xs text-muted-foreground">没有{STATUS_LABELS[status]}的工单</p>
                ) : items.map((workOrder) => (
                  <div role="listitem" key={workOrder.workOrderId}>
                    <WorkOrderCard
                      workOrder={workOrder}
                      selected={search.inspect === workOrder.workOrderId}
                      myId={myId}
                      timeZone={site.timezone}
                      sourceLabel={sourceOf(workOrder)}
                      onOpen={() => setSearch({ inspect: workOrder.workOrderId })}
                    />
                  </div>
                ))}
              </div>
            </div>
          );
        })}
      </section>

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
