import { useEffect, useMemo, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { getRouteApi, useNavigate } from '@tanstack/react-router';
import { ChevronLeft, ChevronRight, Loader2, Pencil, Plus, RotateCcw } from 'lucide-react';
import type {
  UtilityBill,
  UtilityBillMeterScope,
  UtilityBillStatus,
} from '@/api/generated/platformGateway.gen';
import { PageHeader, PageMeta } from '@/blocks/page-header';
import { StateCard } from '@/blocks/state/state-card';
import { Main } from '@/components/layout/Main';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { useNow } from '@/hooks/use-now';
import { formatTime } from '@/lib/operator-format';
import {
  ACCOUNTING_RECALC_GRANULARITY_SHORT_LABEL,
  billingMonthOptions,
  currentSiteBillingMonth,
  describeBillingApiError,
  formatBillingMonthLabel,
  formatRecalcWindowLabel,
  normalizeBillingMonth,
  shiftBillingMonth,
  summarizeMonthlyBudgetProgress,
} from '../billing-model';
import {
  billingKeys,
  siteAccountingRecalcJobsQuery,
  siteBillingMonthElectricityQuery,
  siteMonthlyBudgetQuery,
  siteUtilityBillsQuery,
  type AccountingRecalcJob,
} from '../query';
import { BillReconciliationSheet } from './BillReconciliationSheet';
import { BudgetEditorDialog } from './BudgetEditorDialog';
import { BudgetOverviewTab } from './BudgetOverviewTab';
import { RecalculateDialog } from './RecalculateDialog';
import { UtilityBillDialog } from './UtilityBillDialog';
import { UtilityBillsTab } from './UtilityBillsTab';

const siteRoute = getRouteApi('/_app/_site');
const pageRoute = getRouteApi('/_app/_site/energy-analysis/billing');

function AccountingRecalcStatusStrip({
  job,
  timeZone,
}: {
  readonly job: AccountingRecalcJob;
  readonly timeZone: string;
}) {
  const granularityText = ACCOUNTING_RECALC_GRANULARITY_SHORT_LABEL[job.granularity];
  const windowText = formatRecalcWindowLabel(job.windowStart, job.windowEnd, timeZone);

  if (job.status === 'PENDING') {
    return (
      <div
        role="status"
        aria-live="polite"
        data-testid="accounting-recalc-status-strip"
        className="flex flex-wrap items-center gap-2 rounded-lg border border-border bg-card px-3.5 py-2 text-xs shadow-xs"
      >
        <Badge variant="warning">■ 排队重算中</Badge>
        <span className="font-medium text-foreground">
          {granularityText} · {windowText}
        </span>
        <span className="text-muted-foreground">
          等待后台执行{job.requestedByDisplay ? ` · 由 ${job.requestedByDisplay} 发起` : ''}
        </span>
      </div>
    );
  }

  if (job.status === 'RUNNING') {
    return (
      <div
        role="status"
        aria-live="polite"
        data-testid="accounting-recalc-status-strip"
        className="flex flex-wrap items-center gap-2 rounded-lg border border-primary-line bg-card px-3.5 py-2 text-xs shadow-xs"
      >
        <Badge variant="default">
          <Loader2 className="motion-safe:animate-spin" aria-hidden="true" />
          ● 正在重算
        </Badge>
        <span className="font-medium text-foreground">
          {granularityText} · {windowText}
        </span>
        <span className="text-muted-foreground">正在重新计算分时电费与能耗，完成后将自动刷新</span>
      </div>
    );
  }

  if (job.status === 'FAILED') {
    const failureReason = job.lastError.trim() || '请稍后重试或缩小重算日期范围';
    return (
      <div
        role="status"
        aria-live="polite"
        data-testid="accounting-recalc-status-strip"
        className="flex flex-wrap items-center gap-2 rounded-lg border border-destructive/35 bg-card px-3.5 py-2 text-xs shadow-xs"
      >
        <Badge variant="destructive">◆ 最近重算失败</Badge>
        <span className="font-medium text-foreground">
          {granularityText} · {windowText}
        </span>
        <span className="text-destructive-text">{failureReason}</span>
      </div>
    );
  }

  return (
    <div
      role="status"
      aria-live="polite"
      data-testid="accounting-recalc-status-strip"
      className="flex flex-wrap items-center gap-2 rounded-lg border border-border bg-card px-3.5 py-2 text-xs shadow-xs"
    >
      <Badge variant="success">○ 最近重算完成</Badge>
      <span className="font-medium text-foreground">
        {granularityText} · {windowText}
      </span>
      <span className="text-muted-foreground tabular-nums">
        完成于 {formatTime(job.finishedAt ?? job.updatedAt, timeZone)}
      </span>
    </div>
  );
}

export function BillingWorkspace() {
  const { site, principal } = siteRoute.useRouteContext();
  const queryClient = useQueryClient();
  const search = pageRoute.useSearch();
  const navigate = useNavigate({ from: '/energy-analysis/billing' });
  const nowMs = useNow(60_000);
  const now = useMemo(() => new Date(nowMs), [nowMs]);

  const capabilities = principal.authorization.capabilities;
  const canManageBilling = capabilities.includes('billing.manage');
  const canRecalculateAccounting =
    capabilities.includes('billing.manage') || capabilities.includes('savings.manage');

  const activeView = search.view === 'bills' ? 'bills' : 'overview';
  const defaultMonth = useMemo(
    () => currentSiteBillingMonth(now, site.timezone),
    [now, site.timezone],
  );
  const selectedMonth = useMemo(
    () => normalizeBillingMonth(search.month, now, site.timezone),
    [search.month, now, site.timezone],
  );
  const monthOptions = useMemo(() => {
    const base = billingMonthOptions(now, site.timezone);
    return base.includes(selectedMonth)
      ? base
      : [selectedMonth, ...base].sort((a, b) => b.localeCompare(a));
  }, [now, site.timezone, selectedMonth]);

  const [budgetDialogOpen, setBudgetDialogOpen] = useState(false);
  const [billDialogOpen, setBillDialogOpen] = useState(false);
  const [recalcDialogOpen, setRecalcDialogOpen] = useState(false);
  const [editingBill, setEditingBill] = useState<UtilityBill | null>(null);

  const budgetQuery = useQuery(siteMonthlyBudgetQuery(site.id, selectedMonth));
  const monthBillsQuery = useQuery(
    siteUtilityBillsQuery(site.id, { billingMonth: selectedMonth }),
  );
  const allBillsQuery = useQuery(
    siteUtilityBillsQuery(site.id, {
      meterScope: search.scope,
      status: search.status,
    }),
  );
  const electricityQuery = useQuery(
    siteBillingMonthElectricityQuery({
      tenantId: site.tenantId,
      siteId: site.id,
      timezone: site.timezone,
      billingMonth: selectedMonth,
      now,
      csrfToken: principal.session.csrfToken,
      enabled: true,
    }),
  );
  const recalcJobsQuery = useQuery(siteAccountingRecalcJobsQuery(site.id));
  const latestRecalcJob = recalcJobsQuery.data?.[0] ?? null;

  const prevRecalcJobRef = useRef<{
    readonly id: string;
    readonly status: AccountingRecalcJob['status'];
  } | null>(null);

  useEffect(() => {
    const prev = prevRecalcJobRef.current;
    if (latestRecalcJob) {
      if (
        prev &&
        prev.id === latestRecalcJob.id &&
        (prev.status === 'PENDING' || prev.status === 'RUNNING') &&
        latestRecalcJob.status === 'SUCCEEDED'
      ) {
        void queryClient.invalidateQueries({ queryKey: billingKeys.monthElectricityRoot(site.id) });
        void queryClient.invalidateQueries({
          queryKey: billingKeys.readingWindowElectricityRoot(site.id),
        });
        void queryClient.invalidateQueries({ queryKey: ['energy-series', site.id] });
      }
      prevRecalcJobRef.current = { id: latestRecalcJob.id, status: latestRecalcJob.status };
    } else {
      prevRecalcJobRef.current = null;
    }
  }, [latestRecalcJob, queryClient, site.id]);

  const budgetSummary = useMemo(
    () =>
      summarizeMonthlyBudgetProgress({
        electricity: electricityQuery.data ?? null,
        billingMonth: selectedMonth,
        budget: budgetQuery.data ?? null,
        now,
        timeZone: site.timezone,
      }),
    [electricityQuery.data, selectedMonth, budgetQuery.data, now, site.timezone],
  );

  const inspectedBill = useMemo(() => {
    if (!search.inspect) return null;
    const fromAll = (allBillsQuery.data ?? []).find((item) => item.id === search.inspect);
    if (fromAll) return fromAll;
    return (monthBillsQuery.data ?? []).find((item) => item.id === search.inspect) ?? null;
  }, [search.inspect, allBillsQuery.data, monthBillsQuery.data]);

  const setBillingMonth = (nextMonth: string) => {
    void navigate({
      search: (prev) => ({
        ...prev,
        month: nextMonth === defaultMonth ? undefined : nextMonth,
        inspect: undefined,
      }),
    });
  };

  const openInspectBill = (billId: string) => {
    void navigate({
      search: (prev) => ({
        ...prev,
        inspect: billId,
      }),
    });
  };

  if (budgetQuery.isError && monthBillsQuery.isError) {
    return (
      <Main>
        <PageHeader title="电费账单" />
        <StateCard
          tone="danger"
          title={<h2 className="text-base font-semibold">无法读取站点电费账单数据</h2>}
          description={describeBillingApiError(
            budgetQuery.error,
            '请检查网络连接或稍后重试',
          )}
        >
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              void budgetQuery.refetch();
              void monthBillsQuery.refetch();
            }}
          >
            重试
          </Button>
        </StateCard>
      </Main>
    );
  }

  return (
    <Main className="gap-4">
      <PageHeader
        title="电费账单"
        meta={
          <PageMeta>
            {site.displayName} · {formatBillingMonthLabel(selectedMonth)}
          </PageMeta>
        }
        description={
          <span className="inline-flex flex-wrap items-center gap-x-2 gap-y-1">
            <span>跟踪冷站月度电费预算进度，并按供电局抄表周期核对两部制结算电费单</span>
            {!canManageBilling ? (
              <span className="text-subtle-foreground">· 只读查看</span>
            ) : null}
          </span>
        }
        actions={
          <div className="flex flex-wrap items-center gap-2">
            <div className="flex items-center gap-1">
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                aria-label="上一账期"
                onClick={() => setBillingMonth(shiftBillingMonth(selectedMonth, -1))}
              >
                <ChevronLeft />
              </Button>
              <Select value={selectedMonth} onValueChange={setBillingMonth}>
                <SelectTrigger className="h-8 w-36" aria-label="预算账期">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {monthOptions.map((option) => (
                    <SelectItem key={option} value={option}>
                      {formatBillingMonthLabel(option)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                aria-label="下一账期"
                onClick={() => setBillingMonth(shiftBillingMonth(selectedMonth, 1))}
              >
                <ChevronRight />
              </Button>
            </div>

            {canRecalculateAccounting ? (
              <Button
                type="button"
                size="sm"
                variant="outline"
                onClick={() => setRecalcDialogOpen(true)}
              >
                <RotateCcw data-icon="inline-start" />
                重算时段数据
              </Button>
            ) : null}

            {canManageBilling ? (
              <>
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  onClick={() => setBudgetDialogOpen(true)}
                >
                  <Pencil data-icon="inline-start" />
                  {budgetQuery.data ? '调整月度预算' : '设置月度预算'}
                </Button>
                <Button
                  type="button"
                  size="sm"
                  onClick={() => {
                    setEditingBill(null);
                    setBillDialogOpen(true);
                  }}
                >
                  <Plus data-icon="inline-start" />
                  录入结算电费单
                </Button>
              </>
            ) : null}
          </div>
        }
      />

      {!recalcJobsQuery.isPending && !recalcJobsQuery.isError && latestRecalcJob ? (
        <AccountingRecalcStatusStrip job={latestRecalcJob} timeZone={site.timezone} />
      ) : null}

      <Tabs
        value={activeView}
        onValueChange={(value) => {
          void navigate({
            search: (prev) => ({
              ...prev,
              view: value === 'bills' ? 'bills' : undefined,
            }),
          });
        }}
        className="flex flex-col gap-4"
      >
        <TabsList>
          <TabsTrigger value="overview">预算进度</TabsTrigger>
          <TabsTrigger value="bills">结算电费单核对</TabsTrigger>
        </TabsList>

        <TabsContent value="overview" className="mt-0">
          <BudgetOverviewTab
            siteId={site.id}
            billingMonth={selectedMonth}
            summary={budgetSummary}
            budget={budgetQuery.data ?? null}
            monthBills={monthBillsQuery.data ?? []}
            isElectricityPending={electricityQuery.isPending}
            isElectricityError={electricityQuery.isError}
            electricityErrorMessage={
              electricityQuery.isError
                ? describeBillingApiError(
                    electricityQuery.error,
                    '读取冷站估算电费序列失败，请稍后重试',
                  )
                : null
            }
            canManageBilling={canManageBilling}
            onOpenBudgetDialog={() => setBudgetDialogOpen(true)}
            onOpenCreateBillDialog={() => {
              setEditingBill(null);
              setBillDialogOpen(true);
            }}
            onInspectBill={openInspectBill}
          />
        </TabsContent>

        <TabsContent value="bills" className="mt-0">
          <UtilityBillsTab
            items={allBillsQuery.data ?? []}
            isLoading={allBillsQuery.isPending}
            isError={allBillsQuery.isError}
            error={allBillsQuery.error}
            scopeFilter={search.scope}
            statusFilter={search.status}
            canManageBilling={canManageBilling}
            onRetry={() => void allBillsQuery.refetch()}
            onScopeFilterChange={(next: UtilityBillMeterScope | undefined) => {
              void navigate({
                search: (prev) => ({
                  ...prev,
                  scope: next,
                }),
              });
            }}
            onStatusFilterChange={(next: UtilityBillStatus | undefined) => {
              void navigate({
                search: (prev) => ({
                  ...prev,
                  status: next,
                }),
              });
            }}
            onInspectBill={openInspectBill}
            onEditDraft={(bill) => {
              setEditingBill(bill);
              setBillDialogOpen(true);
            }}
            onOpenCreateBillDialog={() => {
              setEditingBill(null);
              setBillDialogOpen(true);
            }}
          />
        </TabsContent>
      </Tabs>

      <BudgetEditorDialog
        open={budgetDialogOpen}
        onOpenChange={setBudgetDialogOpen}
        siteId={site.id}
        billingMonth={selectedMonth}
        currentBudget={budgetQuery.data ?? null}
        csrfToken={principal.session.csrfToken}
        onRefreshBudget={() => budgetQuery.refetch()}
      />

      <UtilityBillDialog
        open={billDialogOpen}
        onOpenChange={setBillDialogOpen}
        siteId={site.id}
        defaultBillingMonth={selectedMonth}
        editingBill={editingBill}
        monthOptions={monthOptions}
        csrfToken={principal.session.csrfToken}
        onSaved={(saved) => openInspectBill(saved.id)}
      />

      <RecalculateDialog
        open={recalcDialogOpen}
        onOpenChange={setRecalcDialogOpen}
        siteId={site.id}
        billingMonth={selectedMonth}
        timezone={site.timezone}
        csrfToken={principal.session.csrfToken}
        now={now}
      />

      <BillReconciliationSheet
        bill={inspectedBill}
        tenantId={site.tenantId}
        siteId={site.id}
        timezone={site.timezone}
        csrfToken={principal.session.csrfToken}
        canManageBilling={canManageBilling}
        onClose={() => {
          void navigate({
            search: (prev) => ({
              ...prev,
              inspect: undefined,
            }),
          });
        }}
        onEditDraft={(bill) => {
          setEditingBill(bill);
          setBillDialogOpen(true);
        }}
      />
    </Main>
  );
}
