import { useQuery } from '@tanstack/react-query';
import { FlaskConical, Gauge, ShieldAlert } from 'lucide-react';
import { useMemo, useState } from 'react';
import { Area, CartesianGrid, ComposedChart, Line, XAxis, YAxis } from 'recharts';
import { Panel, PanelHeader, PanelLink } from '@/blocks/panel';
import { InfoTip } from '@/components/info-tip';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { ChartContainer, ChartTooltip, type ChartConfig } from '@/components/ui/chart';
import { PeriodTabs, type PeriodOption } from '@/components/ui/period-tabs';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { useDeviceNames } from '@/features/assets/use-device-names';
import { forecastPeak, hourlyForecast } from '@/features/load/forecast-model';
import { siteLoadForecastQuery } from '@/features/load/query';
import { formatDecimal, formatTime } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import {
  formatCelsius as celsius,
  formatSigned as signed,
  parseShadowEvaluation,
  type OptimizationRecommendation,
  type OptimizationShadowEvaluation,
  type PublishedShadowChillerFit,
} from '../optimization-model';
import { latestShadowEvaluationQuery } from '../query';

const FORECAST_VIEW_OPTIONS: readonly PeriodOption<'chart' | 'table'>[] = [
  { value: 'chart', label: '曲线' },
  { value: 'table', label: '表格' },
];
const rangeBand = (low: number, high: number): [number, number] => [low, high];

const FORECAST_CHART_CONFIG = {
  loadKW: { label: '预测负荷', color: 'var(--chart-1)' },
  band: { label: '预测范围', color: 'var(--chart-1)' },
} satisfies ChartConfig;

const CHILLER_STATUS_CLASS: Record<PublishedShadowChillerFit['status'], string> = {
  FITTED: 'bg-success-soft text-success-text',
  INSUFFICIENT_DATA: 'bg-warning-soft text-warning-text',
  REJECTED: 'bg-warning-soft text-warning-text',
};

export function OptimizationAnalysisTab({
  siteId,
  timeZone,
  recommendation,
  active,
}: {
  readonly siteId: string;
  readonly timeZone: string;
  readonly recommendation: OptimizationRecommendation | null;
  readonly active: boolean;
}) {
  const nameByDevice = useDeviceNames(siteId);
  const shadowQuery = useQuery(
    latestShadowEvaluationQuery(siteId, recommendation?.runId ?? null, active && recommendation !== null),
  );
  const forecastQuery = useQuery({
    ...siteLoadForecastQuery(siteId),
    enabled: active,
  });

  const shadow = useMemo(
    () => (shadowQuery.data ? parseShadowEvaluation(shadowQuery.data, nameByDevice) : null),
    [shadowQuery.data, nameByDevice],
  );

  return (
    <div className="flex flex-col gap-4">
      <Alert className="border-primary/25 bg-primary-soft/35">
        <ShieldAlert className="text-primary" />
        <AlertTitle>影子对照 · 只记录不发布，不作为控制指令下发</AlertTitle>
        <AlertDescription>
          冷机 COP 模型基于站点近 14 天 15 分钟读数拟合每台冷机的 COP 公式，仅用于对照验证当前策略求解器的设定点建议；本页签不提供远程控制指令或调温工单触发。
        </AlertDescription>
      </Alert>

      {recommendation === null ? (
        <Panel aria-label="求解器对照与候选步长">
          <div className="flex flex-col items-center gap-2 px-6 py-12 text-center">
            <span className="flex size-9 items-center justify-center rounded-xl bg-muted text-muted-foreground">
              <FlaskConical className="size-[18px]" aria-hidden="true" />
            </span>
            <p className="text-sm font-semibold">尚无影子评估记录</p>
            <p className="max-w-lg text-xs text-muted-foreground">
              当前站点尚未生成优化建议；生成优化建议时后台会同步记录冷机 COP 模型的影子评估结果。
            </p>
          </div>
        </Panel>
      ) : shadowQuery.isPending ? (
        <Skeleton className="h-60 rounded-xl" />
      ) : shadowQuery.isError ? (
        <Panel aria-label="求解器对照与候选步长">
          <div className="flex flex-col items-start gap-2 px-5 py-8">
            <p className="text-sm font-semibold">影子评估暂时读不到</p>
            <Button variant="outline" size="sm" onClick={() => void shadowQuery.refetch()}>
              重试
            </Button>
          </div>
        </Panel>
      ) : !shadow ? (
        <Panel aria-label="求解器对照与候选步长">
          <div className="flex flex-col items-center gap-2 px-6 py-12 text-center">
            <span className="flex size-9 items-center justify-center rounded-xl bg-muted text-muted-foreground">
              <FlaskConical className="size-[18px]" aria-hidden="true" />
            </span>
            <p className="text-sm font-semibold">当前建议尚无影子评估记录</p>
            <p className="max-w-lg text-xs text-muted-foreground">
              可能影子求解器已关闭，或本次优化运行未写出影子评估记录。
            </p>
          </div>
        </Panel>
      ) : (
        <>
          <ShadowComparisonPanel
            recommendation={recommendation}
            shadow={shadow}
            timeZone={timeZone}
          />
          {shadow.fit ? <ChillerFitDiagnosticsPanel fit={shadow.fit} timeZone={timeZone} /> : null}
        </>
      )}

      <ForecastSummaryPanel
        siteId={siteId}
        timeZone={timeZone}
        isPending={forecastQuery.isPending}
        isError={forecastQuery.isError}
        forecast={forecastQuery.data ?? null}
        onRetry={() => void forecastQuery.refetch()}
      />
    </div>
  );
}

function ShadowComparisonPanel({
  recommendation,
  shadow,
  timeZone,
}: {
  readonly recommendation: OptimizationRecommendation | null;
  readonly shadow: OptimizationShadowEvaluation;
  readonly timeZone: string;
}) {
  return (
    <Panel aria-label="求解器对照与候选步长">
      <PanelHeader
        title="求解器对照与候选步长"
        caption={`评估于 ${formatTime(shadow.generatedAt, timeZone)}`}
      />

      <div className="grid grid-cols-2 divide-x divide-divider border-b border-divider">
        <div className="flex flex-col gap-1.5 px-5 py-4">
          <span className="text-xs font-medium text-muted-foreground">已发布建议 · 策略求解器</span>
          {recommendation ? (
            <>
              <div className="flex flex-wrap items-baseline gap-2.5">
                <span className="text-2xl font-semibold tracking-[-0.03em] tabular-nums">
                  {celsius(recommendation.candidate.supplyTempC)}
                </span>
                <span className="text-xs text-secondary-foreground tabular-nums">
                  预计日节电 {formatDecimal(recommendation.saving.energyKWhPerDay, 0)} kWh · ¥{formatDecimal(recommendation.saving.costPerDay, 0)}
                  {' '}（P90 ±{formatDecimal(recommendation.uncertainty.energyP90KWh, 0)} kWh）
                </span>
              </div>
              <span className="text-xs text-muted-foreground tabular-nums">
                供水每升 1 °C 日用电：{signed(recommendation.response.energyKWhPerSupplyC, 0)} kWh · 策略人工设定
              </span>
            </>
          ) : (
            <span className="text-sm text-muted-foreground">当前无已发布建议</span>
          )}
        </div>

        <div className="flex flex-col gap-1.5 px-5 py-4">
          <span className="flex items-center gap-2 text-xs font-medium text-muted-foreground">
            <span>影子对照 · 冷机 COP 模型</span>
            <span
              className={cn(
                'rounded px-1.5 py-0.5 text-[11px] leading-4 font-medium',
                shadow.outcome === 'FEASIBLE' ? 'bg-primary-soft text-primary' : 'bg-warning-soft text-warning-text',
              )}
            >
              {shadow.outcomeLabel}
            </span>
          </span>

          {shadow.outcome === 'FEASIBLE' && shadow.summary ? (
            <div className="flex flex-wrap items-baseline gap-2.5">
              <span className="text-2xl font-semibold tracking-[-0.03em] tabular-nums">
                {celsius(shadow.summary.supplyTempC)}
              </span>
              <span className="text-xs text-secondary-foreground tabular-nums">
                预计日节电 {formatDecimal(shadow.summary.energySavingKWhPerDay, 0)} kWh · ¥{formatDecimal(shadow.summary.costSavingPerDay, 0)}
                {' '}（P90 ±{formatDecimal(shadow.summary.energyP90KWh, 0)} kWh）
              </span>
              {shadow.summary.savingWithinEnergyUncertainty ? (
                <span className="rounded bg-muted px-1.5 py-0.5 text-[11px] leading-4 text-muted-foreground">
                  节电量在拟合误差内
                </span>
              ) : null}
            </div>
          ) : (
            <p className="text-xs text-secondary-foreground">
              {shadow.outcomeDetail}
            </p>
          )}
          {shadow.energyResponse ? (
            <span className="text-xs text-muted-foreground tabular-nums">
              供水每升 1 °C 日用电：{signed(shadow.energyResponse.fittedKWhPerSupplyC, 1)} kWh · 站点历史拟合（替代策略值 {signed(shadow.energyResponse.policyKWhPerSupplyC, 0)} kWh）
            </span>
          ) : null}
        </div>
      </div>

      {shadow.candidates.length > 0 ? (
        <Table aria-label="影子候选供水温度步长">
          <TableHeader>
            <TableRow>
              <TableHead className="pl-5">候选供水温度</TableHead>
              <TableHead>预计室内温度</TableHead>
              <TableHead className="text-right">冷机日耗电变化</TableHead>
              <TableHead className="text-right">预计全站日用电</TableHead>
              <TableHead className="text-right">预计日估算电费</TableHead>
              <TableHead className="pr-5">可行性与判定</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {shadow.candidates.map((row) => (
              <TableRow key={row.supplyTempC} className={cn(row.selected && 'bg-primary-soft/20')}>
                <TableCell className="pl-5 font-medium tabular-nums">{celsius(row.supplyTempC)}</TableCell>
                <TableCell className="tabular-nums">{celsius(row.projectedZoneTempC)}</TableCell>
                <TableCell className="text-right tabular-nums">
                  {signed(row.chillerEnergyDeltaKWh, 1)} kWh
                </TableCell>
                <TableCell className="text-right tabular-nums">
                  {formatDecimal(row.dailyEnergyKWh, 0)} kWh
                </TableCell>
                <TableCell className="text-right tabular-nums">
                  ¥{formatDecimal(row.dailyCost, 0)}
                </TableCell>
                <TableCell className="pr-5">
                  <span className="flex flex-wrap items-center gap-1.5">
                    {!row.feasible ? (
                      <span className="rounded bg-warning-soft px-1.5 py-0.5 text-[11px] leading-4 font-medium text-warning-text">
                        {row.infeasibleLabel}
                      </span>
                    ) : row.selected ? (
                      <span className="rounded bg-primary-soft px-1.5 py-0.5 text-[11px] leading-4 font-medium text-primary">
                        影子最优
                      </span>
                    ) : (
                      <span className="text-xs text-muted-foreground">满足约束</span>
                    )}
                    {row.extrapolated ? (
                      <span className="rounded bg-warning-soft px-1.5 py-0.5 text-[11px] leading-4 font-medium text-warning-text">
                        超出历史范围
                      </span>
                    ) : null}
                  </span>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      ) : null}

      <div className="flex items-center justify-between gap-4 border-t border-divider bg-muted/20 px-5 py-2.5 text-xs text-muted-foreground">
        <span>冷却水温度重置：{shadow.condenserResetNote}</span>
        {shadow.energyResponse ? (
          <span className="tabular-nums">
            参考窗口：近 {shadow.energyResponse.referenceHours} 小时（{shadow.energyResponse.referenceSamples} 个稳态点）
          </span>
        ) : null}
      </div>
    </Panel>
  );
}

function formatChillerCoefficients(
  coefficients: NonNullable<OptimizationShadowEvaluation['fit']>['chillers'][number]['coefficients'],
): string | null {
  if (!coefficients) return null;
  const parts = [
    `常数 ${formatDecimal(coefficients.intercept, 2)}`,
    `负载率 ${signed(coefficients.loadRatio, 2)}`,
    `负载率² ${signed(coefficients.loadRatioSquared, 2)}`,
    `冷冻水出水 ${signed(coefficients.chilledWaterSupplyTempC, 3)}/°C`,
  ];
  if (coefficients.condenserReturnTempC !== undefined) {
    parts.push(`冷却水出水 ${signed(coefficients.condenserReturnTempC, 3)}/°C`);
  }
  return `回归系数（COP 公式）：${parts.join('，')}`;
}

function ChillerFitDiagnosticsPanel({
  fit,
  timeZone,
}: {
  readonly fit: NonNullable<OptimizationShadowEvaluation['fit']>;
  readonly timeZone: string;
}) {
  const nonZeroSetAside = fit.setAsideBreakdown.filter((entry) => entry.count > 0);
  const nonZeroAllocation = fit.loadAllocationBreakdown.filter((entry) => entry.count > 0);

  return (
    <Panel aria-label="冷机 COP 拟合诊断">
      <PanelHeader
        title="冷机 COP 拟合诊断"
        caption={`近 14 天（${formatTime(fit.from, timeZone)} 至 ${formatTime(fit.to, timeZone)}）· ${fit.bucketMinutes} 分钟运行桶：共 ${fit.runningBuckets} 个开机桶，筛除 ${fit.setAsideTotal} 个非稳态或异常桶`}
        action={
          fit.cvRmsePower !== null ? (
            <span className="text-xs text-muted-foreground tabular-nums">
              全站功率 CV(RMSE)：<b className="font-semibold text-foreground">{formatDecimal(fit.cvRmsePower * 100, 1)}%</b>
            </span>
          ) : undefined
        }
      />

      {nonZeroSetAside.length > 0 || nonZeroAllocation.length > 0 ? (
        <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-2 border-b border-divider bg-muted/15 px-5 py-2.5 text-xs text-muted-foreground">
          {nonZeroSetAside.length > 0 ? (
            <div className="flex flex-wrap items-center gap-2">
              <span>筛除原因明细：</span>
              {nonZeroSetAside.map((entry) => (
                <span key={entry.key} className="rounded bg-muted px-2 py-0.5 text-[11px] leading-4 tabular-nums">
                  {entry.label} {entry.count}
                </span>
              ))}
            </div>
          ) : null}
          {nonZeroAllocation.length > 0 ? (
            <div className="flex flex-wrap items-center gap-2">
              <span>冷量分配方式：</span>
              {nonZeroAllocation.map((entry) => (
                <span key={entry.key} className="rounded bg-muted px-2 py-0.5 text-[11px] leading-4 tabular-nums">
                  {entry.label} {entry.count}
                </span>
              ))}
            </div>
          ) : null}
        </div>
      ) : null}

      <Table aria-label="冷机拟合明细">
        <TableHeader>
          <TableRow>
            <TableHead className="pl-5">冷机资产</TableHead>
            <TableHead>拟合状态</TableHead>
            <TableHead className="text-right">稳态样本数</TableHead>
            <TableHead className="text-right">P99 参考冷量</TableHead>
            <TableHead className="text-right">功率 CV(RMSE)</TableHead>
            <TableHead className="text-right">平均 COP</TableHead>
            <TableHead className="text-right">观测供水范围</TableHead>
            <TableHead className="text-right">供水温度斜率</TableHead>
            <TableHead className="pr-5">核验说明</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {fit.chillers.length === 0 ? (
            <TableRow>
              <TableCell colSpan={9} className="py-8 text-center text-xs text-muted-foreground">
                近 14 天未观测到同时上报功率与冷冻水出水温度的冷机运行样本。
              </TableCell>
            </TableRow>
          ) : (
            fit.chillers.map((chiller) => {
              const coeffTip = formatChillerCoefficients(chiller.coefficients);
              return (
                <TableRow key={chiller.deviceId}>
                  <TableCell className="pl-5 font-medium">{chiller.displayName}</TableCell>
                  <TableCell>
                    <span className={cn('rounded px-1.5 py-0.5 text-[11px] leading-4 font-medium', CHILLER_STATUS_CLASS[chiller.status])}>
                      {chiller.statusLabel}
                    </span>
                  </TableCell>
                  <TableCell className="text-right tabular-nums">{chiller.samples}</TableCell>
                  <TableCell className="text-right tabular-nums">
                    {chiller.capacityKW !== null ? `${formatDecimal(chiller.capacityKW, 0)} kW` : '—'}
                  </TableCell>
                  <TableCell className="text-right tabular-nums">
                    {chiller.cvRmsePower !== null ? `${formatDecimal(chiller.cvRmsePower * 100, 1)}%` : '—'}
                  </TableCell>
                  <TableCell className="text-right tabular-nums">
                    {chiller.meanCOP !== null ? formatDecimal(chiller.meanCOP, 2) : '—'}
                  </TableCell>
                  <TableCell className="text-right tabular-nums">
                    {chiller.supplyRangeC
                      ? `${formatDecimal(chiller.supplyRangeC[0], 1)}–${formatDecimal(chiller.supplyRangeC[1], 1)} °C`
                      : '—'}
                  </TableCell>
                  <TableCell className="text-right tabular-nums">
                    {chiller.supplyTempSlopeCOP !== null ? (
                      <span className="inline-flex items-center justify-end gap-1">
                        <span>{signed(chiller.supplyTempSlopeCOP, 3)} /°C</span>
                        {coeffTip ? <InfoTip>{coeffTip}</InfoTip> : null}
                      </span>
                    ) : (
                      '—'
                    )}
                  </TableCell>
                  <TableCell className="pr-5 text-xs text-muted-foreground">
                    {chiller.reasonLabel ?? '通过物理方向与 ASHRAE 误差上限（≤30%）校验'}
                  </TableCell>
                </TableRow>
              );
            })
          )}
        </TableBody>
      </Table>
    </Panel>
  );
}

function ForecastSummaryPanel({
  siteId,
  timeZone,
  isPending,
  isError,
  forecast,
  onRetry,
}: {
  readonly siteId: string;
  readonly timeZone: string;
  readonly isPending: boolean;
  readonly isError: boolean;
  readonly forecast: Awaited<ReturnType<NonNullable<ReturnType<typeof siteLoadForecastQuery>['queryFn']>>> | null;
  readonly onRetry: () => void;
}) {
  const [viewMode, setViewMode] = useState<'chart' | 'table'>('chart');
  const next24hPoints = useMemo(
    () => (forecast ? forecast.points.slice(0, 96) : []),
    [forecast],
  );
  const hours = useMemo(
    () => hourlyForecast(next24hPoints, timeZone).map((hour) => ({
      ...hour,
      band: hour.lowerKW !== null && hour.upperKW !== null ? rangeBand(hour.lowerKW, hour.upperKW) : null,
    })),
    [next24hPoints, timeZone],
  );
  const peak = useMemo(() => forecastPeak(next24hPoints), [next24hPoints]);

  return (
    <Panel aria-label="未来 24 小时负荷预测">
      <PanelHeader
        title="未来 24 小时负荷预测"
        caption={
          peak
            ? `预计峰值 ${formatDecimal(peak.loadKW, 0)} kW（${formatTime(peak.at, timeZone)}）${forecast?.quality === 'FALLBACK' ? ' · 当前沿用最近实测值回退预测' : ''}`
            : '冷站电负荷逐时预测曲线（kW）'
        }
        action={
          <>
            {hours.length > 0 ? (
              <PeriodTabs
                aria-label="预测视图"
                options={FORECAST_VIEW_OPTIONS}
                value={viewMode}
                onValueChange={setViewMode}
              />
            ) : null}
            <PanelLink to="/energy-analysis/load" search={{ site: siteId }}>
              查看用电负荷
            </PanelLink>
          </>
        }
      />

      {isPending ? (
        <div className="p-5">
          <Skeleton className="h-[220px] rounded-lg" />
        </div>
      ) : isError ? (
        <div className="flex flex-col items-start gap-2 px-5 py-8">
          <p className="text-sm font-semibold">站点负荷预测暂时读不到</p>
          <Button variant="outline" size="sm" onClick={onRetry}>
            重试
          </Button>
        </div>
      ) : hours.length === 0 ? (
        <div className="flex flex-col items-center gap-2 px-6 py-10 text-center">
          <span className="flex size-9 items-center justify-center rounded-xl bg-muted text-muted-foreground">
            <Gauge className="size-[18px]" aria-hidden="true" />
          </span>
          <p className="text-sm font-semibold">还没有已发布的站点负荷预测</p>
          <p className="text-xs text-muted-foreground">负荷预测服务按小时滚动发布未来 24 小时冷站电负荷。</p>
        </div>
      ) : viewMode === 'table' ? (
        <div className="max-h-[268px] overflow-y-auto">
          <Table aria-label="未来 24 小时负荷预测表">
            <TableHeader>
              <TableRow>
                <TableHead className="pl-5">时段</TableHead>
                <TableHead className="text-right">预测负荷</TableHead>
                <TableHead className="pr-5 text-right">预测范围</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {hours.map((row) => (
                <TableRow key={row.start}>
                  <TableCell className="pl-5 font-medium tabular-nums">{row.label}</TableCell>
                  <TableCell className="text-right tabular-nums">{formatDecimal(row.loadKW, 1)} kW</TableCell>
                  <TableCell className="pr-5 text-right text-muted-foreground tabular-nums">
                    {row.band ? `${formatDecimal(row.band[0], 0)}–${formatDecimal(row.band[1], 0)} kW` : '—'}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      ) : (
        <div className="px-5 pt-4 pb-3">
          <ChartContainer config={FORECAST_CHART_CONFIG} className="aspect-auto h-[220px] w-full">
            <ComposedChart data={hours} margin={{ left: 4, right: 12, top: 10 }}>
              <CartesianGrid vertical={false} />
              <XAxis dataKey="label" tickLine={false} axisLine={false} minTickGap={24} />
              <YAxis tickLine={false} axisLine={false} width={44} domain={[0, 'auto']} />
              <ChartTooltip
                cursor={{ strokeDasharray: '3 3' }}
                content={({ active, payload }) => {
                  const row = payload?.[0]?.payload as (typeof hours)[number] | undefined;
                  if (!active || !row) return null;
                  return (
                    <div className="grid min-w-36 gap-1 rounded-lg border bg-background px-2.5 py-1.5 text-xs shadow-xl">
                      <p className="font-medium">{row.label}</p>
                      <p className="flex justify-between gap-4">
                        <span className="text-muted-foreground">预测负荷</span>
                        <span className="tabular-nums">{formatDecimal(row.loadKW, 1)} kW</span>
                      </p>
                      {row.band ? (
                        <p className="flex justify-between gap-4">
                          <span className="text-muted-foreground">预测范围</span>
                          <span className="tabular-nums">
                            {formatDecimal(row.band[0], 0)}–{formatDecimal(row.band[1], 0)} kW
                          </span>
                        </p>
                      ) : null}
                    </div>
                  );
                }}
              />
              <Area
                dataKey="band"
                stroke="none"
                fill="var(--color-band)"
                fillOpacity={0.12}
                connectNulls={false}
                isAnimationActive={false}
              />
              <Line
                dataKey="loadKW"
                stroke="var(--color-loadKW)"
                strokeWidth={2}
                dot={false}
                connectNulls={false}
                isAnimationActive={false}
              />
            </ComposedChart>
          </ChartContainer>
        </div>
      )}
    </Panel>
  );
}
