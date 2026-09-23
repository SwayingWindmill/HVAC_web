import { useEffect, useMemo, useState, type ReactNode } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import {
  Bot,
  Bug,
  Building2,
  CalendarDays,
  CircleDollarSign,
  CloudSun,
  FileCheck2,
  Gauge,
  LineChart,
  Network,
  ShieldCheck,
  Sparkles,
  Zap,
} from 'lucide-react';
import PageScaffold from '@/components/PageScaffold';
import {
  OperationsInsightBand,
  OperationsMetrics,
  OperationsPanelHeading,
} from '@/components/OperationsUI';
import type { CurrentPrincipalResponse, DashboardMetric, Site } from '@/api/generated/platformGateway.gen';
import { useSiteDashboardSummary } from '@/api/site-dashboard';
import {
  getLatestOptimizationRecommendation,
  getSiteLoadForecast,
  getSitePVForecast,
  listSiteFDDFindings,
  type FDDFinding,
  type PublishedForecast,
  type PublishedRecommendation,
} from '@/api/intelligence';
import { FDD_READ_MODEL_BOUNDARY } from '@/features/fdd/capability';
import { FORECAST_READ_MODEL_BOUNDARY } from '@/features/forecast/capability';
import { OPTIMIZATION_READ_MODEL_BOUNDARY } from '@/features/optimization/capability';
import { SETTLEMENT_READ_MODEL_BOUNDARY } from '@/features/settlement/capability';
import { FocusHeading } from '@/app/FocusHeading';
import { siteRoute } from '@/app/router-paths';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { DataTable, type DataTableFeatures } from '@/components/data-table';
import { useDataTable } from '@/hooks/use-data-table';
import { Textarea } from '@/components/ui/textarea';
import './product-pages.css';

interface ProductPageProps {
  site: Readonly<Site>;
  principal: CurrentPrincipalResponse;
}

function useIntelligenceResource<T>(siteId: string, load: (siteId: string, signal?: AbortSignal) => Promise<T>) {
  const [data, setData] = useState<T | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError(null);
    load(siteId, controller.signal)
      .then((value) => setData(value))
      .catch((reason: unknown) => {
        if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : '分析服务暂时不可用。');
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [siteId, load]);
  return { data, loading, error };
}

function numberValue(object: Record<string, unknown>, key: string): number | null {
  const value = object[key];
  return typeof value === 'number' && Number.isFinite(value) ? value : null;
}

function stringValue(object: Record<string, unknown>, key: string): string | null {
  const value = object[key];
  return typeof value === 'string' && value.length > 0 ? value : null;
}

function ProductBoundary({
  children,
  site,
  testId,
  state = 'NOT_INTEGRATED',
}: {
  children: ReactNode;
  site: Readonly<Site>;
  testId: string;
  state?: string;
}) {
  return <section data-testid={testId} data-business-state={state} data-site-id={site.id}>{children}</section>;
}

function PageHeading({ icon, children }: { readonly icon: ReactNode; readonly children: ReactNode }) {
  return <FocusHeading className="ops-page-title"><span className="inline-flex items-center gap-2">{icon}{children}</span></FocusHeading>;
}

function BoundaryBadge({ integrated }: { readonly integrated: boolean }) {
  return integrated
    ? <Badge variant="outline" className="border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900 dark:bg-emerald-950/40 dark:text-emerald-300">权威数据已接入</Badge>
    : <Badge variant="outline">能力待接入</Badge>;
}

function EmptyState({ children }: { readonly children: ReactNode }) {
  return <div className="grid min-h-32 place-items-center rounded-md border border-dashed p-6 text-center text-sm text-muted-foreground">{children}</div>;
}

function FactGrid({ items }: { readonly items: ReadonlyArray<{ label: string; value: ReactNode; note?: ReactNode }> }) {
  return (
    <dl className="grid overflow-hidden rounded-md border sm:grid-cols-2">
      {items.map((item, index) => (
        <div key={item.label} className={`min-w-0 p-3 ${index % 2 === 1 ? 'sm:border-l' : ''} ${index >= 2 ? 'border-t' : ''}`}>
          <dt className="text-xs text-muted-foreground">{item.label}</dt>
          <dd className="mt-1 text-sm font-medium">{item.value}</dd>
          {item.note ? <span className="mt-1 block text-xs text-muted-foreground">{item.note}</span> : null}
        </div>
      ))}
    </dl>
  );
}

function qualityLabel(value: string | null | undefined): string {
  if (!value) return '未知';
  if (value === 'VALID' || value === 'GOOD') return '有效';
  if (value === 'FALLBACK') return '回退结果';
  if (value === 'STALE') return '数据陈旧';
  if (value === 'UNAVAILABLE') return '不可用';
  if (value === 'PARTIAL') return '部分可用';
  return '待确认';
}

function riskLabel(value: string | null): string {
  if (!value) return '未提供';
  if (value === 'LOW') return '低';
  if (value === 'MEDIUM') return '中';
  if (value === 'HIGH') return '高';
  if (value === 'CRITICAL') return '严重';
  return '待确认';
}

function approvalLabel(value: string | null | undefined): string {
  if (!value) return '未提供';
  if (value === 'APPROVED') return '已批准';
  if (value === 'REJECTED') return '未批准';
  if (value === 'PENDING') return '待审批';
  return '待确认';
}

function objectFactCount(value: unknown): number {
  return value && typeof value === 'object' ? Object.keys(value as Record<string, unknown>).length : 0;
}

export function FddPage({ site }: ProductPageProps) {
  const { data: findings, loading, error } = useIntelligenceResource<FDDFinding[]>(site.id, listSiteFDDFindings);
  const rows = findings ?? [];
  const linkedWorkOrders = rows.filter((finding) => Boolean(finding.workOrderId)).length;
  const linkedAlarms = rows.filter((finding) => Boolean(finding.alarmId)).length;
  const highConfidence = rows.filter((finding) => finding.confidence >= 0.8).length;
  const averageConfidence = rows.length > 0 ? Math.round(rows.reduce((sum, finding) => sum + finding.confidence, 0) / rows.length * 100) : 0;

  const findingColumns = useMemo<Array<ColumnDef<DataTableFeatures, FDDFinding>>>(() => [
    { id: 'finding', header: '诊断', cell: ({ row }) => <span className="font-medium">{row.original.findingType}</span> },
    { id: 'window', header: '评估窗口', cell: ({ row }) => <span>{new Date(row.original.evaluationFrom).toLocaleString()} → {new Date(row.original.evaluationTo).toLocaleString()}</span> },
    { id: 'confidence', header: '置信度', cell: ({ row }) => <span>{Math.round(row.original.confidence * 100)}%</span> },
    { id: 'evidence', header: '证据', cell: ({ row }) => <span>{row.original.evidenceIds.length} 项</span> },
    {
      id: 'links',
      header: '处置关联',
      cell: ({ row }) => (
        <div className="flex flex-wrap gap-1">
          <Badge variant="outline">{row.original.alarmId ? '已关联告警' : '未关联告警'}</Badge>
          <Badge variant="outline">{row.original.workOrderId ? '已关联工单' : '未关联工单'}</Badge>
        </div>
      ),
    },
  ], []);

  const findingTable = useDataTable({
    key: `product-fdd-${site.id}`,
    data: [...rows],
    columns: findingColumns,
    paginate: false,
    getRowId: (row) => row.id,
  });

  return (
    <ProductBoundary site={site} testId="real-site-route-fdd" state={FDD_READ_MODEL_BOUNDARY.status}>
      <PageScaffold
        title="故障检测与诊断 FDD"
        heading={<PageHeading icon={<Bug className="size-5" />}>故障检测与诊断 FDD</PageHeading>}
        extra={<BoundaryBadge integrated={FDD_READ_MODEL_BOUNDARY.status === 'INTEGRATED'} />}
      >
        <div className="space-y-4">
          <Alert variant={error ? 'destructive' : 'default'}>
            <AlertTitle>{error ? '诊断结果读取失败' : '诊断运行正常'}</AlertTitle>
            <AlertDescription>{error ?? '系统持续监测设备运行状态与能效指标，发现异常将生成诊断建议与关联工单。'}</AlertDescription>
          </Alert>
          <OperationsMetrics items={[
            { label: '当前诊断', value: loading ? '…' : rows.length, detail: '已发布诊断结果', icon: <Bug />, tone: 'accent' },
            { label: '高置信诊断', value: loading ? '…' : highConfidence, detail: '置信度 ≥ 80%', icon: <ShieldCheck /> },
            { label: '已关联告警', value: loading ? '…' : linkedAlarms, detail: '存在明确告警关联', icon: <Network /> },
            { label: '已关联工单', value: loading ? '…' : linkedWorkOrders, detail: rows.length ? `平均置信度 ${averageConfidence}%` : '当前无诊断', icon: <FileCheck2 /> },
          ]} />
          <Card>
            <CardHeader><CardTitle><OperationsPanelHeading icon={<Network />} title="诊断列表" meta={`${rows.length} 条`} /></CardTitle></CardHeader>
            <CardContent>
              {loading ? <EmptyState>正在读取诊断结果…</EmptyState> : rows.length === 0 ? <EmptyState>当前站点没有已发布诊断结果</EmptyState> : (
                <DataTable
                  table={findingTable}
                  tableAriaLabel="FDD 诊断列表"
                />
              )}
            </CardContent>
          </Card>
        </div>
      </PageScaffold>
    </ProductBoundary>
  );
}

export function OptimizePage({ site }: ProductPageProps) {
  const { data: published, loading, error } = useIntelligenceResource<PublishedRecommendation | null>(site.id, getLatestOptimizationRecommendation);
  const recommendation = published?.recommendation ?? null;
  const energySaving = recommendation ? numberValue(recommendation.expectedImpact, 'energySavingKWhPerDay') : null;
  const costSaving = recommendation ? numberValue(recommendation.expectedImpact, 'costSavingPerDay') : null;
  const risk = recommendation ? stringValue(recommendation.risk, 'level') : null;
  const candidateSupply = recommendation ? numberValue(recommendation.candidate, 'supplyTempC') : null;
  const revalidationState = recommendation?.currentStateRevalidation?.accepted ? '已复核' : '待复核';

  return (
    <ProductBoundary site={site} testId="real-site-route-optimize" state={OPTIMIZATION_READ_MODEL_BOUNDARY.status}>
      <PageScaffold
        title="节能优化建议"
        heading={<PageHeading icon={<Zap className="size-5" />}>节能优化建议</PageHeading>}
        extra={<BoundaryBadge integrated={OPTIMIZATION_READ_MODEL_BOUNDARY.status === 'INTEGRATED'} />}
      >
        <div className="space-y-4">
          <Alert variant={error ? 'destructive' : 'default'}>
            <AlertTitle>{error ? '优化建议读取失败' : '节能优化建议'}</AlertTitle>
            <AlertDescription>{error ?? '优化建议综合考虑系统能效预期与安全边界，经人工复核审批后方可下发执行。'}</AlertDescription>
          </Alert>
          <OperationsMetrics items={[
            { label: '预计节电', value: loading ? '…' : energySaving ?? '—', suffix: energySaving == null ? undefined : 'kWh/天', detail: recommendation ? '模型预期收益' : '当前无已发布建议', icon: <Zap />, tone: 'accent' },
            { label: '预计节省', value: loading ? '…' : costSaving ?? '—', detail: recommendation ? '基于同一评估基线' : '当前无已发布建议', icon: <CircleDollarSign /> },
            { label: '风险', value: loading ? '…' : riskLabel(risk), detail: recommendation ? '建议风险评估' : '当前无已发布建议', icon: <ShieldCheck /> },
            { label: '当前状态复核', value: loading ? '…' : recommendation ? revalidationState : '—', detail: '执行前必须重新确认', icon: <Gauge /> },
          ]} />
          {recommendation ? (
            <div className="grid gap-4 xl:grid-cols-[1.15fr_.85fr]">
              <Card>
                <CardHeader><CardTitle>最新优化建议</CardTitle><CardDescription>当前推荐的运行设定值与预期效益</CardDescription></CardHeader>
                <CardContent>
                  <FactGrid items={[
                    { label: '候选冷冻水供水设定', value: candidateSupply === null ? '未提供' : `${candidateSupply} °C` },
                    { label: '风险', value: riskLabel(risk) },
                    { label: '审批状态', value: approvalLabel(recommendation.approval) },
                    { label: '当前状态复核', value: revalidationState },
                  ]} />
                </CardContent>
              </Card>
              <Card>
                <CardHeader><CardTitle>方案前置材料</CardTitle><CardDescription>下发前需具备的约束条件与验证预案</CardDescription></CardHeader>
                <CardContent>
                  <FactGrid items={[
                    { label: '约束条件', value: objectFactCount(recommendation.constraints) > 0 ? '已提供' : '未提供' },
                    { label: '不确定性', value: objectFactCount(recommendation.uncertainty) > 0 ? '已提供' : '未提供' },
                    { label: '回滚计划', value: objectFactCount(recommendation.rollbackPlan) > 0 ? '已提供' : '未提供' },
                    { label: '验证计划', value: objectFactCount(recommendation.verificationPlan) > 0 ? '已提供' : '未提供' },
                  ]} />
                </CardContent>
              </Card>
            </div>
          ) : !loading && !error ? <EmptyState>当前站点没有已发布优化建议</EmptyState> : null}
        </div>
      </PageScaffold>
    </ProductBoundary>
  );
}

export function ForecastPage({ site }: ProductPageProps) {
  const [forecastTarget, setForecastTarget] = useState<'load' | 'pv'>('load');
  const forecastLoader = forecastTarget === 'load' ? getSiteLoadForecast : getSitePVForecast;
  const { data: forecast, loading, error } = useIntelligenceResource<PublishedForecast | null>(site.id, forecastLoader);
  const points = forecast?.points ?? [];
  const first = points[0];
  const last = points[points.length - 1];
  const targetLabel = forecastTarget === 'load' ? '站点负荷' : '光伏发电';
  const isFallback = forecast?.snapshot.quality === 'FALLBACK';
  type ForecastPoint = PublishedForecast['points'][number];

  const forecastColumns = useMemo<Array<ColumnDef<DataTableFeatures, ForecastPoint>>>(() => [
    { id: 'time', header: '预测时间', cell: ({ row }) => new Date(row.original.forecast_for).toLocaleString() },
    { id: 'horizon', header: '提前量', cell: ({ row }) => <span>{row.original.horizon_minutes} 分钟</span> },
    { id: 'value', header: '预测值', cell: ({ row }) => <span className="font-medium">{row.original.value.toFixed(1)} {row.original.unit}</span> },
    { id: 'lower', header: '下界', cell: ({ row }) => row.original.lower_bound == null ? '—' : `${row.original.lower_bound.toFixed(1)} ${row.original.unit}` },
    { id: 'upper', header: '上界', cell: ({ row }) => row.original.upper_bound == null ? '—' : `${row.original.upper_bound.toFixed(1)} ${row.original.unit}` },
    { id: 'quality', header: '质量', cell: ({ row }) => <Badge variant="outline">{qualityLabel(row.original.quality)}</Badge> },
  ], []);

  const forecastTable = useDataTable({
    key: `product-forecast-${site.id}-${forecastTarget}`,
    data: [...points],
    columns: forecastColumns,
    paginate: false,
    getRowId: (row) => row.forecast_id,
  });

  return (
    <ProductBoundary site={site} testId="real-site-route-forecast" state={FORECAST_READ_MODEL_BOUNDARY.status}>
      <PageScaffold
        title="预测与基线"
        heading={<PageHeading icon={<LineChart className="size-5" />}>预测与基线</PageHeading>}
        extra={(
          <div className="flex flex-wrap items-center gap-2">
            <div className="inline-flex rounded-md border bg-muted/30 p-0.5" role="group" aria-label="预测对象">
              <Button size="sm" variant={forecastTarget === 'load' ? 'secondary' : 'ghost'} aria-pressed={forecastTarget === 'load'} onClick={() => setForecastTarget('load')}>站点负荷</Button>
              <Button size="sm" variant={forecastTarget === 'pv' ? 'secondary' : 'ghost'} aria-pressed={forecastTarget === 'pv'} onClick={() => setForecastTarget('pv')}>光伏发电</Button>
            </div>
            <BoundaryBadge integrated={FORECAST_READ_MODEL_BOUNDARY.status === 'INTEGRATED'} />
          </div>
        )}
      >
        <div className="space-y-4">
          <Alert variant={error ? 'destructive' : 'default'} className={isFallback && !error ? 'border-amber-300 bg-amber-50/60 dark:border-amber-900 dark:bg-amber-950/20' : undefined}>
            <AlertTitle>{error ? `${targetLabel}预测读取失败` : isFallback ? `${targetLabel}使用历史均值参考` : `${targetLabel}预测已就绪`}</AlertTitle>
            <AlertDescription>{error ?? (isFallback ? '当前历史样本积累不足，展示近期运行基准参考值。' : '基于气象预测与负荷模型生成未来逐时负荷预测曲线。')}</AlertDescription>
          </Alert>
          <OperationsMetrics items={[
            { label: '预测状态', value: loading ? '…' : qualityLabel(forecast?.snapshot.quality), detail: forecast ? targetLabel : '当前无已发布预测', icon: <LineChart />, tone: 'accent' },
            { label: '预测点数', value: loading ? '…' : points.length, detail: first && last ? `${new Date(first.forecast_for).toLocaleString()} → ${new Date(last.forecast_for).toLocaleString()}` : '当前无预测点', icon: <CalendarDays /> },
            { label: '不确定性区间', value: loading ? '…' : first?.lower_bound != null && first?.upper_bound != null ? '已提供' : '未提供', detail: isFallback ? '历史参考值' : '预测波动范围', icon: <ShieldCheck /> },
            { label: '站点时区', value: site.timezone, detail: '预测时间按站点时区呈现', icon: <Building2 /> },
          ]} />
          {forecast ? (
            <Card>
              <CardHeader><CardTitle>预测范围</CardTitle><CardDescription>当前发布结果的时间范围与质量状态。</CardDescription></CardHeader>
              <CardContent><FactGrid items={[
                { label: '预测对象', value: targetLabel },
                { label: '预测起点', value: new Date(forecast.snapshot.forecastOrigin).toLocaleString() },
                { label: '覆盖窗口', value: `${new Date(forecast.snapshot.windowStart).toLocaleString()} → ${new Date(forecast.snapshot.windowEnd).toLocaleString()}` },
                { label: '结果质量', value: qualityLabel(forecast.snapshot.quality) },
              ]} /></CardContent>
            </Card>
          ) : null}
          <Card>
            <CardHeader><CardTitle><OperationsPanelHeading icon={<LineChart />} title="预测序列" meta={`${points.length} 条`} /></CardTitle></CardHeader>
            <CardContent>
              {loading ? <EmptyState>正在读取预测结果…</EmptyState> : points.length === 0 ? <EmptyState>当前站点没有已发布{targetLabel}预测</EmptyState> : (
                <DataTable
                  table={forecastTable}
                  tableAriaLabel={`${targetLabel}预测序列`}
                />
              )}
            </CardContent>
          </Card>
        </div>
      </PageScaffold>
    </ProductBoundary>
  );
}

export function CostPage({ site }: ProductPageProps) {
  return (
    <ProductBoundary site={site} testId="real-site-route-cost">
      <PageScaffold
        title="成本与绩效"
        heading={<PageHeading icon={<CircleDollarSign className="size-5" />}>成本与绩效</PageHeading>}
        extra={<Badge variant="outline">月度视图</Badge>}
      >
        <div className="space-y-4">
          <Alert>
            <AlertTitle>成本与收益数据对接中</AlertTitle>
            <AlertDescription>待接入站点分时电价、能源预算与碳排放基准数据后，将自动计算费用与节能效益。</AlertDescription>
          </Alert>
          <OperationsMetrics items={[
            { label: '今日电费', value: '—', detail: '等待成本数据同步', icon: <CircleDollarSign />, tone: 'accent' },
            { label: '累计节能收益', value: '—', detail: '等待优化效果核算', icon: <Zap />, tone: 'positive' },
            { label: '累计减碳', value: '—', suffix: 'kgCO₂', detail: '等待折算指标同步', icon: <CloudSun /> },
            { label: '投资回报 ROI', value: '—', suffix: '%', detail: '等待成本与效益数据', icon: <LineChart /> },
          ]} />
          <OperationsInsightBand title="成本概况" icon={<ShieldCheck />} items={[
            { text: '峰平谷电价方案与月度预算基线接入中。', tone: 'info' },
            { text: '节能收益将结合已执行的控制优化措施进行动态计算。', tone: 'positive' },
          ]} />
          <div className="grid gap-4 xl:grid-cols-[1.5fr_1fr]">
            <Card><CardHeader><CardTitle>能耗成本趋势</CardTitle><CardDescription>当前周期</CardDescription></CardHeader><CardContent><EmptyState>成本时序接入中</EmptyState></CardContent></Card>
            <Card><CardHeader><CardTitle>今日峰平谷费用结构</CardTitle></CardHeader><CardContent><EmptyState>分时费用数据同步中</EmptyState></CardContent></Card>
          </div>
          <div className="grid gap-4 xl:grid-cols-3">
            <Card><CardHeader><CardTitle>年度节能达标率</CardTitle></CardHeader><CardContent><EmptyState>年度目标基线同步中</EmptyState></CardContent></Card>
            <Card><CardHeader><CardTitle>峰平谷成本明细</CardTitle></CardHeader><CardContent><EmptyState>分时明细计算中</EmptyState></CardContent></Card>
            <Card><CardHeader><CardTitle>碳减排折算</CardTitle></CardHeader><CardContent><EmptyState>碳排放因子对接中</EmptyState></CardContent></Card>
          </div>
          <Card><CardHeader><CardTitle>已批准节能建议</CardTitle></CardHeader><CardContent><EmptyState>暂无已批准的节能建议记录</EmptyState></CardContent></Card>
        </div>
      </PageScaffold>
    </ProductBoundary>
  );
}

export function SettlementPage({ site }: ProductPageProps) {
  return (
    <ProductBoundary site={site} testId="real-site-route-settlement" state={SETTLEMENT_READ_MODEL_BOUNDARY.status}>
      <PageScaffold
        title="结算与对账"
        heading={<PageHeading icon={<FileCheck2 className="size-5" />}>结算与对账</PageHeading>}
        extra={<BoundaryBadge integrated={false} />}
      >
        <div className="space-y-4">
          <Alert>
            <AlertTitle>结算对账功能对接中</AlertTitle>
            <AlertDescription>待接入用能账单与电网结算数据后，支持分时电费核对与账单版本管理。</AlertDescription>
          </Alert>
          <OperationsMetrics items={[
            { label: '结算周期', value: '—', detail: '等待账单数据同步', icon: <CalendarDays />, tone: 'accent' },
            { label: '锁定状态', value: '—', detail: '等待账单归档状态', icon: <ShieldCheck /> },
            { label: '修订版本', value: '—', detail: '等待版本流水', icon: <FileCheck2 /> },
            { label: '对账差异', value: '—', detail: '等待核验对账结果', icon: <CircleDollarSign /> },
          ]} />
          <Card>
            <CardHeader><CardTitle>结算概况</CardTitle><CardDescription>结算周期与对账状态信息</CardDescription></CardHeader>
            <CardContent><FactGrid items={[
              { label: '结算周期', value: '—' },
              { label: '锁定状态', value: '—' },
              { label: '修订版本', value: '—' },
              { label: '来源读数', value: '—' },
              { label: '费率版本', value: '—' },
              { label: '对账结果', value: '—' },
              { label: '更正历史', value: '—' },
              { label: '站点时区', value: site.timezone },
            ]} /></CardContent>
          </Card>
          <OperationsInsightBand title="结算状态" icon={<ShieldCheck />} items={[
            { text: '结算系统暂未同步当前周期账单数据。', tone: 'info' },
            { text: '完成核验锁定的结算周期将作为能耗成本分析的基准输入。', tone: 'positive' },
          ]} />
        </div>
      </PageScaffold>
    </ProductBoundary>
  );
}

export function AiLanding({ site, principal, operationsPath }: ProductPageProps & { operationsPath: string }) {
  return (
    <ProductBoundary site={site} testId="real-site-route-ai" state="READY">
      <PageScaffold
        title="智能运维助手"
        heading={<PageHeading icon={<Bot className="size-5" />}>智能运维助手</PageHeading>}
        extra={<Badge variant="outline" className="border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900 dark:bg-emerald-950/40 dark:text-emerald-300">运维排查</Badge>}
      >
        <div className="space-y-4">
          <Alert>
            <AlertTitle>智能运维辅助诊断</AlertTitle>
            <AlertDescription>辅助分析设备异常并提供优化排查建议，关键控制指令须经人工确认。</AlertDescription>
          </Alert>
          <OperationsMetrics items={[
            { label: '排查任务', value: '进入工作台查看', detail: '当前站点', icon: <Bot />, tone: 'accent' },
            { label: '数据来源', value: '实时测点', detail: '现场数据同步', icon: <ShieldCheck /> },
            { label: '人工确认', value: '必需', detail: '关键操作经人工确认', icon: <FileCheck2 /> },
            { label: '工具执行', value: '受控', detail: '受权限范围约束', icon: <Sparkles /> },
          ]} />
          <div className="grid gap-4 xl:grid-cols-[.8fr_1.5fr_.8fr]">
            <Card>
              <CardHeader><CardTitle>排查与任务</CardTitle></CardHeader>
              <CardContent className="space-y-3"><Input disabled placeholder="搜索排查任务或设备" /><div className="flex h-9 items-center rounded-md border bg-muted/30 px-3 text-sm text-muted-foreground">全部状态</div><EmptyState>排查列表由运维工作台提供</EmptyState></CardContent>
            </Card>
            <Card>
              <CardHeader><CardTitle>智能排查工作台</CardTitle><CardDescription>针对设备异常现象与运行数据进行辅助排查</CardDescription></CardHeader>
              <CardContent className="space-y-4">
                <div className="real-ai-assistant-intro"><Bot /><h3>面向当前站点的运行辅助诊断</h3><p className="text-sm text-muted-foreground">在线协助分析设备运行故障，结合测点数据与诊断规则生成排查思路，所有关键操作均需工程师确认。</p></div>
                <Textarea disabled rows={5} placeholder="输入设备编号、异常现象或运行工况疑问进行排查..." />
                <Button asChild><a href={operationsPath}><Bot />进入排查工作台</a></Button>
              </CardContent>
            </Card>
            <div className="space-y-4">
              <Card><CardHeader><CardTitle>当前上下文</CardTitle></CardHeader><CardContent><FactGrid items={[
                { label: '站点', value: site.displayName },
                { label: '时区', value: site.timezone },
                { label: '用户', value: principal.principal.displayName },
                { label: '授权能力', value: `${principal.authorization.capabilities.length} 项` },
              ]} /></CardContent></Card>
              <Card><CardHeader><CardTitle>关联业务</CardTitle></CardHeader><CardContent className="grid gap-2"><Button variant="outline" asChild><a href={siteRoute(site, 'assets')}>查看设备台账</a></Button><Button variant="outline" asChild><a href={siteRoute(site, 'fdd')}>查看诊断证据</a></Button><Button variant="outline" asChild><a href={siteRoute(site, 'alarms')}>进入告警中心</a></Button><Button variant="outline" asChild><a href={siteRoute(site, 'optimize')}>评审优化建议</a></Button></CardContent></Card>
            </div>
          </div>
        </div>
      </PageScaffold>
    </ProductBoundary>
  );
}

type BigScreenScene = 'overview' | 'energy' | 'operations';

function BigScreenPanel({ title, eyebrow, children, accent = false }: {
  title: string;
  eyebrow: string;
  children: ReactNode;
  accent?: boolean;
}) {
  return (
    <section className={`bigscreen-panel${accent ? ' is-accent' : ''}`}>
      <header className="bigscreen-panel-header"><span className="bigscreen-panel-heading"><small className="bigscreen-panel-eyebrow">{eyebrow}</small><strong className="bigscreen-panel-title">{title}</strong></span></header>
      <div className="bigscreen-panel-body">{children}</div>
    </section>
  );
}

function formatBigScreenMetric(metric: DashboardMetric | undefined, fallbackUnit = ''): [string, string] {
  if (!metric || metric.value === null) return ['—', '暂无可用数据'];
  const value = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 1 }).format(metric.value);
  const unit = metric.unit ?? fallbackUnit;
  return [`${value}${unit ? ` ${unit}` : ''}`, qualityLabel(metric.state)];
}

export function BigScreenPage({ site, principal }: ProductPageProps) {
  const [scene, setScene] = useState<BigScreenScene>('overview');
  const [clock, setClock] = useState(() => new Date());
  useEffect(() => {
    const timer = window.setInterval(() => setClock(new Date()), 1000);
    return () => window.clearInterval(timer);
  }, []);
  const clockText = useMemo(() => clock.toLocaleTimeString('zh-CN', { hour12: false }), [clock]);
  const summaryQuery = useSiteDashboardSummary(principal.context.tenantId, site.id, `${principal.session.id}:${principal.authorization.policyRevision}`);
  const summary = summaryQuery.data;
  const [powerValue, powerState] = formatBigScreenMetric(summary?.fastMetrics.currentPower, 'kW');
  const [copValue, copState] = formatBigScreenMetric(summary?.slowMetrics.cop);
  const [energyValue, energyState] = formatBigScreenMetric(summary?.slowMetrics.siteLocalDayEnergy, 'kWh');
  const [savingsValue, savingsState] = formatBigScreenMetric(summary?.slowMetrics.baselineSavings, '%');
  const availabilityValue = summary?.devicePopulation.availabilityPercent == null ? '—' : `${new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 1 }).format(summary.devicePopulation.availabilityPercent)}%`;
  const alarmValue = summary?.fastMetrics.openAlarms.activeCount == null ? '—' : String(summary.fastMetrics.openAlarms.activeCount);

  return (
    <ProductBoundary site={site} testId="real-site-route-bigscreen" state={summaryQuery.isPending ? 'LOADING' : summary?.quality ?? 'UNAVAILABLE'}>
      <main className="bigscreen-shell real-bigscreen-shell">
        <span className="real-shell-sr-only" data-testid="real-shell-site">{site.displayName}</span>
        <div className="bigscreen-stage">
          <header className="bigscreen-header">
            <div className="bigscreen-brand"><div className="bigscreen-brand-mark"><LineChart /></div><div className="bigscreen-brand-copy"><span className="bigscreen-eyebrow">HVAC INTELLIGENT OPERATIONS</span><h1>智慧能源运行大屏</h1><p>{site.displayName} · {site.code}</p></div></div>
            <nav className="bigscreen-scenes" aria-label="大屏场景">
              {([['overview', '运行总览'], ['energy', '能耗分析'], ['operations', '运维行动']] as [BigScreenScene, string][]).map(([value, label]) => <button key={value} type="button" className={`bigscreen-scene-button${scene === value ? ' is-active' : ''}`} onClick={() => setScene(value)}>{label}</button>)}
            </nav>
            <div className="bigscreen-header-meta"><span className="bigscreen-live-status">权威 Summary · {summaryQuery.isPending ? '加载中' : qualityLabel(summary?.quality)}</span><span className="bigscreen-meta-item is-optional">{summary?.asOf ? `更新 ${new Date(summary.asOf).toLocaleString('zh-CN')}` : site.timezone}</span><strong className="bigscreen-clock">{clockText}</strong></div>
          </header>

          <section className="bigscreen-kpi-band" aria-label="运行关键指标">
            {[
              ['实时功率', powerValue, powerState], ['综合 COP', copValue, copState], ['今日能耗', energyValue, energyState], ['今日节能', savingsValue, savingsState], ['设备可用率', availabilityValue, qualityLabel(summary?.devicePopulation.state)], ['活动告警', alarmValue, qualityLabel(summary?.fastMetrics.openAlarms.state)],
            ].map(([label, value, sub]) => <div className="bigscreen-kpi-card" key={label}><span className="bigscreen-kpi-icon"><Zap /></span><span className="bigscreen-kpi-copy"><span className="bigscreen-kpi-label">{label}</span><strong className="bigscreen-kpi-value">{value}</strong><span className="bigscreen-kpi-sub">{sub}</span></span></div>)}
          </section>

          <section className="bigscreen-body" data-scene={scene}>
            <div className="bigscreen-column bigscreen-column-left">
              <BigScreenPanel eyebrow="ENERGY" title="站点本地日能耗">{summary ? <FactGrid items={[{ label: '本地日能耗', value: energyValue }, { label: '数据状态', value: energyState }]} /> : <EmptyState>能耗摘要尚未加载</EmptyState>}</BigScreenPanel>
              <BigScreenPanel eyebrow="PERFORMANCE" title="系统效率与基线"><EmptyState>效率与节能基线待接入</EmptyState></BigScreenPanel>
              <BigScreenPanel eyebrow="COST" title="峰平谷费用结构"><EmptyState>分时成本模型待接入</EmptyState></BigScreenPanel>
            </div>
            <div className="bigscreen-main">
              <BigScreenPanel eyebrow="CENTRAL PLANT" title="冷源系统运行总览" accent>
                <div className="bigscreen-system-meta"><span>站点 <strong>{site.displayName}</strong></span><span>数据范围 <strong className="is-accent">设备 · 实时数据 · 能源</strong></span></div>
                <div className="bigscreen-system-canvas"><EmptyState>大屏专用系统拓扑尚未接入；不会使用演示场景数据替代</EmptyState></div>
                <div className="bigscreen-device-rail">{[
                  ['已登记', summary?.devicePopulation.registered ?? '—', '设备台账'], ['在线', summary?.devicePopulation.online ?? '—', '实时状态'], ['离线', summary?.devicePopulation.offline ?? '—', '实时状态'], ['陈旧', summary?.devicePopulation.stale ?? '—', '数据新鲜度'], ['未知/不可用', summary ? summary.devicePopulation.unknown + summary.devicePopulation.unavailable : '—', '不进入可用率分母'],
                ].map(([label, value, detail]) => <div className="bigscreen-device-item" key={label}><span className="bigscreen-device-head"><strong>{label}</strong><i /></span><span className="bigscreen-device-data"><strong>{value}</strong><span>{detail}</span></span></div>)}</div>
              </BigScreenPanel>
            </div>
            <div className="bigscreen-column bigscreen-column-right">
              <BigScreenPanel eyebrow="ASSET HEALTH" title="设备健康"><div className="bigscreen-health-summary"><div className="bigscreen-health-score"><strong>{availabilityValue}</strong><span>{qualityLabel(summary?.devicePopulation.state)}</span></div></div></BigScreenPanel>
              <BigScreenPanel eyebrow="FDD" title="故障诊断"><EmptyState>故障诊断摘要待接入</EmptyState></BigScreenPanel>
              <BigScreenPanel eyebrow="ALARM" title="活动告警">{summary ? <FactGrid items={[{ label: '活动告警', value: alarmValue }, { label: '最高级别', value: summary.fastMetrics.openAlarms.highestSeverity ?? '无' }, { label: '数据状态', value: qualityLabel(summary.fastMetrics.openAlarms.state) }]} /> : <EmptyState>告警摘要尚未加载</EmptyState>}</BigScreenPanel>
              <BigScreenPanel eyebrow="OPTIMIZATION" title="优化机会"><EmptyState>优化机会摘要待接入</EmptyState></BigScreenPanel>
            </div>
          </section>
          <footer className="bigscreen-footer"><span className="bigscreen-footer-item"><ShieldCheck /><strong>所有空值均表示能力或数据尚不可用，不代表 0</strong></span><span className="bigscreen-footer-item"><Building2 />{site.timezone}</span><Button className="bigscreen-exit-button" asChild><a href={siteRoute(site, 'overview')}>退出大屏</a></Button></footer>
        </div>
      </main>
    </ProductBoundary>
  );
}
