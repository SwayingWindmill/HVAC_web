import { Link } from '@tanstack/react-router';
import { CalendarClock, ClipboardList, ExternalLink, Pencil, TrendingUp } from 'lucide-react';
import type { OperatingException, SiteTariff } from '@/api/generated/platformGateway.gen';
import type { WorkOrder } from '@/api/work-orders';
import { Button } from '@/components/ui/button';
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
import { daySchedule, PERIOD_LABEL } from '@/features/tariff/tariff-model';
import { STATUS_LABELS } from '@/features/work-orders/work-order-presentation';
import { formatTime } from '@/lib/operator-format';
import {
  buildDeviationTrendsSearch,
  DAY_OF_WEEK_LABELS,
  EXCEPTION_KIND_LABELS,
  findActiveDeviationWorkOrder,
  formatMinuteOfDay,
  type EffectiveCoolingSlot,
  type EffectiveScheduleDay,
  type EffectiveScheduleWeek,
  type ScheduleDeviation,
  type SchedulePlantMonitorTarget,
} from '../schedule-model';

export type ScheduleInspectionTarget =
  | {
      readonly kind: 'slot';
      readonly day: EffectiveScheduleDay;
      readonly slot: EffectiveCoolingSlot;
    }
  | {
      readonly kind: 'shutdown';
      readonly day: EffectiveScheduleDay;
      readonly exception: OperatingException;
    }
  | {
      readonly kind: 'deviation';
      readonly deviation: ScheduleDeviation;
    };

export function resolveScheduleInspection(
  inspect: string | undefined,
  week: EffectiveScheduleWeek,
  deviations: readonly ScheduleDeviation[],
): ScheduleInspectionTarget | null {
  if (!inspect) return null;
  if (inspect.startsWith('deviation:')) {
    const found = deviations.find((dev) => dev.id === inspect);
    return found ? { kind: 'deviation', deviation: found } : null;
  }
  if (inspect.startsWith('shutdown:')) {
    for (const day of week.days) {
      if (day.approvedException && `shutdown:${day.approvedException.id}:${day.date}` === inspect) {
        return { kind: 'shutdown', day, exception: day.approvedException };
      }
    }
    return null;
  }
  for (const day of week.days) {
    const slot = day.effectiveSlots.find((candidate) => candidate.id === inspect);
    if (slot) {
      return { kind: 'slot', day, slot };
    }
  }
  return null;
}

export interface ScheduleInspectorDrawerProps {
  readonly siteId: string;
  readonly timezone: string;
  readonly target: ScheduleInspectionTarget | null;
  readonly week: EffectiveScheduleWeek;
  readonly tariff: SiteTariff | null;
  readonly monitorTargets: readonly SchedulePlantMonitorTarget[];
  readonly externalWorkOrders: readonly WorkOrder[];
  readonly canManageSchedule: boolean;
  readonly canRequestException: boolean;
  readonly canCreateWorkOrder: boolean;
  readonly onClose: () => void;
  readonly onOpenEditor: () => void;
  readonly onOpenExceptionsTab: () => void;
  readonly onCreateDeviationWorkOrder: (deviation: ScheduleDeviation) => void;
  readonly onRequestOvertimeForDate: (date: string) => void;
}

export function ScheduleInspectorDrawer({
  siteId,
  timezone,
  target,
  week,
  tariff,
  monitorTargets,
  externalWorkOrders,
  canManageSchedule,
  canRequestException,
  canCreateWorkOrder,
  onClose,
  onOpenEditor,
  onOpenExceptionsTab,
  onCreateDeviationWorkOrder,
  onRequestOvertimeForDate,
}: ScheduleInspectorDrawerProps) {
  return (
    <Sheet open={target !== null} onOpenChange={(open) => (!open ? onClose() : undefined)}>
      <SheetContent side="right" className="flex flex-col justify-between overflow-y-auto">
        {target?.kind === 'slot' ? (
          <SlotInspectorBody
            day={target.day}
            slot={target.slot}
            week={week}
            tariff={tariff}
            canManageSchedule={canManageSchedule}
            onOpenEditor={() => {
              onClose();
              onOpenEditor();
            }}
            onOpenExceptionsTab={() => {
              onClose();
              onOpenExceptionsTab();
            }}
          />
        ) : target?.kind === 'shutdown' ? (
          <ShutdownInspectorBody
            day={target.day}
            exception={target.exception}
            timezone={timezone}
            onOpenExceptionsTab={() => {
              onClose();
              onOpenExceptionsTab();
            }}
          />
        ) : target?.kind === 'deviation' ? (
          <DeviationInspectorBody
            siteId={siteId}
            deviation={target.deviation}
            week={week}
            monitorTargets={monitorTargets}
            externalWorkOrders={externalWorkOrders}
            canRequestException={canRequestException}
            canCreateWorkOrder={canCreateWorkOrder}
            onClose={onClose}
            onCreateDeviationWorkOrder={onCreateDeviationWorkOrder}
            onRequestOvertimeForDate={onRequestOvertimeForDate}
          />
        ) : null}
      </SheetContent>
    </Sheet>
  );
}

function FactRow({ label, children }: { readonly label: string; readonly children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1 border-b border-divider py-3 last:border-b-0">
      <span className="text-xs font-medium text-muted-foreground">{label}</span>
      <div className="text-sm text-foreground">{children}</div>
    </div>
  );
}

function SlotInspectorBody({
  day,
  slot,
  week,
  tariff,
  canManageSchedule,
  onOpenEditor,
  onOpenExceptionsTab,
}: {
  readonly day: EffectiveScheduleDay;
  readonly slot: EffectiveCoolingSlot;
  readonly week: EffectiveScheduleWeek;
  readonly tariff: SiteTariff | null;
  readonly canManageSchedule: boolean;
  readonly onOpenEditor: () => void;
  readonly onOpenExceptionsTab: () => void;
}) {
  const preStart = Math.max(0, slot.startMinute - week.preCoolLeadMinutes);
  const lagEnd = Math.min(1440, slot.endMinute + week.coastDownLagMinutes);

  const overlappingPeriods = tariff
    ? daySchedule(tariff, new Date(day.startMs + 12 * 3_600_000), week.timezone).filter(
        (period) => period.startMinute < slot.endMinute && period.endMinute > slot.startMinute,
      )
    : [];

  return (
    <>
      <div>
        <SheetHeader className="border-b border-divider pb-4">
          <div className="flex items-center gap-2">
            <span className="rounded-sm bg-primary-soft px-2 py-0.5 text-xs font-medium text-primary">
              {slot.source === 'EXCEPTION' ? '例外日程覆盖' : '常规周计划'}
            </span>
            {slot.spanGroupId ? (
              <span className="rounded-sm bg-muted px-2 py-0.5 text-xs text-muted-foreground">
                跨午夜拆分段
              </span>
            ) : null}
          </div>
          <SheetTitle className="mt-1 text-base">
            {day.date}（{day.label}）供冷时段
          </SheetTitle>
          <SheetDescription className="tabular-nums">
            预期供冷窗口 {formatMinuteOfDay(slot.startMinute)}–{formatMinuteOfDay(slot.endMinute)}
          </SheetDescription>
        </SheetHeader>

        <div className="px-4 py-2">
          <FactRow label="生效来源">
            {slot.source === 'EXCEPTION' && day.approvedException ? (
              <div className="space-y-1">
                <p className="font-medium">
                  {EXCEPTION_KIND_LABELS[day.approvedException.kind]} · {day.approvedException.title}
                </p>
                <p className="text-xs text-muted-foreground">
                  覆盖日期：{day.approvedException.startDate} 至 {day.approvedException.endDate}（全天 00:00–24:00 覆盖常规周计划）
                </p>
              </div>
            ) : (
              <span>常规周运行计划</span>
            )}
          </FactRow>

          <FactRow label="预冷提前与停机滞后缓冲窗">
            <div className="space-y-1 tabular-nums">
              <p>
                允许开机范围：{formatMinuteOfDay(preStart)}–{formatMinuteOfDay(lagEnd)}
              </p>
              <p className="text-xs text-muted-foreground">
                含预冷提前 {week.preCoolLeadMinutes} 分钟、停机余冷滞后 {week.coastDownLagMinutes} 分钟；缓冲窗内的启停过渡不会记为计划外开机。
              </p>
            </div>
          </FactRow>

          <FactRow label="交接参考供水温度">
            <div className="space-y-1.5">
              <p className="font-semibold tabular-nums">
                {slot.targetSupplyTempC !== null ? `${slot.targetSupplyTempC.toFixed(1)} °C` : '未设置'}
              </p>
              <div className="rounded-md border border-border bg-muted/40 px-3 py-2 text-xs text-muted-foreground">
                仅作冷站值班交接参考，平台不会据此自动下发设备启停或设定值指令。如需下发冷冻水供水温度设定值，请前往「优化建议」或「控制指令」。
              </div>
            </div>
          </FactRow>

          <FactRow label="涉及分时电价时段">
            {tariff === null ? (
              <span className="text-muted-foreground">当前站点尚未配置分时电价</span>
            ) : overlappingPeriods.length === 0 ? (
              <span className="text-muted-foreground">无匹配的分时时段</span>
            ) : (
              <ul className="space-y-1 tabular-nums">
                {overlappingPeriods.map((period) => {
                  const overlapStart = Math.max(slot.startMinute, period.startMinute);
                  const overlapEnd = Math.min(slot.endMinute, period.endMinute);
                  return (
                    <li key={`${period.periodCode}-${period.startMinute}`} className="flex items-center justify-between text-xs">
                      <span className="font-medium">{PERIOD_LABEL[period.periodCode]}时段</span>
                      <span className="text-muted-foreground">
                        {formatMinuteOfDay(overlapStart)}–{formatMinuteOfDay(overlapEnd)}（{period.energyRatePerKWh} 元/kWh）
                      </span>
                    </li>
                  );
                })}
              </ul>
            )}
          </FactRow>
        </div>
      </div>

      <SheetFooter className="border-t border-divider p-4 sm:flex-row sm:justify-end">
        <Button variant="outline" size="sm" onClick={onOpenExceptionsTab}>
          <CalendarClock data-icon="inline-start" />
          管理例外日程
        </Button>
        {slot.source === 'WEEKLY' && canManageSchedule ? (
          <Button size="sm" onClick={onOpenEditor}>
            <Pencil data-icon="inline-start" />
            编辑周计划
          </Button>
        ) : null}
      </SheetFooter>
    </>
  );
}

function ShutdownInspectorBody({
  day,
  exception,
  timezone,
  onOpenExceptionsTab,
}: {
  readonly day: EffectiveScheduleDay;
  readonly exception: OperatingException;
  readonly timezone: string;
  readonly onOpenExceptionsTab: () => void;
}) {
  return (
    <>
      <div>
        <SheetHeader className="border-b border-divider pb-4">
          <div className="flex items-center gap-2">
            <span className="rounded-sm bg-muted px-2 py-0.5 text-xs font-medium text-foreground">
              {EXCEPTION_KIND_LABELS[exception.kind]}
            </span>
          </div>
          <SheetTitle className="mt-1 text-base">
            {day.date}（{day.label}）全天停机例外
          </SheetTitle>
          <SheetDescription>{exception.title}</SheetDescription>
        </SheetHeader>

        <div className="px-4 py-2">
          <FactRow label="覆盖日期范围（全天 00:00–24:00）">
            <span className="tabular-nums">
              {exception.startDate} 至 {exception.endDate}
            </span>
          </FactRow>
          <FactRow label="申请原因">{exception.reason}</FactRow>
          <FactRow label="申请人">
            <span>
              {exception.requestedByDisplay} · {formatTime(exception.createdAt, timezone)}
            </span>
          </FactRow>
          {exception.decidedByDisplay ? (
            <FactRow label="审批记录">
              <div className="space-y-0.5">
                <p>
                  {exception.decidedByDisplay}
                  {exception.decidedAt ? ` · ${formatTime(exception.decidedAt, timezone)}` : ''}
                </p>
                {exception.decisionNote ? (
                  <p className="text-xs text-muted-foreground">备注：{exception.decisionNote}</p>
                ) : null}
              </div>
            </FactRow>
          ) : null}
        </div>
      </div>

      <SheetFooter className="border-t border-divider p-4 sm:flex-row sm:justify-end">
        <Button size="sm" onClick={onOpenExceptionsTab}>
          在例外日程中查看
        </Button>
      </SheetFooter>
    </>
  );
}

function DeviationInspectorBody({
  siteId,
  deviation,
  week,
  monitorTargets,
  externalWorkOrders,
  canRequestException,
  canCreateWorkOrder,
  onClose,
  onCreateDeviationWorkOrder,
  onRequestOvertimeForDate,
}: {
  readonly siteId: string;
  readonly deviation: ScheduleDeviation;
  readonly week: EffectiveScheduleWeek;
  readonly monitorTargets: readonly SchedulePlantMonitorTarget[];
  readonly externalWorkOrders: readonly WorkOrder[];
  readonly canRequestException: boolean;
  readonly canCreateWorkOrder: boolean;
  readonly onClose: () => void;
  readonly onCreateDeviationWorkOrder: (deviation: ScheduleDeviation) => void;
  readonly onRequestOvertimeForDate: (date: string) => void;
}) {
  const trendsSearch = buildDeviationTrendsSearch(deviation, monitorTargets);
  const activeWorkOrder = findActiveDeviationWorkOrder(externalWorkOrders, siteId, deviation);
  const dayLabel = DAY_OF_WEEK_LABELS[deviation.dayOfWeek] ?? '';

  return (
    <>
      <div>
        <SheetHeader className="border-b border-divider pb-4">
          <div className="flex items-center gap-2">
            <span className="inline-flex items-center gap-1 rounded-sm bg-warning-soft px-2 py-0.5 text-xs font-semibold text-warning-text">
              <span aria-hidden="true">▲</span>
              计划外开机
            </span>
          </div>
          <SheetTitle className="mt-1 text-base">计划外开机核查</SheetTitle>
          <SheetDescription className="tabular-nums">
            {deviation.date}（{dayLabel}）{formatMinuteOfDay(deviation.startMinute)}–{formatMinuteOfDay(deviation.endMinute)} · 持续 {deviation.durationMinutes} 分钟
          </SheetDescription>
        </SheetHeader>

        <div className="px-4 py-2">
          <FactRow label="核查判定依据">
            <p className="text-xs leading-relaxed text-muted-foreground">
              在站点预期停机窗口（已排除预冷提前 {week.preCoolLeadMinutes} 分钟与停机余冷滞后 {week.coastDownLagMinutes} 分钟）内，15 分钟历史遥测聚合桶检测到冷站主机或冷冻水泵处于运行状态。
            </p>
          </FactRow>

          <FactRow label="运行设备">
            <span className="font-medium">{deviation.runningDeviceNames.join('、')}</span>
          </FactRow>

          <FactRow label="期间冷水机有功功率（15 分钟桶）">
            {deviation.averagePowerKw !== null ? (
              <span className="tabular-nums">
                平均 <strong>{deviation.averagePowerKw.toFixed(1)}</strong> kW · 峰值{' '}
                <strong>{(deviation.maxPowerKw ?? deviation.averagePowerKw).toFixed(1)}</strong> kW
              </span>
            ) : (
              <span className="text-muted-foreground">由运行状态遥测判定（未采集有功功率）</span>
            )}
          </FactRow>

          <FactRow label="建议处置方式">
            <p className="text-xs leading-relaxed text-muted-foreground">
              核实是否为现场手动启机、群控未按时停机或未登记的临时加班供冷。若属正常加班供冷，补登并审批通过当日「加班供冷」例外后，该偏差标记将自动解除。
            </p>
          </FactRow>
        </div>
      </div>

      <SheetFooter className="flex-col gap-2 border-t border-divider p-4 sm:flex-col">
        <div className="flex w-full flex-wrap items-center justify-end gap-2">
          <Button asChild variant="outline" size="sm">
            <Link to="/operations/trends" search={trendsSearch} onClick={onClose}>
              <TrendingUp data-icon="inline-start" />
              在历史趋势查看
            </Link>
          </Button>

          {activeWorkOrder ? (
            <Button asChild size="sm">
              <Link
                to="/operations/work-center"
                search={{ inspect: activeWorkOrder.workOrderId }}
                onClick={onClose}
              >
                <ExternalLink data-icon="inline-start" />
                查看关联工单（{STATUS_LABELS[activeWorkOrder.status]}）
              </Link>
            </Button>
          ) : canCreateWorkOrder ? (
            <Button size="sm" onClick={() => onCreateDeviationWorkOrder(deviation)}>
              <ClipboardList data-icon="inline-start" />
              创建核查工单
            </Button>
          ) : null}
        </div>

        {canRequestException ? (
          <div className="flex w-full justify-end">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => {
                onClose();
                onRequestOvertimeForDate(deviation.date);
              }}
            >
              补登当日加班供冷例外
            </Button>
          </div>
        ) : null}
      </SheetFooter>
    </>
  );
}
