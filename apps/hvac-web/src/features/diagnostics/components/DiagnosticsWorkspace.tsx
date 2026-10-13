import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { getRouteApi, Link, useNavigate } from '@tanstack/react-router';
import { ClipboardPlus, Link2, SearchCheck, SlidersHorizontal, Stethoscope } from 'lucide-react';
import { useMemo, useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import { listWorkOrders } from '@/api/work-orders';
import { PageHeader } from '@/blocks/page-header';
import { Panel } from '@/blocks/panel';
import { InfoTip } from '@/components/info-tip';
import { Main } from '@/components/layout/Main';
import { SeverityMark } from '@/components/severity-mark';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { ComposedChart } from '@/components/ui/composed-chart';
import { Skeleton } from '@/components/ui/skeleton';
import { Spinner } from '@/components/ui/spinner';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { plantReading, type PlantReading, type PlantView } from '@/features/operations/realtime/plant-model';
import { useRealtimePlant } from '@/features/operations/realtime/use-realtime-plant';
import { deviceSpanHistoryQuery } from '@/features/operations/systems-devices/device-history';
import { CreateWorkOrderDialog } from '@/features/work-orders/CreateWorkOrderDialog';
import { workOrderKeys } from '@/features/work-orders/work-order-queries';
import { STATUS_LABELS } from '@/features/work-orders/work-order-presentation';
import { formatCompactTime, formatDecimal, formatTime } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import { LOW_DELTA_T, lowDeltaTTarget, meterOf, ruleFor, severity, type DiagnosticRule, type Finding, type RuleRevision } from '../diagnostics-model';
import { FindingSeverityBadge, SEVERITY } from '../finding-presentation';
import { DiagnosticsRequestError, diagnosticsKeys, evaluateRule, lowDeltaTRuleQuery, siteFindingsQuery, type EvaluationOutcome } from '../query';

const DAY_MS = 86_400_000;
const HOUR_MS = 3_600_000;
const QUARTER_MS = 15 * 60_000;
const DELTA_T_KEY = 'btu_meter.temperature_difference';

const siteRoute = getRouteApi('/_app/_site');
const pageRoute = getRouteApi('/_app/_site/operations/diagnostics');

type Range = '7d' | 'all';

const OUTCOME_LABEL = { FINDING: '发现问题', ONGOING: '仍在持续', CLEAR: '正常' } as const;

const kelvin = (value: number, digits = 1) => `${formatDecimal(value, digits)} K`;

function clock(value: string, timeZone: string) {
  return new Intl.DateTimeFormat('zh-CN', { timeZone, hour: '2-digit', minute: '2-digit', hour12: false }).format(new Date(value));
}

function dayKey(value: string | number, timeZone: string) {
  return new Intl.DateTimeFormat('en-CA', { timeZone, year: 'numeric', month: '2-digit', day: '2-digit' }).format(new Date(value));
}

/** Findings by the Site's calendar day, newest first, labelled 今天 / 昨天 / MM/DD. */
function groupByDay(findings: readonly Finding[], timeZone: string, now: number) {
  const today = dayKey(now, timeZone);
  const yesterday = dayKey(now - DAY_MS, timeZone);
  const groups: { key: string; label: string; findings: Finding[] }[] = [];
  for (const finding of findings) {
    const key = dayKey(finding.evaluationTo, timeZone);
    let group = groups.find((candidate) => candidate.key === key);
    if (!group) {
      const date = `${key.slice(5, 7)}/${key.slice(8, 10)}`;
      group = { key, label: key === today ? `今天 · ${date}` : key === yesterday ? `昨天 · ${date}` : date, findings: [] };
      groups.push(group);
    }
    group.findings.push(finding);
  }
  return groups;
}

/** The current value on a scale from zero past the design value, with the threshold zone and the design value marked. */
function ThresholdTrack({ rule, threshold, value }: { readonly rule: DiagnosticRule; readonly threshold: number | null; readonly value: number | null }) {
  const top = rule.designValue + 2;
  const at = (x: number) => `${(Math.min(top, Math.max(0, x)) / top) * 100}%`;
  const below = value !== null && threshold !== null && value < threshold;
  const marks = [...(threshold === null ? [] : [{ x: threshold, label: `阈值 ${threshold}` }]), { x: rule.designValue, label: `设计 ${rule.designValue}` }];
  return (
    <div className="relative h-9 min-w-48 flex-1" aria-hidden="true">
      <div className="absolute inset-x-0 top-2.5 h-1.5 rounded-full bg-muted" />
      {threshold !== null ? <div className="absolute top-2.5 h-1.5 rounded-l-full bg-destructive-soft" style={{ left: 0, width: at(threshold) }} /> : null}
      {marks.map((mark) => (
        <div key={mark.label} className="absolute top-1 flex -translate-x-1/2 flex-col items-center" style={{ left: at(mark.x) }}>
          <span className="h-4 w-px bg-subtle-foreground" />
          <span className="mt-0.5 text-[11px] whitespace-nowrap text-muted-foreground tabular-nums">{mark.label}</span>
        </div>
      ))}
      {value !== null ? (
        <span
          className={cn('absolute top-[5px] size-3.5 -translate-x-1/2 rounded-full border-2 border-card shadow-sm', below ? 'bg-destructive' : 'bg-primary')}
          style={{ left: at(value) }}
        />
      ) : null}
    </div>
  );
}

/** One diagnosis rule as a single bar: what it watches, the live value against its threshold, the last result and a manual run. */
function RuleBar({ rule, current, target, reading, outcome, latest, timeZone, running, onRun }: {
  readonly rule: DiagnosticRule;
  /** The revision FDD evaluates now, as its query reads it. */
  readonly current: { readonly data?: RuleRevision; readonly isPending: boolean };
  readonly target: { assetName: string } | null;
  readonly reading: PlantReading | null;
  readonly outcome: (EvaluationOutcome & { readonly at: number }) | null;
  readonly latest: Finding | undefined;
  readonly timeZone: string;
  readonly running: boolean;
  readonly onRun: () => void;
}) {
  const live = reading?.state === 'PRESENT' && reading.numeric !== null ? reading.numeric : null;
  const revision = outcome?.rule ?? current.data;
  const threshold = revision?.minimumDeltaTC ?? null;
  return (
    <Panel aria-label={rule.title}>
      <div className="flex min-h-[72px] items-center gap-5 px-5 py-3">
        <span className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-primary-soft text-primary"><Stethoscope className="size-[18px]" aria-hidden="true" /></span>
        <div className="flex min-w-0 flex-col gap-0.5">
          <span className="flex items-center gap-2 text-sm font-semibold whitespace-nowrap">
            {rule.title}
            {revision ? <Badge variant="secondary">第 {revision.revision} 版</Badge> : null}
            {revision ? (
              <InfoTip>{`系统定时自动诊断：最近 ${revision.windowMinutes} 分钟温差低于 ${revision.minimumDeltaTC} K（设计 ${rule.designValue} K）时记为问题，同一问题 24 小时内只记一次。按第 ${revision.revision} 版规则。`}</InfoTip>
            ) : null}
          </span>
          <span className="truncate text-xs text-muted-foreground">
            {target ? target.assetName : '没有登记冷量表，无法诊断'}
            {revision ? ` · ${revision.windowMinutes} 分钟窗口` : current.isPending ? ' · 正在读取诊断规则…' : ' · 诊断规则暂不可用'}
          </span>
        </div>
        <span className="h-9 w-px shrink-0 bg-divider" aria-hidden="true" />
        <div className="flex shrink-0 flex-col gap-0.5">
          <span className="text-xs text-muted-foreground">当前温差</span>
          <span className="text-xl leading-6 font-semibold tabular-nums">
            {live !== null ? formatDecimal(live, 1) : '—'}
            <span className="ml-0.5 text-xs font-normal text-muted-foreground">K</span>
          </span>
        </div>
        <ThresholdTrack rule={rule} threshold={threshold} value={live} />
        <span className="h-9 w-px shrink-0 bg-divider" aria-hidden="true" />
        <div className="flex shrink-0 flex-col gap-0.5">
          <span className="text-xs text-muted-foreground">{outcome ? '本次诊断' : '最近发现'}</span>
          {outcome ? (
            <span className="flex items-center gap-2 text-sm font-medium">
              <span className={cn('rounded-md px-1.5 text-xs leading-5', outcome.status === 'CLEAR' ? 'bg-success-soft text-success-text' : 'bg-warning-soft text-warning-text')}>
                {OUTCOME_LABEL[outcome.status]}
              </span>
              <span className="font-mono text-xs text-muted-foreground">{clock(new Date(outcome.at).toISOString(), timeZone)}</span>
            </span>
          ) : (
            <span className="font-mono text-sm font-medium">{latest ? formatCompactTime(latest.evaluationTo, timeZone) : <span className="font-sans text-muted-foreground">无</span>}</span>
          )}
        </div>
        <Button className="shrink-0" disabled={!target || running} onClick={onRun} title="系统会定时自动诊断，也可以现在诊断一次">
          {running ? <Spinner data-icon="inline-start" /> : <SearchCheck data-icon="inline-start" />}
          立即诊断
        </Button>
      </div>
      {outcome ? (
        <p className="border-t border-divider px-5 py-2.5 text-sm text-secondary-foreground">
          {`本次诊断：最近 ${outcome.rule.windowMinutes} 分钟温差 ${kelvin(outcome.observedValue)}，${outcome.status === 'FINDING' ? `低于阈值 ${outcome.rule.minimumDeltaTC} K，已记为诊断发现。` : outcome.status === 'ONGOING' ? `仍低于阈值 ${outcome.rule.minimumDeltaTC} K，问题仍在持续，24 小时内不重复记录。` : `不低于阈值 ${outcome.rule.minimumDeltaTC} K。`}`}
        </p>
      ) : null}
    </Panel>
  );
}

/** The meter's temperature difference around the diagnosis window, against the threshold and the design value. */
function EvidenceChart({ finding, plant, meterId, rule }: {
  readonly finding: Finding;
  readonly plant: PlantView;
  readonly meterId: string | undefined;
  readonly rule: DiagnosticRule | undefined;
}) {
  const { site, principal } = siteRoute.useRouteContext();
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
    enabled: allowed && Boolean(meterId && pointCode),
  });
  const points = (pointCode ? history.data?.get(pointCode) : undefined) ?? [];
  const windowStart = Date.parse(finding.evaluationFrom);
  // The quarter that holds the diagnosis window, marked on the time axis.
  const marked = points.find((point) => Date.parse(point.periodStart) <= windowStart && windowStart < Date.parse(point.periodStart) + QUARTER_MS);
  return (
    <section className="flex flex-col gap-2 border-b border-divider px-6 py-4">
      <h3 className="flex items-baseline gap-2 text-sm font-semibold">
        诊断证据
        <span className="text-xs font-normal text-muted-foreground">诊断前 6 小时至后 2 小时 · 每 15 分钟</span>
      </h3>
      {!allowed || !meterId || !pointCode ? (
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

/** The selected finding beside the list: conclusion and actions, the evidence, what it means, and its properties. */
function FindingDetail({ finding, assetName, plant, meterId, onCreate }: {
  readonly finding: Finding;
  readonly assetName: string;
  readonly plant: PlantView;
  readonly meterId: string | undefined;
  readonly onCreate: () => void;
}) {
  const { site, principal } = siteRoute.useRouteContext();
  const rule = ruleFor(finding.findingType);
  const level = SEVERITY[severity(finding)];
  const shortfall = Math.round((Math.abs(finding.thresholdValue - finding.observedValue) / finding.thresholdValue) * 100);
  const windowMinutes = Math.round((Date.parse(finding.evaluationTo) - Date.parse(finding.evaluationFrom)) / 60_000);
  const canListWorkOrders = principal.authorization.capabilities.includes('work-order.list');
  const workOrders = useQuery({
    queryKey: workOrderKeys.source(site.id, 'ASSET', finding.assetId),
    queryFn: ({ signal }) => listWorkOrders({ sourceDomain: 'ASSET', sourceRef: finding.assetId, limit: 20 }, { siteId: site.id, signal }),
    enabled: canListWorkOrders,
  });
  const copyLink = () => {
    void navigator.clipboard.writeText(window.location.href).then(() => toast.success('链接已复制'), () => toast.error('无法复制链接'));
  };
  return (
    <section role="region" aria-label="诊断发现详情" className="grid min-h-0 min-w-0 grid-cols-[minmax(0,1fr)_240px]">
      <div className="flex min-h-0 min-w-0 flex-col overflow-y-auto">
        <header className="flex flex-col gap-2 border-b border-divider px-6 pt-[18px] pb-4">
          <div className="flex items-center gap-2.5 text-xs">
            <FindingSeverityBadge finding={finding} />
            <span className="text-muted-foreground">诊断时间 <span className="font-mono">{formatTime(finding.evaluationTo, site.timezone)}</span></span>
            <Button variant="ghost" size="icon-sm" className="ml-auto text-muted-foreground" aria-label="复制链接" onClick={copyLink}>
              <Link2 className="size-3.5" />
            </Button>
          </div>
          <h2 className="flex items-center gap-2 text-xl leading-7 font-semibold tracking-[-0.015em]">
            {rule?.title ?? '诊断发现'}
            <InfoTip>诊断结论是根据运行数据推断的，处理前请到现场核实</InfoTip>
          </h2>
          <p className="text-sm leading-[22px] text-secondary-foreground">
            最近 {windowMinutes} 分钟{rule ? '供回水温差' : '测量值'} {formatDecimal(finding.observedValue, 2)} {finding.valueUnit}，低于阈值 {formatDecimal(finding.thresholdValue, 1)} {finding.valueUnit}
            {rule ? `（设计 ${rule.designValue} ${rule.unit}）` : ''}。
          </p>
          <div className="mt-1 flex flex-wrap items-center gap-2">
            <Button size="sm" onClick={onCreate}>
              <ClipboardPlus data-icon="inline-start" />
              创建工单
            </Button>
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
            <div className="grid grid-cols-2 gap-8">
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

      <aside aria-label="属性" className="flex min-h-0 flex-col overflow-y-auto border-l border-divider bg-muted/40 px-4 py-4">
        <h3 className="mb-3 text-xs font-semibold text-muted-foreground">属性</h3>
        <dl className="flex flex-col">
          <Property label="规则">{rule?.title ?? '诊断发现'}</Property>
          <Property label="设备">
            {meterId ? (
              <Link to="/operations/systems-devices" search={{ site: site.id, inspect: meterId }} className="text-primary hover:underline">{assetName} ›</Link>
            ) : assetName}
          </Property>
          <Property label="测量值">{formatDecimal(finding.observedValue, 2)} {finding.valueUnit}</Property>
          <Property label="阈值">{formatDecimal(finding.thresholdValue, 1)} {finding.valueUnit}</Property>
          <Property label="偏离">{level.label} · 低于阈值 {shortfall}%</Property>
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

function NumberedItem({ index, children }: { readonly index: number; readonly children: ReactNode }) {
  return (
    <li className="flex items-start gap-2 leading-[22px] text-secondary-foreground">
      <span className="mt-[3px] flex size-4 shrink-0 items-center justify-center rounded-full bg-secondary font-mono text-[10px] font-semibold text-secondary-foreground">{index + 1}</span>
      {children}
    </li>
  );
}

export function DiagnosticsWorkspace() {
  const { site, principal } = siteRoute.useRouteContext();
  const search = pageRoute.useSearch();
  const navigate = useNavigate({ from: '/operations/diagnostics' });
  const queryClient = useQueryClient();
  const findings = useQuery(siteFindingsQuery(site.id));
  const currentRule = useQuery(lowDeltaTRuleQuery(site.id));
  const { plant, registry } = useRealtimePlant();
  const [outcome, setOutcome] = useState<(EvaluationOutcome & { readonly at: number }) | null>(null);
  const [creating, setCreating] = useState(false);

  const model = registry.data?.assetModel;
  const target = useMemo(() => (model ? lowDeltaTTarget(model) : null), [model]);
  const assetName = (assetId: string) => model?.assets.find((asset) => asset.id === assetId)?.displayName ?? '未登记设备';
  const setSearch = (next: { range?: Range; inspect?: string }) => void navigate({ search: (previous) => ({ ...previous, ...next }), replace: true });

  const run = useMutation({
    mutationFn: () => evaluateRule(site.id, principal.session.csrfToken, target!),
    onSuccess: (result) => {
      setOutcome({ ...result, at: Date.now() });
      if (result.status === 'FINDING') {
        toast.warning(`发现问题：${LOW_DELTA_T.title}`);
        void queryClient.invalidateQueries({ queryKey: diagnosticsKeys.findings(site.id) });
      } else if (result.status === 'ONGOING') toast.warning(`问题仍在持续：${LOW_DELTA_T.title}`);
      else toast.success('诊断完成，未发现问题');
    },
    onError: (error) => toast.error(error instanceof DiagnosticsRequestError ? error.message : '诊断服务暂时不可用'),
  });

  const all = findings.data ?? [];
  const now = Date.now();
  const recent = all.filter((finding) => Date.parse(finding.evaluationTo) >= now - 7 * DAY_MS);
  const range: Range = search.range ?? '7d';
  const shown = range === '7d' ? recent : all;
  const groups = groupByDay(shown, site.timezone, now);
  const selected = all.find((finding) => finding.id === search.inspect) ?? shown[0];
  const selectedRule = selected ? ruleFor(selected.findingType) : undefined;
  const selectedName = selected ? assetName(selected.assetId) : '';

  return (
    <Main fixed>
      <PageHeader title="故障诊断" description={site.displayName} />

      {registry.isPending ? <Skeleton className="h-[72px] rounded-xl" /> : (
        <RuleBar
          rule={LOW_DELTA_T}
          current={currentRule}
          target={target}
          reading={plantReading(plant, DELTA_T_KEY)}
          outcome={outcome}
          latest={all[0]}
          timeZone={site.timezone}
          running={run.isPending}
          onRun={() => run.mutate()}
        />
      )}

      <Panel aria-label="诊断发现" className="grid h-[calc(100svh-262px)] min-h-[540px] grid-cols-[380px_minmax(0,1fr)]">
        <div className="flex min-h-0 flex-col border-r">
          <ToggleGroup
            type="single"
            value={range}
            onValueChange={(value) => { if (value) setSearch({ range: value as Range, inspect: undefined }); }}
            aria-label="诊断发现范围"
            className="h-11 w-full shrink-0 justify-start gap-5 rounded-none border-b border-divider px-4"
          >
            {([['7d', '近 7 天', recent.length], ['all', '全部', all.length]] as const).map(([value, label, count]) => (
              <ToggleGroupItem
                key={value}
                value={value}
                className="h-full flex-none gap-1.5 rounded-none border-0 px-0 text-[13px] font-medium text-muted-foreground shadow-[inset_0_-2px_0_transparent] hover:bg-transparent hover:text-foreground data-[state=on]:bg-transparent data-[state=on]:text-foreground data-[state=on]:shadow-[inset_0_-2px_0_var(--primary)]"
              >
                {label}
                {findings.isSuccess ? <span className="font-mono text-[11px] text-subtle-foreground">{count}</span> : null}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>

          <div className="min-h-0 flex-1 overflow-y-auto">
            {findings.isPending ? (
              <div className="flex flex-col gap-2 p-4"><Skeleton className="h-14" /><Skeleton className="h-14" /><Skeleton className="h-14" /></div>
            ) : findings.isError ? (
              <div className="flex flex-col items-start gap-2 px-4 py-6">
                <p className="text-sm text-destructive-text">诊断发现暂时读不到：{findings.error.message}</p>
                <Button variant="outline" size="sm" onClick={() => void findings.refetch()}>重试</Button>
              </div>
            ) : shown.length === 0 ? (
              <div className="flex flex-col items-center gap-2 px-6 py-12 text-center">
                <span className="flex size-9 items-center justify-center rounded-xl bg-success-soft text-success-text"><Stethoscope className="size-[18px]" aria-hidden="true" /></span>
                <p className="text-sm font-semibold">{range === '7d' ? '近 7 天没有诊断发现' : '还没有诊断发现'}</p>
                <p className="text-xs text-muted-foreground">
                  {range === '7d' && all.length > 0 ? `更早的 ${all.length} 条在「全部」里` : '诊断发现问题时会记在这里'}
                </p>
              </div>
            ) : (
              <ul aria-label="诊断发现列表">
                {groups.map((group) => (
                  <li key={group.key}>
                    <div className="flex h-[34px] items-center bg-muted px-4 text-xs font-medium text-muted-foreground">
                      {group.label}
                      <span className="ml-auto font-mono text-subtle-foreground">{group.findings.length}</span>
                    </div>
                    <ul>
                      {group.findings.map((finding) => {
                        const isSelected = finding.id === selected?.id;
                        const level = SEVERITY[severity(finding)];
                        return (
                          <li key={finding.id}>
                            <button
                              type="button"
                              aria-current={isSelected ? 'true' : undefined}
                              onClick={() => setSearch({ inspect: finding.id })}
                              className={cn(
                                'relative grid w-full grid-cols-[12px_minmax(0,1fr)_auto] gap-x-2.5 gap-y-[3px] border-b border-divider px-4 pt-[11px] pb-3 text-left outline-none hover:bg-accent focus-visible:bg-accent',
                                isSelected && 'bg-primary-soft hover:bg-primary-soft before:absolute before:inset-y-0 before:left-0 before:w-0.5 before:bg-primary',
                              )}
                            >
                              <span className="row-span-2 mt-[5px]"><SeverityMark level={level.level} label={level.label} /></span>
                              <span className="truncate text-sm font-semibold">{ruleFor(finding.findingType)?.title ?? '诊断发现'}</span>
                              <span className="font-mono text-xs whitespace-nowrap text-subtle-foreground">{clock(finding.evaluationTo, site.timezone)}</span>
                              <span className="col-span-2 col-start-2 truncate text-xs text-muted-foreground">
                                {assetName(finding.assetId)}
                                {' · '}<span className="tabular-nums">{formatDecimal(finding.observedValue, 1)} / {formatDecimal(finding.thresholdValue, 1)} {finding.valueUnit}</span>
                                {' · '}{level.label}
                              </span>
                            </button>
                          </li>
                        );
                      })}
                    </ul>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>

        {selected ? (
          <FindingDetail
            key={selected.id}
            finding={selected}
            assetName={selectedName}
            plant={plant}
            meterId={model ? meterOf(model, selected.assetId) : undefined}
            onCreate={() => setCreating(true)}
          />
        ) : (
          <div className="flex items-center justify-center p-10 text-sm text-muted-foreground">
            {findings.isPending ? null : all.length === 0 ? '没有可查看的诊断发现' : '从左侧选择一条诊断发现'}
          </div>
        )}
      </Panel>

      {selected ? (
        <CreateWorkOrderDialog
          open={creating}
          onOpenChange={setCreating}
          title={`为「${selectedRule?.title ?? '诊断发现'}」创建工单`}
          initial={{
            title: `${selectedName}：${selectedRule?.title ?? '诊断发现'}`,
            priority: SEVERITY[severity(selected)].priority,
            description: `${formatTime(selected.evaluationTo, site.timezone)} 诊断发现：测量值 ${formatDecimal(selected.observedValue, 2)} ${selected.valueUnit}，低于阈值 ${formatDecimal(selected.thresholdValue, 1)} ${selected.valueUnit}。${selectedRule ? `\n处理建议：${selectedRule.actions.join('；')}。` : ''}`,
          }}
          sourceReferences={[{ domain: 'ASSET', resourceId: selected.assetId, relationship: 'ORIGIN' }]}
          onCreated={() => {
            toast.success('工单已创建');
            void queryClient.invalidateQueries({ queryKey: workOrderKeys.all(site.id) });
          }}
        />
      ) : null}
    </Main>
  );
}
