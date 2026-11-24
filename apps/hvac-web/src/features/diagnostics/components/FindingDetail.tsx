import { useQuery } from '@tanstack/react-query';
import { getRouteApi, Link } from '@tanstack/react-router';
import { ClipboardPlus, LineChart, Link2, SlidersHorizontal } from 'lucide-react';
import { useMemo, type ReactNode } from 'react';
import { toast } from 'sonner';
import { siteAssetModelQuery } from '@/api/site-asset-model';
import { listWorkOrders } from '@/api/work-orders';
import { BrandMark } from '@/components/brand-mark';
import { InfoTip } from '@/components/info-tip';
import { Button } from '@/components/ui/button';
import { ComposedChart } from '@/components/ui/composed-chart';
import { Skeleton } from '@/components/ui/skeleton';
import { useAssistantPanel } from '@/features/assistant/assistant-panel-store';
import type { PlantView } from '@/features/operations/realtime/plant-model';
import { deviceSpanHistoryQuery } from '@/features/operations/systems-devices/device-history';
import { buildTrendCatalog } from '@/features/operations/trends/trends-model';
import { buildFindingTrendSearch } from '@/features/operations/trends/trends-overlays';
import { STATUS_LABELS } from '@/features/work-orders/work-order-presentation';
import { workOrderKeys } from '@/features/work-orders/work-order-queries';
import { formatDecimal, formatTime } from '@/lib/operator-format';
import { LOW_DELTA_T, ruleFor, severity, type DiagnosticRule, type Finding } from '../diagnostics-model';
import { FindingSeverityBadge, SEVERITY } from '../finding-presentation';

const HOUR_MS = 3_600_000;
const QUARTER_MS = 15 * 60_000;
const DELTA_T_KEY = 'btu_meter.temperature_difference';

const siteRoute = getRouteApi('/_app/_site');

function clock(value: string, timeZone: string) {
  return new Intl.DateTimeFormat('zh-CN', { timeZone, hour: '2-digit', minute: '2-digit', hour12: false }).format(new Date(value));
}

function EvidenceChart({ finding, plant, meterId, rule }: {
  readonly finding: Finding;
  readonly plant: PlantView;
  readonly meterId: string | undefined;
  readonly rule: DiagnosticRule | undefined;
}) {
  const { site, principal } = siteRoute.useRouteContext();
  const isImbalance = finding.findingType === 'METER_IMBALANCE';
  const allowed = principal.authorization.capabilities.includes('telemetry.history.read');
  const pointCode = plant.devices.find((device) => device.deviceId === meterId)?.row.telemetryPoints
    .find((point) => point.sourceKey === DELTA_T_KEY && point.status === 'ACTIVE')?.pointCode;
  const end = Date.parse(finding.evaluationTo);
  const nextQuarter = Math.floor(Date.now() / QUARTER_MS) * QUARTER_MS + QUARTER_MS;
  const from = new Date(Math.floor((end - 6 * HOUR_MS) / QUARTER_MS) * QUARTER_MS).toISOString();
  const to = new Date(Math.min(Math.ceil((end + 2 * HOUR_MS) / QUARTER_MS) * QUARTER_MS, nextQuarter)).toISOString();
  const history = useQuery({
    ...deviceSpanHistoryQuery({
      siteId: site.id,
      deviceId: meterId ?? '',
      keys: pointCode ? [pointCode] : [],
      from,
      to,
      timezone: site.timezone,
      csrfToken: principal.session.csrfToken,
    }),
    enabled: !isImbalance && allowed && Boolean(meterId && pointCode),
  });
  const points = (pointCode ? history.data?.get(pointCode) : undefined) ?? [];
  const windowStart = Date.parse(finding.evaluationFrom);
  // The quarter that holds the diagnosis window, marked on the time axis.
  const marked = points.find((point) => Date.parse(point.periodStart) <= windowStart && windowStart < Date.parse(point.periodStart) + QUARTER_MS);
  return (
    <section className="flex flex-col gap-2 border-b border-divider px-6 py-4">
      <h3 className="flex items-baseline gap-2 text-sm font-semibold">
        诊断依据
        <span className="text-xs font-normal text-muted-foreground">
          {isImbalance ? '计量回路偏差比比对结果' : '诊断前 6 小时至后 2 小时 · 每 15 分钟'}
        </span>
      </h3>
      {isImbalance ? (
        <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-divider bg-muted/40 px-4 py-3 text-sm">
          <div className="flex flex-col gap-1">
            <span className="font-medium text-foreground">
              {clock(finding.evaluationFrom, site.timezone)} – {clock(finding.evaluationTo, site.timezone)} 期间计量偏差比 {formatDecimal(finding.observedValue, 2)} {finding.valueUnit}（阈值 {formatDecimal(finding.thresholdValue, 1)} {finding.valueUnit}）
            </span>
            <span className="text-xs text-muted-foreground">请对照用能分析中的整栋表计与设备读数变化，排查互感器变比、通信丢包或未纳管负荷。</span>
          </div>
          <Button size="sm" variant="outline" asChild>
            <Link to="/energy-analysis/consumption" search={{ site: site.id }}>
              查看用能分析与表计比对 ›
            </Link>
          </Button>
        </div>
      ) : !allowed || !meterId || !pointCode ? (
        <p className="flex h-[180px] items-center justify-center text-sm text-muted-foreground">没有可用的温差记录</p>
      ) : history.isPending ? <Skeleton className="h-[180px]" /> : history.isError ? (
        <p className="flex h-[180px] items-center justify-center text-sm text-muted-foreground">温差记录暂时读不到</p>
      ) : points.length < 2 ? (
        <p className="flex h-[180px] items-center justify-center text-sm text-muted-foreground">这段时间没有温差记录</p>
      ) : (
        <ComposedChart
          data={points.map((point) => ({
            time: clock(point.periodStart, site.timezone),
            average: Number(point.average.toFixed(2)),
            minimum: Number(point.minimum.toFixed(2)),
          }))}
          xKey="time"
          series={[
            { key: 'average', label: '平均温差 (K)', type: 'line', color: 'var(--chart-1)' },
            { key: 'minimum', label: '最低温差 (K)', type: 'line', color: 'var(--chart-2)', dashed: true },
          ]}
          referenceLines={[
            { y: finding.thresholdValue, label: `阈值 ${formatDecimal(finding.thresholdValue, 1)}`, color: 'var(--destructive)' },
            ...(rule ? [{ y: rule.designValue, label: `设计 ${rule.designValue}` }] : []),
            ...(marked ? [{ x: clock(marked.periodStart, site.timezone), label: '诊断', color: 'var(--foreground)' }] : []),
          ]}
          showGrid
          showYAxis
          yDomain={[0, Math.ceil(Math.max((rule?.designValue ?? 0) + 1, finding.thresholdValue + 1, ...points.map((point) => point.maximum)))]}
          showLegend
          yFormatter={(value) => formatDecimal(value, 1)}
          animate={false}
          className="h-[200px]"
        />
      )}
    </section>
  );
}

function Property({ label, children }: { readonly label: string; readonly children: ReactNode }) {
  return (
    <>
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="mb-2.5 text-sm font-medium break-words">{children}</dd>
    </>
  );
}

function NumberedItem({ index, children }: { readonly index: number; readonly children: ReactNode }) {
  return (
    <li className="flex items-start gap-2 leading-[22px] text-secondary-foreground">
      <span className="mt-[3px] flex size-4 shrink-0 items-center justify-center rounded-full bg-secondary font-mono text-[10px] font-semibold text-secondary-foreground">{index + 1}</span>
      {children}
    </li>
  );
}

/** The selected finding beside the list: conclusion and actions, the evidence, what it means, and its properties. */
export function FindingDetail({ finding, assetName, plant, meterId, onCreate }: {
  readonly finding: Finding;
  readonly assetName: string;
  readonly plant: PlantView;
  readonly meterId: string | undefined;
  readonly onCreate: () => void;
}) {
  const { site, principal } = siteRoute.useRouteContext();
  const explain = useAssistantPanel((state) => state.explain);
  const rule = ruleFor(finding.findingType);
  const level = SEVERITY[severity(finding)];
  const isExcess = finding.findingType === 'METER_IMBALANCE';
  const deviationLabel = isExcess ? '超出阈值' : '低于阈值';
  const shortfall = finding.thresholdValue !== 0
    ? Math.round((Math.abs(finding.observedValue - finding.thresholdValue) / Math.abs(finding.thresholdValue)) * 100)
    : 0;
  const windowMinutes = Math.round((Date.parse(finding.evaluationTo) - Date.parse(finding.evaluationFrom)) / 60_000);
  const canCreateWorkOrder = principal.authorization.capabilities.includes('work-order.create');
  const canListWorkOrders = principal.authorization.capabilities.includes('work-order.list');
  const canReadTrends = principal.authorization.capabilities.includes('telemetry.history.read');
  const assetModel = useQuery({ ...siteAssetModelQuery(site.id), enabled: canReadTrends });
  const trendCatalog = useMemo(
    () => (assetModel.data ? buildTrendCatalog(assetModel.data) : null),
    [assetModel.data],
  );
  const workOrders = useQuery({
    queryKey: workOrderKeys.source(site.id, 'ASSET', finding.assetId),
    queryFn: ({ signal }) => listWorkOrders({ sourceDomain: 'ASSET', sourceRef: finding.assetId, limit: 20 }, { siteId: site.id, signal }),
    enabled: canListWorkOrders,
  });
  const copyLink = () => {
    void navigator.clipboard.writeText(window.location.href).then(() => toast.success('链接已复制'), () => toast.error('无法复制链接'));
  };
  const metricName = isExcess ? '偏差比' : rule ? '供回水温差' : '测量值';
  return (
    <section role="region" aria-label="诊断结论详情" className="flex min-h-0 min-w-0 flex-col overflow-y-auto @5xl/main:grid @5xl/main:grid-cols-[minmax(0,1fr)_240px] @5xl/main:overflow-hidden">
      <div className="flex min-w-0 shrink-0 flex-col @5xl/main:min-h-0 @5xl/main:shrink @5xl/main:overflow-y-auto">
        <header className="flex flex-col gap-2 border-b border-divider px-6 pt-[18px] pb-4">
          <div className="flex items-center gap-2.5 text-xs">
            <FindingSeverityBadge finding={finding} />
            <span className="text-muted-foreground">诊断时间 <span className="font-mono">{formatTime(finding.evaluationTo, site.timezone)}</span></span>
            <Button variant="ghost" size="icon-sm" className="ml-auto text-muted-foreground" aria-label="复制链接" onClick={copyLink}>
              <Link2 className="size-3.5" />
            </Button>
          </div>
          <h2 className="flex items-center gap-2 text-xl leading-7 font-semibold tracking-[-0.015em]">
            {rule?.title ?? '诊断结论'}
            <InfoTip>诊断结论是根据运行数据推断的，处理前请到现场核实</InfoTip>
          </h2>
          <p className="text-sm leading-[22px] text-secondary-foreground">
            最近 {windowMinutes} 分钟{metricName} {formatDecimal(finding.observedValue, 2)} {finding.valueUnit}，{deviationLabel} {formatDecimal(finding.thresholdValue, 1)} {finding.valueUnit}
            {rule ? `（设计 ${rule.designValue} ${rule.unit}）` : ''}。
          </p>
          <div className="mt-1 flex flex-wrap items-center gap-2">
            {canCreateWorkOrder ? (
              <Button size="sm" onClick={onCreate}>
                <ClipboardPlus data-icon="inline-start" />
                创建工单
              </Button>
            ) : null}
            <Button
              size="sm"
              variant="outline"
              onClick={() => explain(
                `请分析「${assetName}」的「${rule?.title ?? '诊断结论'}」：${formatTime(finding.evaluationTo, site.timezone)} 时${metricName}为 ${formatDecimal(finding.observedValue, 2)} ${finding.valueUnit}，${deviationLabel} ${formatDecimal(finding.thresholdValue, 1)} ${finding.valueUnit}，可能的原因和排查步骤是什么？`,
              )}
            >
              <BrandMark data-icon="inline-start" className="size-3.5 text-primary" />
              分析原因
            </Button>
            {canReadTrends ? (
              <Button size="sm" variant="outline" asChild>
                <Link
                  to="/operations/trends"
                  search={buildFindingTrendSearch({
                    siteId: site.id,
                    finding,
                    title: rule?.title ?? '诊断结论',
                    catalog: trendCatalog,
                  })}
                >
                  <LineChart data-icon="inline-start" />
                  查看当时趋势
                </Link>
              </Button>
            ) : null}
            {rule === LOW_DELTA_T ? (
              <Button size="sm" variant="outline" asChild>
                <Link to="/operations/control" search={{ site: site.id }}>
                  <SlidersHorizontal data-icon="inline-start" />
                  去调整水泵
                </Link>
              </Button>
            ) : null}
          </div>
        </header>

        <EvidenceChart finding={finding} plant={plant} meterId={meterId} rule={rule} />

        {rule ? (
          <div className="flex flex-col gap-4 px-6 py-4 text-sm">
            <section className="flex flex-col gap-1.5">
              <h3 className="font-semibold">说明</h3>
              <p className="leading-[22px] text-secondary-foreground">{rule.meaning}</p>
            </section>
            <div className="grid grid-cols-1 gap-6 @3xl/main:grid-cols-2 @3xl/main:gap-8">
              <section className="flex flex-col gap-2">
                <h3 className="font-semibold">常见原因</h3>
                <ol className="flex flex-col gap-1.5">
                  {rule.causes.map((cause, index) => <NumberedItem key={cause} index={index}>{cause}</NumberedItem>)}
                </ol>
              </section>
              <section className="flex flex-col gap-2">
                <h3 className="font-semibold">处理建议</h3>
                <ol className="flex flex-col gap-1.5">
                  {rule.actions.map((action, index) => <NumberedItem key={action} index={index}>{action}</NumberedItem>)}
                </ol>
              </section>
            </div>
          </div>
        ) : null}
      </div>

      <aside aria-label="属性" className="flex shrink-0 flex-col border-t border-divider bg-muted/40 px-4 py-4 @5xl/main:min-h-0 @5xl/main:overflow-y-auto @5xl/main:border-t-0 @5xl/main:border-l">
        <h3 className="mb-3 text-xs font-semibold text-muted-foreground">属性</h3>
        <dl className="flex flex-col">
          <Property label="规则">{rule?.title ?? '诊断结论'}</Property>
          <Property label="设备">
            {meterId ? (
              <Link to="/operations/systems-devices" search={{ site: site.id, inspect: meterId }} className="text-primary hover:underline">{assetName} ›</Link>
            ) : assetName}
          </Property>
          <Property label="测量值">{formatDecimal(finding.observedValue, 2)} {finding.valueUnit}</Property>
          <Property label="阈值">{formatDecimal(finding.thresholdValue, 1)} {finding.valueUnit}</Property>
          <Property label="偏离">{level.label} · {deviationLabel} {shortfall}%</Property>
          <Property label="诊断窗口"><span className="font-mono">{clock(finding.evaluationFrom, site.timezone)} – {clock(finding.evaluationTo, site.timezone)}</span></Property>
          <Property label="可信度">{formatDecimal(finding.confidence * 100, 0)}%</Property>
          {canListWorkOrders ? (
            <Property label="设备工单">
              {workOrders.isPending ? <Skeleton className="h-5 w-24" /> : workOrders.isError ? <span className="font-normal text-muted-foreground">暂时读不到</span> : workOrders.data.items.length === 0 ? (
                <span className="font-normal text-muted-foreground">无</span>
              ) : (
                <ul className="flex flex-col gap-1.5">
                  {workOrders.data.items.slice(0, 5).map((workOrder) => (
                    <li key={workOrder.workOrderId}>
                      <Link to="/operations/work-center" search={{ site: site.id, view: 'all', inspect: workOrder.workOrderId }} className="flex flex-col rounded-sm hover:underline">
                        <span className="truncate text-primary">{workOrder.title}</span>
                        <span className="text-xs font-normal text-muted-foreground">{STATUS_LABELS[workOrder.status]}</span>
                      </Link>
                    </li>
                  ))}
                </ul>
              )}
            </Property>
          ) : null}
        </dl>
      </aside>
    </section>
  );
}
