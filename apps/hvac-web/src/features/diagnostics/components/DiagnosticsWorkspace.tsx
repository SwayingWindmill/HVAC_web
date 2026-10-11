import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { getRouteApi, Link, useNavigate } from '@tanstack/react-router';
import { ClipboardPlus, SearchCheck, SlidersHorizontal, Stethoscope } from 'lucide-react';
import { useMemo, useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import { PageHeader } from '@/blocks/page-header';
import { Main } from '@/components/layout/Main';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from '@/components/ui/empty';
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet';
import { Skeleton } from '@/components/ui/skeleton';
import { Spinner } from '@/components/ui/spinner';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { plantReading, type PlantReading } from '@/features/operations/realtime/plant-model';
import { useRealtimePlant } from '@/features/operations/realtime/use-realtime-plant';
import { CreateWorkOrderDialog } from '@/features/work-orders/CreateWorkOrderDialog';
import { workOrderKeys } from '@/features/work-orders/work-order-queries';
import { formatDecimal, formatTime } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import { LOW_DELTA_T, lowDeltaTTarget, ruleFor, severity, type DiagnosticRule, type Finding } from '../diagnostics-model';
import { DiagnosticsRequestError, diagnosticsKeys, evaluateRule, siteFindingsQuery, type EvaluationOutcome } from '../query';

const siteRoute = getRouteApi('/_app/_site');
const pageRoute = getRouteApi('/_app/_site/operations/diagnostics');

const SEVERITY = {
  high: { label: '严重偏离', className: 'border-destructive/30 bg-destructive/10 text-destructive', priority: 'HIGH' },
  medium: { label: '明显偏离', className: 'border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300', priority: 'MEDIUM' },
  low: { label: '轻微偏离', className: 'border-sky-500/30 bg-sky-500/10 text-sky-700 dark:text-sky-300', priority: 'LOW' },
} as const;

const kelvin = (value: number) => `${formatDecimal(value, 1)} K`;

/** A value on a scale from zero past the design value, with the rule's threshold and design marked. */
function ThresholdBar({ rule, value }: { readonly rule: DiagnosticRule; readonly value: number | null }) {
  const top = rule.designValue + 2;
  const at = (x: number) => `${(Math.min(top, Math.max(0, x)) / top) * 100}%`;
  const below = value !== null && value < rule.threshold;
  return (
    <div className="relative h-12" aria-hidden="true">
      <div className="absolute inset-x-0 top-5 h-2 rounded-full bg-muted" />
      <div className="absolute top-5 h-2 rounded-l-full bg-destructive/20" style={{ left: 0, width: at(rule.threshold) }} />
      {[{ x: rule.threshold, label: `阈值 ${rule.threshold}` }, { x: rule.designValue, label: `设计 ${rule.designValue}` }].map((mark) => (
        <div key={mark.label} className="absolute top-3.5 flex -translate-x-1/2 flex-col items-center" style={{ left: at(mark.x) }}>
          <span className="h-5 w-px bg-muted-foreground/60" />
          <span className="mt-1 whitespace-nowrap text-[11px] text-muted-foreground">{mark.label}</span>
        </div>
      ))}
      {value !== null ? (
        <span
          className={cn('absolute top-4 size-4 -translate-x-1/2 rounded-full border-2 border-background shadow-sm', below ? 'bg-destructive' : 'bg-primary')}
          style={{ left: at(value) }}
        />
      ) : null}
    </div>
  );
}

function RuleCard({ rule, target, reading, outcome }: {
  readonly rule: DiagnosticRule;
  readonly target: { assetName: string } | null;
  readonly reading: PlantReading | null;
  readonly outcome: EvaluationOutcome | null;
}) {
  const live = reading?.state === 'PRESENT' && reading.numeric !== null ? reading.numeric : null;
  return (
    <Card role="region" aria-label={rule.title}>
      <CardHeader>
        <CardTitle>{rule.title}</CardTitle>
        <CardDescription>{target ? `${target.assetName} · ${rule.watches}` : '没有登记冷量表，无法诊断'}</CardDescription>
        {outcome ? (
          <CardAction>
            <Badge variant="outline" className={outcome.status === 'FINDING' ? SEVERITY.medium.className : 'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300'}>
              {outcome.status === 'FINDING' ? '发现问题' : '正常'}
            </Badge>
          </CardAction>
        ) : null}
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <div className="flex items-baseline justify-between gap-3">
          <span className="text-sm text-muted-foreground">当前温差</span>
          <span className="text-2xl font-semibold tabular-nums">
            {live !== null ? formatDecimal(live, 1) : '—'}
            <span className="ml-1 text-sm font-normal text-muted-foreground">K</span>
          </span>
        </div>
        <ThresholdBar rule={rule} value={live} />
        <p className="text-sm text-muted-foreground">
          {outcome
            ? `本次诊断：最近 ${rule.windowMinutes} 分钟温差 ${kelvin(outcome.observedValue)}，${outcome.status === 'FINDING' ? `低于阈值 ${rule.threshold} K，已记为诊断发现。` : `不低于阈值 ${rule.threshold} K。`}`
            : `温差低于 ${rule.threshold} K（设计 ${rule.designValue} K）时记为问题；诊断取最近 ${rule.windowMinutes} 分钟的数据。`}
        </p>
      </CardContent>
    </Card>
  );
}

function Fact({ label, children }: { readonly label: string; readonly children: ReactNode }) {
  return (
    <div className="flex flex-col gap-1">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="font-medium tabular-nums">{children}</dd>
    </div>
  );
}

function FindingSheet({ finding, assetName, timeZone, onClose }: {
  readonly finding: Finding | undefined;
  readonly assetName: (assetId: string) => string;
  readonly timeZone: string;
  readonly onClose: () => void;
}) {
  const { site } = siteRoute.useRouteContext();
  const queryClient = useQueryClient();
  const [creating, setCreating] = useState(false);
  const rule = finding ? ruleFor(finding.findingType) : undefined;
  const level = finding ? SEVERITY[severity(finding)] : null;
  const name = finding ? assetName(finding.assetId) : '';
  return (
    <Sheet open={Boolean(finding)} onOpenChange={(open) => { if (!open) onClose(); }}>
      <SheetContent className="w-full overflow-y-auto sm:max-w-md">
        {finding && level ? (
          <>
            <SheetHeader>
              <SheetTitle>{rule?.title ?? '诊断发现'}</SheetTitle>
              <SheetDescription>{name} · {formatTime(finding.evaluationTo, timeZone)}</SheetDescription>
            </SheetHeader>
            <div className="flex flex-col gap-6 px-4 pb-6">
              <dl className="grid grid-cols-2 gap-x-6 gap-y-4 text-sm">
                <Fact label="测量值">{formatDecimal(finding.observedValue, 2)} {finding.valueUnit}</Fact>
                <Fact label="阈值">{formatDecimal(finding.thresholdValue, 1)} {finding.valueUnit}</Fact>
                <Fact label="偏离程度"><Badge variant="outline" className={level.className}>{level.label}</Badge></Fact>
                <Fact label="置信度">{formatDecimal(finding.confidence * 100, 0)}%</Fact>
                <Fact label="诊断窗口">{formatTime(finding.evaluationFrom, timeZone).slice(-5)}–{formatTime(finding.evaluationTo, timeZone).slice(-5)}</Fact>
              </dl>
              {rule ? (
                <>
                  <section className="flex flex-col gap-2 text-sm">
                    <h3 className="font-medium">说明</h3>
                    <p className="text-muted-foreground">{rule.meaning}</p>
                  </section>
                  <section className="flex flex-col gap-2 text-sm">
                    <h3 className="font-medium">常见原因</h3>
                    <ul className="list-disc space-y-1 pl-5 text-muted-foreground">{rule.causes.map((cause) => <li key={cause}>{cause}</li>)}</ul>
                  </section>
                  <section className="flex flex-col gap-2 text-sm">
                    <h3 className="font-medium">处理建议</h3>
                    <ul className="list-disc space-y-1 pl-5 text-muted-foreground">{rule.actions.map((action) => <li key={action}>{action}</li>)}</ul>
                  </section>
                </>
              ) : null}
              <div className="flex flex-wrap gap-2">
                <Button size="sm" onClick={() => setCreating(true)}>
                  <ClipboardPlus data-icon="inline-start" />
                  创建工单
                </Button>
                <Button size="sm" variant="outline" asChild>
                  <Link to="/operations/control" search={{ site: site.id }}>
                    <SlidersHorizontal data-icon="inline-start" />
                    去调整水泵
                  </Link>
                </Button>
              </div>
            </div>
            <CreateWorkOrderDialog
              open={creating}
              onOpenChange={setCreating}
              title={`为「${rule?.title ?? '诊断发现'}」创建工单`}
              initial={{
                title: `${name}：${rule?.title ?? '诊断发现'}`,
                priority: level.priority,
                description: `${formatTime(finding.evaluationTo, timeZone)} 诊断发现：测量值 ${formatDecimal(finding.observedValue, 2)} ${finding.valueUnit}，低于阈值 ${formatDecimal(finding.thresholdValue, 1)} ${finding.valueUnit}。${rule ? `\n处理建议：${rule.actions.join('；')}。` : ''}`,
              }}
              sourceReferences={[{ domain: 'ASSET', resourceId: finding.assetId, relationship: 'ORIGIN' }]}
              onCreated={() => {
                toast.success('工单已创建');
                void queryClient.invalidateQueries({ queryKey: workOrderKeys.all(site.id) });
              }}
            />
          </>
        ) : null}
      </SheetContent>
    </Sheet>
  );
}

export function DiagnosticsWorkspace() {
  const { site, principal } = siteRoute.useRouteContext();
  const search = pageRoute.useSearch();
  const navigate = useNavigate({ from: '/operations/diagnostics' });
  const queryClient = useQueryClient();
  const findings = useQuery(siteFindingsQuery(site.id));
  const { plant, registry } = useRealtimePlant();
  const [outcome, setOutcome] = useState<EvaluationOutcome | null>(null);

  const model = registry.data?.assetModel;
  const target = useMemo(() => (model ? lowDeltaTTarget(model) : null), [model]);
  const assetName = (assetId: string) => model?.assets.find((asset) => asset.id === assetId)?.displayName ?? '未登记设备';
  const inspect = (findingId: string | undefined) => void navigate({ search: (previous) => ({ ...previous, inspect: findingId }), replace: true });

  const run = useMutation({
    mutationFn: () => evaluateRule(site.id, principal.session.csrfToken, LOW_DELTA_T, target!, new Date()),
    onSuccess: (result) => {
      setOutcome(result);
      if (result.status === 'FINDING') {
        toast.warning(`发现问题：${LOW_DELTA_T.title}`);
        void queryClient.invalidateQueries({ queryKey: diagnosticsKeys.findings(site.id) });
      } else toast.success('诊断完成，未发现问题');
    },
    onError: (error) => toast.error(error instanceof DiagnosticsRequestError ? error.message : '诊断服务暂时不可用'),
  });

  const list = findings.data ?? [];
  return (
    <Main className="@container/main flex flex-col gap-4 md:gap-6">
      <PageHeader
        title="故障诊断"
        description={`${site.displayName} · 用诊断规则检查设备运行数据，发现能效与运行问题`}
        actions={
          <Button size="sm" disabled={!target || run.isPending} onClick={() => run.mutate()}>
            {run.isPending ? <Spinner data-icon="inline-start" /> : <SearchCheck data-icon="inline-start" />}
            立即诊断
          </Button>
        }
      />

      <div className="grid items-start gap-4 md:gap-6 @5xl/main:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]">
        <section aria-label="诊断项目" className="flex flex-col gap-4">
          {registry.isPending ? <Skeleton className="h-64 rounded-xl" /> : (
            <RuleCard rule={LOW_DELTA_T} target={target} reading={plantReading(plant, 'btu_meter.temperature_difference')} outcome={outcome} />
          )}
        </section>

        <Card role="region" aria-label="诊断发现">
          <CardHeader>
            <CardTitle>诊断发现</CardTitle>
            <CardDescription>最近 100 条，按诊断时间倒序；点击查看原因与处理建议</CardDescription>
          </CardHeader>
          <CardContent>
            {findings.isPending ? <Skeleton className="h-48" /> : findings.isError ? (
              <p className="py-10 text-center text-sm text-muted-foreground">诊断发现暂不可用</p>
            ) : list.length === 0 ? (
              <Empty className="py-10">
                <EmptyHeader>
                  <EmptyMedia variant="icon"><Stethoscope /></EmptyMedia>
                  <EmptyTitle>还没有诊断发现</EmptyTitle>
                  <EmptyDescription>诊断发现问题时会记录在这里，包括测量值、阈值与处理建议。</EmptyDescription>
                </EmptyHeader>
              </Empty>
            ) : (
              <Table aria-label="诊断发现列表" className="[&_td]:whitespace-nowrap [&_th]:whitespace-nowrap">
                <TableHeader>
                  <TableRow>
                    <TableHead>诊断时间</TableHead>
                    <TableHead>问题</TableHead>
                    <TableHead>设备</TableHead>
                    <TableHead className="text-right">测量值 / 阈值</TableHead>
                    <TableHead>偏离程度</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {list.map((finding) => {
                    const level = SEVERITY[severity(finding)];
                    return (
                      <TableRow key={finding.id} className="cursor-pointer" onClick={() => inspect(finding.id)}>
                        <TableCell className="whitespace-nowrap tabular-nums">{formatTime(finding.evaluationTo, site.timezone)}</TableCell>
                        <TableCell>
                          <button type="button" className="text-left font-medium hover:underline" onClick={(event) => { event.stopPropagation(); inspect(finding.id); }}>
                            {ruleFor(finding.findingType)?.title ?? '诊断发现'}
                          </button>
                        </TableCell>
                        <TableCell>{assetName(finding.assetId)}</TableCell>
                        <TableCell className="text-right tabular-nums">
                          {formatDecimal(finding.observedValue, 1)} / {formatDecimal(finding.thresholdValue, 1)} {finding.valueUnit}
                        </TableCell>
                        <TableCell><Badge variant="outline" className={level.className}>{level.label}</Badge></TableCell>
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>
            )}
          </CardContent>
        </Card>
      </div>

      <FindingSheet
        finding={list.find((finding) => finding.id === search.inspect)}
        assetName={assetName}
        timeZone={site.timezone}
        onClose={() => inspect(undefined)}
      />
    </Main>
  );
}
