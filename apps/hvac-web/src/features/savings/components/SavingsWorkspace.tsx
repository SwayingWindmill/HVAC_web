import { useQuery, useQueryClient } from '@tanstack/react-query';
import { getRouteApi, useNavigate } from '@tanstack/react-router';
import { Leaf, Plus } from 'lucide-react';
import { useMemo, useState } from 'react';
import { toast } from 'sonner';
import { MetricCard, MetricGrid, MetricValue } from '@/blocks/metric-card';
import { PageHeader } from '@/blocks/page-header';
import { Main } from '@/components/layout/Main';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from '@/components/ui/empty';
import { Skeleton } from '@/components/ui/skeleton';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { formatDecimal, formatTime } from '@/lib/operator-format';
import { measuresQuery, savingsKeys, SavingsRequestError, summaryQuery } from '../query';
import { boundaryLabel, costBasis, KIND_LABEL, monthToDate, savingsRate, siteSavingsRate, type Measure } from '../savings-model';
import { usePlantMachines } from '../use-plant-machines';
import { MeasureSheet } from './MeasureSheet';
import { MeasureStatusBadge } from './measure-presentation';
import { RegisterMeasureDialog } from './RegisterMeasureDialog';

const siteRoute = getRouteApi('/_app/_site');
const pageRoute = getRouteApi('/_app/_site/energy-analysis/savings');

const STATUS_ORDER = { IMPLEMENTED: 0, PLANNED: 1, ENDED: 2 } as const;

function LedgerRow({ measure, timeZone, names, onInspect }: {
  readonly measure: Measure;
  readonly timeZone: string;
  readonly names: ReadonlyMap<string, string> | null;
  readonly onInspect: () => void;
}) {
  const rate = savingsRate(measure.totals);
  const verified = measure.totals.days > 0;
  return (
    <TableRow className="cursor-pointer" onClick={onInspect}>
      <TableCell>
        <button type="button" className="text-left font-medium hover:underline" onClick={(event) => { event.stopPropagation(); onInspect(); }}>
          {measure.name}
        </button>
        <div className="text-xs text-muted-foreground">{KIND_LABEL[measure.kind]}</div>
      </TableCell>
      <TableCell>{boundaryLabel(measure.boundaryDeviceId, names)}</TableCell>
      <TableCell><MeasureStatusBadge measure={measure} /></TableCell>
      <TableCell className="tabular-nums">{formatTime(measure.implementedAt ?? measure.plannedAt, timeZone)}{measure.implementedAt ? '' : '（计划）'}</TableCell>
      <TableCell className="text-right tabular-nums">{verified ? measure.totals.days : '—'}</TableCell>
      <TableCell className="text-right tabular-nums">{verified ? formatDecimal(measure.totals.avoidedKWh, 0) : '—'}</TableCell>
      <TableCell className="text-right tabular-nums">{verified ? `¥${formatDecimal(measure.totals.avoidedCost, 0)}` : '—'}</TableCell>
      <TableCell className="text-right tabular-nums">{rate === null ? '—' : `${formatDecimal(rate * 100, 1)}%`}</TableCell>
    </TableRow>
  );
}

export function SavingsWorkspace() {
  const { site, principal } = siteRoute.useRouteContext();
  const search = pageRoute.useSearch();
  const navigate = useNavigate({ from: '/energy-analysis/savings' });
  const queryClient = useQueryClient();
  const [registering, setRegistering] = useState(false);
  const month = useMemo(() => monthToDate(new Date(), site.timezone), [site.timezone]);
  const measures = useQuery(measuresQuery(site.id));
  const summary = useQuery(summaryQuery(site.id, month.from, month.to));
  const { machines, names } = usePlantMachines();
  const canManage = principal.authorization.capabilities.includes('savings.manage');
  const inspect = (measureId: string | undefined) => void navigate({ search: (previous) => ({ ...previous, inspect: measureId }), replace: true });

  const list = [...(measures.data ?? [])].sort((left, right) =>
    STATUS_ORDER[left.status] - STATUS_ORDER[right.status] || right.plannedAt.localeCompare(left.plannedAt));
  const reporting = list.filter((measure) => measure.status === 'IMPLEMENTED');
  const untrusted = reporting.filter((measure) => !measure.baseline?.trusted).length;
  const lifetime = list.reduce((sum, measure) => ({ kwh: sum.kwh + measure.totals.avoidedKWh, cost: sum.cost + measure.totals.avoidedCost }), { kwh: 0, cost: 0 });
  const monthTotals = summary.data?.totals;
  const monthRate = monthTotals ? siteSavingsRate(monthTotals) : null;

  return (
    <Main>
      <PageHeader
        title="节能项目"
        description={`${site.displayName} · 以实施前 14 天的运行数据为基线，按实际供冷量调整后逐日核算节电量与电费`}
        actions={canManage ? (
          <Button size="sm" onClick={() => setRegistering(true)}>
            <Plus data-icon="inline-start" />
            登记措施
          </Button>
        ) : null}
      />

      {summary.isSuccess && measures.isSuccess && list.length > 0 ? (
        <MetricGrid ariaLabel="节能成效">
          <MetricCard
            label="本月节电量"
            value={<MetricValue value={monthTotals!.days > 0 ? formatDecimal(monthTotals!.avoidedKWh, 0) : null} unit="kWh" />}
            lead={monthTotals!.days > 0 ? `已核算 ${monthTotals!.days} 天` : '本月还没有核算结果'}
            detail="调整后基线 − 实际用电"
          />
          <MetricCard
            label="本月节省电费"
            value={<MetricValue value={monthTotals!.days > 0 ? `¥${formatDecimal(monthTotals!.avoidedCost, 0)}` : null} />}
            lead="节电量逐时乘以当时的电度电价"
            detail={costBasis(monthTotals!)}
          />
          <MetricCard
            label="本月节电率"
            value={<MetricValue value={monthRate === null ? null : formatDecimal(monthRate * 100, 1)} unit="%" />}
            lead={monthTotals!.days > 0 ? `冷站同期用电 ${formatDecimal(monthTotals!.plantKWh, 0)} kWh` : '有核算结果后显示'}
            detail="节电量 ÷（冷站同期用电 + 节电量）"
          />
          <MetricCard
            label="正在核算"
            value={<MetricValue value={reporting.length === 0 ? '无' : reporting.length === 1 ? reporting[0].name : `${reporting.length} 项措施`} />}
            lead={reporting.length === 0 ? '没有正在核算的措施' : untrusted === 0 ? '基线检验均通过' : `${untrusted} 项基线不可信，暂不核算`}
            detail={`累计节电 ${formatDecimal(lifetime.kwh, 0)} kWh · 节省 ¥${formatDecimal(lifetime.cost, 0)}`}
          />
        </MetricGrid>
      ) : null}

      <Card role="region" aria-label="措施台账" className="pb-0">
        <CardHeader>
          <CardTitle>措施台账</CardTitle>
          <CardDescription>同一测量边界同一时间只核算一项措施，冷站总表的措施不与设备措施同时核算；点击查看基线检验与逐日核算</CardDescription>
        </CardHeader>
        <CardContent className="border-t px-0">
          {measures.isPending ? <Skeleton className="h-48" /> : measures.isError ? (
            <p className="py-10 text-center text-sm text-muted-foreground">
              {measures.error instanceof SavingsRequestError ? measures.error.message : '节能核算服务暂时不可用'}
            </p>
          ) : list.length === 0 ? (
            <Empty className="py-12">
              <EmptyHeader>
                <EmptyMedia variant="icon"><Leaf /></EmptyMedia>
                <EmptyTitle>还没有节能措施</EmptyTitle>
                <EmptyDescription>
                  登记一项措施，例如提高冷冻水供水温度。确认实施后，系统以实施前 14 天的数据为基线，逐日核算节电量与电费。
                </EmptyDescription>
              </EmptyHeader>
              {canManage ? (
                <EmptyContent>
                  <Button size="sm" onClick={() => setRegistering(true)}>
                    <Plus data-icon="inline-start" />
                    登记措施
                  </Button>
                </EmptyContent>
              ) : null}
            </Empty>
          ) : (
            <Table aria-label="节能措施列表" className="[&_td]:whitespace-nowrap [&_th]:whitespace-nowrap">
              <TableHeader>
                <TableRow>
                  <TableHead>措施</TableHead>
                  <TableHead>测量边界</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead>实施时间</TableHead>
                  <TableHead className="text-right">已核算天数</TableHead>
                  <TableHead className="text-right">累计节电 kWh</TableHead>
                  <TableHead className="text-right">节省电费</TableHead>
                  <TableHead className="text-right">节电率</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {list.map((measure) => (
                  <LedgerRow key={measure.id} measure={measure} timeZone={site.timezone} names={names} onInspect={() => inspect(measure.id)} />
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <RegisterMeasureDialog
        open={registering}
        machines={machines}
        onOpenChange={setRegistering}
        onRegistered={(measure) => {
          toast.success('措施已登记');
          void queryClient.invalidateQueries({ queryKey: savingsKeys.all(site.id) });
          inspect(measure.id);
        }}
      />
      <MeasureSheet measureId={search.inspect} names={names} canManage={canManage} onClose={() => inspect(undefined)} />
    </Main>
  );
}
