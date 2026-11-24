import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { getRouteApi, useNavigate } from '@tanstack/react-router';
import { SearchCheck, Stethoscope } from 'lucide-react';
import { useMemo, useState } from 'react';
import { toast } from 'sonner';
import { GatewayRequestError } from '@/api/gateway-client';
import { PageHeader } from '@/blocks/page-header';
import { Panel } from '@/blocks/panel';
import { InfoTip } from '@/components/info-tip';
import { Main } from '@/components/layout/Main';
import { SeverityMark } from '@/components/severity-mark';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { Spinner } from '@/components/ui/spinner';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { plantReading, type PlantReading } from '@/features/operations/realtime/plant-model';
import { useRealtimePlant } from '@/features/operations/realtime/use-realtime-plant';
import { CreateWorkOrderDialog } from '@/features/work-orders/CreateWorkOrderDialog';
import { workOrderKeys } from '@/features/work-orders/work-order-queries';
import { formatCompactTime, formatDecimal, formatTime } from '@/lib/operator-format';
import { isOneOf } from '@/lib/typed';
import { cn } from '@/lib/utils';
import { LOW_DELTA_T, lowDeltaTTarget, meterOf, operatingCondition, ruleFor, severity, type DiagnosticRule, type Finding, type RuleRevision } from '../diagnostics-model';
import { SEVERITY } from '../finding-presentation';
import { diagnosticsKeys, evaluateRule, lowDeltaTRuleQuery, siteFindingsQuery, type EvaluationOutcome } from '../query';
import { FindingDetail } from './FindingDetail';
import { DiagnosticsViewToggle, WaveformCapturePanel } from './WaveformCapturePanel';

const DAY_MS = 86_400_000;
const DELTA_T_KEY = 'btu_meter.temperature_difference';

const siteRoute = getRouteApi('/_app/_site');
const pageRoute = getRouteApi('/_app/_site/operations/diagnostics');

type Range = '7d' | 'all';
const RANGES = ['7d', 'all'] as const satisfies readonly Range[];

const OUTCOME_LABEL = { FINDING: '发现问题', ONGOING: '仍在持续', CLEAR: '正常', NOT_OPERATING: '未运行' } as const;

const kelvin = (value: number, digits = 1) => `${formatDecimal(value, digits)} K`;
const percent = (share: number) => `${Math.round(share * 100)}%`;

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
    <Panel aria-label={rule.title} className="shrink-0">
      <div className="flex min-h-[72px] flex-wrap items-center gap-x-5 gap-y-3 px-5 py-3">
        <span className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-primary-soft text-primary"><Stethoscope className="size-[18px]" aria-hidden="true" /></span>
        <div className="flex min-w-0 flex-col gap-0.5">
          <span className="flex items-center gap-2 text-sm font-semibold whitespace-nowrap">
            {rule.title}
            {revision ? <Badge variant="secondary">第 {revision.revision} 版</Badge> : null}
            {revision ? (
              <InfoTip>{`系统定时自动诊断：最近 ${revision.windowMinutes} 分钟温差低于 ${revision.minimumDeltaTC} K（设计 ${rule.designValue} K）时记为问题，同一问题 24 小时内只记一次。只看冷站运行（${operatingCondition(revision.operatingGates)}）的时间，运行不足窗口的 ${percent(revision.operatingGates.minimumOperatingShare)} 时不判断。按第 ${revision.revision} 版规则。`}</InfoTip>
            ) : null}
          </span>
          <span className="truncate text-xs text-muted-foreground">
            {target ? target.assetName : '没有登记冷量表，无法诊断'}
            {revision ? ` · ${revision.windowMinutes} 分钟窗口` : current.isPending ? ' · 正在读取诊断规则…' : ' · 诊断规则暂不可用'}
          </span>
        </div>
        <span className="hidden h-9 w-px shrink-0 bg-divider @4xl/main:block" aria-hidden="true" />
        <div className="flex shrink-0 flex-col gap-0.5">
          <span className="text-xs text-muted-foreground">当前温差</span>
          <span className="text-xl leading-6 font-semibold tabular-nums">
            {live !== null ? formatDecimal(live, 1) : '—'}
            <span className="ml-0.5 text-xs font-normal text-muted-foreground">K</span>
          </span>
        </div>
        <ThresholdTrack rule={rule} threshold={threshold} value={live} />
        <span className="hidden h-9 w-px shrink-0 bg-divider @4xl/main:block" aria-hidden="true" />
        <div className="flex shrink-0 flex-col gap-0.5">
          <span className="text-xs text-muted-foreground">{outcome ? '本次诊断' : '最近发现'}</span>
          {outcome ? (
            <span className="flex items-center gap-2 text-sm font-medium">
              <span className={cn('rounded-md px-1.5 text-xs leading-5', outcome.status === 'CLEAR' ? 'bg-success-soft text-success-text' : outcome.status === 'NOT_OPERATING' ? 'bg-muted text-muted-foreground' : 'bg-warning-soft text-warning-text')}>
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
          {outcome.status === 'NOT_OPERATING'
            ? `本次诊断：最近 ${outcome.rule.windowMinutes} 分钟里冷站运行 ${outcome.window.operatingMinutes} 分钟，不足 ${percent(outcome.rule.operatingGates.minimumOperatingShare)}，不判断温差。`
            : `本次诊断：最近 ${outcome.rule.windowMinutes} 分钟里冷站运行 ${outcome.window.operatingMinutes} 分钟，温差 ${kelvin(outcome.observedValue)}，${outcome.status === 'FINDING' ? `低于阈值 ${outcome.rule.minimumDeltaTC} K，已记为诊断结论。` : outcome.status === 'ONGOING' ? `仍低于阈值 ${outcome.rule.minimumDeltaTC} K，问题仍在持续，24 小时内不重复记录。` : `不低于阈值 ${outcome.rule.minimumDeltaTC} K。`}`}
        </p>
      ) : null}
    </Panel>
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
  const setSearch = (next: { range?: Range; inspect?: string; view?: 'findings' | 'waveforms' }) => void navigate({ search: (previous) => ({ ...previous, ...next }), replace: true });
  const view = search.view ?? 'findings';

  const run = useMutation({
    mutationFn: () => evaluateRule(site.id, principal.session.csrfToken, target!),
    onSuccess: (result) => {
      setOutcome({ ...result, at: Date.now() });
      if (result.status === 'FINDING') {
        toast.warning(`发现问题：${LOW_DELTA_T.title}`);
        void queryClient.invalidateQueries({ queryKey: diagnosticsKeys.findings(site.id) });
      } else if (result.status === 'ONGOING') toast.warning(`问题仍在持续：${LOW_DELTA_T.title}`);
      else if (result.status === 'NOT_OPERATING') toast.info('冷站运行时间不足，本次不判断温差');
      else toast.success('诊断完成，未发现问题');
    },
    onError: (error) => toast.error(error instanceof GatewayRequestError ? error.message : '诊断服务暂时不可用'),
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
      <PageHeader
        title="故障诊断"
        description={site.displayName}
        actions={<DiagnosticsViewToggle view={view} onChange={(next) => setSearch({ view: next })} />}
      />

      {view === 'waveforms' ? (
        <WaveformCapturePanel
          devices={model?.devices ?? []}
          telemetryPoints={model?.telemetryPoints ?? []}
          canRead={principal.authorization.capabilities.includes('device.read')}
          canWrite={principal.authorization.capabilities.includes('device.write')}
          csrfToken={principal.session.csrfToken}
          timeZone={site.timezone}
        />
      ) : (<>
      {registry.isPending ? <Skeleton className="h-[72px] shrink-0 rounded-xl" /> : (
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

      <Panel aria-label="诊断结论" className="grid min-h-[540px] flex-1 grid-cols-1 @4xl/main:min-h-0 @4xl/main:grid-cols-[340px_minmax(0,1fr)] @6xl/main:grid-cols-[380px_minmax(0,1fr)]">
        <div className="flex max-h-[360px] min-h-0 flex-col border-b @4xl/main:max-h-none @4xl/main:border-r @4xl/main:border-b-0">
          <ToggleGroup
            type="single"
            value={range}
            onValueChange={(value) => { if (isOneOf(RANGES, value)) setSearch({ range: value, inspect: undefined }); }}
            aria-label="诊断结论范围"
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
                <p className="text-sm text-destructive-text">诊断结论暂时读不到：{findings.error.message}</p>
                <Button variant="outline" size="sm" onClick={() => void findings.refetch()}>重试</Button>
              </div>
            ) : shown.length === 0 ? (
              <div className="flex flex-col items-center gap-2 px-6 py-12 text-center">
                <span className="flex size-9 items-center justify-center rounded-xl bg-success-soft text-success-text"><Stethoscope className="size-[18px]" aria-hidden="true" /></span>
                <p className="text-sm font-semibold">{range === '7d' ? '近 7 天没有诊断结论' : '还没有诊断结论'}</p>
                <p className="text-xs text-muted-foreground">
                  {range === '7d' && all.length > 0 ? `更早的 ${all.length} 条在「全部」里` : '诊断检出问题时会记在这里'}
                </p>
              </div>
            ) : (
              <ul aria-label="诊断结论列表">
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
                              <span className="truncate text-sm font-semibold">{ruleFor(finding.findingType)?.title ?? '诊断结论'}</span>
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
            {findings.isPending ? null : all.length === 0 ? '没有可查看的诊断结论' : '从左侧选择一条诊断结论'}
          </div>
        )}
      </Panel>

      {selected ? (
        <CreateWorkOrderDialog
          open={creating}
          onOpenChange={setCreating}
          title={`为「${selectedRule?.title ?? '诊断结论'}」创建工单`}
          initial={{
            title: `${selectedName}：${selectedRule?.title ?? '诊断结论'}`,
            priority: SEVERITY[severity(selected)].priority,
            description: `${formatTime(selected.evaluationTo, site.timezone)} 诊断结论：${selected.findingType === 'METER_IMBALANCE' ? '偏差比' : '测量值'} ${formatDecimal(selected.observedValue, 2)} ${selected.valueUnit}，${selected.findingType === 'METER_IMBALANCE' ? '超出阈值' : '低于阈值'} ${formatDecimal(selected.thresholdValue, 1)} ${selected.valueUnit}。${selectedRule ? `\n处理建议：${selectedRule.actions.join('；')}。` : ''}`,
          }}
          sourceReferences={[{ domain: 'ASSET', resourceId: selected.assetId, relationship: 'ORIGIN' }]}
          onCreated={() => {
            void queryClient.invalidateQueries({ queryKey: workOrderKeys.all(site.id) });
          }}
        />
      ) : null}</>)}
    </Main>
  );
}
