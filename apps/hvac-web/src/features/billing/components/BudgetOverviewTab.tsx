import { useState } from 'react';
import { Area, CartesianGrid, ComposedChart, Line, ReferenceLine, XAxis, YAxis } from 'recharts';
import { ArrowUpRight, Pencil, Plus, Receipt } from 'lucide-react';
import type { MonthlyElectricityBudget, UtilityBill } from '@/api/generated/platformGateway.gen';
import { Panel, PanelHeader, PanelLink } from '@/blocks/panel';
import { InfoTip } from '@/components/info-tip';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from '@/components/ui/chart';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { PERIOD_COLOR } from '@/features/tariff/period-presentation';
import { formatDecimal } from '@/lib/operator-format';
import {
  formatBillingMonthLabel,
  METER_SCOPE_LABEL,
  UTILITY_BILL_STATUS_LABEL,
  type MonthlyBudgetProgressSummary,
} from '../billing-model';

const budgetChartConfig = {
  cumulativeCostCny: {
    label: '累计估算电度电费（元）',
    color: 'var(--chart-1)',
  },
  linearBudgetCny: {
    label: '预算匀速参考（元）',
    color: 'var(--muted-foreground)',
  },
} satisfies ChartConfig;

function formatCurrency(amount: number | null): string {
  if (amount === null) return '—';
  return `¥${formatDecimal(amount, 2)}`;
}

export interface BudgetOverviewTabProps {
  readonly siteId: string;
  readonly billingMonth: string;
  readonly summary: MonthlyBudgetProgressSummary;
  readonly budget: MonthlyElectricityBudget | null;
  readonly monthBills: readonly UtilityBill[];
  readonly isElectricityPending: boolean;
  readonly isElectricityError: boolean;
  readonly electricityErrorMessage: string | null;
  readonly canManageBilling: boolean;
  readonly onOpenBudgetDialog: () => void;
  readonly onOpenCreateBillDialog: () => void;
  readonly onInspectBill: (billId: string) => void;
}

export function BudgetOverviewTab({
  siteId,
  billingMonth,
  summary,
  budget,
  monthBills,
  isElectricityPending,
  isElectricityError,
  electricityErrorMessage,
  canManageBilling,
  onOpenBudgetDialog,
  onOpenCreateBillDialog,
  onInspectBill,
}: BudgetOverviewTabProps) {
  const [seriesDisplayMode, setSeriesDisplayMode] = useState<'chart' | 'table'>('chart');
  const monthText = formatBillingMonthLabel(billingMonth);
  const isCurrent = summary.window.state === 'CURRENT';
  const isPast = summary.window.state === 'PAST';
  const confirmedBills = monthBills.filter((bill) => bill.status === 'CONFIRMED');
  const draftBills = monthBills.filter((bill) => bill.status === 'DRAFT');

  const cumulativeLabel = isCurrent ? '本月累计估算电费' : '当月累计估算电费';
  const projectedLabel = isCurrent
    ? '预计全月估算电费'
    : isPast
      ? '全月结算前估算电费'
      : '预计全月估算电费';

  const alreadyExceeded =
    summary.budgetAmountCny !== null &&
    summary.cumulativeEstimatedCny !== null &&
    summary.cumulativeEstimatedCny > summary.budgetAmountCny;

  let cumulativeNote = '按冷站电表有效读数与分时电价计算';
  if (isElectricityError) {
    cumulativeNote = electricityErrorMessage ?? '暂无法读取冷站能耗与估算电费';
  } else if (summary.pricingState === 'NO_DATA') {
    cumulativeNote =
      summary.window.state === 'FUTURE' ? '该月尚未开始' : '该月暂无有效冷站电表读数';
  } else if (summary.pricingState === 'UNPRICED') {
    cumulativeNote = '站点尚未配置购电分时电价，未计电费';
  } else if (summary.pricingState === 'PARTIAL_UNPRICED') {
    cumulativeNote = `月内有 ${formatDecimal(summary.unpricedKWh, 0)} kWh 缺少电价覆盖，未合算整月电费`;
  } else if (summary.usageRatio !== null) {
    cumulativeNote = `已用月度预算 ${formatDecimal(summary.usageRatio * 100, 1)}%`;
  }

  let projectedNote = '至少满 1 天有效读数后按日均线性外推';
  if (isPast && summary.cumulativeEstimatedCny !== null) {
    projectedNote = '自然月已结束，等于当月累计估算电费';
  } else if (summary.projectedOverrunCny !== null) {
    projectedNote =
      summary.projectedOverrunCny > 0
        ? `预计超支 ¥${formatDecimal(summary.projectedOverrunCny, 2)}`
        : `预计结余 ¥${formatDecimal(Math.abs(summary.projectedOverrunCny), 2)}`;
  }

  return (
    <div className="flex flex-col gap-4">
      {/* Strictly 3 KPI cards per spec */}
      <div className="grid gap-3 md:grid-cols-3">
        <Panel>
          <div className="flex flex-col gap-1.5 px-4 py-4">
            <span className="inline-flex items-center gap-1 text-xs font-medium text-muted-foreground">
              {cumulativeLabel}
              <InfoTip>
                估算电费仅统计冷站电表有效读数在分时电价下的电度电费，不含整栋公区照明电梯、基本电费及力调奖罚；正式结算金额以供电局结算电费单为准。
              </InfoTip>
            </span>
            {isElectricityPending ? (
              <Skeleton className="h-8 w-36" />
            ) : (
              <span className="text-2xl font-semibold tracking-tight text-foreground tabular-nums">
                {formatCurrency(summary.cumulativeEstimatedCny)}
              </span>
            )}
            <span className="text-xs text-muted-foreground">{cumulativeNote}</span>
          </div>
        </Panel>

        <Panel>
          <div className="flex flex-col gap-1.5 px-4 py-4">
            <span className="inline-flex items-center gap-1 text-xs font-medium text-muted-foreground">
              {projectedLabel}
              <InfoTip>
                进行中月份按已走完日历天数的日均估算电费线性外推至月末；已结束月份直接等于当月累计估算电费。
              </InfoTip>
            </span>
            {isElectricityPending ? (
              <Skeleton className="h-8 w-36" />
            ) : (
              <div className="flex flex-wrap items-baseline gap-2">
                <span className="text-2xl font-semibold tracking-tight text-foreground tabular-nums">
                  {formatCurrency(summary.projectedMonthEndCny)}
                </span>
                {summary.projectedOverrunCny !== null ? (
                  alreadyExceeded ? (
                    <Badge variant="destructive">◆ 已超月度预算</Badge>
                  ) : summary.projectedOverrunCny > 0 ? (
                    <Badge variant="warning">▲ 预计超预算</Badge>
                  ) : (
                    <Badge variant="success">○ 预计在预算内</Badge>
                  )
                ) : null}
              </div>
            )}
            <span className="text-xs text-muted-foreground">{projectedNote}</span>
          </div>
        </Panel>

        <Panel>
          <div className="flex flex-col gap-1.5 px-4 py-4">
            <div className="flex items-center justify-between gap-2">
              <span className="text-xs font-medium text-muted-foreground">月度电费预算</span>
              {canManageBilling ? (
                <Button
                  type="button"
                  size="xs"
                  variant="ghost"
                  onClick={onOpenBudgetDialog}
                >
                  <Pencil data-icon="inline-start" />
                  {budget ? '调整预算' : '设置预算'}
                </Button>
              ) : null}
            </div>
            <span className="text-2xl font-semibold tracking-tight text-foreground tabular-nums">
              {budget ? formatCurrency(budget.budgetAmountCny) : '未设置'}
            </span>
            <span className="line-clamp-1 text-xs text-muted-foreground">
              {budget
                ? budget.note
                  ? `${budget.note} · ${budget.updatedByDisplay || '管理员'}`
                  : `由 ${budget.updatedByDisplay || '管理员'} 维护`
                : canManageBilling
                  ? `尚未设置 ${monthText} 冷站电费预算`
                  : `${monthText} 暂未登记月度电费预算`}
            </span>
          </div>
        </Panel>
      </div>

      {isElectricityError && electricityErrorMessage ? (
        <Alert variant="destructive">
          <AlertTitle>无法读取当月冷站估算电费序列</AlertTitle>
          <AlertDescription>{electricityErrorMessage}</AlertDescription>
        </Alert>
      ) : null}

      {!isElectricityPending &&
      !isElectricityError &&
      (summary.pricingState === 'UNPRICED' || summary.pricingState === 'PARTIAL_UNPRICED') ? (
        <Alert>
          <AlertTitle>
            {summary.pricingState === 'UNPRICED'
              ? '站点尚未配置生效的分时电价'
              : '月内部分时段用电缺少分时电价覆盖'}
          </AlertTitle>
          <AlertDescription className="tabular-nums">
            {summary.pricingState === 'UNPRICED'
              ? `${monthText} 已计量冷站用电 ${formatDecimal(summary.totalEnergyKWh ?? 0, 0)} kWh，但因未配置分时电价无法折算估算电费；你仍可正常录入并核对供电局结算电费单电量。`
              : `${monthText} 共计量冷站用电 ${formatDecimal(summary.totalEnergyKWh ?? 0, 0)} kWh，其中 ${formatDecimal(summary.unpricedKWh, 0)} kWh 所在时段无电价（已定价部分电度电费 ¥${formatDecimal(summary.pricedPortionCny, 2)}）。为避免低估整月电费，累计估算总额暂不合算。`}
          </AlertDescription>
        </Alert>
      ) : null}

      {/* Main 2/3 chart + 1/3 TOU & Utility Bill quick card */}
      <div className="grid gap-4 lg:grid-cols-3">
        <Panel className="lg:col-span-2">
          <PanelHeader
            title="逐日累计估算电费与预算节奏"
            caption={`${monthText}（1 日至 ${summary.window.daysInMonth} 日）`}
            action={
              summary.pricingState === 'PRICED' ? (
                <ToggleGroup
                  type="single"
                  size="sm"
                  variant="outline"
                  value={seriesDisplayMode}
                  onValueChange={(value) => {
                    if (value === 'chart' || value === 'table') setSeriesDisplayMode(value);
                  }}
                  aria-label="累计估算电费视图切换"
                >
                  <ToggleGroupItem value="chart" className="text-xs">
                    图表
                  </ToggleGroupItem>
                  <ToggleGroupItem value="table" className="text-xs">
                    表格
                  </ToggleGroupItem>
                </ToggleGroup>
              ) : undefined
            }
          />
          <div className="p-4">
            {isElectricityPending ? (
              <Skeleton className="h-[300px] w-full" />
            ) : summary.pricingState !== 'PRICED' ? (
              <div className="flex h-[300px] flex-col items-center justify-center gap-2 rounded-lg border border-dashed border-border px-6 text-center">
                <p className="text-sm font-medium text-foreground">
                  {summary.pricingState === 'NO_DATA'
                    ? `${monthText} 暂无可绘制的逐日估算电费曲线`
                    : '待分时电价完整覆盖全月时段后显示累计估算电费曲线'}
                </p>
                <p className="max-w-md text-xs text-muted-foreground">
                  {budget
                    ? `当前已设置月度电费预算 ¥${formatDecimal(budget.budgetAmountCny, 2)}。`
                    : '你可以先为该月设置月度电费预算，或在收到供电局通知单后录入结算电费单。'}
                </p>
              </div>
            ) : seriesDisplayMode === 'table' ? (
              <div className="max-h-[300px] overflow-y-auto">
                <Table aria-label="逐日累计估算电费与预算参考">
                  <TableHeader>
                    <TableRow>
                      <TableHead>日期</TableHead>
                      <TableHead className="text-right">当日估算电费</TableHead>
                      <TableHead className="text-right">累计估算电费</TableHead>
                      <TableHead className="text-right">预算匀速参考</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {summary.dailySeries.map((point) => (
                      <TableRow key={point.dateKey} className="tabular-nums">
                        <TableCell>{point.dateKey}</TableCell>
                        <TableCell className="text-right">
                          {formatCurrency(point.dailyCostCny)}
                        </TableCell>
                        <TableCell className="text-right font-medium">
                          {formatCurrency(point.cumulativeCostCny)}
                        </TableCell>
                        <TableCell className="text-right text-muted-foreground">
                          {formatCurrency(point.linearBudgetCny)}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            ) : (
              <ChartContainer config={budgetChartConfig} className="aspect-auto h-[300px] w-full">
                <ComposedChart
                  data={[...summary.dailySeries]}
                  margin={{ left: 8, right: 12, top: 12, bottom: 4 }}
                >
                  <CartesianGrid vertical={false} />
                  <XAxis
                    dataKey="dayLabel"
                    tickLine={false}
                    axisLine={false}
                    tickMargin={8}
                    minTickGap={16}
                  />
                  <YAxis
                    tickLine={false}
                    axisLine={false}
                    width={64}
                    tickFormatter={(value: number) =>
                      value >= 10_000 ? `¥${(value / 10_000).toFixed(1)}万` : `¥${value}`
                    }
                  />
                  <ChartTooltip content={<ChartTooltipContent indicator="dot" />} />
                  {budget ? (
                    <ReferenceLine
                      y={budget.budgetAmountCny}
                      stroke="var(--warning)"
                      strokeDasharray="4 4"
                      ifOverflow="extendDomain"
                    />
                  ) : null}
                  {budget ? (
                    <Line
                      type="monotone"
                      dataKey="linearBudgetCny"
                      stroke="var(--color-linearBudgetCny)"
                      strokeDasharray="4 4"
                      strokeWidth={1.5}
                      dot={false}
                      isAnimationActive={false}
                    />
                  ) : null}
                  <Area
                    type="monotone"
                    dataKey="cumulativeCostCny"
                    stroke="var(--color-cumulativeCostCny)"
                    fill="var(--color-cumulativeCostCny)"
                    fillOpacity={0.14}
                    strokeWidth={2}
                    connectNulls={false}
                    isAnimationActive={false}
                  />
                </ComposedChart>
              </ChartContainer>
            )}
          </div>
        </Panel>

        <div className="flex flex-col gap-4">
          <Panel>
            <PanelHeader title="峰平谷电度电费构成" caption="按冷站已定价电量统计" />
            <div className="space-y-3 p-4">
              {summary.touPeriods.map((period) => (
                <div key={period.code} className="space-y-1">
                  <div className="flex items-baseline justify-between gap-2 text-xs tabular-nums">
                    <span className="font-medium text-foreground">{period.label}时段</span>
                    <span className="text-muted-foreground">
                      {formatDecimal(period.energyKWh, 0)} kWh ·{' '}
                      <strong className="font-semibold text-foreground">
                        ¥{formatDecimal(period.costCny, 2)}
                      </strong>{' '}
                      ({formatDecimal(period.shareRatio * 100, 1)}%)
                    </span>
                  </div>
                  <div className="h-2 w-full overflow-hidden rounded-full bg-muted" aria-hidden="true">
                    <div
                      className="h-full rounded-full"
                      style={{
                        width: `${Math.min(100, Math.max(0, period.shareRatio * 100))}%`,
                        backgroundColor: PERIOD_COLOR[period.code],
                      }}
                    />
                  </div>
                </div>
              ))}
            </div>
          </Panel>

          <Panel>
            <PanelHeader
              title="当月已确认结算电费单"
              caption="供电局正式结算"
              action={
                monthBills.length > 0 ? (
                  <PanelLink
                    to="/energy-analysis/billing"
                    search={{ site: siteId, view: 'bills', month: billingMonth }}
                  >
                    查看全部
                  </PanelLink>
                ) : undefined
              }
            />
            <div className="space-y-2.5 p-4">
              {confirmedBills.length === 0 ? (
                <div className="space-y-2.5 rounded-lg border border-dashed border-border p-3.5">
                  <div className="flex items-center gap-2 text-xs font-medium text-foreground">
                    <Receipt className="size-4 text-muted-foreground" aria-hidden="true" />
                    <span>
                      {draftBills.length > 0
                        ? `${monthText} 尚无已确认归档单据（有 ${draftBills.length} 张草稿待确认）`
                        : `${monthText} 尚未录入结算电费单`}
                    </span>
                  </div>
                  <p className="text-xs text-muted-foreground">
                    {draftBills.length > 0
                      ? '请前往「结算电费单核对」页签核对草稿并确认归档后，此处与月报将展示正式结算金额。'
                      : '收到供电局电费发票或结算单后录入，可自动按抄表起止日期对齐平台电量并核对偏差或冷站分摊金额。'}
                  </p>
                  {draftBills.length > 0 ? (
                    <Button
                      type="button"
                      size="sm"
                      variant="outline"
                      onClick={() => onInspectBill(draftBills[0]!.id)}
                    >
                      核对待确认草稿
                    </Button>
                  ) : canManageBilling ? (
                    <Button
                      type="button"
                      size="sm"
                      variant="outline"
                      onClick={onOpenCreateBillDialog}
                    >
                      <Plus data-icon="inline-start" />
                      录入结算电费单
                    </Button>
                  ) : null}
                </div>
              ) : (
                <div className="space-y-2">
                  {confirmedBills.map((bill) => (
                    <button
                      key={bill.id}
                      type="button"
                      onClick={() => onInspectBill(bill.id)}
                      className="flex w-full items-start justify-between gap-3 rounded-lg border border-border bg-card p-3 text-left transition-colors hover:bg-muted/40"
                    >
                      <div className="min-w-0 space-y-1">
                        <div className="flex flex-wrap items-center gap-1.5">
                          <span className="text-xs font-semibold text-foreground">
                            {METER_SCOPE_LABEL[bill.meterScope]}
                          </span>
                          <Badge variant="success">
                            {UTILITY_BILL_STATUS_LABEL.CONFIRMED}
                          </Badge>
                        </div>
                        <div className="text-xs text-muted-foreground tabular-nums">
                          抄表周期：{bill.readingStartDate} 至 {bill.readingEndDate}
                        </div>
                      </div>
                      <div className="flex shrink-0 items-center gap-1 text-right tabular-nums">
                        <div>
                          <div className="text-sm font-semibold text-foreground">
                            ¥{formatDecimal(bill.totalPayableCny, 2)}
                          </div>
                          <div className="text-[11px] text-muted-foreground">
                            {formatDecimal(bill.billedEnergyKwh, 0)} kWh
                          </div>
                        </div>
                        <ArrowUpRight className="size-4 text-muted-foreground" aria-hidden="true" />
                      </div>
                    </button>
                  ))}
                </div>
              )}
            </div>
          </Panel>
        </div>
      </div>
    </div>
  );
}
