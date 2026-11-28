import { useQuery } from '@tanstack/react-query';
import { FlaskConical, Gauge } from 'lucide-react';
import { useMemo } from 'react';
import { FactTile } from '@/blocks/fact-list';
import { Panel, PanelHeader, PanelLink } from '@/blocks/panel';
import { InfoTip } from '@/components/info-tip';
import { Button } from '@/components/ui/button';
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
import { siteLoadForecastQuery } from '@/features/load/query';
import { formatTime } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import {
  parseShadowEvaluation,
  type OptimizationGovernanceSummary,
  type OptimizationRecommendation,
} from '../optimization-model';
import { latestShadowEvaluationQuery, siteOptimizationGovernanceSummaryQuery } from '../query';
import {
  ChillerFitDiagnosticsPanel,
  ForecastSummaryPanel,
  ShadowComparisonPanel,
} from './OptimizationShadowPanels';
import { SolverSourceBadge } from './SolverSourceBadge';

function copUpliftClass(value: number | null): string {
  if (value === null || Math.abs(value) < 0.05) return 'text-muted-foreground';
  return value > 0 ? 'text-success-text' : 'text-warning-text';
}

export function OptimizationAnalysisTab({
  siteId,
  timeZone,
  recommendation,
  active,
  canManageControl = false,
}: {
  readonly siteId: string;
  readonly timeZone: string;
  readonly recommendation: OptimizationRecommendation | null;
  readonly active: boolean;
  readonly canManageControl?: boolean;
}) {
  const nameByDevice = useDeviceNames(siteId);
  const governanceQuery = useQuery(siteOptimizationGovernanceSummaryQuery(siteId, active));
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
      <EffectComparisonPanel
        siteId={siteId}
        timeZone={timeZone}
        canManageControl={canManageControl}
        isPending={governanceQuery.isPending}
        isError={governanceQuery.isError}
        summary={governanceQuery.data ?? null}
        onRetry={() => void governanceQuery.refetch()}
      />

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

function EffectComparisonPanel({
  siteId,
  timeZone,
  canManageControl,
  isPending,
  isError,
  summary,
  onRetry,
}: {
  readonly siteId: string;
  readonly timeZone: string;
  readonly canManageControl: boolean;
  readonly isPending: boolean;
  readonly isError: boolean;
  readonly summary: OptimizationGovernanceSummary | null;
  readonly onRetry: () => void;
}) {
  const headerAction = (
    <div className="flex flex-wrap items-center gap-3">
      <InfoTip>
        按室外湿球温度与冷负荷同步分箱对比 AI 闭环与规则基准能效，仅展示统计周期内的客观对照结果。
      </InfoTip>
      {canManageControl ? (
        <PanelLink to="/system" search={{ tab: 'governance', site: siteId, section: 'optimization' }}>
          灰度与负样本
        </PanelLink>
      ) : null}
    </div>
  );

  if (isPending) {
    return <Skeleton className="h-64 rounded-xl" />;
  }

  if (isError) {
    return (
      <Panel aria-label="运行效果对照">
        <PanelHeader title="运行效果对照" action={headerAction} />
        <div className="flex flex-col items-start gap-2 px-5 py-8">
          <p className="text-sm font-semibold">运行效果对照统计暂时读不到</p>
          <Button variant="outline" size="sm" onClick={onRetry}>
            重试
          </Button>
        </div>
      </Panel>
    );
  }

  if (!summary) {
    return (
      <Panel aria-label="运行效果对照">
        <PanelHeader title="运行效果对照" action={headerAction} />
        <div className="flex flex-col items-center gap-2 px-6 py-10 text-center">
          <span className="flex size-9 items-center justify-center rounded-xl bg-muted text-muted-foreground">
            <Gauge className="size-[18px]" aria-hidden="true" />
          </span>
          <p className="text-sm font-semibold">尚无近 14 天运行效果对照统计</p>
          <p className="max-w-lg text-xs text-muted-foreground">
            积累优化调度与自动控制周期后，此处将自动展示当前求解器策略、运行周期分布与同湿球温度分箱 COP 对照。
          </p>
        </div>
      </Panel>
    );
  }

  return (
    <Panel aria-label="运行效果对照">
      <PanelHeader
        title="运行效果对照"
        badge={
          <SolverSourceBadge
            source={summary.activeSolver.source}
            label={summary.activeSolver.sourceLabel}
          />
        }
        caption={`近 ${summary.windowDays} 天统计 · 更新于 ${formatTime(summary.generatedAt, timeZone)}`}
        action={headerAction}
      />

      <dl className="grid grid-cols-2 gap-x-6 gap-y-4 border-b border-divider px-5 py-4 sm:grid-cols-3 lg:grid-cols-6">
        <FactTile label="当前生效求解器" valueClassName="text-sm font-semibold text-foreground">
          <span>{summary.activeSolver.solverLabel}</span>
          <span className="mt-0.5 block text-xs font-normal text-muted-foreground">
            {summary.activeSolver.loopLabel}
            {summary.activeSolver.hasDeploymentBinding ? ' · 已绑定站点版本' : ''}
          </span>
        </FactTile>
        <FactTile label="AI 闭环加权 COP" valueClassName="text-lg font-semibold text-foreground">
          {summary.copComparison.weightedAiCOPLabel}
        </FactTile>
        <FactTile label="规则基准加权 COP" valueClassName="text-lg font-semibold text-muted-foreground">
          {summary.copComparison.weightedBaselineCOPLabel}
        </FactTile>
        <FactTile
          label="同条件 COP 变化"
          valueClassName={cn('text-lg font-semibold', copUpliftClass(summary.copComparison.weightedCopUpliftPct))}
        >
          {summary.copComparison.weightedCopUpliftLabel}
        </FactTile>
        <FactTile label="分箱累计估算节电" valueClassName="text-lg font-semibold text-primary">
          {summary.copComparison.totalEstimatedSavedLabel}
        </FactTile>
        <FactTile label="可比对照分箱" valueClassName="text-sm font-semibold text-foreground">
          <span>
            {summary.copComparison.matchedBinCount > 0
              ? `${summary.copComparison.matchedBinCount} 个条件箱`
              : '暂无对照箱'}
          </span>
          {summary.copComparison.matchedBinCount > 0 ? (
            <span className="mt-0.5 block text-xs font-normal text-muted-foreground tabular-nums">
              闭环 {summary.copComparison.matchedAiSamples} · 基准 {summary.copComparison.matchedBaselineSamples} 周期
            </span>
          ) : null}
        </FactTile>
      </dl>

      <section aria-label={`近 ${summary.windowDays} 天运行周期分布`} className="flex flex-col gap-3 border-b border-divider px-5 py-4">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <h3 className="text-xs font-semibold text-foreground">近 {summary.windowDays} 天运行周期分布</h3>
          <span className="text-xs text-muted-foreground tabular-nums">
            共 {summary.tickCounts.totalTicks} 个周期
          </span>
        </div>
        <dl className="grid grid-cols-2 gap-x-5 gap-y-3 sm:grid-cols-4">
          <FactTile label="AI 闭环周期" valueClassName="text-base font-semibold text-primary">
            {summary.tickCounts.aiClosedLoop}
          </FactTile>
          <FactTile label="AI 影子跟跑" valueClassName="text-base font-semibold text-foreground">
            {summary.tickCounts.aiShadow}
          </FactTile>
          <FactTile label="规则基准周期" valueClassName="text-base font-semibold text-foreground">
            {summary.tickCounts.ruleBaseline}
          </FactTile>
          <FactTile
            label="人工接管周期"
            valueClassName={
              summary.tickCounts.manualOverride > 0
                ? 'text-base font-semibold text-warning-text'
                : 'text-base font-semibold text-muted-foreground'
            }
          >
            {summary.tickCounts.manualOverride}
          </FactTile>
        </dl>
      </section>

      <div className="flex flex-wrap items-center gap-1.5 border-b border-divider bg-muted/20 px-5 py-2.5">
        <h3 className="text-xs font-semibold text-foreground">同湿球温度与冷负荷分箱 COP 对照</h3>
        <InfoTip>仅展示同时覆盖 AI 闭环与规则基准周期的天气负荷分箱。</InfoTip>
      </div>

      <Table aria-label="同湿球与冷负荷分箱 COP 对照">
        <TableHeader>
          <TableRow>
            <TableHead className="pl-5">室外湿球区间</TableHead>
            <TableHead>冷负荷区间</TableHead>
            <TableHead className="text-right">基准 COP</TableHead>
            <TableHead className="text-right">AI 闭环 COP</TableHead>
            <TableHead className="text-right">COP 变化</TableHead>
            <TableHead className="text-right">估算节电</TableHead>
            <TableHead className="pr-5 text-right">对照周期（闭环 / 基准）</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {summary.copComparison.cells.length === 0 ? (
            <TableRow>
              <TableCell colSpan={7} className="py-8 text-center text-xs text-muted-foreground">
                尚无满足同湿球与同冷负荷分箱条件的对照周期，积累闭环与基准周期后将自动展示。
              </TableCell>
            </TableRow>
          ) : (
            summary.copComparison.cells.map((cell) => (
              <TableRow key={cell.key}>
                <TableCell className="pl-5 font-medium tabular-nums">{cell.wetBulbRangeLabel}</TableCell>
                <TableCell className="tabular-nums">{cell.coolingLoadRangeLabel}</TableCell>
                <TableCell className="text-right tabular-nums text-muted-foreground">{cell.baselineCOPLabel}</TableCell>
                <TableCell className="text-right font-medium tabular-nums">{cell.aiCOPLabel}</TableCell>
                <TableCell className={cn('text-right font-medium tabular-nums', copUpliftClass(cell.copUpliftPct))}>
                  {cell.copUpliftLabel}
                </TableCell>
                <TableCell className="text-right tabular-nums">{cell.estimatedSavedLabel}</TableCell>
                <TableCell className="pr-5 text-right tabular-nums text-muted-foreground">
                  {cell.samplesPairLabel}
                </TableCell>
              </TableRow>
            ))
          )}
        </TableBody>
      </Table>
    </Panel>
  );
}
