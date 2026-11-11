import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { getRouteApi, Link, useNavigate } from '@tanstack/react-router';
import {
  ArrowRight,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  CircleDashed,
  ClipboardList,
  Lightbulb,
  Send,
  ShieldCheck,
  Sparkles,
  TriangleAlert,
  Undo2,
} from 'lucide-react';
import { useEffect, useMemo, useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import { GatewayRequestError } from '@/api/gateway-client';
import type { WorkOrder } from '@/api/work-orders';
import { PageHeader } from '@/blocks/page-header';
import { Panel, PanelHeader } from '@/blocks/panel';
import { InfoTip } from '@/components/info-tip';
import { Main } from '@/components/layout/Main';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { Spinner } from '@/components/ui/spinner';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { siteCommandsQuery, type Command } from '@/features/control/command-api';
import { APPROVAL_LABEL, STATUS_PRESENTATION } from '@/features/control/command-presentation';
import { CommandMark } from '@/features/control/components/CommandMark';
import { ConfirmCommand, useSubmitCommand, type PendingCommand } from '@/features/control/components/CommandConfirmation';
import { controlTargets, type ControlSetting, type ControlTarget } from '@/features/control/control-model';
import { useRealtimePlant } from '@/features/operations/realtime/use-realtime-plant';
import { CreateWorkOrderDialog } from '@/features/work-orders/CreateWorkOrderDialog';
import { StatusShape } from '@/features/work-orders/components/StatusShape';
import { STATUS_LABELS } from '@/features/work-orders/work-order-presentation';
import { workOrderKeys } from '@/features/work-orders/work-order-queries';
import { useNow } from '@/hooks/use-now';
import { formatDecimal, formatTime } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import {
  buildOptimizationWorkOrderDraft,
  buildReadinessSummary,
  changesSupply,
  findActiveRecommendationWorkOrder,
  preparationProblem,
  recommendedSetpoint,
  TRIGGER_LABEL,
  type OptimizationRecommendation,
  type ReadinessItem,
  type ReadinessStatus,
  type ReadinessSummary,
  type RecommendationRisk,
} from '../optimization-model';
import {
  latestPreparationQuery,
  latestRecommendationQuery,
  recommendationWorkOrdersQuery,
  requestRecommendation,
} from '../query';

const siteRoute = getRouteApi('/_app/_site');
const pageRoute = getRouteApi('/_app/_site/energy-analysis/optimization');

const RISK: Record<RecommendationRisk, { label: string; className: string }> = {
  LOW: { label: '低风险', className: 'bg-success-soft text-success-text' },
  MEDIUM: { label: '中风险', className: 'bg-warning-soft text-warning-text' },
  HIGH: { label: '高风险', className: 'bg-destructive-soft text-destructive-text' },
};

// A requested run publishes within seconds; past this the page stops waiting and says so.
const RUN_PATIENCE_MS = 90_000;

const celsius = (value: number) => `${formatDecimal(value, 1)} °C`;
const signed = (value: number, digits: number) => `${value > 0 ? '+' : value < 0 ? '−' : ''}${formatDecimal(Math.abs(value), digits)}`;

interface Marker {
  readonly value: number;
  readonly label: string;
  readonly emphasis?: boolean;
}

/** A temperature scale with its allowed band shaded and the current and proposed values marked. */
function RangeTrack({ label, axis, band, bandLabel, markers }: {
  readonly label: string;
  readonly axis: readonly [number, number];
  readonly band: readonly [number, number];
  readonly bandLabel: string;
  readonly markers: readonly Marker[];
}) {
  const at = (value: number) => `${((Math.min(axis[1], Math.max(axis[0], value)) - axis[0]) / (axis[1] - axis[0])) * 100}%`;
  const summary = `${label}：${bandLabel} ${celsius(band[0])}–${celsius(band[1])}，${markers.map((marker) => `${marker.label} ${celsius(marker.value)}`).join('，')}`;
  return (
    <figure className="relative h-[60px] min-w-0 flex-1" aria-label={summary}>
      <div className="absolute top-[22px] h-2 w-full rounded-full bg-muted" aria-hidden="true" />
      <div className="absolute top-[22px] h-2 rounded-full bg-primary/20" style={{ left: at(band[0]), width: `calc(${at(band[1])} - ${at(band[0])})` }} aria-hidden="true" />
      {/* The proposal is labelled above the track and the present value below, so close values never collide. */}
      {markers.map((marker) => (
        <div
          key={marker.label}
          className={cn('absolute flex -translate-x-1/2 items-center', marker.emphasis ? 'top-0 flex-col' : 'top-[18px] flex-col-reverse')}
          style={{ left: at(marker.value) }}
          aria-hidden="true"
        >
          <span className={cn('text-[11px] leading-4 whitespace-nowrap', marker.emphasis ? 'font-semibold text-primary' : 'text-muted-foreground')}>
            {marker.label} {formatDecimal(marker.value, 1)}
          </span>
          <span className={cn('size-3.5 rounded-full border-2 border-card shadow-sm', marker.emphasis ? 'mt-0.5 bg-primary' : 'mb-0.5 bg-muted-foreground')} />
        </div>
      ))}
      <div className="absolute inset-x-0 top-[44px] flex justify-between text-[11px] text-subtle-foreground tabular-nums" aria-hidden="true">
        <span>{axis[0]}</span>
        <span>{axis[1]} °C</span>
      </div>
    </figure>
  );
}

function ConstraintRow({ title, detail, children }: { readonly title: string; readonly detail: ReactNode; readonly children: ReactNode }) {
  return (
    <div className="flex items-center gap-8 border-b border-divider px-5 py-4 last:border-b-0">
      <span className="flex w-72 shrink-0 flex-col gap-0.5">
        <span className="text-sm font-semibold">{title}</span>
        <span className="text-xs text-muted-foreground">{detail}</span>
      </span>
      {children}
    </div>
  );
}

function supplySetpoint(targets: readonly ControlTarget[]): { target: ControlTarget; setting: ControlSetting } | null {
  for (const target of targets) {
    const setting = target.settings.find((candidate) => candidate.capability === 'SET_CHILLED_WATER_TEMPERATURE_SETPOINT');
    if (setting) return { target, setting };
  }
  return null;
}

function Effect({ label, value, unit, info, children }: { readonly label: string; readonly value: string; readonly unit?: string; readonly info?: string; readonly children: ReactNode }) {
  return (
    <div className="flex flex-col gap-1">
      <span className="flex items-center gap-1 text-xs font-medium text-muted-foreground">{label}{info ? <InfoTip>{info}</InfoTip> : null}</span>
      <span className="text-2xl leading-[29px] font-semibold tracking-[-0.03em] tabular-nums">
        {value}{unit ? <span className="ml-1 text-xs font-medium tracking-normal text-subtle-foreground">{unit}</span> : null}
      </span>
      <span className="text-xs text-muted-foreground">{children}</span>
    </div>
  );
}

export function OptimizationWorkspace() {
  const { site, principal } = siteRoute.useRouteContext();
  const search = pageRoute.useSearch();
  const navigate = useNavigate({ from: '/energy-analysis/optimization' });
  const queryClient = useQueryClient();
  const activeView = search.view ?? 'proposal';
  const capabilities = principal.authorization.capabilities;
  const canListWorkOrders = capabilities.includes('work-order.list');
  const canCreateWorkOrder = capabilities.includes('work-order.create');
  const now = useNow(30_000);
  const [waiting, setWaiting] = useState<{ readonly runId: string; readonly since: number } | null>(null);
  const [notice, setNotice] = useState<{ readonly title: string; readonly detail: string } | null>(null);
  const [pending, setPending] = useState<PendingCommand | null>(null);
  const [creatingWorkOrder, setCreatingWorkOrder] = useState(false);
  const latest = useQuery(latestRecommendationQuery(site.id, waiting?.runId ?? null));
  const commands = useQuery({
    ...siteCommandsQuery(site.id),
    enabled: capabilities.includes('site.read'),
  });
  const { plant, registry } = useRealtimePlant();
  const submit = useSubmitCommand(site.id, principal.session.csrfToken);
  const recommendation = latest.data ?? null;
  // How the hourly schedule last fired, so a page with no new recommendation says which input was missing.
  const preparation = useQuery(latestPreparationQuery(site.id));
  const scheduleProblem = preparationProblem(preparation.data ?? null);
  const firedAt = preparation.data ? formatTime(preparation.data.firedAt, site.timezone) : '';
  const staleRecommendation = recommendation !== null && scheduleProblem !== null && preparation.data !== null && preparation.data !== undefined
    && Date.parse(preparation.data.firedAt) > Date.parse(recommendation.createdAt);

  const request = useMutation({
    mutationFn: () => requestRecommendation(site.id, principal.session.csrfToken),
    onMutate: () => setNotice(null),
    onSuccess: ({ runId }) => setWaiting({ runId, since: Date.now() }),
    onError: (error) => setNotice({
      title: '没有生成建议',
      detail: error instanceof GatewayRequestError ? error.message : '优化服务暂时不可用',
    }),
  });

  useEffect(() => {
    if (!waiting) return;
    if (recommendation?.runId === waiting.runId) {
      setWaiting(null);
      toast.success('已生成新的优化建议');
      return;
    }
    const timer = window.setTimeout(() => {
      setWaiting(null);
      setNotice({ title: '本次计算没有产出建议', detail: '在安全与舒适约束内没有找到可行的供水温度，或计算未能完成。稍后可以再试一次。' });
    }, Math.max(0, waiting.since + RUN_PATIENCE_MS - Date.now()));
    return () => window.clearTimeout(timer);
  }, [waiting, recommendation?.runId]);

  const supply = useMemo(
    () => (registry.data ? supplySetpoint(controlTargets(registry.data.assetModel, new Date(now))) : null),
    [registry.data, now],
  );
  const manualWorkOrderMode = registry.isSuccess && supply === null && recommendation !== null && changesSupply(recommendation);
  const recommendationWorkOrders = useQuery(
    recommendationWorkOrdersQuery(site.id, recommendation?.id ?? null, manualWorkOrderMode && canListWorkOrders),
  );
  const activeWorkOrder = useMemo(
    () =>
      recommendation && recommendationWorkOrders.data
        ? findActiveRecommendationWorkOrder(recommendationWorkOrders.data.items, recommendation.id)
        : null,
    [recommendation, recommendationWorkOrders.data],
  );
  const workOrderDraft = useMemo(
    () => (recommendation ? buildOptimizationWorkOrderDraft(recommendation) : null),
    [recommendation],
  );
  const readiness = useMemo(
    () => buildReadinessSummary({
      recommendation,
      preparation: preparation.data ?? null,
      hasWritableSetpoint: supply !== null,
    }),
    [recommendation, preparation.data, supply],
  );
  const setpoint = recommendation && supply ? recommendedSetpoint(recommendation, supply.setting) : null;
  const supplyReading = supply?.setting.feedbackSourceKey
    ? plant.devices.find((device) => device.deviceId === supply.target.deviceId)?.reading(supply.setting.feedbackSourceKey) ?? null
    : null;
  // The supply setpoint command sent since this recommendation was made, newest first.
  const sent = recommendation && supply
    ? commands.data?.find((command) => command.pointId === supply.setting.pointId && Date.parse(command.createdAt) >= Date.parse(recommendation.createdAt))
    : undefined;
  const dispatch = () => {
    if (!supply || setpoint === null) return;
    setPending({
      target: supply.target, label: supply.setting.label, pointId: supply.setting.pointId,
      parameters: { [supply.setting.parameterKey]: setpoint },
      change: { from: supplyReading, to: setpoint, unit: supply.setting.unit },
    });
  };

  const busy = request.isPending || waiting !== null;
  const generateButton = (variant: 'default' | 'outline') => (
    <Button variant={variant} size="sm" disabled={busy} onClick={() => request.mutate()}>
      {busy ? <Spinner data-icon="inline-start" /> : <Sparkles data-icon="inline-start" />}
      {busy ? '正在计算' : recommendation ? '重新生成' : '生成建议'}
    </Button>
  );

  return (
    <Main>
      <PageHeader
        title="优化建议"
        description={site.displayName}
        actions={generateButton(recommendation ? 'outline' : 'default')}
      />

      <Tabs
        value={activeView}
        onValueChange={(nextView) => {
          void navigate({
            search: (prev) => ({
              ...prev,
              view: nextView === 'analysis' ? 'analysis' : undefined,
            }),
          });
        }}
        className="gap-[18px]"
      >
        <div className="border-b border-divider pb-1">
          <TabsList variant="line">
            <TabsTrigger value="proposal">当前建议</TabsTrigger>
            <TabsTrigger value="analysis">模型与负荷分析</TabsTrigger>
          </TabsList>
        </div>

        <TabsContent value="proposal" className="flex flex-col gap-[18px]">
          {notice ? (
            <Alert>
              <TriangleAlert />
              <AlertTitle>{notice.title}</AlertTitle>
              <AlertDescription>{notice.detail}</AlertDescription>
            </Alert>
          ) : null}

          {staleRecommendation ? (
            <Alert>
              <TriangleAlert />
              <AlertTitle>最近一次定时计算没有生成新建议</AlertTitle>
              <AlertDescription>{firedAt}：{scheduleProblem}。下面是上一条建议。</AlertDescription>
            </Alert>
          ) : null}

          {latest.isPending ? (
            <Skeleton className="h-64 rounded-xl" />
          ) : latest.isError ? (
            <Panel aria-label="当前建议">
              <div className="flex flex-col items-start gap-2 px-5 py-8">
                <p className="text-sm font-semibold">优化建议暂时读不到</p>
                <Button variant="outline" size="sm" onClick={() => void latest.refetch()}>重试</Button>
              </div>
            </Panel>
          ) : !recommendation ? (
            <>
              <Panel aria-label="当前建议">
                <div className="flex flex-col items-center gap-2 px-6 py-14 text-center">
                  <span className="flex size-9 items-center justify-center rounded-xl bg-primary-soft text-primary"><Lightbulb className="size-[18px]" aria-hidden="true" /></span>
                  <p className="text-sm font-semibold">还没有优化建议</p>
                  {scheduleProblem ? (
                    <p className="max-w-lg text-sm text-secondary-foreground">最近一次定时计算（<span className="font-mono">{firedAt}</span>）没有生成建议：{scheduleProblem}。</p>
                  ) : preparation.isSuccess && preparation.data === null ? (
                    <p className="max-w-lg text-sm text-secondary-foreground">站点还没有配置定时优化，建议只能手动生成。</p>
                  ) : null}
                  <p className="max-w-lg text-xs text-muted-foreground">
                    建议依据最近一天的冷站用电与电费、最新负荷预测和实时供水、室内温度计算；用电与电费每天零点后结算，某天缺数据时不出电费，改用更早一天。
                  </p>
                  <div className="mt-2">{generateButton('default')}</div>
                </div>
              </Panel>
              <ReadinessDisclosure readiness={readiness} recommendation={null} />
            </>
          ) : (
            <Proposal
              recommendation={recommendation}
              readiness={readiness}
              timeZone={site.timezone}
              siteId={site.id}
              setpoint={setpoint}
              setting={supply?.setting ?? null}
              registryState={registry.isPending ? 'pending' : registry.isError ? 'error' : 'ready'}
              sent={sent}
              activeWorkOrder={activeWorkOrder}
              canCreateWorkOrder={canCreateWorkOrder}
              onDispatch={dispatch}
              onCreateWorkOrder={() => setCreatingWorkOrder(true)}
            />
          )}
        </TabsContent>

        <TabsContent value="analysis" className="flex flex-col gap-[18px]">
          <Panel aria-label="模型与负荷分析">
            <div className="flex flex-col gap-1 px-6 py-8">
              <p className="text-sm font-semibold">模型与负荷分析</p>
              <p className="text-xs text-muted-foreground">
                按需查看冷机 COP 模型影子对照结果、近 14 天运行数据拟合状态与未来 24 小时冷站负荷预测。
              </p>
            </div>
          </Panel>
        </TabsContent>
      </Tabs>

      {workOrderDraft && recommendation ? (
        <CreateWorkOrderDialog
          open={creatingWorkOrder}
          onOpenChange={setCreatingWorkOrder}
          title="创建人工调温工单"
          initial={workOrderDraft.initial}
          sourceReferences={workOrderDraft.sourceReferences}
          onCreated={(created) => {
            toast.success('已创建人工调温工单');
            const sourceKey = workOrderKeys.source(site.id, 'EXTERNAL', workOrderDraft.sourceReferences[0].resourceId);
            queryClient.setQueryData(sourceKey, {
              schemaVersion: 1,
              items: [created, ...(recommendationWorkOrders.data?.items ?? [])],
              nextCursor: null,
              hasMore: false,
            });
            void queryClient.invalidateQueries({ queryKey: workOrderKeys.all(site.id) });
          }}
        />
      ) : null}

      <ConfirmCommand
        pending={pending}
        submitting={submit.isPending}
        onCancel={() => setPending(null)}
        onConfirm={() => { if (pending) submit.mutate(pending, { onSettled: () => setPending(null) }); }}
      />
    </Main>
  );
}

const READINESS_STATUS: Record<ReadinessStatus, { readonly badge: string; readonly className: string; readonly icon: typeof CheckCircle2 }> = {
  ready: { badge: '就绪', className: 'bg-success-soft text-success-text', icon: CheckCircle2 },
  manual: { badge: '人工工单', className: 'bg-primary-soft text-primary', icon: ClipboardList },
  blocked: { badge: '未就绪', className: 'bg-warning-soft text-warning-text', icon: TriangleAlert },
  unknown: { badge: '待核验', className: 'bg-muted text-muted-foreground', icon: CircleDashed },
};

function ReadinessRow({ item }: { readonly item: ReadinessItem }) {
  const presentation = READINESS_STATUS[item.status];
  const Icon = presentation.icon;
  return (
    <li className="flex items-start gap-2.5 rounded-lg border border-divider bg-muted/20 px-3.5 py-2.5">
      <Icon className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
      <span className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="flex flex-wrap items-center gap-2">
          <span className="text-sm font-medium">{item.label}</span>
          <span className={cn('rounded px-1.5 py-0.5 text-[11px] leading-4 font-medium', presentation.className)}>
            {presentation.badge}
          </span>
        </span>
        <span className="text-xs text-muted-foreground">{item.detail}</span>
      </span>
    </li>
  );
}

function ReadinessDisclosure({
  readiness,
  recommendation,
}: {
  readonly readiness: ReadinessSummary;
  readonly recommendation: OptimizationRecommendation | null;
}) {
  const [expanded, setExpanded] = useState(false);
  const hasBlocked = readiness.items.some((item) => item.status === 'blocked');
  const SummaryIcon = hasBlocked
    ? TriangleAlert
    : readiness.readyCount >= 5
      ? CheckCircle2
      : CircleDashed;

  return (
    <Panel>
      <button
        type="button"
        aria-expanded={expanded}
        onClick={() => setExpanded((prev) => !prev)}
        className="flex w-full items-center justify-between gap-4 px-5 py-3.5 text-left outline-none hover:bg-muted/30 focus-visible:ring-3 focus-visible:ring-ring-soft"
      >
        <span className="flex min-w-0 items-center gap-2.5">
          <SummaryIcon
            className={cn(
              'size-4 shrink-0',
              hasBlocked ? 'text-warning' : readiness.readyCount >= 5 ? 'text-success' : 'text-muted-foreground',
            )}
            aria-hidden="true"
          />
          <span className="truncate text-sm font-semibold">{readiness.headline}</span>
        </span>
        <span className="flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground">
          <span>{expanded ? '收起核验与依据' : '查看核验与依据'}</span>
          <ChevronDown className={cn('size-4 transition-transform', expanded && 'rotate-180')} aria-hidden="true" />
        </span>
      </button>

      {expanded ? (
        <section role="region" aria-label="前置与安全条件" className="flex flex-col gap-4 border-t border-divider px-5 py-4">
          <ul className="grid grid-cols-2 gap-2.5">
            {readiness.items.map((item) => (
              <ReadinessRow key={item.key} item={item} />
            ))}
          </ul>

          {recommendation ? (
            <div className="grid grid-cols-2 items-start gap-6 border-t border-divider pt-4">
              <div className="flex flex-col gap-1.5">
                <span className="flex items-center gap-1.5 text-xs font-semibold text-foreground">
                  策略响应与基线依据
                  <InfoTip>标「人工设定」的响应系数由人在优化策略里填写，不是从站点运行数据拟合的</InfoTip>
                </span>
                <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-5">
                  {([
                    ['最近一天冷站用电', `${formatDecimal(recommendation.baseline.dailyEnergyKWh, 0)} kWh · ¥${formatDecimal(recommendation.baseline.dailyCost, 0)}`],
                    ['供水每升高 1 °C 日用电', `${signed(recommendation.response.energyKWhPerSupplyC, 0)} kWh · 人工设定`],
                    ['供水每升高 1 °C 室内温度', `${signed(recommendation.response.zoneCPerSupplyC, 1)} °C · 人工设定`],
                  ] as const).map(([label, value]) => (
                    <div key={label} className="col-span-2 grid grid-cols-subgrid border-b border-divider py-1.5 last:border-b-0">
                      <dt className="text-xs leading-5 text-muted-foreground">{label}</dt>
                      <dd className="text-xs leading-5 font-medium tabular-nums">{value}</dd>
                    </div>
                  ))}
                </dl>
              </div>

              <div className="flex flex-col gap-2">
                <span className="flex items-center gap-1.5 text-xs font-semibold text-foreground">
                  <Undo2 className="size-3.5 text-primary" aria-hidden="true" />
                  观察与回退规则
                </span>
                <p className="text-xs text-muted-foreground">
                  调整后需同时保持室内温度在 {formatDecimal(recommendation.comfortRange.minC, 0)}–{formatDecimal(recommendation.comfortRange.maxC, 0)} °C 舒适范围内，且冷站负荷不高于调整前（最近一天冷站用电 {formatDecimal(recommendation.baseline.dailyEnergyKWh, 0)} kWh）。
                </p>
                <p className="rounded-md bg-muted/40 px-3 py-2 text-xs text-secondary-foreground">
                  下发后观察 {recommendation.verificationMinutes} 分钟；任一条件不满足，将供水温度恢复为{' '}
                  <b className="font-semibold text-foreground">{celsius(recommendation.restoreSupplyTempC)}</b>
                </p>
              </div>
            </div>
          ) : null}
        </section>
      ) : null}
    </Panel>
  );
}

function Proposal({
  recommendation,
  readiness,
  timeZone,
  siteId,
  setpoint,
  setting,
  registryState,
  sent,
  activeWorkOrder,
  canCreateWorkOrder,
  onDispatch,
  onCreateWorkOrder,
}: {
  readonly recommendation: OptimizationRecommendation;
  readonly readiness: ReadinessSummary;
  readonly timeZone: string;
  readonly siteId: string;
  readonly setpoint: number | null;
  readonly setting: ControlSetting | null;
  readonly registryState: 'pending' | 'error' | 'ready';
  readonly sent: Command | undefined;
  readonly activeWorkOrder: WorkOrder | null;
  readonly canCreateWorkOrder: boolean;
  readonly onDispatch: () => void;
  readonly onCreateWorkOrder: () => void;
}) {
  const { baseline, candidate, comfortRange, supplyRange } = recommendation;
  const change = changesSupply(recommendation);
  const risk = RISK[recommendation.risk];
  const share = baseline.dailyEnergyKWh > 0 ? recommendation.saving.energyKWhPerDay / baseline.dailyEnergyKWh : null;
  const stepNote = setting && setpoint !== null && Math.abs(setpoint - candidate.supplyTempC) >= 0.05
    ? `冷机只能按 ${formatDecimal(setting.step, 1)} °C 调整，实际下发 ${celsius(setpoint)}；建议不会自动执行`
    : `下发为 ${celsius(setpoint ?? candidate.supplyTempC)}；建议不会自动执行`;

  return (
    <>
      <Panel aria-label="当前建议" className="grid grid-cols-[minmax(0,1.5fr)_minmax(0,1fr)]">
        <div className="flex flex-col gap-3 border-r border-divider px-6 py-5">
          <span className="flex flex-wrap items-center gap-2 text-xs">
            <span className="text-title font-semibold">当前建议</span>
            <span className={cn('rounded-md px-1.5 leading-5 font-medium', risk.className)}>{risk.label}</span>
            <span className="text-muted-foreground">{TRIGGER_LABEL[recommendation.trigger]}于 <span className="font-mono">{formatTime(recommendation.createdAt, timeZone)}</span></span>
          </span>
          <span className="flex flex-col gap-1">
            <span className="text-xs text-muted-foreground">冷冻水供水温度</span>
            {change ? (
              <span className="flex items-center gap-3 text-[34px] leading-10 font-semibold tracking-[-0.03em] tabular-nums">
                <span className="text-muted-foreground">{formatDecimal(baseline.supplyTempC, 1)}</span>
                <ArrowRight className="size-6 text-subtle-foreground" aria-label="→" />
                <span className="text-primary">{formatDecimal(candidate.supplyTempC, 1)}</span>
                <span className="text-sm font-medium tracking-normal text-subtle-foreground">°C</span>
              </span>
            ) : (
              <span className="text-[34px] leading-10 font-semibold tracking-[-0.03em] tabular-nums">
                保持 {formatDecimal(baseline.supplyTempC, 1)}<span className="ml-1 text-sm font-medium tracking-normal text-subtle-foreground">°C</span>
              </span>
            )}
          </span>
          <p className="text-sm text-secondary-foreground">
            {change
              ? `供水温度${candidate.supplyTempC > baseline.supplyTempC ? '升高' : '降低'}后，预计室内温度 ${celsius(candidate.projectedZoneTempC)}，仍在 ${formatDecimal(comfortRange.minC, 0)}–${formatDecimal(comfortRange.maxC, 0)} °C 舒适范围内，距边界还有 ${celsius(recommendation.comfortMarginC)}。`
              : '在安全与舒适约束内，当前供水温度已是最省电的选择。'}
          </p>
          {change ? (
            registryState === 'pending' ? (
              <Skeleton className="h-9 w-40 rounded-md" />
            ) : registryState === 'error' ? (
              <p className="text-xs text-muted-foreground">站点资产台账暂时读不到，无法核验可执行通道。</p>
            ) : setting !== null ? (
              sent ? (
                <Link
                  to="/operations/control"
                  search={{ site: siteId, inspect: sent.commandId }}
                  className="flex items-center gap-3 rounded-lg border bg-muted/40 px-4 py-3 outline-none hover:bg-accent focus-visible:ring-3 focus-visible:ring-ring-soft"
                >
                  <CommandMark status={sent.status} />
                  <span className="flex min-w-0 flex-1 flex-col">
                    <span className="text-sm font-semibold">已提交为控制指令 · {STATUS_PRESENTATION[sent.status].label}</span>
                    <span className="truncate text-xs text-muted-foreground">
                      {setting.label} → {setpoint === null ? '—' : celsius(setpoint)} · <span className="font-mono">{formatTime(sent.createdAt, timeZone).slice(-5)}</span>
                      {' · '}{APPROVAL_LABEL[sent.approvalPolicy]}{sent.requiredApprovalCount > 0 ? ` ${sent.approvalCount} / ${sent.requiredApprovalCount}` : ''}
                    </span>
                  </span>
                  <span className="flex items-center text-xs font-medium text-primary">查看指令<ChevronRight className="size-3.5" aria-hidden="true" /></span>
                </Link>
              ) : (
                <div className="flex flex-wrap items-center gap-3">
                  <Button disabled={setpoint === null} onClick={onDispatch}>
                    <Send data-icon="inline-start" />
                    按建议下发
                  </Button>
                  <span className="text-xs text-muted-foreground">{stepNote}</span>
                </div>
              )
            ) : activeWorkOrder ? (
              <Link
                to="/operations/work-center"
                search={{ site: siteId, inspect: activeWorkOrder.workOrderId }}
                className="flex items-center gap-3 rounded-lg border bg-muted/40 px-4 py-3 outline-none hover:bg-accent focus-visible:ring-3 focus-visible:ring-ring-soft"
              >
                <StatusShape status={activeWorkOrder.status} className="size-3" />
                <span className="flex min-w-0 flex-1 flex-col">
                  <span className="text-sm font-semibold">已转为人工调温工单 · {STATUS_LABELS[activeWorkOrder.status]}</span>
                  <span className="truncate text-xs text-muted-foreground">
                    {activeWorkOrder.title} · <span className="font-mono">{formatTime(activeWorkOrder.createdAt, timeZone).slice(-5)}</span>
                  </span>
                </span>
                <span className="flex items-center text-xs font-medium text-primary">查看工单<ChevronRight className="size-3.5" aria-hidden="true" /></span>
              </Link>
            ) : canCreateWorkOrder ? (
              <div className="flex flex-wrap items-center gap-3">
                <Button onClick={onCreateWorkOrder}>
                  <ClipboardList data-icon="inline-start" />
                  创建人工调温工单
                </Button>
                <span className="text-xs text-muted-foreground">当前站点未登记可写冷冻水供水温度设定点，转为人工调温工单现场执行</span>
              </div>
            ) : (
              <p className="text-xs text-muted-foreground">
                当前站点未登记可写冷冻水供水温度设定点；当前账号无创建工单权限，需由运维人员现场调温。
              </p>
            )
          ) : null}
        </div>
        <section role="region" aria-label="预计效果" className="flex flex-col gap-5 px-6 py-5">
          <span className="text-xs font-medium text-muted-foreground">预计效果</span>
          <Effect label="预计日节电" value={formatDecimal(recommendation.saving.energyKWhPerDay, 0)} unit="kWh">
            90% 把握在 ±{formatDecimal(recommendation.uncertainty.energyP90KWh, 0)} kWh 内{share !== null ? ` · 约占最近一天冷站用电 ${formatDecimal(share * 100, 1)}%` : ''}
          </Effect>
          <Effect label="预计日节省" info="按当天峰谷均价折算，不含需量电费" value={`¥${formatDecimal(recommendation.saving.costPerDay, 0)}`}>
            按最近一天电费 ¥{formatDecimal(baseline.dailyCost, 0)} 折算
          </Effect>
        </section>
      </Panel>

      <Panel aria-label="约束">
        <PanelHeader
          title={<span className="flex items-center gap-1.5"><ShieldCheck className="size-4 text-primary" aria-hidden="true" />安全与舒适约束<InfoTip>舒适余量已扣除室内温度预测的不确定性</InfoTip></span>}
          caption={`建议只在两条范围之内取值，每次调整不超过 ${celsius(supplyRange.maxStepC)}`}
        />
        <ConstraintRow title="冷冻水供水温度" detail={`安全范围 ${formatDecimal(supplyRange.minC, 1)}–${celsius(supplyRange.maxC)} · ${change ? `调整 ${formatDecimal(Math.abs(candidate.supplyTempC - baseline.supplyTempC), 1)} °C` : '保持当前温度'}`}>
          <RangeTrack
            label="冷冻水供水温度"
            axis={[Math.floor(supplyRange.minC) - 1, Math.ceil(supplyRange.maxC) + 1]}
            band={[supplyRange.minC, supplyRange.maxC]}
            bandLabel="安全范围"
            markers={change
              ? [{ value: baseline.supplyTempC, label: '当前' }, { value: candidate.supplyTempC, label: '调整为', emphasis: true }]
              : [{ value: baseline.supplyTempC, label: '当前', emphasis: true }]}
          />
        </ConstraintRow>
        <ConstraintRow
          title="室内温度"
          detail={`舒适范围 ${formatDecimal(comfortRange.minC, 0)}–${formatDecimal(comfortRange.maxC, 0)} °C · 预计 ±${formatDecimal(recommendation.uncertainty.zoneTempP90C, 1)} °C · 余量 ${celsius(recommendation.comfortMarginC)}`}
        >
          <RangeTrack
            label="室内温度"
            axis={[Math.floor(comfortRange.minC) - 2, Math.ceil(comfortRange.maxC) + 2]}
            band={[comfortRange.minC, comfortRange.maxC]}
            bandLabel="舒适范围"
            markers={change
              ? [{ value: baseline.zoneTempC, label: '当前' }, { value: candidate.projectedZoneTempC, label: '预计', emphasis: true }]
              : [{ value: baseline.zoneTempC, label: '当前', emphasis: true }]}
          />
        </ConstraintRow>
      </Panel>

      <ReadinessDisclosure readiness={readiness} recommendation={recommendation} />
    </>
  );
}
