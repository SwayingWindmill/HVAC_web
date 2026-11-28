import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { getRouteApi, useNavigate } from '@tanstack/react-router';
import { Lightbulb, TriangleAlert } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import { notifySuccess } from '@/lib/toast';
import { GatewayRequestError } from '@/api/gateway-client';
import type { AutoControlConfirmationMode } from '@/api/generated/platformGateway.gen';
import { PageHeader } from '@/blocks/page-header';
import { Panel } from '@/blocks/panel';
import { Main } from '@/components/layout/Main';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { Spinner } from '@/components/ui/spinner';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { siteCommandsQuery } from '@/features/control/command-api';
import { ConfirmCommand, type PendingCommand } from '@/features/control/components/CommandConfirmation';
import { controlTargets, type ControlSetting, type ControlTarget } from '@/features/control/control-model';
import { siteControlStateQuery, type SiteControlMode } from '@/features/control/control-state';
import { useRealtimePlant } from '@/features/operations/realtime/use-realtime-plant';
import { CreateWorkOrderDialog } from '@/features/work-orders/CreateWorkOrderDialog';
import { workOrderKeys } from '@/features/work-orders/work-order-queries';
import { useNow } from '@/hooks/use-now';
import { formatTime } from '@/lib/operator-format';
import {
  buildOptimizationWorkOrderDraft,
  buildReadinessSummary,
  changesSupply,
  findActiveRecommendationWorkOrder,
  formatAuxiliaryCapabilityLabel,
  formatAuxiliaryLoopLabel,
  formatCelsius as celsius,
  isUnscheduledReady,
  matchAuxiliaryControlTarget,
  preparationProblem,
  preparationReadinessReasons,
  recommendedAuxiliaryValue,
  recommendedSetpoint,
} from '../optimization-model';
import { siteAutoControlLeaseQuery } from '../auto-control-api';
import {
  latestPreparationQuery,
  latestRecommendationQuery,
  latestStagingQuery,
  recommendationWorkOrdersQuery,
  requestRecommendation,
} from '../query';
import { stagingView } from '../staging-model';
import { AutoControlPanel } from './AutoControlPanel';
import { OptimizationAnalysisTab } from './OptimizationAnalysisTab';
import {
  OptimizationProposalPanel,
  type AuxiliaryControlBinding,
} from './OptimizationProposalPanel';
import { ReadinessDisclosure } from './OptimizationReadinessDisclosure';
import { StagingPanel } from './StagingPanel';

const siteRoute = getRouteApi('/_app/_site');
const pageRoute = getRouteApi('/_app/_site/energy-analysis/optimization');

// A requested run publishes within seconds; past this the page stops waiting and says so.
const RUN_PATIENCE_MS = 90_000;

function supplySetpoint(targets: readonly ControlTarget[]): { target: ControlTarget; setting: ControlSetting } | null {
  for (const target of targets) {
    const setting = target.settings.find((candidate) => candidate.capability === 'SET_CHILLED_WATER_TEMPERATURE_SETPOINT');
    if (setting) return { target, setting };
  }
  return null;
}

export function OptimizationWorkspace() {
  const { site, principal } = siteRoute.useRouteContext();
  const search = pageRoute.useSearch();
  const navigate = useNavigate({ from: '/energy-analysis/optimization' });
  const queryClient = useQueryClient();
  const activeView = search.view ?? 'proposal';
  const capabilities = principal.authorization.capabilities;
  const canReadSite = capabilities.includes('site.read');
  const canManageLease = capabilities.includes('schedule.manage');
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
    enabled: canReadSite,
  });
  const autoControlLease = useQuery({
    ...siteAutoControlLeaseQuery(site.id),
    enabled: canReadSite,
  });
  const confirmationMode: AutoControlConfirmationMode = autoControlLease.data?.confirmationMode ?? 'MANUAL_CONFIRM';
  const { plant, registry } = useRealtimePlant();
  const staging = useQuery(latestStagingQuery(site.id));
  const controlState = useQuery(siteControlStateQuery(site.id));
  const recommendation = latest.data ?? null;
  // How the hourly schedule last fired, so a page with no new recommendation says which input was missing.
  const preparation = useQuery(latestPreparationQuery(site.id));
  const scheduleProblem = preparationProblem(preparation.data ?? null);
  const readinessReasons = preparationReadinessReasons(preparation.data ?? null);
  const unscheduledReady = isUnscheduledReady(preparation.data ?? null);
  const firedAt = preparation.data?.firedAt ? formatTime(preparation.data.firedAt, site.timezone) : '';
  const staleRecommendation = recommendation !== null && scheduleProblem !== null && preparation.data !== null && preparation.data !== undefined
    && preparation.data.firedAt !== null
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
      notifySuccess('已生成新的优化建议');
      return;
    }
    const timer = window.setTimeout(() => {
      setWaiting(null);
      setNotice({ title: '暂无需调整的建议', detail: '在安全与舒适约束内没有找到更优的供水温度，或计算未能完成。稍后可以再试一次。' });
    }, Math.max(0, waiting.since + RUN_PATIENCE_MS - Date.now()));
    return () => window.clearTimeout(timer);
  }, [waiting, recommendation?.runId]);

  const targets = useMemo(() => (registry.data ? controlTargets(registry.data.assetModel, new Date(now)) : []), [registry.data, now]);
  const supply = useMemo(() => (registry.data ? supplySetpoint(targets) : null), [registry.data, targets]);
  const stagingResult = useMemo(() => {
    if (!staging.data) return null;
    const names = new Map(registry.data?.assetModel.assets.map((asset) => [asset.id, asset.displayName]) ?? []);
    return stagingView(staging.data, (assetId) => names.get(assetId));
  }, [staging.data, registry.data]);
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
      hasWritableSetpoint: registry.isSuccess ? supply !== null : null,
      timeZone: site.timezone,
    }),
    [recommendation, preparation.data, registry.isSuccess, supply, site.timezone],
  );
  const setpoint = recommendation && supply ? recommendedSetpoint(recommendation, supply.setting) : null;
  const supplyReading = supply?.setting.feedbackSourceKey
    ? plant.devices.find((device) => device.deviceId === supply.target.deviceId)?.reading(supply.setting.feedbackSourceKey) ?? null
    : null;
  const auxiliaryControls = useMemo<readonly AuxiliaryControlBinding[]>(() => {
    if (!recommendation) return [];
    const createdAfter = Date.parse(recommendation.createdAt);
    return recommendation.auxiliaryTargets.map((aux) => {
      const matched = registry.data ? matchAuxiliaryControlTarget(aux, targets) : null;
      const dispatchedValue = matched ? recommendedAuxiliaryValue(aux.targetValue, matched.setting) : null;
      const sent = matched
        ? commands.data?.find((command) => command.pointId === matched.setting.pointId && Date.parse(command.createdAt) >= createdAfter)
        : undefined;
      return { aux, matched, dispatchedValue, sent };
    });
  }, [recommendation, registry.data, targets, commands.data]);
  // The supply setpoint command sent since this recommendation was made, newest first.
  const sent = recommendation && supply
    ? commands.data?.find((command) => command.pointId === supply.setting.pointId && Date.parse(command.createdAt) >= Date.parse(recommendation.createdAt))
    : undefined;
  const controlMode: SiteControlMode = controlState.data?.controlMode ?? staging.data?.siteControlMode ?? 'REMOTE_AUTO';

  const dispatch = () => {
    if (!recommendation || !supply || setpoint === null || controlMode !== 'REMOTE_AUTO') return;
    setPending({
      target: supply.target, label: supply.setting.label, pointId: supply.setting.pointId,
      parameters: { [supply.setting.parameterKey]: setpoint },
      change: { from: supplyReading, to: setpoint, unit: supply.setting.unit },
      acknowledgement: '我已核实当前冷机负荷与运行状态，确认按此建议下发',
      facts: [
        {
          label: '观察与回退',
          value: `下发后观察 ${recommendation.verificationMinutes} 分钟；任一条件不满足时恢复至 ${celsius(recommendation.restoreSupplyTempC)}`,
        },
      ],
    });
  };
  const dispatchAuxiliary = (binding: AuxiliaryControlBinding) => {
    if (!recommendation || !binding.matched || binding.dispatchedValue === null || controlMode !== 'REMOTE_AUTO') return;
    const { aux, matched, dispatchedValue } = binding;
    const reading = matched.setting.feedbackSourceKey
      ? plant.devices.find((device) => device.deviceId === matched.target.deviceId)?.reading(matched.setting.feedbackSourceKey) ?? null
      : null;
    setPending({
      target: matched.target,
      label: matched.setting.label,
      pointId: matched.setting.pointId,
      parameters: { [matched.setting.parameterKey]: dispatchedValue },
      change: { from: reading, to: dispatchedValue, unit: matched.setting.unit },
      acknowledgement: '我已核实当前辅机回路运行状态，确认按此建议下发',
      facts: [
        {
          label: '协同回路',
          value: `${formatAuxiliaryLoopLabel(aux.loopKey)} · ${formatAuxiliaryCapabilityLabel(aux.capability)}`,
        },
        {
          label: '观察窗口',
          value: `下发后观察 ${recommendation.verificationMinutes} 分钟，确认压差、频率与供水温度平稳`,
        },
      ],
    });
  };

  const busy = request.isPending || waiting !== null;
  const generateButton = (variant: 'default' | 'outline') => (
    <Button variant={variant} size="sm" disabled={busy} onClick={() => request.mutate()}>
      {busy ? <Spinner data-icon="inline-start" /> : <Lightbulb data-icon="inline-start" />}
      {busy ? '正在计算' : recommendation ? '重新生成' : '生成建议'}
    </Button>
  );

  const stagingActive = Boolean(stagingResult && stagingResult.cards.length > 0);
  const stagingPanel = (
    <StagingPanel
      siteId={site.id}
      timeZone={site.timezone}
      state={staging.isPending ? 'pending' : staging.isError ? 'error' : 'ready'}
      view={stagingResult}
      evaluatedAt={staging.data?.evaluatedAt ?? null}
      mode={controlMode}
      targets={targets}
      commands={commands.data ?? []}
      onConfirm={setPending}
      onRetry={() => void staging.refetch()}
    />
  );

  const supplyOptimizationSection = (
    <>
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
          <AlertDescription>{firedAt ? `${firedAt}：` : ''}{scheduleProblem}。下面是上一条建议。</AlertDescription>
        </Alert>
      ) : null}

      {recommendation !== null && readinessReasons.length > 0 ? (
        <Alert>
          <TriangleAlert />
          <AlertTitle>当前站点供水温度优化条件尚未完全就绪</AlertTitle>
          <AlertDescription>{readinessReasons.join('、')}。</AlertDescription>
        </Alert>
      ) : null}

      {latest.isPending ? (
        <Skeleton className="h-64 rounded-xl" />
      ) : latest.isError ? (
        <Panel aria-label="当前建议">
          <div className="flex flex-col items-start gap-2 px-5 py-8">
            <h2 className="text-sm font-semibold">优化建议暂时读不到</h2>
            <Button variant="outline" size="sm" onClick={() => void latest.refetch()}>重试</Button>
          </div>
        </Panel>
      ) : !recommendation ? (
        <>
          <Panel aria-label="当前建议">
            <div className="flex flex-col items-center gap-2 px-6 py-10 text-center">
              <span className="flex size-9 items-center justify-center rounded-xl bg-primary-soft text-primary"><Lightbulb className="size-[18px]" aria-hidden="true" /></span>
              <h2 className="text-sm font-semibold">还没有优化建议</h2>
              {scheduleProblem ? (
                <p className="max-w-lg text-sm text-secondary-foreground">
                  最近一次定时计算{firedAt ? <>（<span className="font-mono">{firedAt}</span>）</> : null}没有生成建议：{scheduleProblem}。
                </p>
              ) : preparation.isSuccess && unscheduledReady ? (
                <p className="max-w-lg text-sm text-secondary-foreground">站点还没有配置定时优化，建议只能手动生成。</p>
              ) : null}
              {readinessReasons.length > 0 ? (
                <div className="mt-1 flex max-w-lg flex-col gap-1.5 rounded-lg border border-divider bg-muted px-4 py-3 text-left text-xs text-secondary-foreground">
                  <span className="font-semibold text-foreground">当前站点暂未启用供水温度优化，原因如下：</span>
                  <ul className="list-inside list-disc space-y-1">
                    {preparation.data?.responseCoefficient.status === 'NOT_CALIBRATED' ? (
                      <li>响应系数还没标定：尚未完成供水温度变化对能耗与室温影响的站点标定</li>
                    ) : null}
                    {preparation.data?.tenantStrategy.status === 'PENDING' ? (
                      <li>共享租户策略还没确认：适用策略、必填点位映射或模型部署尚未确认启用</li>
                    ) : null}
                  </ul>
                  <span className="text-muted-foreground">
                    {preparation.data?.status === 'NOT_SCHEDULED' ? '站点也还没有配置定时优化任务；' : ''}同页的「减台与辅机停机」不受影响，可独立评估与执行。
                  </span>
                </div>
              ) : null}
              <div className="mt-2">{generateButton('default')}</div>
            </div>
          </Panel>
          <ReadinessDisclosure readiness={readiness} recommendation={null} />
        </>
      ) : (
        <OptimizationProposalPanel
          recommendation={recommendation}
          readiness={readiness}
          timeZone={site.timezone}
          siteId={site.id}
          controlMode={controlMode}
          setpoint={setpoint}
          setting={supply?.setting ?? null}
          confirmationMode={confirmationMode}
          registryState={registry.isPending ? 'pending' : registry.isError ? 'error' : 'ready'}
          workOrdersState={
            !manualWorkOrderMode || !canListWorkOrders
              ? 'ready'
              : recommendationWorkOrders.isPending
                ? 'pending'
                : recommendationWorkOrders.isError
                  ? 'error'
                  : 'ready'
          }
          sent={sent}
          auxiliaryControls={auxiliaryControls}
          activeWorkOrder={activeWorkOrder}
          canCreateWorkOrder={canCreateWorkOrder}
          onDispatch={dispatch}
          onDispatchAuxiliary={dispatchAuxiliary}
          onCreateWorkOrder={() => setCreatingWorkOrder(true)}
          onRetryWorkOrders={() => void recommendationWorkOrders.refetch()}
        />
      )}
    </>
  );

  return (
    <Main>
      <PageHeader
        title="优化建议"
        description={site.displayName}
        actions={recommendation ? generateButton('outline') : undefined}
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
        className="w-full min-w-0 gap-4"
      >
        <div className="border-b border-divider pb-1">
          <TabsList variant="line">
            <TabsTrigger value="proposal">当前建议</TabsTrigger>
            <TabsTrigger value="analysis">模型与负荷分析</TabsTrigger>
          </TabsList>
        </div>

        <TabsContent value="proposal" className="flex w-full min-w-0 flex-col gap-4">
          {stagingActive ? stagingPanel : supplyOptimizationSection}
          {stagingActive ? supplyOptimizationSection : stagingPanel}

          <AutoControlPanel
            siteId={site.id}
            timeZone={site.timezone}
            csrfToken={principal.session.csrfToken}
            canRead={canReadSite}
            canManage={canManageLease}
          />
        </TabsContent>

        <TabsContent value="analysis" className="flex flex-col gap-4">
          <OptimizationAnalysisTab
            siteId={site.id}
            timeZone={site.timezone}
            recommendation={recommendation}
            active={activeView === 'analysis'}
            canManageControl={canManageLease}
          />
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

      <ConfirmCommand pending={pending} siteId={site.id} csrfToken={principal.session.csrfToken} onClose={() => setPending(null)} />
    </Main>
  );
}
