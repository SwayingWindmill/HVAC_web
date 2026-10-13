import { FactList, FactRow } from '@/blocks/fact-list';
import { useMutation, useQuery } from '@tanstack/react-query';
import { getRouteApi } from '@tanstack/react-router';
import { Lightbulb, Send, ShieldCheck, Sparkles, TriangleAlert, Undo2 } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import { toast } from 'sonner';
import { MetricCard, MetricGrid, MetricValue } from '@/blocks/metric-card';
import { PageHeader } from '@/blocks/page-header';
import { Main } from '@/components/layout/Main';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardAction, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from '@/components/ui/card';
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from '@/components/ui/empty';
import { Skeleton } from '@/components/ui/skeleton';
import { Spinner } from '@/components/ui/spinner';
import { ConfirmCommand, useSubmitCommand, type PendingCommand } from '@/features/control/components/CommandConfirmation';
import { controlTargets, type ControlSetting, type ControlTarget } from '@/features/control/control-model';
import { useRealtimePlant } from '@/features/operations/realtime/use-realtime-plant';
import { useNow } from '@/hooks/use-now';
import { formatDecimal, formatTime } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import { changesSupply, recommendedSetpoint, TRIGGER_LABEL, type OptimizationRecommendation, type RecommendationRisk } from '../optimization-model';
import { latestRecommendationQuery, OptimizationRequestError, requestRecommendation } from '../query';

const siteRoute = getRouteApi('/_app/_site');

const RISK: Record<RecommendationRisk, { label: string; className: string }> = {
  LOW: { label: '低风险', className: 'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300' },
  MEDIUM: { label: '中风险', className: 'border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300' },
  HIGH: { label: '高风险', className: 'border-destructive/30 bg-destructive/10 text-destructive' },
};

// A requested run publishes within seconds; past this the page stops waiting and says so.
const RUN_PATIENCE_MS = 90_000;

const celsius = (value: number) => `${formatDecimal(value, 1)} °C`;

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
    <figure className="flex flex-col gap-2" aria-label={summary}>
      <figcaption className="flex items-baseline justify-between gap-3 text-sm">
        <span className="font-medium">{label}</span>
        <span className="text-xs text-muted-foreground">{bandLabel} {formatDecimal(band[0], 1)}–{celsius(band[1])}</span>
      </figcaption>
      {/* The proposal is labelled above the track and the present value below, so close values never collide. */}
      <div className="relative h-16" aria-hidden="true">
        <div className="absolute inset-x-0 top-6 h-2 rounded-full bg-muted" />
        <div className="absolute top-6 h-2 rounded-full bg-primary/25" style={{ left: at(band[0]), width: `calc(${at(band[1])} - ${at(band[0])})` }} />
        {markers.map((marker) => (
          <div
            key={marker.label}
            className={cn('absolute flex -translate-x-1/2 items-center', marker.emphasis ? 'top-0 flex-col' : 'top-[18px] flex-col-reverse')}
            style={{ left: at(marker.value) }}
          >
            <span className={cn('whitespace-nowrap text-[11px] leading-4', marker.emphasis ? 'font-semibold text-primary' : 'text-muted-foreground')}>
              {marker.label} {formatDecimal(marker.value, 1)}
            </span>
            <span className={cn('size-3.5 rounded-full border-2 border-background shadow-sm', marker.emphasis ? 'mt-0.5 bg-primary' : 'mb-0.5 bg-muted-foreground')} />
          </div>
        ))}
        <div className="absolute inset-x-0 top-12 flex justify-between text-[11px] tabular-nums text-muted-foreground/70">
          <span>{axis[0]}</span>
          <span>{axis[1]} °C</span>
        </div>
      </div>
    </figure>
  );
}

function supplySetpoint(targets: readonly ControlTarget[]): { target: ControlTarget; setting: ControlSetting } | null {
  for (const target of targets) {
    const setting = target.settings.find((candidate) => candidate.capability === 'SET_CHILLED_WATER_TEMPERATURE_SETPOINT');
    if (setting) return { target, setting };
  }
  return null;
}

function RecommendationHero({ recommendation, timeZone, setpoint, dispatchable, onDispatch }: {
  readonly recommendation: OptimizationRecommendation;
  readonly timeZone: string;
  readonly setpoint: number | null;
  readonly dispatchable: boolean;
  readonly onDispatch: () => void;
}) {
  const { baseline, candidate, comfortRange } = recommendation;
  const change = changesSupply(recommendation);
  const risk = RISK[recommendation.risk];
  return (
    <Card role="region" aria-label="当前建议" className="overflow-hidden">
      <CardHeader>
        <CardDescription className="flex items-center gap-2">
          <Lightbulb className="size-4 text-primary" />
          冷冻水供水温度建议 · {TRIGGER_LABEL[recommendation.trigger]}于 {formatTime(recommendation.createdAt, timeZone)}
        </CardDescription>
        <CardTitle className="flex flex-wrap items-baseline gap-x-3 gap-y-1 text-3xl font-semibold tabular-nums">
          {change ? (
            <>
              <span className="text-muted-foreground">{formatDecimal(baseline.supplyTempC, 1)}</span>
              <span aria-label="调整为" className="text-muted-foreground">→</span>
              <span className="text-primary">{formatDecimal(candidate.supplyTempC, 1)}</span>
              <span className="text-base font-normal text-muted-foreground">°C</span>
            </>
          ) : (
            <>
              保持 {formatDecimal(baseline.supplyTempC, 1)}
              <span className="text-base font-normal text-muted-foreground">°C</span>
            </>
          )}
        </CardTitle>
        <CardAction>
          <Badge variant="outline" className={risk.className}>{risk.label}</Badge>
        </CardAction>
      </CardHeader>
      <CardContent className="text-sm text-muted-foreground">
        {change
          ? `供水温度${candidate.supplyTempC > baseline.supplyTempC ? '升高' : '降低'}后，预计室内温度 ${celsius(candidate.projectedZoneTempC)}，仍在 ${formatDecimal(comfortRange.minC, 0)}–${formatDecimal(comfortRange.maxC, 0)} °C 舒适范围内，距边界还有 ${celsius(recommendation.comfortMarginC)}。`
          : '在安全与舒适约束内，当前供水温度已是最省电的选择。'}
      </CardContent>
      {change ? (
        <CardFooter className="flex flex-wrap items-center justify-between gap-3 border-t bg-muted/30">
          <p className="text-xs text-muted-foreground">
            {dispatchable && setpoint !== null
              ? `按冷机设定步长下发为 ${celsius(setpoint)}；建议不会自动执行，确认后作为控制指令提交。`
              : '这台冷机没有登记供水温度设定点，无法从这里下发。'}
          </p>
          <Button size="sm" disabled={!dispatchable || setpoint === null} onClick={onDispatch}>
            <Send data-icon="inline-start" />
            按建议下发
          </Button>
        </CardFooter>
      ) : null}
    </Card>
  );
}

export function OptimizationWorkspace() {
  const { site, principal } = siteRoute.useRouteContext();
  const now = useNow(30_000);
  const [waiting, setWaiting] = useState<{ readonly runId: string; readonly since: number } | null>(null);
  const [notice, setNotice] = useState<{ readonly title: string; readonly detail: string } | null>(null);
  const [pending, setPending] = useState<PendingCommand | null>(null);
  const latest = useQuery(latestRecommendationQuery(site.id, waiting?.runId ?? null));
  const { plant, registry } = useRealtimePlant();
  const submit = useSubmitCommand(site.id, principal.session.csrfToken);
  const recommendation = latest.data ?? null;

  const request = useMutation({
    mutationFn: () => requestRecommendation(site.id, principal.session.csrfToken),
    onMutate: () => setNotice(null),
    onSuccess: ({ runId }) => setWaiting({ runId, since: Date.now() }),
    onError: (error) => setNotice({
      title: '没有生成建议',
      detail: error instanceof OptimizationRequestError ? error.message : '优化服务暂时不可用',
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
  const dispatch = () => {
    if (!supply || setpoint === null) return;
    setPending({
      target: supply.target, label: supply.setting.label, pointId: supply.setting.pointId,
      parameters: { [supply.setting.parameterKey]: setpoint },
      change: { from: supplyReading, to: setpoint, unit: supply.setting.unit },
    });
  };

  const busy = request.isPending || waiting !== null;
  const share = recommendation && recommendation.baseline.dailyEnergyKWh > 0
    ? recommendation.saving.energyKWhPerDay / recommendation.baseline.dailyEnergyKWh
    : null;

  return (
    <Main>
      <PageHeader
        title="优化建议"
        description={`${site.displayName} · 在安全与舒适约束内，寻找更省电的冷冻水供水温度`}
        actions={
          <Button size="sm" disabled={busy} onClick={() => request.mutate()}>
            {busy ? <Spinner data-icon="inline-start" /> : <Sparkles data-icon="inline-start" />}
            {busy ? '正在计算' : '生成建议'}
          </Button>
        }
      />

      {notice ? (
        <Alert>
          <TriangleAlert />
          <AlertTitle>{notice.title}</AlertTitle>
          <AlertDescription>{notice.detail}</AlertDescription>
        </Alert>
      ) : null}

      {latest.isPending ? (
        <Skeleton className="h-64 rounded-xl" />
      ) : latest.isError ? (
        <p className="rounded-xl border py-16 text-center text-sm text-muted-foreground">优化建议暂不可用</p>
      ) : !recommendation ? (
        <Empty className="rounded-xl border">
          <EmptyHeader>
            <EmptyMedia variant="icon"><Lightbulb /></EmptyMedia>
            <EmptyTitle>还没有优化建议</EmptyTitle>
            <EmptyDescription>
              建议依据最近一天的冷站用电与电费、最新负荷预测和实时供水、室内温度计算；用电与电费每天零点后结算，某天缺数据时不出电费，改用更早一天。
            </EmptyDescription>
          </EmptyHeader>
          <EmptyContent>
            <Button size="sm" disabled={busy} onClick={() => request.mutate()}>
              {busy ? <Spinner data-icon="inline-start" /> : <Sparkles data-icon="inline-start" />}
              生成建议
            </Button>
          </EmptyContent>
        </Empty>
      ) : (
        <>
          <RecommendationHero
            recommendation={recommendation}
            timeZone={site.timezone}
            setpoint={setpoint}
            dispatchable={supply !== null}
            onDispatch={dispatch}
          />

          <MetricGrid ariaLabel="预计效果">
            <MetricCard
              label="预计日节电"
              value={<MetricValue value={formatDecimal(recommendation.saving.energyKWhPerDay, 0)} unit="kWh" />}
              lead={share !== null ? `约占最近一天冷站用电 ${formatDecimal(share * 100, 1)}%` : '最近一天冷站用电为零'}
              detail={`90% 把握在 ±${formatDecimal(recommendation.uncertainty.energyP90KWh, 0)} kWh 内`}
            />
            <MetricCard
              label="预计日节省"
              value={<MetricValue value={formatDecimal(recommendation.saving.costPerDay, 0)} unit="元" />}
              lead={`按最近一天电费 ${formatDecimal(recommendation.baseline.dailyCost, 0)} 元折算`}
              detail="按当天峰谷均价折算，不含需量电费"
            />
            <MetricCard
              label="预计室内温度"
              value={<MetricValue value={formatDecimal(recommendation.candidate.projectedZoneTempC, 1)} unit="°C" />}
              lead={`当前 ${celsius(recommendation.baseline.zoneTempC)}`}
              detail={`90% 把握在 ±${formatDecimal(recommendation.uncertainty.zoneTempP90C, 1)} °C 内`}
            />
            <MetricCard
              label="舒适余量"
              value={<MetricValue value={formatDecimal(recommendation.comfortMarginC, 1)} unit="°C" />}
              lead={`距舒适范围 ${formatDecimal(recommendation.comfortRange.minC, 0)}–${formatDecimal(recommendation.comfortRange.maxC, 0)} °C 边界`}
              detail="已扣除室内温度的不确定性"
            />
          </MetricGrid>

          <div className="grid items-start gap-4 md:gap-6 @4xl/main:grid-cols-2">
            <Card role="region" aria-label="约束">
              <CardHeader>
                <CardTitle className="flex items-center gap-2"><ShieldCheck className="size-4 text-primary" />安全与舒适约束</CardTitle>
                <CardDescription>建议只在两条范围之内取值，每次调整不超过 {celsius(recommendation.supplyRange.maxStepC)}</CardDescription>
              </CardHeader>
              <CardContent className="flex flex-col gap-6">
                <RangeTrack
                  label="冷冻水供水温度"
                  axis={[Math.floor(recommendation.supplyRange.minC) - 1, Math.ceil(recommendation.supplyRange.maxC) + 1]}
                  band={[recommendation.supplyRange.minC, recommendation.supplyRange.maxC]}
                  bandLabel="安全范围"
                  markers={changesSupply(recommendation)
                    ? [{ value: recommendation.baseline.supplyTempC, label: '当前' }, { value: recommendation.candidate.supplyTempC, label: '建议', emphasis: true }]
                    : [{ value: recommendation.baseline.supplyTempC, label: '当前', emphasis: true }]}
                />
                <RangeTrack
                  label="室内温度"
                  axis={[Math.floor(recommendation.comfortRange.minC) - 2, Math.ceil(recommendation.comfortRange.maxC) + 2]}
                  band={[recommendation.comfortRange.minC, recommendation.comfortRange.maxC]}
                  bandLabel="舒适范围"
                  markers={changesSupply(recommendation)
                    ? [{ value: recommendation.baseline.zoneTempC, label: '当前' }, { value: recommendation.candidate.projectedZoneTempC, label: '预计', emphasis: true }]
                    : [{ value: recommendation.baseline.zoneTempC, label: '当前', emphasis: true }]}
                />
              </CardContent>
            </Card>

            <div className="flex flex-col gap-4 md:gap-6">
              <Card role="region" aria-label="计算依据">
                <CardHeader>
                  <CardTitle>计算依据</CardTitle>
                  <CardDescription>生成建议时冻结的输入</CardDescription>
                </CardHeader>
                <CardContent>
                  <FactList>
                    <FactRow label="最近一天冷站用电">{formatDecimal(recommendation.baseline.dailyEnergyKWh, 0)} kWh</FactRow>
                    <FactRow label="最近一天电费">{formatDecimal(recommendation.baseline.dailyCost, 0)} 元</FactRow>
                    <FactRow label="供水温度">{celsius(recommendation.baseline.supplyTempC)}</FactRow>
                    <FactRow label="室内温度">{celsius(recommendation.baseline.zoneTempC)}</FactRow>
                  </FactList>
                </CardContent>
              </Card>
              <Card role="region" aria-label="验证与回退">
                <CardHeader>
                  <CardTitle className="flex items-center gap-2"><Undo2 className="size-4 text-primary" />验证与回退</CardTitle>
                </CardHeader>
                <CardContent className="flex flex-col gap-2 text-sm text-muted-foreground">
                  <p>下发后观察 {recommendation.verificationMinutes} 分钟：室内温度保持在舒适范围内，冷站负荷不高于调整前。</p>
                  <p>任一条件不满足，将供水温度恢复为 <span className="font-medium text-foreground">{celsius(recommendation.baseline.supplyTempC)}</span>。</p>
                </CardContent>
              </Card>
            </div>
          </div>
        </>
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
