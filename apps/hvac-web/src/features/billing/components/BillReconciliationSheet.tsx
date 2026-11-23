import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { CheckCircle2, Lock, Pencil } from 'lucide-react';
import type { UtilityBill } from '@/api/generated/platformGateway.gen';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
import { Skeleton } from '@/components/ui/skeleton';
import { formatDecimal, formatTime } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import {
  BASIC_CHARGE_MODE_LABEL,
  formatBillingMonthLabel,
  formatTouBreakdownSummary,
  METER_SCOPE_LABEL,
  reconcileUtilityBill,
  resolveReadingDateWindow,
  UTILITY_BILL_STATUS_LABEL,
} from '../billing-model';
import { describeBillingApiError, siteReadingWindowElectricityQuery } from '../query';
import { ConfirmArchiveBillDialog } from './ConfirmArchiveBillDialog';

function formatSignedCurrency(amount: number | null): string {
  if (amount === null) return '—';
  const sign = amount > 0 ? '+' : amount < 0 ? '-' : '';
  return `${sign}¥${formatDecimal(Math.abs(amount), 2)}`;
}

function formatSignedPercent(ratio: number | null): string {
  if (ratio === null) return '—';
  const pct = ratio * 100;
  const sign = pct > 0 ? '+' : '';
  return `${sign}${formatDecimal(pct, 2)}%`;
}

export interface BillReconciliationSheetProps {
  readonly bill: UtilityBill | null;
  readonly tenantId: string;
  readonly siteId: string;
  readonly timezone: string;
  readonly csrfToken: string;
  readonly canManageBilling: boolean;
  readonly onClose: () => void;
  readonly onEditDraft: (bill: UtilityBill) => void;
}

export function BillReconciliationSheet({
  bill,
  tenantId,
  siteId,
  timezone,
  csrfToken,
  canManageBilling,
  onClose,
  onEditDraft,
}: BillReconciliationSheetProps) {
  const [confirmOpen, setConfirmOpen] = useState(false);

  const windowQuery = useQuery(
    siteReadingWindowElectricityQuery({
      tenantId,
      siteId,
      timezone,
      readingStartDate: bill?.readingStartDate ?? '2026-01-01',
      readingEndDate: bill?.readingEndDate ?? '2026-01-01',
      csrfToken,
      enabled: Boolean(bill),
    }),
  );

  const reconciliation = useMemo(
    () => (bill ? reconcileUtilityBill(bill, windowQuery.data ?? null) : null),
    [bill, windowQuery.data],
  );

  const readingWindow = useMemo(
    () =>
      bill
        ? resolveReadingDateWindow(bill.readingStartDate, bill.readingEndDate, timezone)
        : null,
    [bill, timezone],
  );

  const touSummaryText = useMemo(
    () => formatTouBreakdownSummary(bill?.touBreakdown),
    [bill?.touBreakdown],
  );

  return (
    <>
      <Sheet
        open={Boolean(bill)}
        onOpenChange={(open) => {
          if (!open) onClose();
        }}
      >
        <SheetContent className="flex w-full flex-col overflow-y-auto sm:max-w-xl">
          {bill && reconciliation ? (
            <>
              <SheetHeader className="space-y-2 border-b border-divider pb-4">
                <div className="flex flex-wrap items-center gap-2">
                  <Badge variant="outline" className="text-foreground">
                    {METER_SCOPE_LABEL[bill.meterScope]}
                  </Badge>
                  <Badge variant={bill.status === 'CONFIRMED' ? 'success' : 'warning'}>
                    {UTILITY_BILL_STATUS_LABEL[bill.status]}
                  </Badge>
                </div>
                <SheetTitle>
                  {formatBillingMonthLabel(bill.billingMonth)} · 结算电费单核对
                </SheetTitle>
                <SheetDescription className="tabular-nums">
                  供电局抄表周期：{bill.readingStartDate} 至 {bill.readingEndDate}
                  {readingWindow ? `（含首尾共 ${readingWindow.dayCount} 天）` : ''}
                  {bill.utilityAccountNo ? ` · 户号 ${bill.utilityAccountNo}` : ''}
                  {bill.invoiceNo ? ` · 单据号 ${bill.invoiceNo}` : ''}
                </SheetDescription>
              </SheetHeader>

              <div className="flex-1 space-y-5 py-4">
                {/* 1. Four-part payable breakdown */}
                <section className="space-y-2.5">
                  <div className="flex items-baseline justify-between">
                    <h3 className="text-sm font-semibold text-foreground">票面应收电费四项构成</h3>
                    <span className="text-xs text-muted-foreground tabular-nums">
                      抄表电量 {formatDecimal(bill.billedEnergyKwh, 2)} kWh
                    </span>
                  </div>
                  <div className="divide-y divide-divider rounded-lg border border-border bg-card text-sm tabular-nums">
                    <div className="flex items-center justify-between px-3.5 py-2.5">
                      <div className="space-y-0.5">
                        <div className="text-muted-foreground">1. 电度电费</div>
                        {touSummaryText ? (
                          <div className="text-xs text-muted-foreground">{touSummaryText}</div>
                        ) : null}
                      </div>
                      <span className="font-medium text-foreground">
                        ¥{formatDecimal(bill.energyChargeCny, 2)}
                      </span>
                    </div>
                    <div className="flex items-center justify-between px-3.5 py-2.5">
                      <div className="space-y-0.5">
                        <div className="text-muted-foreground">2. 基本电费</div>
                        <div className="text-xs text-muted-foreground">
                          {BASIC_CHARGE_MODE_LABEL[bill.basicChargeMode]}
                          {bill.basicChargeQuantity !== null
                            ? ` · ${formatDecimal(bill.basicChargeQuantity, 2)} ${
                                bill.basicChargeMode === 'TRANSFORMER_CAPACITY_KVA' ? 'kVA' : 'kW'
                              }`
                            : ''}
                        </div>
                      </div>
                      <span className="font-medium text-foreground">
                        ¥{formatDecimal(bill.basicChargeCny, 2)}
                      </span>
                    </div>
                    <div className="flex items-center justify-between px-3.5 py-2.5">
                      <div className="space-y-0.5">
                        <div className="text-muted-foreground">3. 力调电费（功率因数奖罚）</div>
                        {bill.standardPowerFactor !== null || bill.actualPowerFactor !== null ? (
                          <div className="text-xs text-muted-foreground">
                            {bill.standardPowerFactor !== null
                              ? `考核标准 ${formatDecimal(bill.standardPowerFactor, 2)}`
                              : ''}
                            {bill.standardPowerFactor !== null && bill.actualPowerFactor !== null
                              ? ' · '
                              : ''}
                            {bill.actualPowerFactor !== null
                              ? `实际功率因数 ${formatDecimal(bill.actualPowerFactor, 3)}`
                              : ''}
                          </div>
                        ) : null}
                      </div>
                      <span
                        className={cn(
                          'font-medium',
                          bill.powerFactorAdjustmentCny < 0
                            ? 'text-success-text'
                            : bill.powerFactorAdjustmentCny > 0
                              ? 'text-warning-text'
                              : 'text-foreground',
                        )}
                      >
                        {bill.powerFactorAdjustmentCny < 0
                          ? `-¥${formatDecimal(Math.abs(bill.powerFactorAdjustmentCny), 2)}（奖励减免）`
                          : `¥${formatDecimal(bill.powerFactorAdjustmentCny, 2)}`}
                      </span>
                    </div>
                    <div className="flex items-center justify-between px-3.5 py-2.5">
                      <span className="text-muted-foreground">4. 其他代征及附加</span>
                      <span className="font-medium text-foreground">
                        {bill.otherChargesCny < 0
                          ? `-¥${formatDecimal(Math.abs(bill.otherChargesCny), 2)}`
                          : `¥${formatDecimal(bill.otherChargesCny, 2)}`}
                      </span>
                    </div>
                    <div className="flex items-center justify-between bg-muted/30 px-3.5 py-3">
                      <div className="space-y-0.5">
                        <div className="font-semibold text-foreground">应收总电费</div>
                        {reconciliation.nonEnergyShareRatio !== null ? (
                          <div className="text-xs text-muted-foreground">
                            非电度费用合计 ¥{formatDecimal(reconciliation.nonEnergyChargeCny, 2)}（占{' '}
                            {formatDecimal(reconciliation.nonEnergyShareRatio * 100, 1)}%）
                          </div>
                        ) : null}
                      </div>
                      <span className="text-base font-semibold text-foreground">
                        ¥{formatDecimal(bill.totalPayableCny, 2)}
                      </span>
                    </div>
                  </div>
                </section>

                {/* 2. Meter-cycle reconciliation */}
                <section className="space-y-2.5">
                  <h3 className="text-sm font-semibold text-foreground">
                    {reconciliation.mode === 'DIRECT_VARIANCE'
                      ? '同抄表周期冷站电表偏差核对'
                      : '同抄表周期冷站电费分摊折算'}
                  </h3>

                  {windowQuery.isPending ? (
                    <Skeleton className="h-36 w-full" />
                  ) : windowQuery.isError ? (
                    <Alert variant="destructive">
                      <AlertTitle>无法读取该抄表周期的平台冷站电量</AlertTitle>
                      <AlertDescription>
                        {describeBillingApiError(
                          windowQuery.error,
                          '读取冷站电表历史区间失败，请稍后重试',
                        )}
                      </AlertDescription>
                    </Alert>
                  ) : reconciliation.mode === 'DIRECT_VARIANCE' ? (
                    <div className="space-y-3 rounded-lg border border-border bg-card p-3.5 text-sm tabular-nums">
                      <div className="grid gap-3 sm:grid-cols-2">
                        <div className="space-y-1 rounded-md bg-muted/30 p-2.5">
                          <div className="text-xs text-muted-foreground">电量核对（供电局 - 平台）</div>
                          <div className="text-base font-semibold text-foreground">
                            {reconciliation.energyVarianceKwh !== null
                              ? `${reconciliation.energyVarianceKwh > 0 ? '+' : ''}${formatDecimal(
                                  reconciliation.energyVarianceKwh,
                                  2,
                                )} kWh`
                              : '暂无平台电量'}
                          </div>
                          <div className="text-xs text-muted-foreground">
                            平台冷站计量：
                            {reconciliation.plantEnergyKwh !== null
                              ? `${formatDecimal(reconciliation.plantEnergyKwh, 2)} kWh（偏差 ${formatSignedPercent(
                                  reconciliation.energyVarianceRatio,
                                )}）`
                              : '该周期无有效读数'}
                          </div>
                        </div>

                        <div className="space-y-1 rounded-md bg-muted/30 p-2.5">
                          <div className="text-xs text-muted-foreground">
                            电度电费核对（票面 - 估算）
                          </div>
                          <div className="text-base font-semibold text-foreground">
                            {formatSignedCurrency(reconciliation.energyChargeVarianceCny)}
                          </div>
                          <div className="text-xs text-muted-foreground">
                            {reconciliation.plantEstimatedEnergyCostCny !== null
                              ? `平台同窗估算：¥${formatDecimal(
                                  reconciliation.plantEstimatedEnergyCostCny,
                                  2,
                                )}（偏差 ${formatSignedPercent(
                                  reconciliation.energyChargeVarianceRatio,
                                )}）`
                              : reconciliation.plantPricingState === 'PARTIAL_UNPRICED'
                                ? `有 ${formatDecimal(reconciliation.plantUnpricedKwh, 0)} kWh 缺电价，暂不比对金额`
                                : '暂无完整估算电度电费'}
                          </div>
                        </div>
                      </div>
                      <p className="text-xs text-muted-foreground">
                        核对口径：按供电局实际抄表周期（{bill.readingStartDate} 00:00 至{' '}
                        {bill.readingEndDate} 24:00）对齐平台冷站电表，消除自然月与跨月抄表日错位；基本电费、力调奖罚与代征附加不计入分时电度电费偏差。
                      </p>
                    </div>
                  ) : (
                    <div className="space-y-3 rounded-lg border border-border bg-card p-3.5 text-sm tabular-nums">
                      {reconciliation.meterExceedsBuilding &&
                      reconciliation.shareSource === 'MEASURED_ENERGY_RATIO' ? (
                        <Alert variant="destructive">
                          <AlertTitle>冷站计量电量高于整栋总表抄表电量</AlertTitle>
                          <AlertDescription>
                            同周期平台冷站计量电量（
                            {formatDecimal(reconciliation.plantEnergyKwh ?? 0, 2)} kWh）大于整栋抄表电量（
                            {formatDecimal(bill.billedEnergyKwh, 2)}{' '}
                            kWh），系统已拒绝按超过 100% 比例自动分摊；请检查互感器倍率或编辑草稿填写财务指定分摊比例。
                          </AlertDescription>
                        </Alert>
                      ) : null}

                      <div className="grid gap-3 sm:grid-cols-2">
                        <div className="space-y-1 rounded-md bg-muted/30 p-2.5">
                          <div className="text-xs text-muted-foreground">冷站用电分摊比例</div>
                          <div className="text-base font-semibold text-foreground">
                            {reconciliation.effectiveShareRatio !== null
                              ? `${formatDecimal(reconciliation.effectiveShareRatio * 100, 2)}%`
                              : '待核实'}
                          </div>
                          <div className="text-xs text-muted-foreground">
                            {reconciliation.shareSource === 'FINANCE_OVERRIDE'
                              ? `采用财务指定比例（实测电量占比 ${
                                  reconciliation.measuredChillerShareRatio !== null
                                    ? `${formatDecimal(
                                        reconciliation.measuredChillerShareRatio * 100,
                                        2,
                                      )}%`
                                    : '—'
                                }）`
                              : reconciliation.plantEnergyKwh !== null
                                ? `冷站同窗计量 ${formatDecimal(
                                    reconciliation.plantEnergyKwh,
                                    0,
                                  )} kWh ÷ 整栋 ${formatDecimal(bill.billedEnergyKwh, 0)} kWh`
                                : '该周期暂无冷站电表读数'}
                          </div>
                        </div>

                        <div className="space-y-1 rounded-md bg-muted/30 p-2.5">
                          <div className="text-xs text-muted-foreground">冷站应分摊结算总电费</div>
                          <div className="text-base font-semibold text-foreground">
                            {reconciliation.allocatedChillerTotalCostCny !== null
                              ? `¥${formatDecimal(reconciliation.allocatedChillerTotalCostCny, 2)}`
                              : '—'}
                          </div>
                          <div className="text-xs text-muted-foreground">
                            {reconciliation.averageEnergyUnitPriceCnyPerKwh !== null &&
                            reconciliation.blendedUnitPriceCnyPerKwh !== null
                              ? `整栋综合平均电度单价 ¥${formatDecimal(
                                  reconciliation.averageEnergyUnitPriceCnyPerKwh,
                                  4,
                                )}/kWh（含基本及力调 ¥${formatDecimal(
                                  reconciliation.blendedUnitPriceCnyPerKwh,
                                  4,
                                )}/kWh）`
                              : '抄表电量为 0，不折算综合单价'}
                          </div>
                        </div>
                      </div>

                      <div className="space-y-1 text-xs text-muted-foreground">
                        <div>
                          同周期平台冷站参考：计量电量{' '}
                          {reconciliation.plantEnergyKwh !== null
                            ? `${formatDecimal(reconciliation.plantEnergyKwh, 2)} kWh`
                            : '暂无有效读数'}
                          {' · '}
                          分时估算电度电费{' '}
                          {reconciliation.plantEstimatedEnergyCostCny !== null
                            ? `¥${formatDecimal(reconciliation.plantEstimatedEnergyCostCny, 2)}`
                            : reconciliation.plantPricingState === 'PARTIAL_UNPRICED'
                              ? `有 ${formatDecimal(reconciliation.plantUnpricedKwh, 0)} kWh 缺电价未合算`
                              : '暂无完整估算电费'}
                        </div>
                        {reconciliation.allocatedChillerEnergyChargeCny !== null &&
                        reconciliation.allocatedChillerNonEnergyChargeCny !== null ? (
                          <div>
                            其中分摊电度电费 ¥
                            {formatDecimal(reconciliation.allocatedChillerEnergyChargeCny, 2)}
                            ，分摊基本电费、力调及附加 ¥
                            {formatDecimal(reconciliation.allocatedChillerNonEnergyChargeCny, 2)}
                            。整栋总表含公区照明、电梯及租户用电，不与冷站电表直接相减计为误差。
                          </div>
                        ) : (
                          <div>
                            整栋总表含公区照明、电梯及租户用电，系统按冷站同周期电量占比折算应分摊电费，不将整栋差额误报为计量偏差。
                          </div>
                        )}
                      </div>
                    </div>
                  )}
                </section>

                {/* 3. Notes & audit metadata */}
                <section className="space-y-1.5 rounded-lg border border-border bg-muted/20 p-3 text-xs text-muted-foreground">
                  {bill.note ? <div className="text-foreground">备注：{bill.note}</div> : null}
                  <div>
                    录入与最近维护：{bill.updatedByDisplay || '管理员'} ·{' '}
                    {formatTime(bill.updatedAt, timezone)}
                  </div>
                  {bill.status === 'CONFIRMED' && bill.confirmedAt ? (
                    <div className="flex items-center gap-1.5 font-medium text-success-text">
                      <Lock className="size-3.5" aria-hidden="true" />
                      <span>
                        已由 {bill.confirmedByDisplay || '管理员'} 于{' '}
                        {formatTime(bill.confirmedAt, timezone)}{' '}
                        确认归档（已锁定不可修改，同账期同范围生效电费单）
                      </span>
                    </div>
                  ) : null}
                </section>
              </div>

              {bill.status === 'DRAFT' && canManageBilling ? (
                <SheetFooter className="flex-row justify-end gap-2 border-t border-divider pt-4">
                  <Button
                    type="button"
                    variant="outline"
                    onClick={() => onEditDraft(bill)}
                  >
                    <Pencil data-icon="inline-start" />
                    编辑草稿
                  </Button>
                  <Button type="button" onClick={() => setConfirmOpen(true)}>
                    <CheckCircle2 data-icon="inline-start" />
                    确认归档
                  </Button>
                </SheetFooter>
              ) : null}
            </>
          ) : null}
        </SheetContent>
      </Sheet>

      <ConfirmArchiveBillDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        bill={bill}
        siteId={siteId}
        csrfToken={csrfToken}
      />
    </>
  );
}
