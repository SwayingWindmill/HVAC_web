import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { getRouteApi, Link } from '@tanstack/react-router';
import { CircleCheck, CircleDashed, CircleX, Flag, Link2, RefreshCcw, Rocket } from 'lucide-react';
import { useState } from 'react';
import { Bar, BarChart, CartesianGrid, Cell, ReferenceLine, XAxis, YAxis } from 'recharts';
import { toast } from 'sonner';
import { FactTile } from '@/blocks/fact-list';
import { InfoTip } from '@/components/info-tip';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { ChartContainer, ChartTooltip, ChartTooltipContent, type ChartConfig } from '@/components/ui/chart';
import { Sheet, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from '@/components/ui/sheet';
import { Skeleton } from '@/components/ui/skeleton';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { useExecutedCommands } from '@/features/control/use-executed-commands';
import { formatDecimal, formatTime } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import { endMeasure, implementMeasure, measureQuery, refitMeasure, savingsKeys, SavingsRequestError } from '../query';
import {
  boundaryLabel, costBasis, dayRows, fitChecks, instantFromSiteInput, ipmvpOption, KIND_LABEL, measureActions, REASON_TEXT, savingsRate, siteInputValue,
  type Measure, type MeasureAction,
} from '../savings-model';
import { MeasureStatusBadge } from './measure-presentation';
import { MeasureTimeDialog } from './MeasureTimeDialog';

const siteRoute = getRouteApi('/_app/_site');

const dayConfig = {
  avoided: { label: '节电量', color: 'var(--chart-1)' },
} satisfies ChartConfig;

const kwh = (value: number) => `${formatDecimal(value, 0)} kWh`;
const yuan = (value: number) => `¥${formatDecimal(value, 0)}`;

function BaselineSection({ measure, timeZone }: { readonly measure: Measure; readonly timeZone: string }) {
  const baseline = measure.baseline;
  if (!baseline) {
    return <p className="text-sm text-muted-foreground">确认实施后，系统用实施前 14 天的逐时用电与供冷量算出对比基线，之后不再变动。</p>;
  }
  return (
    <div className="flex flex-col gap-3">
      {baseline.trusted ? null : (
        <Alert variant="destructive">
          <CircleX />
          <AlertTitle>对比基线不可靠，暂不统计</AlertTitle>
          <AlertDescription>{baseline.reason ? REASON_TEXT[baseline.reason] : '检验未通过'}。可换一段运行稳定、数据完整的时段重新计算。</AlertDescription>
        </Alert>
      )}
      <Table aria-label="基线检验">
        <TableHeader>
          <TableRow>
            <TableHead>检验项</TableHead>
            <TableHead>要求</TableHead>
            <TableHead className="text-right">结果</TableHead>
            <TableHead className="w-12 text-center">通过</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {fitChecks(baseline).map((check) => (
            <TableRow key={check.label} className="hover:bg-transparent">
              <TableCell className="font-medium">{check.label}</TableCell>
              <TableCell className="text-muted-foreground tabular-nums">{check.limit}</TableCell>
              <TableCell className="text-right tabular-nums">{check.value}</TableCell>
              <TableCell>
                <span className="flex justify-center">
                  {check.pass === null ? <CircleDashed aria-label="未检验" className="size-4 text-muted-foreground" />
                    : check.pass ? <CircleCheck aria-label="通过" className="size-4 text-success" />
                      : <CircleX aria-label="未通过" className="size-4 text-destructive" />}
                </span>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      <p className="text-xs text-muted-foreground">
        对比基线期 <span className="font-mono">{formatTime(baseline.from, timeZone)} – {formatTime(baseline.to, timeZone)}</span>
        {baseline.model ? (
          <>
            ，逐时
            <span className="mx-1 font-medium text-foreground tabular-nums">
              {measure.boundaryDeviceId === null ? '用电' : '设备用电'} = {formatDecimal(baseline.model.interceptKWh, 1)} + {formatDecimal(baseline.model.slope, 3)} × 冷站供冷量
            </span>
          </>
        ) : '，数据不足，未拟合模型'}
      </p>
    </div>
  );
}

function VerifiedDays({ measure }: { readonly measure: Measure }) {
  const rows = dayRows(measure.days ?? []);
  if (rows.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        {measure.status === 'PLANNED'
          ? '实施后开始统计。'
          : '实施后 24 小时为稳定期，不计入统计；之后每天 02:00 统计前一天，结果发布后不再改动。'}
      </p>
    );
  }
  return (
    <div className="flex flex-col gap-3">
      <ChartContainer config={dayConfig} className="aspect-auto h-[180px] w-full" aria-label="每日节电量">
        <BarChart data={[...rows]} margin={{ left: 4, right: 8, top: 8 }}>
          <CartesianGrid vertical={false} />
          <XAxis dataKey="label" tickLine={false} axisLine={false} tickMargin={8} minTickGap={16} />
          <YAxis tickLine={false} axisLine={false} width={48} tickFormatter={(value: number) => formatDecimal(value, 0)} />
          <ReferenceLine y={0} stroke="var(--border)" />
          <ChartTooltip cursor={false} content={<ChartTooltipContent hideIndicator formatter={(value) => `节电 ${kwh(Number(value))}`} />} />
          <Bar dataKey="avoided" radius={[3, 3, 0, 0]} maxBarSize={28}>
            {rows.map((row) => <Cell key={row.day} fill={row.avoided < 0 ? 'var(--destructive)' : 'var(--color-avoided)'} />)}
          </Bar>
        </BarChart>
      </ChartContainer>
      <Table aria-label="逐日核算">
        <TableHeader>
          <TableRow>
            <TableHead>日期</TableHead>
            <TableHead className="text-right">没做措施时预计</TableHead>
            <TableHead className="text-right">实际</TableHead>
            <TableHead className="text-right">节电</TableHead>
            <TableHead className="text-right">完整度</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {[...rows].reverse().map((row) => (
            <TableRow key={row.day} className="hover:bg-transparent">
              <TableCell className="font-mono text-xs">{row.day}</TableCell>
              <TableCell className="text-right tabular-nums">{formatDecimal(row.adjusted, 0)}</TableCell>
              <TableCell className="text-right tabular-nums">{formatDecimal(row.actual, 0)}</TableCell>
              <TableCell className={cn('text-right font-semibold tabular-nums', row.avoided < 0 && 'text-destructive-text')}>{formatDecimal(row.avoided, 0)}</TableCell>
              <TableCell className={cn('text-right tabular-nums', row.coverage < 0.9 && 'text-warning-text')}>{formatDecimal(row.coverage * 100, 0)}%</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

/** What the measure rests on: the executed command it came from, as the control page shows it, and the operator's note. */
function Evidence({ measure }: { readonly measure: Measure }) {
  const { site } = siteRoute.useRouteContext();
  const commands = useExecutedCommands(site.id, site.timezone, measure.evidenceCommandId !== null);
  if (measure.evidenceCommandId === null) return <span className="text-secondary-foreground">{measure.evidence || '无关联指令'}</span>;
  const cited = commands.executed.find((command) => command.commandId === measure.evidenceCommandId);
  return (
    <span className="flex flex-col gap-0.5">
      <Link to="/operations/control" search={{ site: site.id, inspect: measure.evidenceCommandId }} className="text-primary hover:underline">
        {cited ? cited.description : commands.isPending ? '正在读取指令…' : '指令已不在最近记录中，查看控制指令'}
      </Link>
      {measure.evidence ? <span className="text-xs text-muted-foreground">{measure.evidence}</span> : null}
    </span>
  );
}

const ACTION = {
  implement: { label: '确认实施', icon: Rocket },
  refit: { label: '重新拟合基线', icon: RefreshCcw },
  end: { label: '结束措施', icon: Flag },
} as const;

export function MeasureSheet({ measureId, names, canManage, onClose }: {
  readonly measureId: string | undefined;
  /** The plant's machines by device, until the asset model loads null. */
  readonly names: ReadonlyMap<string, string> | null;
  readonly canManage: boolean;
  readonly onClose: () => void;
}) {
  const { site, principal } = siteRoute.useRouteContext();
  const queryClient = useQueryClient();
  const detail = useQuery({ ...measureQuery(site.id, measureId ?? ''), enabled: Boolean(measureId) });
  const [action, setAction] = useState<MeasureAction | null>(null);
  const measure = detail.data;
  const csrf = principal.session.csrfToken;
  const tz = site.timezone;

  const step = useMutation({
    mutationFn: async (values: Record<string, string>) => {
      const at = (key: string) => instantFromSiteInput(values[key], tz).toISOString();
      switch (action) {
        case 'implement': return implementMeasure(site.id, measureId!, csrf, at('at'));
        case 'refit': return refitMeasure(site.id, measureId!, csrf, at('from'), at('to'));
        default: return endMeasure(site.id, measureId!, csrf, at('at'));
      }
    },
    onSuccess: (updated) => {
      queryClient.setQueryData(savingsKeys.measure(site.id, updated.id), updated);
      void queryClient.invalidateQueries({ queryKey: savingsKeys.all(site.id) });
      if (action === 'end') toast.success('措施已结束，此后不再统计');
      else if (updated.baseline?.trusted) toast.success('对比基线已算出，检验通过');
      else toast.warning('对比基线检验未通过，暂不统计');
      setAction(null);
    },
  });

  const now = siteInputValue(new Date(), tz);
  const implementedAt = measure?.implementedAt ? new Date(measure.implementedAt) : null;
  const dialogs: Record<MeasureAction, { title: string; description: string; confirm: string; fields: { id: string; label: string; initial: string }[] }> = {
    implement: {
      title: '确认实施',
      description: '按站点当地时间填写措施实际生效的时间。系统用此前 14 天的逐时数据算出对比基线，之后不再变动；同一块电表同一时间只能统计一项措施。',
      confirm: '确认实施并拟合基线',
      fields: [{ id: 'at', label: '实施时间', initial: now }],
    },
    refit: {
      title: '重新拟合基线',
      description: '换一段实施前、运行稳定且数据完整的时段，至少 72 个小时。发布第一天统计结果后对比基线固定，不能再改。',
      confirm: '重新拟合',
      fields: [
        { id: 'from', label: '基线开始', initial: implementedAt ? siteInputValue(new Date(implementedAt.getTime() - 7 * 86_400_000), tz) : now },
        { id: 'to', label: '基线结束', initial: implementedAt ? siteInputValue(implementedAt, tz) : now },
      ],
    },
    end: {
      title: '结束措施',
      description: '结束后不再统计节电，已发布的每日结果保留。之后可以实施下一项措施。',
      confirm: '结束措施',
      fields: [{ id: 'at', label: '结束时间', initial: now }],
    },
  };
  const dialog = action ? dialogs[action] : null;
  const rate = measure ? savingsRate(measure.totals) : null;
  const copyLink = () => {
    void navigator.clipboard.writeText(window.location.href).then(() => toast.success('链接已复制'), () => toast.error('无法复制链接'));
  };

  return (
    <Sheet open={Boolean(measureId)} onOpenChange={(open) => { if (!open) onClose(); }}>
      <SheetContent className="overflow-y-auto data-[side=right]:sm:max-w-[600px]">
        {detail.isPending ? (
          <div className="flex flex-col gap-4 p-6"><Skeleton className="h-8 w-2/3" /><Skeleton className="h-64" /></div>
        ) : detail.isError || !measure ? (
          <SheetHeader className="px-6 pt-5">
            <SheetTitle>节能措施</SheetTitle>
            <SheetDescription>{detail.error instanceof SavingsRequestError ? detail.error.message : '措施暂时读不到'}</SheetDescription>
          </SheetHeader>
        ) : (
          <>
            <SheetHeader className="gap-2 border-b border-divider px-6 pt-5 pb-4">
              <div className="flex items-center gap-2 pr-8 text-xs">
                <MeasureStatusBadge measure={measure} />
                <span className="text-muted-foreground">{KIND_LABEL[measure.kind]}</span>
                <Button variant="ghost" size="icon-sm" className="ml-auto text-muted-foreground" aria-label="复制链接" onClick={copyLink}><Link2 className="size-3.5" /></Button>
              </div>
              <SheetTitle>{measure.name}</SheetTitle>
              <SheetDescription className="flex flex-col gap-0.5">
                {measure.description ? <span>{measure.description}</span> : null}
                <span className="text-xs">
                  按 {boundaryLabel(measure.boundaryDeviceId, names)} 统计 · {measure.implementedAt
                    ? <>实施于 <span className="font-mono">{formatTime(measure.implementedAt, tz)}</span></>
                    : <>计划 <span className="font-mono">{formatTime(measure.plannedAt, tz)}</span></>}
                  {measure.endedAt ? <> · 结束于 <span className="font-mono">{formatTime(measure.endedAt, tz)}</span></> : null}
                </span>
              </SheetDescription>
            </SheetHeader>
            <div className="flex flex-col">
              <section aria-label="成效" className="flex flex-col gap-3 border-b border-divider px-6 py-4">
                <h3 className="text-sm font-semibold">成效</h3>
                <dl className="grid grid-cols-4 gap-x-5 gap-y-3 text-sm">
                  <FactTile label="累计节电">{measure.totals.days > 0 ? kwh(measure.totals.avoidedKWh) : '—'}</FactTile>
                  <FactTile label="节电率">{rate === null ? '—' : `${formatDecimal(rate * 100, 1)}%`}</FactTile>
                  <FactTile label="节省电费">
                    {measure.totals.days > 0 ? yuan(measure.totals.avoidedCost) : '—'}
                    {measure.totals.unpricedHours > 0 ? <span className="block text-xs font-normal text-muted-foreground">{costBasis(measure.totals)}</span> : null}
                  </FactTile>
                  <FactTile label="预期年节电">{measure.expectedAnnualKWh === null ? '—' : kwh(measure.expectedAnnualKWh)}</FactTile>
                  <FactTile label="计量电表">{boundaryLabel(measure.boundaryDeviceId, names)}</FactTile>
                  <FactTile label="投资额">{measure.investmentCny === null ? '—' : yuan(measure.investmentCny)}</FactTile>
                  <FactTile label="已统计">{measure.totals.days > 0 ? `${measure.totals.days} 天` : '—'}</FactTile>
                </dl>
              </section>

              <section aria-label="基线" className="flex flex-col gap-3 border-b border-divider px-6 py-4">
                <h3 className="flex items-center gap-2 text-sm font-semibold">
                  对比基线是否可靠
                  {measure.baseline ? (
                    <span className={cn('rounded-md px-1.5 text-xs leading-5 font-medium', measure.baseline.trusted ? 'bg-success-soft text-success-text' : 'bg-warning-soft text-warning-text')}>
                      {measure.baseline.trusted ? '通过' : '未通过'}
                    </span>
                  ) : null}
                  <InfoTip>实施后，系统用实施前 14 天的用电与供冷数据，算出“没做这项措施时会用多少电”作为对比基线，之后不再变动</InfoTip>
                  <span className="ml-auto text-xs font-normal text-muted-foreground">{ipmvpOption(measure.boundaryDeviceId)} · {boundaryLabel(measure.boundaryDeviceId, names)}</span>
                </h3>
                <BaselineSection measure={measure} timeZone={tz} />
              </section>

              <section aria-label="逐日核算" className="flex flex-col gap-3 border-b border-divider px-6 py-4">
                <h3 className="flex items-center gap-2 text-sm font-semibold">每日节电<span className="text-xs font-normal text-muted-foreground">每天 02:00 统计前一天 · kWh</span></h3>
                <VerifiedDays measure={measure} />
              </section>

              <section aria-label="依据" className="flex flex-col gap-1.5 px-6 py-4 text-sm">
                <h3 className="font-semibold">依据</h3>
                <Evidence measure={measure} />
              </section>
            </div>

            {canManage && measureActions(measure).length > 0 ? (
              <SheetFooter className="mt-auto flex-row flex-wrap items-center gap-2 border-t border-divider px-6 py-3">
                {measureActions(measure).map((next, index) => {
                  const { label, icon: Icon } = ACTION[next];
                  return (
                    <Button key={next} size="sm" variant={index === 0 && next !== 'end' ? 'default' : 'outline'} onClick={() => { step.reset(); setAction(next); }}>
                      <Icon data-icon="inline-start" />
                      {label}
                    </Button>
                  );
                })}
                {measureActions(measure).includes('end') ? <span className="text-xs text-muted-foreground">结束后不再统计节电</span> : null}
              </SheetFooter>
            ) : null}
            {dialog ? (
              <MeasureTimeDialog
                open
                onOpenChange={(open) => { if (!open) setAction(null); }}
                title={dialog.title}
                description={dialog.description}
                fields={dialog.fields}
                confirmLabel={dialog.confirm}
                pending={step.isPending}
                error={step.isError ? (step.error instanceof SavingsRequestError ? step.error.message : '节能统计服务暂时不可用，请稍后重试') : null}
                onConfirm={(values) => step.mutate(values)}
              />
            ) : null}
          </>
        )}
      </SheetContent>
    </Sheet>
  );
}
