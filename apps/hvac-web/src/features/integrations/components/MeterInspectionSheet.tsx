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
  type MeterReconciliationRow,
} from '../integrations-model';
import {
  devicePointHourlyHistoryQuery,
  externalSourceWorkOrdersQuery,
  integrationsKeys,
} from '../query';
import { DrawerHourlyHistorySection, GovernanceBadge } from './GovernanceShared';

export interface MeterInspectionSheetProps {
  readonly row: MeterReconciliationRow | null;
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

export function MeterInspectionSheet({
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
}: MeterInspectionSheetProps) {
  return (
    <Sheet
      open={Boolean(row)}
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <SheetContent className="overflow-y-auto data-[side=right]:sm:max-w-[480px]">
        {row ? (
          <MeterInspectionDetail
            key={row.deviceId}
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

function MeterInspectionDetail({
  row,
  siteId,
  timezone,
  windowFromMs,
  windowToMs,
  csrfToken,
  canReadHistory,
  canReadWorkOrders,
  canCreateWorkOrder,
}: Omit<MeterInspectionSheetProps, 'row' | 'onClose'> & {
  readonly row: MeterReconciliationRow;
}) {
  const queryClient = useQueryClient();
  const [creatingWorkOrder, setCreatingWorkOrder] = useState(false);

  const workOrdersQuery = useQuery(
    externalSourceWorkOrdersQuery(siteId, row.workOrderSourceRef, canReadWorkOrders),
  );
  const openWorkOrder =
    findOpenExternalWorkOrder(workOrdersQuery.data, row.workOrderSourceRef) ??
    row.openWorkOrder;

  const primaryHistoryKey = row.historyKeys[0] ?? null;
  const historyQuery = useQuery(
    devicePointHourlyHistoryQuery({
      siteId,
      deviceId: row.deviceId,
      pointCode: primaryHistoryKey,
      timezone,
      fromMs: windowFromMs,
      toMs: windowToMs,
      csrfToken,
      enabled: canReadHistory && Boolean(primaryHistoryKey),
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
          <span className="font-mono text-muted-foreground">{row.deviceCode}</span>
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
        <SheetTitle>{row.displayName}</SheetTitle>
        <SheetDescription>
          {row.meterRoleLabel}
          {row.spaceName ? ` · ${row.spaceName}` : ''}
          {' · '}
          {row.channelLabel}
        </SheetDescription>
      </SheetHeader>

      <section className="grid grid-cols-2 gap-x-4 gap-y-3 border-b border-divider px-6 py-4 text-xs">
        <div>
          <span className="text-muted-foreground">当前累计读数</span>
          <div className="mt-0.5 font-mono text-sm font-semibold tabular-nums">
            {row.primaryReadingValue}
            {row.primaryReadingValue !== '—' && row.primaryReadingUnit
              ? ` ${row.primaryReadingUnit}`
              : ''}
          </div>
        </div>
        <div>
          <span className="text-muted-foreground">最新采样时间</span>
          <div className="mt-0.5 font-mono text-sm tabular-nums">
            {row.sampledAt ? formatSlotDateTime(row.sampledAt, timezone) : '—'}
          </div>
        </div>
        <div>
          <span className="text-muted-foreground">通信通道与采样</span>
          <div className="mt-0.5 font-medium text-foreground">
            {row.channelLabel} · {row.sampleIntervalLabel}
          </div>
        </div>
        <div>
          <span className="text-muted-foreground">回零 / 翻转规则</span>
          <div className="mt-0.5 font-medium text-foreground">{row.counterPolicyLabel}</div>
        </div>
        {row.hasHourlySeries ? (
          <>
            <div>
              <span className="text-muted-foreground">近 24 小时有效计量</span>
              <div className="mt-0.5 font-mono text-sm font-medium tabular-nums">
                {row.validEnergyKwh !== null
                  ? `${formatDecimal(row.validEnergyKwh, 2)} kWh`
                  : '—'}
              </div>
            </div>
            <div>
              <span className="text-muted-foreground">可疑差值 / 缺段</span>
              <div className="mt-0.5 font-mono text-sm font-medium tabular-nums">
                {row.suspectEnergyKwh > 0.001
                  ? `▲ +${formatDecimal(row.suspectEnergyKwh, 2)} kWh`
                  : '0.00 kWh'}
                {row.missingCompletedHours > 0 ? ` · 缺 ${row.missingCompletedHours}h` : ''}
              </div>
            </div>
          </>
        ) : null}
      </section>

      <section className="flex flex-col gap-2 border-b border-divider px-6 py-4">
        <h3 className="text-sm font-semibold">核对结论与治理边界</h3>
        {row.diagnosticNotes.length > 0 ? (
          <ul className="space-y-1.5 text-xs text-foreground">
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
            当前表计通信与近 24 小时累计读数正常，未发现单步异常跳变或读数回退。
          </p>
        )}
        <p className="rounded-md border border-divider bg-muted/40 px-3 py-2 text-xs text-muted-foreground">
          平台保留原始采集记录，不在网页端手工覆盖历史电量；换表、脉冲倍率调整或通信寄存器变更请通过站点接入清单同步。
        </p>
      </section>

      {canReadHistory && primaryHistoryKey ? (
        <DrawerHourlyHistorySection
          title={`${row.primaryReadingLabel}小时趋势`}
          unit={row.primaryReadingUnit}
          isCounter={Boolean(row.primaryCounterPoint)}
          rows={historyQuery.data}
          isLoading={historyQuery.isPending}
          isError={historyQuery.isError}
          windowFromMs={windowFromMs}
          windowToMs={windowToMs}
          timezone={timezone}
        />
      ) : null}

      <section className="flex flex-wrap items-center justify-between gap-2 px-6 py-4">
        {row.trendsSearch.points ? (
          <Button variant="outline" size="sm" asChild>
            <Link to="/operations/trends" search={row.trendsSearch}>
              查看当时趋势
              <ArrowUpRight data-icon="inline-end" />
            </Link>
          </Button>
        ) : (
          <span className="text-xs text-muted-foreground">该表计暂无瞬时趋势点位</span>
        )}

        {openWorkOrder ? (
          <Button variant="outline" size="sm" asChild>
            <Link
              to="/operations/work-center"
              search={{ site: siteId, view: 'all', inspect: openWorkOrder.workOrderId }}
            >
              查看核查工单
              <ArrowUpRight data-icon="inline-end" />
            </Link>
          </Button>
        ) : canCreateWorkOrder ? (
          <Button size="sm" onClick={() => setCreatingWorkOrder(true)}>
            <ClipboardList data-icon="inline-start" />
            创建核查工单
          </Button>
        ) : null}
      </section>

      {canCreateWorkOrder ? (
        <CreateWorkOrderDialog
          open={creatingWorkOrder}
          onOpenChange={setCreatingWorkOrder}
          title="创建计量核查工单？"
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
            toast.success('已创建计量核查工单');
            void queryClient.invalidateQueries({
              queryKey: integrationsKeys.externalWorkOrders(siteId, row.workOrderSourceRef),
            });
          }}
        />
      ) : null}
    </>
  );
}
