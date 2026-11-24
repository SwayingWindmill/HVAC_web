import { useQuery } from '@tanstack/react-query';
import { ChevronDown, ChevronRight, Download, ShieldAlert } from 'lucide-react';
import { Fragment, useState } from 'react';
import { toast } from 'sonner';
import { FactTile } from '@/blocks/fact-list';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { PeriodTabs, type PeriodOption } from '@/components/ui/period-tabs';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
import { Skeleton } from '@/components/ui/skeleton';
import { Spinner } from '@/components/ui/spinner';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import type { OptimizationNegativeSampleFilterKind } from '@/api/generated/platformGateway.gen';
import { formatDecimal, formatTime } from '@/lib/operator-format';
import {
  formatGuardrailAction,
  formatGuardrailStage,
  formatNegativeSampleKind,
  formatNegativeSampleReason,
  formatSolverRef,
  serializeNegativeSamplesJsonl,
  type NegativeSampleKind,
} from '../optimization-model';
import {
  fetchSiteOptimizationNegativeSamples,
  siteOptimizationNegativeSamplesQuery,
} from '../query';

type WindowOptionValue = '7' | '14' | '30';

const WINDOW_OPTIONS: readonly PeriodOption<WindowOptionValue>[] = [
  { value: '7', label: '近 7 天' },
  { value: '14', label: '近 14 天' },
  { value: '30', label: '近 30 天' },
];

const KIND_OPTIONS: readonly { readonly value: OptimizationNegativeSampleFilterKind; readonly label: string }[] = [
  { value: 'ALL', label: '全部类别' },
  { value: 'GUARDRAIL_CLAMPED', label: '护栏限幅修正' },
  { value: 'GUARDRAIL_VETOED', label: '物理安全拦截' },
  { value: 'MANUAL_OVERRIDE', label: '人工接管覆写' },
  { value: 'MONITOR_TRIPPED', label: '监控越界回退' },
  { value: 'TRACKING_DEVIATION', label: '跟踪偏差回退' },
];

const SAMPLE_BADGE_VARIANT: Record<NegativeSampleKind, 'warning' | 'destructive' | 'secondary'> = {
  GUARDRAIL_CLAMPED: 'warning',
  GUARDRAIL_VETOED: 'destructive',
  MANUAL_OVERRIDE: 'secondary',
  MONITOR_TRIPPED: 'destructive',
  TRACKING_DEVIATION: 'warning',
};

const PAGE_SIZE = 15;

function isSampleFilterKind(value: string): value is OptimizationNegativeSampleFilterKind {
  return KIND_OPTIONS.some((option) => option.value === value);
}

function formatSetpointTransition(rawVal: number | null, appliedVal: number | null): string {
  const rawLabel = rawVal !== null ? formatDecimal(rawVal, 1) : '—';
  const appliedLabel = appliedVal !== null ? formatDecimal(appliedVal, 1) : '—';
  return `${rawLabel} → ${appliedLabel}`;
}

export function NegativeSamplesDrawer({
  siteId,
  timeZone,
  open,
  onOpenChange,
}: {
  readonly siteId: string;
  readonly timeZone: string;
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
}) {
  const [windowValue, setWindowValue] = useState<WindowOptionValue>('14');
  const [sampleKind, setSampleKind] = useState<OptimizationNegativeSampleFilterKind>('ALL');
  const [offset, setOffset] = useState(0);
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const [exporting, setExporting] = useState(false);

  const windowDays = Number(windowValue);
  const queryParams = { windowDays, sampleKind, limit: PAGE_SIZE, offset };
  const samplesQuery = useQuery(siteOptimizationNegativeSamplesQuery(siteId, queryParams, open));
  const data = samplesQuery.data;

  async function handleExportJsonl() {
    setExporting(true);
    try {
      const page = await fetchSiteOptimizationNegativeSamples(siteId, {
        windowDays,
        sampleKind,
        limit: 100,
        offset: 0,
      });
      if (page.items.length === 0) {
        toast.info('当前筛选条件下没有可导出的负样本记录');
        return;
      }
      const jsonl = serializeNegativeSamplesJsonl(page.items);
      const blob = new Blob([jsonl], { type: 'application/x-ndjson;charset=utf-8' });
      const objectUrl = URL.createObjectURL(blob);
      const anchor = document.createElement('a');
      anchor.href = objectUrl;
      anchor.download = `negative-samples-${windowValue}d.jsonl`;
      document.body.append(anchor);
      anchor.click();
      anchor.remove();
      URL.revokeObjectURL(objectUrl);
      toast.success(`已导出 ${page.items.length} 条负样本 JSONL 数据集`);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : '导出负样本失败，请稍后重试');
    } finally {
      setExporting(false);
    }
  }

  const totalPages = data && data.total > 0 ? Math.ceil(data.total / PAGE_SIZE) : 1;
  const currentPage = Math.floor(offset / PAGE_SIZE) + 1;

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="right" className="w-full overflow-y-auto sm:max-w-[760px] data-[side=right]:sm:max-w-[760px]">
        <SheetHeader className="border-b border-divider pb-3">
          <SheetTitle className="flex items-center gap-2">
            <ShieldAlert className="size-4 text-warning-text" aria-hidden="true" />
            安全护栏负样本明细与复盘导出
          </SheetTitle>
          <SheetDescription>
            查看被物理护栏钳位、拦截或触发人工退避与自动回退的异常调控记录，并导出为 JSONL 离线纠偏数据集。
          </SheetDescription>
        </SheetHeader>

        <div className="flex flex-col gap-4 p-4">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div className="flex flex-wrap items-center gap-2">
              <Select
                value={sampleKind}
                onValueChange={(value) => {
                  if (isSampleFilterKind(value)) {
                    setSampleKind(value);
                    setOffset(0);
                    setExpandedId(null);
                  }
                }}
              >
                <SelectTrigger className="w-40" aria-label="按负样本类别筛选">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {KIND_OPTIONS.map((option) => (
                    <SelectItem key={option.value} value={option.value}>
                      {option.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>

              <PeriodTabs
                aria-label="负样本统计窗口"
                options={WINDOW_OPTIONS}
                value={windowValue}
                onValueChange={(value) => {
                  setWindowValue(value);
                  setOffset(0);
                  setExpandedId(null);
                }}
              />
            </div>

            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={exporting || samplesQuery.isPending || !data || data.total === 0}
              onClick={() => void handleExportJsonl()}
            >
              {exporting ? <Spinner data-icon="inline-start" /> : <Download data-icon="inline-start" aria-hidden="true" />}
              {exporting ? '导出中…' : '导出 JSONL'}
            </Button>
          </div>

          {samplesQuery.isPending ? (
            <Skeleton className="h-64 rounded-lg" />
          ) : samplesQuery.isError ? (
            <Alert variant="destructive">
              <AlertTitle>负样本明细暂时读不到</AlertTitle>
              <AlertDescription className="flex flex-wrap items-center justify-between gap-2">
                <span>{samplesQuery.error instanceof Error ? samplesQuery.error.message : '读取负样本明细失败'}</span>
                <Button variant="outline" size="sm" onClick={() => void samplesQuery.refetch()}>
                  重试
                </Button>
              </AlertDescription>
            </Alert>
          ) : data ? (
            <>
              <div className="overflow-hidden rounded-lg border border-divider">
                <Table aria-label="安全护栏负样本明细表">
                  <TableHeader>
                    <TableRow>
                      <TableHead className="w-8 pl-3" />
                      <TableHead className="whitespace-nowrap">发生时间</TableHead>
                      <TableHead className="whitespace-nowrap">样本类别</TableHead>
                      <TableHead>触发原因</TableHead>
                      <TableHead>求解策略</TableHead>
                      <TableHead className="whitespace-nowrap pr-4 text-right">推荐 → 生效</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {data.items.length === 0 ? (
                      <TableRow>
                        <TableCell colSpan={6} className="py-10 text-center text-xs text-muted-foreground">
                          所选时间窗口与类别下暂无安全护栏负样本记录。
                        </TableCell>
                      </TableRow>
                    ) : (
                      data.items.map((item) => {
                        const expanded = expandedId === item.id;
                        return (
                          <Fragment key={item.id}>
                            <TableRow
                              className="cursor-pointer"
                              onClick={() => setExpandedId(expanded ? null : item.id)}
                            >
                              <TableCell className="pl-3">
                                <Button
                                  type="button"
                                  variant="ghost"
                                  size="icon-sm"
                                  aria-label={expanded ? '收起护栏判定细节' : '展开护栏判定细节'}
                                  aria-expanded={expanded}
                                  onClick={(event) => {
                                    event.stopPropagation();
                                    setExpandedId(expanded ? null : item.id);
                                  }}
                                >
                                  {expanded
                                    ? <ChevronDown className="size-3.5" aria-hidden="true" />
                                    : <ChevronRight className="size-3.5" aria-hidden="true" />}
                                </Button>
                              </TableCell>
                              <TableCell className="whitespace-nowrap text-xs tabular-nums">
                                {formatTime(item.occurredAt, timeZone)}
                              </TableCell>
                              <TableCell className="whitespace-nowrap">
                                <Badge variant={SAMPLE_BADGE_VARIANT[item.sampleKind]}>
                                  {formatNegativeSampleKind(item.sampleKind)}
                                </Badge>
                              </TableCell>
                              <TableCell className="text-xs font-medium">
                                {formatNegativeSampleReason(item.reasonCode)}
                              </TableCell>
                              <TableCell
                                className="max-w-[200px] truncate text-xs text-muted-foreground"
                                title={formatSolverRef(item.solverServingRef)}
                              >
                                {formatSolverRef(item.solverServingRef)}
                              </TableCell>
                              <TableCell className="whitespace-nowrap pr-4 text-right font-mono text-xs tabular-nums">
                                {formatSetpointTransition(item.rawRecommendedValue, item.appliedOrClampedValue)}
                              </TableCell>
                            </TableRow>

                            {expanded ? (
                              <TableRow>
                                <TableCell colSpan={6} className="bg-muted/20 p-4">
                                  <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
                                    <div className="flex flex-col gap-2">
                                      <h4 className="text-xs font-semibold text-foreground">安全护栏判定轨迹</h4>
                                      {item.guardrailTrace.length === 0 ? (
                                        <p className="text-xs text-muted-foreground">该记录未附带逐级护栏轨迹明细。</p>
                                      ) : (
                                        <ul className="flex flex-col gap-1.5">
                                          {item.guardrailTrace.map((step, index) => (
                                            <li
                                              key={`${item.id}-step-${index}`}
                                              className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-divider bg-background px-2.5 py-1.5 text-xs"
                                            >
                                              <span className="font-medium text-foreground">
                                                {formatGuardrailStage(step.stage)}
                                              </span>
                                              <span className="flex items-center gap-2 text-muted-foreground tabular-nums">
                                                <span>{formatDecimal(step.before, 2)} → {formatDecimal(step.after, 2)}</span>
                                                <Badge variant={step.action === 'vetoed' ? 'destructive' : step.action === 'clamped' ? 'warning' : 'secondary'}>
                                                  {formatGuardrailAction(step.action)}
                                                </Badge>
                                              </span>
                                            </li>
                                          ))}
                                        </ul>
                                      )}
                                    </div>

                                    <div className="flex flex-col gap-2">
                                      <h4 className="text-xs font-semibold text-foreground">发生时刻采集快照</h4>
                                      <dl className="grid grid-cols-2 gap-x-4 gap-y-2 rounded-md border border-divider bg-background p-3">
                                        <FactTile label="供水温度" valueClassName="text-xs font-semibold">
                                          {item.featureSnapshot.supplyTempC != null
                                            ? `${formatDecimal(item.featureSnapshot.supplyTempC, 1)} °C`
                                            : '—'}
                                        </FactTile>
                                        <FactTile label="室内最高温" valueClassName="text-xs font-semibold">
                                          {item.featureSnapshot.zoneMaxTempC != null
                                            ? `${formatDecimal(item.featureSnapshot.zoneMaxTempC, 1)} °C`
                                            : '—'}
                                        </FactTile>
                                        <FactTile label="室外湿球温度" valueClassName="text-xs font-semibold">
                                          {item.featureSnapshot.outdoorWetBulbC != null
                                            ? `${formatDecimal(item.featureSnapshot.outdoorWetBulbC, 1)} °C`
                                            : '—'}
                                        </FactTile>
                                        <FactTile label="冷站负荷 / 功率" valueClassName="text-xs font-semibold">
                                          {item.featureSnapshot.coolingLoadKW != null
                                            ? `${formatDecimal(item.featureSnapshot.coolingLoadKW, 0)} kW`
                                            : '—'}
                                          {item.featureSnapshot.chillerPowerKW != null
                                            ? ` / ${formatDecimal(item.featureSnapshot.chillerPowerKW, 0)} kW`
                                            : ''}
                                        </FactTile>
                                        <FactTile label="运行冷机" valueClassName="text-xs font-semibold">
                                          {item.featureSnapshot.runningChillers} 台
                                          {item.featureSnapshot.observedCOP != null
                                            ? ` · COP ${formatDecimal(item.featureSnapshot.observedCOP, 2)}`
                                            : ''}
                                        </FactTile>
                                        <FactTile label="供水限值区间" valueClassName="text-xs font-semibold">
                                          {formatDecimal(item.featureSnapshot.supplyTempMinC, 1)} ~ {formatDecimal(item.featureSnapshot.supplyTempMaxC, 1)} °C
                                        </FactTile>
                                      </dl>
                                    </div>
                                  </div>
                                </TableCell>
                              </TableRow>
                            ) : null}
                          </Fragment>
                        );
                      })
                    )}
                  </TableBody>
                </Table>
              </div>

              <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground tabular-nums">
                <span>
                  共 {data.total} 条 · 第 {currentPage} / {totalPages} 页
                </span>
                <div className="flex items-center gap-2">
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    disabled={offset === 0}
                    onClick={() => {
                      setOffset(Math.max(0, offset - PAGE_SIZE));
                      setExpandedId(null);
                    }}
                  >
                    上一页
                  </Button>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    disabled={!data.hasMore}
                    onClick={() => {
                      setOffset(offset + PAGE_SIZE);
                      setExpandedId(null);
                    }}
                  >
                    下一页
                  </Button>
                </div>
              </div>
            </>
          ) : null}
        </div>
      </SheetContent>
    </Sheet>
  );
}
