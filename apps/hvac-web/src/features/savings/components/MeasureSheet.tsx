import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { getRouteApi } from '@tanstack/react-router';
import { CircleCheck, CircleDashed, CircleX, Flag, RefreshCcw, Rocket } from 'lucide-react';
import { useState } from 'react';
import { Bar, CartesianGrid, ComposedChart, Line, XAxis, YAxis } from 'recharts';
import { toast } from 'sonner';
import { FactTile } from '@/blocks/fact-list';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { ChartContainer, ChartLegend, ChartLegendContent, ChartTooltip, ChartTooltipContent, type ChartConfig } from '@/components/ui/chart';
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet';
import { Skeleton } from '@/components/ui/skeleton';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { formatDecimal, formatTime } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import { endMeasure, implementMeasure, measureQuery, refitMeasure, savingsKeys, SavingsRequestError } from '../query';
import {
  costBasis, dayRows, fitChecks, instantFromSiteInput, KIND_LABEL, measureActions, REASON_TEXT, savingsRate, siteInputValue,
  type Measure, type MeasureAction,
} from '../savings-model';
import { MeasureStatusBadge } from './measure-presentation';
import { MeasureTimeDialog } from './MeasureTimeDialog';

const siteRoute = getRouteApi('/_app/_site');

const dayConfig = {
  actual: { label: '实际用电', color: 'var(--chart-2)' },
  adjusted: { label: '调整后基线', color: 'var(--chart-1)' },
} satisfies ChartConfig;

const kwh = (value: number) => `${formatDecimal(value, 0)} kWh`;
const yuan = (value: number) => `¥${formatDecimal(value, 0)}`;

function BaselineSection({ measure, timeZone }: { readonly measure: Measure; readonly timeZone: string }) {
  const baseline = measure.baseline;
  if (!baseline) {
    return <p className="text-sm text-muted-foreground">确认实施后，系统用实施前 14 天的逐时用电与供冷量拟合基线并冻结。</p>;
  }
  return (
    <div className="flex flex-col gap-3">
      {baseline.trusted ? null : (
        <Alert variant="destructive">
          <CircleX />
          <AlertTitle>基线不可信，暂不核算</AlertTitle>
          <AlertDescription>{baseline.reason ? REASON_TEXT[baseline.reason] : '检验未通过'}。可换一段运行稳定、数据完整的时段重新拟合。</AlertDescription>
        </Alert>
      )}
      <p className="text-sm text-muted-foreground">
        基线期 {formatTime(baseline.from, timeZone)} – {formatTime(baseline.to, timeZone)}
        {baseline.model ? (
          <>
            ，逐时
            <span className="mx-1 font-medium text-foreground tabular-nums">
              用电 = {formatDecimal(baseline.model.interceptKWh, 1)} + {formatDecimal(baseline.model.slope, 3)} × 供冷量
            </span>
          </>
        ) : '，数据不足，未拟合模型'}
      </p>
      <Table aria-label="基线检验" className="[&_td]:py-2">
        <TableHeader>
          <TableRow>
            <TableHead>检验项</TableHead>
            <TableHead className="text-right">结果</TableHead>
            <TableHead className="text-right">要求</TableHead>
            <TableHead className="w-8"><span className="sr-only">是否通过</span></TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {fitChecks(baseline).map((check) => (
            <TableRow key={check.label}>
              <TableCell className="font-medium">{check.label}</TableCell>
              <TableCell className="text-right tabular-nums">{check.value}</TableCell>
              <TableCell className="text-right text-muted-foreground tabular-nums">{check.limit}</TableCell>
              <TableCell>
                {check.pass === null ? <CircleDashed aria-label="未检验" className="size-4 text-muted-foreground" />
                  : check.pass ? <CircleCheck aria-label="通过" className="size-4 text-emerald-600 dark:text-emerald-400" />
                    : <CircleX aria-label="未通过" className="size-4 text-destructive" />}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

function VerifiedDays({ measure }: { readonly measure: Measure }) {
  const rows = dayRows(measure.days ?? []);
  if (rows.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        {measure.status === 'PLANNED'
          ? '实施后开始核算。'
          : '实施后 24 小时为稳定期，不计入核算；之后每天 02:00 核算前一天，结果发布后不再改动。'}
      </p>
    );
  }
  const rate = savingsRate(measure.totals);
  return (
    <div className="flex flex-col gap-4">
      <dl className="grid grid-cols-3 gap-x-6 gap-y-4 text-sm">
        <FactTile label="累计节电">{kwh(measure.totals.avoidedKWh)}</FactTile>
        <FactTile label="节省电费">
          {yuan(measure.totals.avoidedCost)}
          {measure.totals.unpricedHours > 0 ? <span className="block text-xs font-normal text-muted-foreground">{costBasis(measure.totals)}</span> : null}
        </FactTile>
        <FactTile label="节电率">{rate === null ? '—' : `${formatDecimal(rate * 100, 1)}%`}</FactTile>
      </dl>
      <ChartContainer config={dayConfig} className="aspect-auto h-[220px] w-full">
        <ComposedChart data={[...rows]} margin={{ left: 4, right: 8, top: 8 }}>
          <CartesianGrid vertical={false} />
          <XAxis dataKey="label" tickLine={false} axisLine={false} tickMargin={8} minTickGap={16} />
          <YAxis tickLine={false} axisLine={false} width={48} tickFormatter={(value: number) => formatDecimal(value, 0)} />
          <ChartTooltip content={<ChartTooltipContent formatter={(value, name) => `${dayConfig[name as keyof typeof dayConfig]?.label ?? name} ${kwh(Number(value))}`} />} />
          <ChartLegend content={<ChartLegendContent />} />
          <Bar dataKey="actual" fill="var(--color-actual)" radius={[3, 3, 0, 0]} maxBarSize={22} />
          <Line dataKey="adjusted" stroke="var(--color-adjusted)" strokeWidth={2} dot={false} type="monotone" />
        </ComposedChart>
      </ChartContainer>
      <Table aria-label="逐日核算" className="[&_td]:whitespace-nowrap [&_th]:whitespace-nowrap">
        <TableHeader>
          <TableRow>
            <TableHead>日期</TableHead>
            <TableHead className="text-right">调整后基线</TableHead>
            <TableHead className="text-right">实际</TableHead>
            <TableHead className="text-right">节电</TableHead>
            <TableHead className="text-right">数据覆盖</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {[...rows].reverse().map((row) => (
            <TableRow key={row.day}>
              <TableCell className="tabular-nums">{row.day}</TableCell>
              <TableCell className="text-right tabular-nums">{formatDecimal(row.adjusted, 0)}</TableCell>
              <TableCell className="text-right tabular-nums">{formatDecimal(row.actual, 0)}</TableCell>
              <TableCell className={cn('text-right font-medium tabular-nums', row.avoided < 0 && 'text-destructive')}>{formatDecimal(row.avoided, 0)}</TableCell>
              <TableCell className={cn('text-right tabular-nums', row.coverage < 0.9 && 'text-amber-700 dark:text-amber-300')}>{formatDecimal(row.coverage * 100, 0)}%</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

const ACTION = {
  implement: { label: '确认实施', icon: Rocket },
  refit: { label: '重新拟合基线', icon: RefreshCcw },
  end: { label: '结束措施', icon: Flag },
} as const;

export function MeasureSheet({ measureId, canManage, onClose }: {
  readonly measureId: string | undefined;
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
      if (action === 'end') toast.success('措施已结束，此后不再核算');
      else if (updated.baseline?.trusted) toast.success('基线已冻结，检验通过');
      else toast.warning('基线检验未通过，暂不核算');
      setAction(null);
    },
  });

  const now = siteInputValue(new Date(), tz);
  const implementedAt = measure?.implementedAt ? new Date(measure.implementedAt) : null;
  const dialogs: Record<MeasureAction, { title: string; description: string; confirm: string; fields: { id: string; label: string; initial: string }[] }> = {
    implement: {
      title: '确认实施',
      description: '按站点当地时间填写措施实际生效的时间。系统读取此前 14 天的逐时数据拟合基线并冻结；同一站点同一时间只能有一项措施在核算。',
      confirm: '确认实施并拟合基线',
      fields: [{ id: 'at', label: '实施时间', initial: now }],
    },
    refit: {
      title: '重新拟合基线',
      description: '换一段实施前、运行稳定且数据完整的时段，至少 72 个小时。发布第一天核算结果后基线冻结，不能再改。',
      confirm: '重新拟合',
      fields: [
        { id: 'from', label: '基线开始', initial: implementedAt ? siteInputValue(new Date(implementedAt.getTime() - 7 * 86_400_000), tz) : now },
        { id: 'to', label: '基线结束', initial: implementedAt ? siteInputValue(implementedAt, tz) : now },
      ],
    },
    end: {
      title: '结束措施',
      description: '结束后不再核算，已发布的逐日结果保留。之后可以实施下一项措施。',
      confirm: '结束措施',
      fields: [{ id: 'at', label: '结束时间', initial: now }],
    },
  };
  const dialog = action ? dialogs[action] : null;

  return (
    <Sheet open={Boolean(measureId)} onOpenChange={(open) => { if (!open) onClose(); }}>
      <SheetContent className="overflow-y-auto data-[side=right]:sm:max-w-[640px]">
        {detail.isPending ? (
          <div className="flex flex-col gap-4 p-4"><Skeleton className="h-8 w-2/3" /><Skeleton className="h-64" /></div>
        ) : detail.isError || !measure ? (
          <SheetHeader>
            <SheetTitle>节能措施</SheetTitle>
            <SheetDescription>{detail.error instanceof SavingsRequestError ? detail.error.message : '措施暂不可用'}</SheetDescription>
          </SheetHeader>
        ) : (
          <>
            <SheetHeader>
              <div className="flex items-center gap-2">
                <SheetTitle>{measure.name}</SheetTitle>
                <MeasureStatusBadge measure={measure} />
              </div>
              <SheetDescription>{KIND_LABEL[measure.kind]}{measure.description ? ` · ${measure.description}` : ''}</SheetDescription>
            </SheetHeader>
            <div className="flex flex-col gap-8 px-4 pb-8">
              <dl className="grid grid-cols-3 gap-x-6 gap-y-4 text-sm">
                <FactTile label="计划实施">{formatTime(measure.plannedAt, tz)}</FactTile>
                <FactTile label="实际实施">{formatTime(measure.implementedAt ?? undefined, tz)}</FactTile>
                <FactTile label="结束">{formatTime(measure.endedAt ?? undefined, tz)}</FactTile>
                <FactTile label="投资额">{measure.investmentCny === null ? '—' : yuan(measure.investmentCny)}</FactTile>
                <FactTile label="预期年节电">{measure.expectedAnnualKWh === null ? '—' : kwh(measure.expectedAnnualKWh)}</FactTile>
                <FactTile label="依据">{measure.evidence || '—'}</FactTile>
              </dl>

              {canManage && measureActions(measure).length > 0 ? (
                <div className="flex flex-wrap gap-2">
                  {measureActions(measure).map((next, index) => {
                    const { label, icon: Icon } = ACTION[next];
                    return (
                      <Button key={next} size="sm" variant={index === 0 && next !== 'end' ? 'default' : 'outline'} onClick={() => { step.reset(); setAction(next); }}>
                        <Icon data-icon="inline-start" />
                        {label}
                      </Button>
                    );
                  })}
                </div>
              ) : null}

              <section aria-label="基线" className="flex flex-col gap-3">
                <h3 className="text-sm font-medium">基线（IPMVP 选项 C · 冷站总表）</h3>
                <BaselineSection measure={measure} timeZone={tz} />
              </section>

              <section aria-label="逐日核算" className="flex flex-col gap-3">
                <h3 className="text-sm font-medium">逐日核算</h3>
                <VerifiedDays measure={measure} />
              </section>
            </div>
            {dialog ? (
              <MeasureTimeDialog
                open
                onOpenChange={(open) => { if (!open) setAction(null); }}
                title={dialog.title}
                description={dialog.description}
                fields={dialog.fields}
                confirmLabel={dialog.confirm}
                pending={step.isPending}
                error={step.isError ? (step.error instanceof SavingsRequestError ? step.error.message : '节能核算服务暂时不可用，请稍后重试') : null}
                onConfirm={(values) => step.mutate(values)}
              />
            ) : null}
          </>
        )}
      </SheetContent>
    </Sheet>
  );
}
