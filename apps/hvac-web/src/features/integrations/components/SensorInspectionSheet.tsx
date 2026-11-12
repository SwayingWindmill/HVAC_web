import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { ArrowUpRight, ClipboardList, Link2 } from 'lucide-react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
import { CreateWorkOrderDialog } from '@/features/work-orders/CreateWorkOrderDialog';
import { formatDecimal } from '@/lib/operator-format';
import {
  findOpenExternalWorkOrder,
  formatSlotDateTime,
  type SensorCalibrationRow,
} from '../integrations-model';
import {
  devicePointHourlyHistoryQuery,
  externalSourceWorkOrdersQuery,
  integrationsKeys,
} from '../query';
import { DrawerHourlyHistorySection, GovernanceBadge } from './GovernanceShared';

export interface SensorInspectionSheetProps {
  readonly row: SensorCalibrationRow | null;
  readonly siteId: string;
  readonly timezone: string;
  readonly windowFromMs: number;
  readonly windowToMs: number;
  readonly csrfToken: string;
  readonly canReadHistory: boolean;
  readonly canReadWorkOrders: boolean;
  readonly canCreateWorkOrder: boolean;
  readonly onClose: () => void;
}

export function SensorInspectionSheet({
  row,
  siteId,
  timezone,
  windowFromMs,
  windowToMs,
  csrfToken,
  canReadHistory,
  canReadWorkOrders,
  canCreateWorkOrder,
  onClose,
}: SensorInspectionSheetProps) {
  return (
    <Sheet
      open={Boolean(row)}
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <SheetContent className="overflow-y-auto data-[side=right]:sm:max-w-[480px]">
        {row ? (
          <SensorInspectionDetail
            key={row.pointId}
            row={row}
            siteId={siteId}
            timezone={timezone}
            windowFromMs={windowFromMs}
            windowToMs={windowToMs}
            csrfToken={csrfToken}
            canReadHistory={canReadHistory}
            canReadWorkOrders={canReadWorkOrders}
            canCreateWorkOrder={canCreateWorkOrder}
          />
        ) : null}
      </SheetContent>
    </Sheet>
  );
}

function SensorInspectionDetail({
  row,
  siteId,
  timezone,
  windowFromMs,
  windowToMs,
  csrfToken,
  canReadHistory,
  canReadWorkOrders,
  canCreateWorkOrder,
}: Omit<SensorInspectionSheetProps, 'row' | 'onClose'> & {
  readonly row: SensorCalibrationRow;
}) {
  const queryClient = useQueryClient();
  const [creatingWorkOrder, setCreatingWorkOrder] = useState(false);

  const workOrdersQuery = useQuery(
    externalSourceWorkOrdersQuery(siteId, row.workOrderSourceRef, canReadWorkOrders),
  );
  const openWorkOrder =
    findOpenExternalWorkOrder(workOrdersQuery.data, row.workOrderSourceRef) ??
    row.openWorkOrder;

  const historyQuery = useQuery(
    devicePointHourlyHistoryQuery({
      siteId,
      deviceId: row.deviceId,
      pointCode: row.pointCode,
      timezone,
      fromMs: windowFromMs,
      toMs: windowToMs,
      csrfToken,
      enabled: canReadHistory,
    }),
  );

  const copyLink = () => {
    void navigator.clipboard.writeText(window.location.href).then(
      () => toast.success('链接已复制'),
      () => toast.error('无法复制链接'),
    );
  };

  return (
    <>
      <SheetHeader className="gap-2 border-b border-divider px-6 pt-5 pb-4">
        <div className="flex items-center gap-2 pr-8 text-xs">
          <GovernanceBadge status={row.status} />
          <span className="font-mono text-muted-foreground">{row.inspectKey}</span>
          <Button
            variant="ghost"
            size="icon-sm"
            className="ml-auto text-muted-foreground"
            aria-label="复制链接"
            onClick={copyLink}
          >
            <Link2 className="size-3.5" />
          </Button>
        </div>
        <SheetTitle>{row.pointLabel}</SheetTitle>
        <SheetDescription>
          {row.deviceDisplayName}
          {row.spaceName ? ` · ${row.spaceName}` : ''}
          {' · '}
          {row.channelLabel}
        </SheetDescription>
      </SheetHeader>

      <section className="grid grid-cols-2 gap-x-4 gap-y-3 border-b border-divider px-6 py-4 text-xs">
        <div>
          <span className="text-muted-foreground">当前实测值</span>
          <div className="mt-0.5 font-mono text-sm font-semibold tabular-nums">
            {row.currentValue}
            {row.currentValue !== '—' && row.unit ? ` ${row.unit}` : ''}
          </div>
        </div>
        <div>
          <span className="text-muted-foreground">最新采样时间</span>
          <div className="mt-0.5 font-mono text-sm tabular-nums">
            {row.sampledAt ? formatSlotDateTime(row.sampledAt, timezone) : '—'}
          </div>
        </div>
        <div>
          <span className="text-muted-foreground">通信通道与周期</span>
          <div className="mt-0.5 font-medium text-foreground">
            {row.channelLabel} · {row.sampleIntervalLabel}
          </div>
        </div>
        <div>
          <span className="text-muted-foreground">定检到期</span>
          <div className="mt-0.5 font-medium text-foreground">
            {row.calibration.dueAt
              ? `${row.calibration.dueAt.slice(0, 10)}（${row.calibration.label}）`
              : '未登记'}
          </div>
        </div>
        {row.sensorMeta ? (
          <div className="col-span-2">
            <span className="text-muted-foreground">关联物理传感器</span>
            <div className="mt-0.5 font-medium text-foreground">
              {row.sensorMeta.displayName}（{row.sensorMeta.code}）
              {row.sensorMeta.manufacturer || row.sensorMeta.model
                ? ` · ${[row.sensorMeta.manufacturer, row.sensorMeta.model]
                    .filter(Boolean)
                    .join(' ')}`
                : ''}
              {row.sensorMeta.serialNumber ? ` · SN ${row.sensorMeta.serialNumber}` : ''}
            </div>
          </div>
        ) : null}
        {row.boundAsset &&
        (row.boundAsset.ratedPowerKw !== null || row.boundAsset.ratedCapacityKw !== null) ? (
          <div className="col-span-2">
            <span className="text-muted-foreground">设备铭牌物理校核基准</span>
            <div className="mt-0.5 font-mono text-xs tabular-nums">
              {row.boundAsset.ratedPowerKw !== null
                ? `额定功率 ${formatDecimal(row.boundAsset.ratedPowerKw, 1)} kW`
                : ''}
              {row.boundAsset.ratedPowerKw !== null && row.boundAsset.ratedCapacityKw !== null
                ? ' · '
                : ''}
              {row.boundAsset.ratedCapacityKw !== null
                ? `额定制冷量 ${formatDecimal(row.boundAsset.ratedCapacityKw, 1)} kW`
                : ''}
            </div>
          </div>
        ) : null}
      </section>

      {row.probeOffsets.length > 0 ? (
        <section className="flex flex-col gap-2.5 border-b border-divider px-6 py-4">
          <h3 className="text-sm font-semibold">探头零点偏移评估（仅水泵运行稳态）</h3>
          {row.probeOffsets.map((offset) => (
            <div
              key={offset.pairCode}
              className="space-y-1.5 rounded-lg border border-warning/40 bg-warning-soft/50 p-3 text-xs"
            >
              <div className="flex items-center justify-between gap-2">
                <span className="font-semibold text-warning-text">▲ {offset.summaryLabel}</span>
                <span className="text-muted-foreground">{offset.pairLabel}</span>
              </div>
              <div className="font-mono text-foreground tabular-nums">
                实测均值偏差 {offset.offsetK >= 0 ? '+' : ''}
                {offset.offsetK.toFixed(2)} K ± {offset.stddevK.toFixed(2)} K · 稳态样本{' '}
                {offset.sampleCount} 组
              </div>
              {offset.windowStartedAt || offset.windowEndedAt ? (
                <div className="font-mono text-muted-foreground tabular-nums">
                  采样窗口：{formatSlotDateTime(offset.windowStartedAt, timezone)} 至{' '}
                  {formatSlotDateTime(offset.windowEndedAt, timezone)}
                </div>
              ) : null}
              <p className="text-muted-foreground">
                建议状态：未采用 — 平台不在网页端直接覆盖原始温度曲线，请现场核实探头导热、套管浸没深度与变送器零点后再修正硬件或站点标定参数。
              </p>
            </div>
          ))}
        </section>
      ) : null}

      {row.measurement ? (
        <section className="flex flex-col gap-2 border-b border-divider px-6 py-4">
          <h3 className="text-sm font-semibold">派生量测量不确定度</h3>
          <div className="space-y-1.5 rounded-lg border border-divider bg-muted/40 p-3 text-xs">
            <div className="font-mono text-sm font-medium text-foreground tabular-nums">
              {row.measurementSummaryLabel}
            </div>
            {row.measurement.deltaTK !== undefined ? (
              <div className="font-mono text-muted-foreground tabular-nums">
                当前供回水温差 ΔT = {row.measurement.deltaTK.toFixed(2)} K
                {row.measurement.deltaTAccuracyK !== undefined
                  ? `（探头合成精度 ±${row.measurement.deltaTAccuracyK.toFixed(2)} K）`
                  : ''}
              </div>
            ) : null}
            {row.measurement.lowConfidence ? (
              <p className="text-warning-text">
                ▲ 当前温差相对探头允许误差偏小，冷量与能效相对不确定度超过 10%，COP 均值、故障诊断、小时供冷与节能验证不再采用这一段。
              </p>
            ) : null}
          </div>
        </section>
      ) : null}

      <section className="flex flex-col gap-2 border-b border-divider px-6 py-4">
        <h3 className="text-sm font-semibold">质量校核结论</h3>
        {row.diagnosticNotes.length > 0 ? (
          <ul className="space-y-1.5 text-xs">
            {row.diagnosticNotes.map((note) => (
              <li
                key={note}
                className="rounded-md bg-warning-soft/60 px-2.5 py-1.5 text-warning-text"
              >
                ▲ {note}
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-xs text-muted-foreground">
            当前传感器读数在合理物理量程与热力学边界内，未触发卡值、跳变或越界标记。
          </p>
        )}
      </section>

      {canReadHistory ? (
        <DrawerHourlyHistorySection
          title={`${row.pointLabel}小时均值趋势`}
          unit={row.unit}
          isCounter={false}
          rows={historyQuery.data}
          isLoading={historyQuery.isPending}
          isError={historyQuery.isError}
          windowFromMs={windowFromMs}
          windowToMs={windowToMs}
          timezone={timezone}
        />
      ) : null}

      <section className="flex flex-wrap items-center justify-between gap-2 px-6 py-4">
        <Button variant="outline" size="sm" asChild>
          <Link to="/operations/trends" search={row.trendsSearch}>
            查看当时趋势
            <ArrowUpRight data-icon="inline-end" />
          </Link>
        </Button>

        {openWorkOrder ? (
          <Button variant="outline" size="sm" asChild>
            <Link
              to="/operations/work-center"
              search={{ site: siteId, view: 'all', inspect: openWorkOrder.workOrderId }}
            >
              查看校准工单
              <ArrowUpRight data-icon="inline-end" />
            </Link>
          </Button>
        ) : canCreateWorkOrder ? (
          <Button size="sm" onClick={() => setCreatingWorkOrder(true)}>
            <ClipboardList data-icon="inline-start" />
            创建校准工单
          </Button>
        ) : null}
      </section>

      {canCreateWorkOrder ? (
        <CreateWorkOrderDialog
          open={creatingWorkOrder}
          onOpenChange={setCreatingWorkOrder}
          title="创建传感器校准工单？"
          siteId={siteId}
          csrfToken={csrfToken}
          initial={{
            title: row.suggestedWorkOrderTitle,
            priority: row.status.level === 'CRITICAL' ? 'HIGH' : 'MEDIUM',
            description: row.suggestedWorkOrderDescription,
          }}
          sourceReferences={[
            {
              domain: 'EXTERNAL',
              resourceId: row.workOrderSourceRef,
              relationship: 'ORIGIN',
            },
          ]}
          onCreated={() => {
            toast.success('已创建传感器校准工单');
            void queryClient.invalidateQueries({
              queryKey: integrationsKeys.externalWorkOrders(siteId, row.workOrderSourceRef),
            });
          }}
        />
      ) : null}
    </>
  );
}
