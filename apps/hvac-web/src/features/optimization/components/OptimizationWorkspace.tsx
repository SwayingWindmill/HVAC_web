import { useMutation, useQuery } from '@tanstack/react-query';
import { getRouteApi, Link } from '@tanstack/react-router';
import { ArrowRight, ChevronRight, Lightbulb, Send, ShieldCheck, Sparkles, TriangleAlert, Undo2 } from 'lucide-react';
import { useEffect, useMemo, useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import { GatewayRequestError } from '@/api/gateway-client';
import { PageHeader } from '@/blocks/page-header';
import { Panel, PanelHeader } from '@/blocks/panel';
import { InfoTip } from '@/components/info-tip';
import { Main } from '@/components/layout/Main';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { Spinner } from '@/components/ui/spinner';
import { siteCommandsQuery, type Command } from '@/features/control/command-api';
import { APPROVAL_LABEL, STATUS_PRESENTATION } from '@/features/control/command-presentation';
import { CommandMark } from '@/features/control/components/CommandMark';
import { ConfirmCommand, useSubmitCommand, type PendingCommand } from '@/features/control/components/CommandConfirmation';
import { controlTargets, type ControlSetting, type ControlTarget } from '@/features/control/control-model';
import { useRealtimePlant } from '@/features/operations/realtime/use-realtime-plant';
import { useNow } from '@/hooks/use-now';
import { formatDecimal, formatTime } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import { changesSupply, preparationProblem, recommendedSetpoint, TRIGGER_LABEL, type OptimizationRecommendation, type RecommendationRisk } from '../optimization-model';
import { latestPreparationQuery, latestRecommendationQuery, requestRecommendation } from '../query';

const siteRoute = getRouteApi('/_app/_site');

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
  const now = useNow(30_000);
  const [waiting, setWaiting] = useState<{ readonly runId: string; readonly since: number } | null>(null);
  const [notice, setNotice] = useState<{ readonly title: string; readonly detail: string } | null>(null);
  const [pending, setPending] = useState<PendingCommand | null>(null);
  const latest = useQuery(latestRecommendationQuery(site.id, waiting?.runId ?? null));
  const commands = useQuery(siteCommandsQuery(site.id));
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
      ) : (
        <Proposal
          recommendation={recommendation}
          timeZone={site.timezone}
          siteId={site.id}
          setpoint={setpoint}
          setting={supply?.setting ?? null}
          sent={sent}
          onDispatch={dispatch}
        />
      )}

      <ConfirmCommand
        pending={pending}
        submitting={submit.isPending}
        onCancel={() => setPending(null)}
        onConfirm={() => { if (pending) submit.mutate(pending, { onSettled: () => setPending(null) }); }}
      />
    </Main>
  );
}

function Proposal({ recommendation, timeZone, siteId, setpoint, setting, sent, onDispatch }: {
  readonly recommendation: OptimizationRecommendation;
  readonly timeZone: string;
  readonly siteId: string;
  readonly setpoint: number | null;
  readonly setting: ControlSetting | null;
  readonly sent: Command | undefined;
  readonly onDispatch: () => void;
}) {
  const { baseline, candidate, comfortRange, supplyRange } = recommendation;
  const change = changesSupply(recommendation);
  const risk = RISK[recommendation.risk];
  const share = baseline.dailyEnergyKWh > 0 ? recommendation.saving.energyKWhPerDay / baseline.dailyEnergyKWh : null;
  const stepNote = setting === null
    ? '这台冷机没有登记供水温度设定点，无法从这里下发'
    : setpoint !== null && Math.abs(setpoint - candidate.supplyTempC) >= 0.05
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
                    {setting?.label} → {setpoint === null ? '—' : celsius(setpoint)} · <span className="font-mono">{formatTime(sent.createdAt, timeZone).slice(-5)}</span>
                    {' · '}{APPROVAL_LABEL[sent.approvalPolicy]}{sent.requiredApprovalCount > 0 ? ` ${sent.approvalCount} / ${sent.requiredApprovalCount}` : ''}
                  </span>
                </span>
                <span className="flex items-center text-xs font-medium text-primary">查看指令<ChevronRight className="size-3.5" aria-hidden="true" /></span>
              </Link>
            ) : (
              <div className="flex flex-wrap items-center gap-3">
                <Button disabled={setting === null || setpoint === null} onClick={onDispatch}>
                  <Send data-icon="inline-start" />
                  按建议下发
                </Button>
                <span className="text-xs text-muted-foreground">{stepNote}</span>
              </div>
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

      <div className="grid items-start gap-[18px] grid-cols-2">
        <Panel aria-label="计算依据">
          <PanelHeader
            title={<span className="flex items-center gap-1.5">计算依据<InfoTip>标「人工设定」的响应系数由人在优化策略里填写，不是从站点运行数据拟合的</InfoTip></span>}
            caption="生成建议时用到的数据"
          />
          <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-6 px-5 py-2">
            {([
              ['最近一天冷站用电', `${formatDecimal(baseline.dailyEnergyKWh, 0)} kWh`],
              ['最近一天电费', `¥${formatDecimal(baseline.dailyCost, 0)} · 按当天峰谷均价`],
              ['供水温度', celsius(baseline.supplyTempC)],
              ['室内温度', celsius(baseline.zoneTempC)],
              ['安全范围', `${formatDecimal(supplyRange.minC, 1)}–${celsius(supplyRange.maxC)}`],
              ['舒适范围', `${formatDecimal(comfortRange.minC, 0)}–${formatDecimal(comfortRange.maxC, 0)} °C`],
              ['供水每升高 1 °C 日用电', `${signed(recommendation.response.energyKWhPerSupplyC, 0)} kWh · 人工设定`],
              ['供水每升高 1 °C 室内温度', `${signed(recommendation.response.zoneCPerSupplyC, 1)} °C · 人工设定`],
            ] as const).map(([label, value]) => (
              <div key={label} className="col-span-2 grid grid-cols-subgrid border-b border-divider py-2 last:border-b-0">
                <dt className="text-xs leading-5 text-muted-foreground">{label}</dt>
                <dd className="text-sm font-medium tabular-nums">{value}</dd>
              </div>
            ))}
          </dl>
        </Panel>
        <Panel aria-label="验证与回退">
          <PanelHeader title={<span className="flex items-center gap-1.5"><Undo2 className="size-4 text-primary" aria-hidden="true" />验证与回退</span>} />
          <ul className="flex flex-col gap-3 px-5 py-4">
            <li className="flex items-start gap-2.5">
              <ShieldCheck className="mt-0.5 size-4 shrink-0 text-success" aria-hidden="true" />
              <span className="flex flex-col">
                <span className="text-sm font-medium">室内温度保持在 {formatDecimal(comfortRange.minC, 0)}–{formatDecimal(comfortRange.maxC, 0)} °C 舒适范围内</span>
                <span className="text-xs text-muted-foreground">预计 {celsius(candidate.projectedZoneTempC)}，距边界 {celsius(recommendation.comfortMarginC)}</span>
              </span>
            </li>
            <li className="flex items-start gap-2.5">
              <ShieldCheck className="mt-0.5 size-4 shrink-0 text-success" aria-hidden="true" />
              <span className="flex flex-col">
                <span className="text-sm font-medium">冷站负荷不高于调整前</span>
                <span className="text-xs text-muted-foreground">调整前最近一天冷站用电 {formatDecimal(baseline.dailyEnergyKWh, 0)} kWh</span>
              </span>
            </li>
          </ul>
          <p className="flex items-start gap-2 border-t border-divider bg-muted/40 px-5 py-3 text-sm text-secondary-foreground">
            <Undo2 className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
            <span>下发后观察 {recommendation.verificationMinutes} 分钟；任一条件不满足，将供水温度恢复为 <b className="font-semibold text-foreground">{celsius(baseline.supplyTempC)}</b></span>
          </p>
        </Panel>
      </div>
    </>
  );
}
