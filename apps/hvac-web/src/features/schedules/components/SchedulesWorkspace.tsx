import { useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { getRouteApi, useNavigate } from '@tanstack/react-router';
import { ChevronLeft, ChevronRight, Pencil, Plus } from 'lucide-react';
import { toast } from 'sonner';
import type { OperatingExceptionStatus } from '@/api/generated/platformGateway.gen';
import { PageHeader, PageMeta } from '@/blocks/page-header';
import { StateCard } from '@/blocks/state/state-card';
import { InfoTip } from '@/components/info-tip';
import { Main } from '@/components/layout/Main';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { wallTime } from '@/features/energy-analysis/consumption/model';
import { siteAssetModelQuery } from '@/features/operations/trends/query';
import { CreateWorkOrderDialog } from '@/features/work-orders/CreateWorkOrderDialog';
import { workOrderKeys } from '@/features/work-orders/work-order-queries';
import { useNow } from '@/hooks/use-now';
import { cn } from '@/lib/utils';
import {
  deviationWorkOrdersQuery,
  scheduleErrorMessage,
  schedulePlantAggregateQuery,
  siteOperatingExceptionsListQuery,
  siteOperatingExceptionsWindowQuery,
  siteOperatingScheduleQuery,
  siteScheduleTariffQuery,
} from '../query';
import {
  addCalendarDays,
  buildDeviationWorkOrderDraft,
  detectUnscheduledRunningDeviations,
  extractSchedulePlantMonitorTargets,
  mondayOfWeek,
  resolveEffectiveWeek,
  siteLocalDateString,
  type ScheduleDeviation,
} from '../schedule-model';
import {
  ExceptionRequestDialog,
  type ExceptionRequestInitial,
} from './ExceptionRequestDialog';
import { ScheduleEditorDialog } from './ScheduleEditorDialog';
import { ScheduleExceptionsTab } from './ScheduleExceptionsTab';
import {
  resolveScheduleInspection,
  ScheduleInspectorDrawer,
} from './ScheduleInspectorDrawer';
import { WeeklyScheduleGrid } from './WeeklyScheduleGrid';

const siteRoute = getRouteApi('/_app/_site');
const pageRoute = getRouteApi('/_app/_site/operations/schedules');

export function SchedulesWorkspace() {
  const { site, principal } = siteRoute.useRouteContext();
  const search = pageRoute.useSearch();
  const navigate = useNavigate({ from: '/operations/schedules' });
  const queryClient = useQueryClient();
  const nowMs = useNow(60_000);

  const capabilities = principal.authorization.capabilities;
  const canManageSchedule = capabilities.includes('schedule.manage');
  const canRequestException =
    canManageSchedule || capabilities.includes('schedule.request');
  const canReadTelemetryHistory = capabilities.includes('telemetry.history.read');
  const canListWorkOrders = capabilities.includes('work-order.list');
  const canCreateWorkOrder = capabilities.includes('work-order.create');

  const activeView = search.view === 'exceptions' ? 'exceptions' : 'week';
  const todayDate = useMemo(
    () => siteLocalDateString(new Date(nowMs), site.timezone),
    [nowMs, site.timezone],
  );
  const currentMondayDate = useMemo(() => mondayOfWeek(todayDate), [todayDate]);
  const selectedMondayDate = useMemo(
    () => mondayOfWeek(search.week ?? todayDate),
    [search.week, todayDate],
  );
  const selectedSundayDate = useMemo(
    () => addCalendarDays(selectedMondayDate, 6),
    [selectedMondayDate],
  );
  const currentMinuteOfDay = useMemo(() => {
    const local = wallTime(new Date(nowMs), site.timezone);
    return local.hour * 60 + local.minute;
  }, [nowMs, site.timezone]);

  const [editorOpen, setEditorOpen] = useState(false);
  const [requestOpen, setRequestOpen] = useState(false);
  const [requestInitial, setRequestInitial] = useState<ExceptionRequestInitial | null>(null);
  const [workOrderDeviation, setWorkOrderDeviation] = useState<ScheduleDeviation | null>(null);

  const scheduleQuery = useQuery(siteOperatingScheduleQuery(site.id));
  const weekExceptionsQuery = useQuery(
    siteOperatingExceptionsWindowQuery(site.id, selectedMondayDate, selectedSundayDate),
  );
  const pendingExceptionsQuery = useQuery(
    siteOperatingExceptionsListQuery(site.id, 'PENDING_APPROVAL'),
  );
  const tabExceptionsQuery = useQuery({
    ...siteOperatingExceptionsListQuery(site.id, search.status),
    enabled: activeView === 'exceptions',
  });
  const tariffQuery = useQuery(siteScheduleTariffQuery(site.id));
  const assetModelQuery = useQuery(siteAssetModelQuery(site.id));

  const monitorTargets = useMemo(
    () =>
      assetModelQuery.data ? extractSchedulePlantMonitorTargets(assetModelQuery.data) : [],
    [assetModelQuery.data],
  );

  const effectiveWeek = useMemo(() => {
    if (!scheduleQuery.data) return null;
    return resolveEffectiveWeek({
      schedule: scheduleQuery.data,
      exceptions: weekExceptionsQuery.data ?? [],
      anchorDate: selectedMondayDate,
      timezone: site.timezone,
    });
  }, [scheduleQuery.data, weekExceptionsQuery.data, selectedMondayDate, site.timezone]);

  const historyToMs = effectiveWeek ? Math.min(effectiveWeek.endMs, nowMs) : 0;
  const historyFromMs = effectiveWeek ? effectiveWeek.startMs : 0;
  const hasScheduleConfigured = Boolean(
    scheduleQuery.data &&
      (scheduleQuery.data.revision > 0 ||
        (weekExceptionsQuery.data ?? []).some((item) => item.status === 'APPROVED')),
  );

  const plantAggregateQuery = useQuery(
    schedulePlantAggregateQuery({
      siteId: site.id,
      timezone: site.timezone,
      csrfToken: principal.session.csrfToken,
      targets: monitorTargets,
      fromMs: historyFromMs,
      toMs: historyToMs,
      enabled:
        canReadTelemetryHistory &&
        effectiveWeek !== null &&
        hasScheduleConfigured &&
        !weekExceptionsQuery.isError &&
        historyToMs > historyFromMs,
    }),
  );

  const deviations = useMemo(() => {
    if (
      !effectiveWeek ||
      !plantAggregateQuery.data ||
      plantAggregateQuery.isError ||
      weekExceptionsQuery.isError ||
      !hasScheduleConfigured
    ) {
      return [];
    }
    return detectUnscheduledRunningDeviations({
      siteId: site.id,
      week: effectiveWeek,
      targets: monitorTargets,
      bucketsByDevice: plantAggregateQuery.data,
      nowMs,
    });
  }, [
    effectiveWeek,
    plantAggregateQuery.data,
    plantAggregateQuery.isError,
    weekExceptionsQuery.isError,
    hasScheduleConfigured,
    site.id,
    monitorTargets,
    nowMs,
  ]);

  const inspectionTarget = useMemo(
    () =>
      effectiveWeek
        ? resolveScheduleInspection(search.inspect, effectiveWeek, deviations)
        : null,
    [effectiveWeek, search.inspect, deviations],
  );

  const inspectedDeviation =
    inspectionTarget?.kind === 'deviation' ? inspectionTarget.deviation : null;
  const externalWorkOrdersQuery = useQuery(
    deviationWorkOrdersQuery(site.id, inspectedDeviation, canListWorkOrders),
  );

  const workOrderDraft = useMemo(
    () =>
      workOrderDeviation ? buildDeviationWorkOrderDraft(site.id, workOrderDeviation) : null,
    [site.id, workOrderDeviation],
  );

  const pendingCount = pendingExceptionsQuery.data?.length ?? 0;

  const setWeekAnchor = (nextMonday: string) => {
    void navigate({
      search: (prev) => ({
        ...prev,
        week: nextMonday === currentMondayDate ? undefined : nextMonday,
        inspect: undefined,
      }),
    });
  };

  if (scheduleQuery.isError) {
    return (
      <Main>
        <PageHeader title="运行计划" />
        <StateCard
          tone="danger"
          title={<h2 className="text-base font-semibold">无法读取站点运行计划</h2>}
          description={scheduleErrorMessage(scheduleQuery.error, '请检查网络连接或稍后重试', 'schedule')}
        >
          <Button variant="outline" size="sm" onClick={() => void scheduleQuery.refetch()}>
            重试
          </Button>
        </StateCard>
      </Main>
    );
  }

  const schedule = scheduleQuery.data;

  return (
    <Main className="gap-4">
      <PageHeader
        title="运行计划"
        meta={
          <PageMeta>
            {schedule?.name ?? '标准供冷周计划'} · {site.timezone}
          </PageMeta>
        }
        description={
          <span className="inline-flex flex-wrap items-center gap-x-2 gap-y-1 tabular-nums">
            {schedule ? (
              <span className="inline-flex items-center gap-1">
                预冷提前 {schedule.preCoolLeadMinutes} 分钟 / 停机滞后 {schedule.coastDownLagMinutes} 分钟
                <InfoTip>
                  供冷时段仅代表站点预期运行窗口，目标冷冻水供水温度仅作冷站值班交接参考，平台不会据此自动下发设备启停或设定值指令；计划外开机核查按 15 分钟历史遥测桶判定，并自动排除预冷提前窗与停机余冷滞后窗。
                </InfoTip>
              </span>
            ) : null}
            {!canManageSchedule && !canRequestException ? (
              <span className="text-subtle-foreground">· 只读查看</span>
            ) : null}
          </span>
        }
        actions={
          <div className="flex flex-wrap items-center gap-2">
            {activeView === 'week' ? (
              <div className="inline-flex items-center rounded-md border border-border bg-card p-0.5">
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  aria-label="上一周"
                  onClick={() => setWeekAnchor(addCalendarDays(selectedMondayDate, -7))}
                >
                  <ChevronLeft className="size-4" />
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  onClick={() => setWeekAnchor(currentMondayDate)}
                  className={cn(
                    'px-2.5 text-xs tabular-nums',
                    selectedMondayDate === currentMondayDate && 'bg-muted font-medium text-foreground',
                  )}
                >
                  {selectedMondayDate === currentMondayDate
                    ? `本周 · ${selectedMondayDate.slice(5)}–${selectedSundayDate.slice(5)}`
                    : `${selectedMondayDate} 至 ${selectedSundayDate}`}
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  aria-label="下一周"
                  onClick={() => setWeekAnchor(addCalendarDays(selectedMondayDate, 7))}
                >
                  <ChevronRight className="size-4" />
                </Button>
              </div>
            ) : null}

            {canRequestException ? (
              <Button
                variant={activeView === 'exceptions' ? 'default' : 'outline'}
                size="sm"
                onClick={() => {
                  setRequestInitial(null);
                  setRequestOpen(true);
                }}
              >
                <Plus data-icon="inline-start" />
                申请运行例外
              </Button>
            ) : null}

            {canManageSchedule && schedule && (hasScheduleConfigured || activeView === 'exceptions') ? (
              <Button
                variant={activeView === 'week' ? 'default' : 'outline'}
                size="sm"
                onClick={() => setEditorOpen(true)}
              >
                <Pencil data-icon="inline-start" />
                {schedule.revision === 0 ? '配置默认周计划' : '编辑周计划'}
              </Button>
            ) : null}
          </div>
        }
      />

      <Tabs
        value={activeView}
        onValueChange={(next) => {
          void navigate({
            search: (prev) => ({
              ...prev,
              view: next === 'exceptions' ? 'exceptions' : undefined,
              inspect: undefined,
            }),
          });
        }}
      >
        <div className="border-b border-divider">
          <TabsList variant="line">
            <TabsTrigger value="week">周运行计划</TabsTrigger>
            <TabsTrigger value="exceptions">
              运行例外
              {pendingCount > 0 ? (
                <span className="ml-1 inline-flex min-w-4 items-center justify-center rounded-full bg-warning-soft px-1.5 py-0.2 text-[11px] font-semibold text-warning-text tabular-nums">
                  {pendingCount}
                </span>
              ) : null}
            </TabsTrigger>
          </TabsList>
        </div>

        <TabsContent value="week" className="mt-3 space-y-3">
          {weekExceptionsQuery.isError ? (
            <Alert variant="destructive">
              <AlertTitle>无法读取本周运行例外覆盖记录</AlertTitle>
              <AlertDescription className="flex flex-wrap items-center justify-between gap-2">
                <span>
                  {scheduleErrorMessage(
                    weekExceptionsQuery.error,
                    '由于运行例外状态未知，已暂停本周计划外开机核查以避免误报。',
                    'exception',
                  )}
                </span>
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  onClick={() => void weekExceptionsQuery.refetch()}
                >
                  重试
                </Button>
              </AlertDescription>
            </Alert>
          ) : null}

          {!schedule || !effectiveWeek ? (
            <Skeleton className="h-[460px] w-full rounded-xl" />
          ) : (
            <WeeklyScheduleGrid
              schedule={schedule}
              week={effectiveWeek}
              tariff={tariffQuery.data ?? null}
              deviations={deviations}
              inspectedId={search.inspect}
              todayDate={todayDate}
              currentMinuteOfDay={currentMinuteOfDay}
              hasEvaluatedHistory={Boolean(
                plantAggregateQuery.data &&
                  !plantAggregateQuery.isError &&
                  !weekExceptionsQuery.isError &&
                  historyToMs > historyFromMs,
              )}
              historyError={plantAggregateQuery.isError || assetModelQuery.isError}
              canManageSchedule={canManageSchedule}
              onInspect={(id) => {
                void navigate({
                  search: (prev) => ({
                    ...prev,
                    inspect: prev.inspect === id ? undefined : id,
                  }),
                });
              }}
              onOpenEditor={() => setEditorOpen(true)}
              onRetryHistory={() => {
                if (assetModelQuery.isError) void assetModelQuery.refetch();
                if (plantAggregateQuery.isError) void plantAggregateQuery.refetch();
              }}
            />
          )}
        </TabsContent>

        <TabsContent value="exceptions" className="mt-3">
          <ScheduleExceptionsTab
            siteId={site.id}
            timezone={site.timezone}
            csrfToken={principal.session.csrfToken}
            currentPrincipalId={principal.principalId}
            items={tabExceptionsQuery.data ?? []}
            isLoading={tabExceptionsQuery.isLoading}
            isError={tabExceptionsQuery.isError}
            error={tabExceptionsQuery.error}
            statusFilter={search.status}
            page={search.page ?? 1}
            pageSize={search.pageSize ?? 20}
            canManageSchedule={canManageSchedule}
            onRetry={() => void tabExceptionsQuery.refetch()}
            onStatusFilterChange={(next: OperatingExceptionStatus | undefined) => {
              void navigate({
                search: (prev) => ({
                  ...prev,
                  status: next,
                  page: undefined,
                }),
              });
            }}
            onPageChange={(nextPage) => {
              void navigate({
                search: (prev) => ({
                  ...prev,
                  page: nextPage <= 1 ? undefined : nextPage,
                }),
              });
            }}
          />
        </TabsContent>
      </Tabs>

      {effectiveWeek ? (
        <ScheduleInspectorDrawer
          siteId={site.id}
          timezone={site.timezone}
          target={inspectionTarget}
          week={effectiveWeek}
          tariff={tariffQuery.data ?? null}
          monitorTargets={monitorTargets}
          externalWorkOrders={externalWorkOrdersQuery.data?.items ?? []}
          canManageSchedule={canManageSchedule}
          canRequestException={canRequestException}
          canCreateWorkOrder={canCreateWorkOrder}
          onClose={() => {
            void navigate({
              search: (prev) => ({ ...prev, inspect: undefined }),
            });
          }}
          onOpenEditor={() => setEditorOpen(true)}
          onOpenExceptionsTab={() => {
            void navigate({
              search: (prev) => ({
                ...prev,
                view: 'exceptions',
                inspect: undefined,
              }),
            });
          }}
          onCreateDeviationWorkOrder={(deviation) => setWorkOrderDeviation(deviation)}
          onRequestOvertimeForDate={(date) => {
            setRequestInitial({
              kind: 'OVERTIME_COOLING',
              startDate: date,
              endDate: date,
            });
            setRequestOpen(true);
          }}
        />
      ) : null}

      {schedule && canManageSchedule ? (
        <ScheduleEditorDialog
          open={editorOpen}
          onOpenChange={setEditorOpen}
          siteId={site.id}
          csrfToken={principal.session.csrfToken}
          schedule={schedule}
          onReloadLatest={async () => {
            const refreshed = await scheduleQuery.refetch();
            return refreshed.data;
          }}
        />
      ) : null}

      {canRequestException ? (
        <ExceptionRequestDialog
          open={requestOpen}
          onOpenChange={setRequestOpen}
          siteId={site.id}
          csrfToken={principal.session.csrfToken}
          defaultDate={todayDate}
          initial={requestInitial}
          canManageSchedule={canManageSchedule}
        />
      ) : null}

      {workOrderDeviation && workOrderDraft ? (
        <CreateWorkOrderDialog
          open={workOrderDeviation !== null}
          onOpenChange={(open) => {
            if (!open) setWorkOrderDeviation(null);
          }}
          title="要为此段计划外开机创建核查工单吗？"
          initial={workOrderDraft.initial}
          sourceReferences={workOrderDraft.sourceReferences}
          onCreated={() => {
            void queryClient.invalidateQueries({ queryKey: workOrderKeys.all(site.id) });
            toast.success('已创建计划外开机核查工单');
          }}
        />
      ) : null}
    </Main>
  );
}
