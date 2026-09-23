import { useCallback, useEffect, useMemo } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import {
  CircleAlert,
  RefreshCw,
  ShieldAlert,
  SlidersHorizontal,
} from 'lucide-react';
import {
  createPlatformGatewayClient,
  type CurrentPrincipalResponse,
  type Site,
} from '@/api/generated/platformGateway.gen';
import {
  createS2TelemetryClient,
  type DeviceObservationSnapshot,
  type TelemetryKeyState,
  type TelemetryQuality,
} from '@/api/generated/s2Telemetry.gen';
import type { HvacRouterContext } from '@/app/router-context';
import { siteRoute } from '@/app/router-paths';
import { Main } from '@/components/layout/Main';
import { PageIntro } from '@/components/layout/PageIntro';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { Item, ItemActions, ItemContent, ItemDescription, ItemGroup, ItemTitle } from '@/components/ui/item';
import { StatusBadge } from '@/components/status-badge';
import { cn } from '@/lib/utils';
import {
  controlCapabilityLabel,
  controlCapabilityRange,
  projectRegisteredControlTargets,
  type RegisteredControlTarget,
} from './control-targets';

export interface ControlCenterSearchState {
  readonly target?: string;
}

interface ControlCenterProps {
  readonly site: Readonly<Site>;
  readonly principal: CurrentPrincipalResponse;
  readonly runtime: HvacRouterContext['runtime'];
  readonly searchState: ControlCenterSearchState;
  readonly onSearchChange: (patch: Partial<ControlCenterSearchState>) => void;
}

interface FeedbackPresentation {
  readonly value: string;
  readonly detail: string;
  readonly state: 'ready' | 'missing' | 'unavailable' | 'pending';
}

const UNKNOWN_PREFLIGHT_FACTS = [
  ['用户授权', '尚未提供执行前授权判定'],
  ['当前控制权', '尚未提供当前控制来源与控制权判定'],
  ['前置条件', '尚未提供执行前条件检查结果'],
  ['联锁', '尚未提供联锁检查结果'],
  ['更高优先级来源', '尚未提供优先级 / 控制源占用状态'],
  ['审批要求', '尚未提供本次控制的审批要求'],
  ['影响范围', '尚未提供本次控制的影响范围'],
  ['临时覆盖', '尚未提供当前覆盖状态'],
] as const;

function formatInstant(value: string, timeZone: string): string {
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone,
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  }).format(new Date(value));
}

function formatTelemetryValue(value: TelemetryKeyState, fallbackUnit: string | null): string {
  if (value.state !== 'PRESENT') return '未观测';
  const unit = value.unit ?? fallbackUnit;
  const formatted = typeof value.value === 'number'
    ? new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 2 }).format(value.value)
    : typeof value.value === 'boolean'
      ? value.value ? '是' : '否'
      : typeof value.value === 'string'
        ? value.value
        : '结构化状态';
  return unit ? `${formatted} ${unit}` : formatted;
}

function formatTelemetryQuality(quality: TelemetryQuality): string {
  switch (quality) {
    case 'GOOD':
      return '良好';
    case 'PARTIAL':
      return '部分可信';
    case 'ESTIMATED':
      return '估算';
    case 'MANUAL':
      return '人工录入';
    case 'STALE':
      return '陈旧';
    case 'INVALID':
      return '无效';
  }
}

function feedbackPresentation(
  target: RegisteredControlTarget | null,
  snapshot: DeviceObservationSnapshot | undefined,
  pending: boolean,
  error: boolean,
  canReadTelemetry: boolean,
  timeZone: string,
): FeedbackPresentation {
  if (!target) return { value: '未选择', detail: '请选择控制目标', state: 'missing' };
  if (!canReadTelemetry) return { value: '不可读取', detail: '当前账号没有读取实时反馈的权限', state: 'unavailable' };
  if (pending) return { value: '正在读取', detail: '正在读取该目标的反馈点', state: 'pending' };
  if (error || !snapshot) return { value: '暂不可用', detail: '实时反馈读取失败；不会用登记值或默认值替代', state: 'unavailable' };
  if (snapshot.deviceId !== target.device.id || snapshot.siteId !== target.device.siteId || snapshot.tenantId !== target.device.tenantId) {
    return { value: '不可用', detail: '反馈范围与当前控制目标不一致', state: 'unavailable' };
  }
  const value = snapshot.values.find((candidate) => candidate.key === target.feedbackPoint.sourceKey);
  if (!value || value.state === 'MISSING') {
    return { value: '未观测', detail: '当前反馈点没有可用观测值；缺失不按 0 处理', state: 'missing' };
  }
  return {
    value: formatTelemetryValue(value, target.feedbackPoint.unit),
    detail: `${formatInstant(value.sampledAt, timeZone)} · ${value.freshness === 'FRESH' ? '新鲜' : '已过期'} · 质量${formatTelemetryQuality(value.quality)}`,
    state: value.freshness === 'FRESH' && value.quality === 'GOOD' ? 'ready' : 'unavailable',
  };
}

function LoadingWorkspace() {
  return (
    <div className="mx-auto flex w-full max-w-[1600px] flex-col gap-5">
      <Skeleton className="h-20 w-full max-w-xl" />
      <Skeleton className="h-24 w-full" />
      <div className="grid gap-4 xl:grid-cols-[minmax(420px,0.9fr)_minmax(0,1.4fr)]">
        <Skeleton className="h-[560px] w-full" />
        <Skeleton className="h-[560px] w-full" />
      </div>
    </div>
  );
}

export function ControlCenter({
  site,
  principal,
  runtime,
  searchState,
  onSearchChange,
}: ControlCenterProps) {
  const queryClient = useQueryClient();
  const platformClient = useMemo(() => createPlatformGatewayClient(), []);
  const telemetryClient = useMemo(() => createS2TelemetryClient(), []);
  const tenantId = principal.context.tenantId;
  const queryPrefix = useMemo(
    () => ['control-center', principal.session.id, principal.authorization.policyRevision, tenantId, site.id] as const,
    [principal.authorization.policyRevision, principal.session.id, site.id, tenantId],
  );
  const capabilities = useMemo(() => new Set(principal.authorization.capabilities), [principal.authorization.capabilities]);
  const canReadRegistry = capabilities.has('asset.list') && capabilities.has('device.list');
  const canReadTelemetry = capabilities.has('telemetry.snapshot.read');

  const purge = useCallback(async () => {
    await queryClient.cancelQueries({ queryKey: queryPrefix });
    queryClient.removeQueries({ queryKey: queryPrefix });
  }, [queryClient, queryPrefix]);

  useEffect(() => runtime.registerProtectedResource({
    id: `control-center-cache:${tenantId}:${site.id}`,
    kind: 'query-cache',
    purge,
  }), [purge, runtime, site.id, tenantId]);

  const registryQuery = useQuery({
    queryKey: [...queryPrefix, 'asset-model'],
    queryFn: async ({ signal }) => (await platformClient.getSiteAssetModel(site.id, { signal })).data,
    enabled: canReadRegistry,
    staleTime: 60_000,
    refetchOnMount: false,
    refetchOnWindowFocus: false,
  });

  const projection = useMemo(
    () => registryQuery.data ? projectRegisteredControlTargets(registryQuery.data) : { targets: [], incompleteRegistrationCount: 0 },
    [registryQuery.data],
  );
  const selected = useMemo(() => {
    const exact = searchState.target
      ? projection.targets.find((target) => target.key === searchState.target)
      : undefined;
    return exact ?? projection.targets[0] ?? null;
  }, [projection.targets, searchState.target]);

  useEffect(() => {
    if (!selected || searchState.target === selected.key) return;
    onSearchChange({ target: selected.key });
  }, [onSearchChange, searchState.target, selected]);

  const feedbackQuery = useQuery({
    queryKey: [...queryPrefix, 'feedback', selected?.device.id, selected?.feedbackPoint.sourceKey],
    queryFn: ({ signal }) => telemetryClient.getDeviceObservationSnapshot(
      selected!.device.id,
      [selected!.feedbackPoint.sourceKey],
      { signal },
    ),
    enabled: Boolean(selected && canReadTelemetry),
    staleTime: 5_000,
    refetchInterval: 15_000,
    retry: false,
  });

  const feedback = feedbackPresentation(
    selected,
    feedbackQuery.data,
    feedbackQuery.isPending && feedbackQuery.fetchStatus !== 'idle',
    feedbackQuery.isError,
    canReadTelemetry,
    site.timezone,
  );

  if (canReadRegistry && registryQuery.isPending) return <LoadingWorkspace />;

  const businessState = !canReadRegistry || registryQuery.isError
    ? 'UNAVAILABLE'
    : projection.targets.length === 0
      ? 'EMPTY'
      : 'READY';

  return (
    <Main
      className="space-y-5"
      data-testid="control-center"
      data-business-state={businessState}
      data-command-state="BLOCKED_PREFLIGHT_UNAVAILABLE"
    >
      <PageIntro
        context={`${site.displayName} · ${site.timezone}`}

        actions={(
          <Button
            variant="outline"
            size="sm"
            disabled={!canReadRegistry || registryQuery.isFetching}
            onClick={() => void registryQuery.refetch()}
          >
            <RefreshCw className={cn(registryQuery.isFetching && 'animate-spin')} />
            刷新控制状态
          </Button>
        )}
      />

      <Alert data-testid="control-safety-boundary" className="border-amber-500/30 bg-amber-500/5 rounded-xl shadow-xs">
        <ShieldAlert className="text-amber-700 dark:text-amber-400 size-4 mt-0.5" />
        <AlertTitle className="font-semibold text-sm text-foreground">控制操作暂不可用</AlertTitle>
        <AlertDescription className="text-xs leading-5 text-muted-foreground">
          当前可查看已登记目标和实时反馈，但执行前所需事实尚未完整提供。本页保持只读，不会用在线、可写或反馈正常替代控制许可。
        </AlertDescription>
      </Alert>

      {!canReadRegistry ? (
        <Alert variant="destructive">
          <CircleAlert />
          <AlertTitle>控制目标不可见</AlertTitle>
          <AlertDescription>当前账号没有读取资产与设备登记信息的权限，因此无法确认哪些对象已登记为控制目标。</AlertDescription>
        </Alert>
      ) : registryQuery.isError ? (
        <Alert variant="destructive">
          <CircleAlert />
          <AlertTitle>控制目标暂不可用</AlertTitle>
          <AlertDescription>控制对象登记信息读取失败。本页不会根据可写属性、在线状态或演示数据生成控制目标。</AlertDescription>
        </Alert>
      ) : projection.targets.length === 0 ? (
        <div className="grid min-h-[360px] place-items-center rounded-xl border border-dashed text-center">
          <div className="max-w-md px-6">
            <SlidersHorizontal className="mx-auto size-8 text-muted-foreground" />
            <h2 className="mt-3 text-sm font-medium">当前没有完整登记的控制目标</h2>
            <p className="mt-1 text-xs leading-5 text-muted-foreground">控制目标必须有明确的控制关系、控制能力和反馈点；设备在线或点位可写本身不足以生成控制操作。</p>
          </div>
        </div>
      ) : selected ? (
        <>
          <div className="grid min-w-0 items-start gap-5 xl:grid-cols-[minmax(320px,0.36fr)_minmax(0,0.64fr)]">
            <div className="min-w-0 space-y-4">
              <section className="min-w-0 overflow-hidden rounded-xl border bg-card shadow-xs" data-testid="control-targets" aria-labelledby="control-targets-title">
                <div className="flex items-start justify-between gap-3 border-b px-4 py-3.5">
                  <div className="flex items-center gap-2.5">
                    <div className="flex size-7 items-center justify-center rounded-md bg-primary/10 text-primary">
                      <SlidersHorizontal className="size-3.5" aria-hidden="true" />
                    </div>
                    <div>
                      <h2 id="control-targets-title" className="text-sm font-semibold">控制目标</h2>
                    </div>
                  </div>
                  <Badge variant="outline" className="text-xs font-normal">{projection.targets.length} 项</Badge>
                </div>
                <ItemGroup className="divide-y" aria-label="已登记控制目标">
                  {projection.targets.map((target) => {
                    const isSelected = target.key === selected.key;
                    return (
                      <Item
                        key={target.key}
                        asChild
                        size="sm"
                        className={cn(
                          'rounded-none px-4 py-3 hover:bg-muted/40',
                          isSelected && 'bg-muted/60 hover:bg-muted/60',
                        )}
                      >
                        <button
                          type="button"
                          aria-pressed={isSelected}
                          onClick={() => onSearchChange({ target: target.key })}
                        >
                          <ItemContent>
                            <ItemTitle>{target.asset.displayName}</ItemTitle>
                            <ItemDescription>
                              {controlCapabilityLabel(target.capability)} · 反馈：{target.feedbackPoint.displayName}
                            </ItemDescription>
                          </ItemContent>
                          <ItemActions>
                            <StatusBadge tone="neutral" label="待确认" />
                          </ItemActions>
                        </button>
                      </Item>
                    );
                  })}
                </ItemGroup>
              </section>
            </div>

            <div className="min-w-0 space-y-4">
              <section className="min-w-0 overflow-hidden rounded-xl border bg-card shadow-xs" data-testid="control-target-detail" aria-labelledby="control-target-title">
                <div className="border-b px-5 py-4">
                  <div className="flex flex-wrap items-center gap-2">
                    <Badge variant="secondary" className="text-xs font-medium">已登记</Badge>
                    <Badge variant="outline" className="text-xs font-medium border-amber-500/40 text-amber-600 dark:text-amber-400 bg-amber-500/5">当前不可执行</Badge>
                  </div>
                  <h2 id="control-target-title" className="mt-2.5 text-base font-semibold text-foreground">{selected.asset.displayName} · {controlCapabilityLabel(selected.capability)}</h2>
                  <p className="mt-1 text-xs text-muted-foreground">{selected.commandPoint.displayName} · 反馈：{selected.feedbackPoint.displayName}</p>
                </div>

                <div className="grid border-b sm:grid-cols-2 xl:grid-cols-[1.2fr_0.8fr_0.8fr]" data-testid="control-current-state">
                  <div className="p-4 sm:col-span-2 xl:col-span-1 bg-muted/15">
                    <div className="text-xs font-medium text-muted-foreground">当前反馈</div>
                    <strong className="mt-1.5 block text-2xl font-bold tracking-tight text-foreground tabular-nums">{feedback.value}</strong>
                    <div className="mt-1 text-xs leading-5 text-muted-foreground">{feedback.detail}</div>
                  </div>
                  <div className="border-t p-4 sm:border-r xl:border-l xl:border-t-0 xl:border-r-0">
                    <div className="text-xs font-medium text-muted-foreground">当前控制来源</div>
                    <div className="mt-1.5 text-sm font-semibold">未知</div>
                  </div>
                  <div className="border-t p-4 xl:border-l xl:border-t-0">
                    <div className="text-xs font-medium text-muted-foreground">临时覆盖</div>
                    <div className="mt-1.5 text-sm font-semibold">未知</div>
                  </div>
                  <div className="border-t p-4">
                    <div className="text-xs font-medium text-muted-foreground">当前模式</div>
                    <div className="mt-1.5 text-sm font-semibold">未提供</div>
                  </div>
                  <div className="border-t p-4 sm:border-l">
                    <div className="text-xs font-medium text-muted-foreground">当前计划</div>
                    <div className="mt-1.5 text-sm font-semibold">未提供</div>
                  </div>
                  <div className="border-t p-4 xl:border-l">
                    <div className="text-xs font-medium text-muted-foreground">允许范围</div>
                    <div className="mt-1.5 text-sm font-semibold tabular-nums">{controlCapabilityRange(selected) ?? '无需数值参数'}</div>
                  </div>
                </div>

                <section className="border-b px-5 py-4" data-testid="control-preflight-status" aria-labelledby="control-preflight-title">
                  <div>
                    <h3 id="control-preflight-title" className="text-sm font-semibold">执行前检查</h3>
                    <p className="mt-0.5 text-xs text-muted-foreground">每项事实独立判断；未提供的状态保持未知。</p>
                  </div>
                  <div className="mt-3.5 grid overflow-hidden rounded-lg border sm:grid-cols-2">
                    {UNKNOWN_PREFLIGHT_FACTS.map(([label, detail], index) => (
                      <div key={label} className={cn('p-3 hover:bg-muted/20 transition-colors', index >= 2 && 'border-t', index % 2 === 1 && 'sm:border-l')}>
                        <div className="flex items-center justify-between gap-3">
                          <strong className="text-xs font-semibold text-foreground">{label}</strong>
                          <span className="rounded bg-muted px-1.5 py-0.5 text-[11px] font-medium text-muted-foreground">未知</span>
                        </div>
                        <span className="mt-1 block text-[11px] leading-4 text-muted-foreground">{detail}</span>
                      </div>
                    ))}
                  </div>
                </section>

                <details className="px-5 py-3.5 group">
                  <summary className="w-fit cursor-pointer text-xs font-medium text-muted-foreground hover:text-foreground">已确认的登记事实</summary>
                  <dl className="mt-3 grid gap-x-6 gap-y-3 text-xs sm:grid-cols-2 xl:grid-cols-4 rounded-lg bg-muted/20 p-3 border">
                    <div><dt className="text-muted-foreground">控制能力</dt><dd className="mt-0.5 font-semibold text-foreground">{controlCapabilityLabel(selected.capability)}</dd></div>
                    <div><dt className="text-muted-foreground">设备</dt><dd className="mt-0.5 font-semibold text-foreground">{selected.device.displayName}</dd></div>
                    <div><dt className="text-muted-foreground">命令点</dt><dd className="mt-0.5 font-semibold text-foreground">{selected.commandPoint.displayName}</dd></div>
                    <div><dt className="text-muted-foreground">反馈点</dt><dd className="mt-0.5 font-semibold text-foreground">{selected.feedbackPoint.displayName}</dd></div>
                  </dl>
                </details>
              </section>
            </div>
          </div>

          <div className="grid gap-4 xl:grid-cols-[minmax(0,0.95fr)_minmax(0,1.05fr)]">
            <section className="rounded-xl border bg-card p-4 sm:p-5 shadow-xs" data-testid="control-priority-attention" aria-labelledby="control-priority-title">
              <h2 id="control-priority-title" className="text-sm font-semibold">需要关注</h2>
              <ul className="mt-3 divide-y divide-border/60 text-sm">
                {projection.incompleteRegistrationCount > 0 ? (
                  <li className="py-2.5 first:pt-0" data-testid="control-registration-warning">
                    <strong className="font-semibold text-xs sm:text-sm text-foreground">{projection.incompleteRegistrationCount} 项控制登记信息不完整</strong>
                    <p className="mt-0.5 text-xs leading-5 text-muted-foreground">尚不能执行，需要核对控制关系、能力配置和反馈点。</p>
                  </li>
                ) : null}
                <li className="py-2.5 first:pt-0">
                  <strong className="font-semibold text-xs sm:text-sm text-foreground">当前控制来源尚未提供</strong>
                  <p className="mt-0.5 text-xs leading-5 text-muted-foreground">无法确认当前控制权归属，也不会按设备在线状态推断。</p>
                </li>
                <li className="py-2.5 last:pb-0">
                  <strong className="font-semibold text-xs sm:text-sm text-foreground">临时覆盖状态尚未提供</strong>
                  <p className="mt-0.5 text-xs leading-5 text-muted-foreground">不能确认是否存在人工或其他来源覆盖。</p>
                </li>
              </ul>
            </section>

            <section className="rounded-xl border bg-card p-4 sm:p-5 shadow-xs" data-testid="control-recent-executions" aria-labelledby="control-recent-title">
              <h2 id="control-recent-title" className="text-sm font-semibold">最近相关执行</h2>
              <div className="mt-3.5 rounded-lg border border-dashed bg-muted/20 px-4 py-5">
                <div className="text-sm font-semibold text-foreground">当前没有可展示的近期执行记录</div>
                <p className="mt-1 text-xs leading-5 text-muted-foreground">不会根据命令点、ACK 或本地历史推断执行结果；只有可验证的执行记录才会显示在这里。</p>
              </div>
            </section>
          </div>

          <div className="flex flex-wrap gap-2 border-t pt-4">
            <Button variant="outline" size="sm" asChild><a href={siteRoute(site, 'devices')}>查看设备与建筑</a></Button>
          </div>
        </>
      ) : null}
    </Main>
  );
}
