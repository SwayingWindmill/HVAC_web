import { CalendarClock, Plus } from 'lucide-react';
import type { OperatingSchedule, SiteTariff, TariffPeriod } from '@/api/generated/platformGateway.gen';
import { Panel, PanelHeader } from '@/blocks/panel';
import { Button } from '@/components/ui/button';
import { daySchedule, PERIOD_LABEL } from '@/features/tariff/tariff-model';
import { cn } from '@/lib/utils';
import {
  EXCEPTION_KIND_LABELS,
  formatMinuteOfDay,
  type EffectiveCoolingSlot,
  type EffectiveScheduleDay,
  type EffectiveScheduleWeek,
  type ScheduleDeviation,
} from '../schedule-model';

const HOUR_TICKS = [0, 3, 6, 9, 12, 15, 18, 21, 24] as const;

const TARIFF_BAND_STYLE: Readonly<Record<TariffPeriod['periodCode'], string>> = {
  SUPER_PEAK: 'bg-destructive/[0.08]',
  PEAK: 'bg-warning/[0.08]',
  FLAT: 'bg-muted/35',
  VALLEY: 'bg-success/[0.07]',
};

const EXCEPTION_BADGE_STYLE = {
  HOLIDAY_SHUTDOWN: 'bg-muted text-muted-foreground border-border',
  MAINTENANCE_WINDOW: 'bg-warning-soft text-warning-text border-warning/30',
  OVERTIME_COOLING: 'bg-info-soft text-info-text border-info/30',
} as const;

function pct(minute: number): string {
  const clamped = Math.max(0, Math.min(1440, minute));
  return `${(clamped / 1440) * 100}%`;
}

function spanPct(startMinute: number, endMinute: number, minPct = 0): string {
  const raw = ((Math.max(0, endMinute - startMinute)) / 1440) * 100;
  return `${Math.max(minPct, raw)}%`;
}

export interface WeeklyScheduleGridProps {
  readonly schedule: OperatingSchedule;
  readonly week: EffectiveScheduleWeek;
  readonly tariff: SiteTariff | null;
  readonly deviations: readonly ScheduleDeviation[];
  readonly inspectedId: string | undefined;
  readonly todayDate: string;
  readonly currentMinuteOfDay: number | null;
  readonly hasEvaluatedHistory: boolean;
  readonly canManageSchedule: boolean;
  readonly onInspect: (id: string) => void;
  readonly onOpenEditor: () => void;
}

export function WeeklyScheduleGrid({
  schedule,
  week,
  tariff,
  deviations,
  inspectedId,
  todayDate,
  currentMinuteOfDay,
  hasEvaluatedHistory,
  canManageSchedule,
  onInspect,
  onOpenEditor,
}: WeeklyScheduleGridProps) {
  const isUnconfigured =
    schedule.revision === 0 &&
    schedule.slots.length === 0 &&
    !week.days.some((day) => day.approvedException !== null);

  const totalScheduledMinutes = week.days.reduce(
    (sum, day) =>
      sum + day.effectiveSlots.reduce((daySum, slot) => daySum + (slot.endMinute - slot.startMinute), 0),
    0,
  );
  const totalScheduledHours = (totalScheduledMinutes / 60).toFixed(1);
  const overriddenDaysCount = week.days.filter((day) => day.approvedException !== null).length;

  if (isUnconfigured) {
    return (
      <Panel>
        <PanelHeader
          title="周运行计划"
          caption={`${week.mondayDate} 至 ${week.sundayDate} · 站点时区 ${week.timezone}`}
        />
        <div className="flex flex-col items-center justify-center gap-3 px-6 py-14 text-center">
          <div className="flex size-11 items-center justify-center rounded-full bg-primary-soft text-primary">
            <CalendarClock className="size-5" aria-hidden="true" />
          </div>
          <div className="max-w-md space-y-1">
            <h3 className="text-base font-semibold text-foreground">尚未配置周运行计划</h3>
            <p className="text-sm text-muted-foreground">
              配置每周各日预期供冷时段、预冷提前窗与停机滞后窗后，平台将按 15 分钟历史遥测自动核查停机窗口内的计划外开机。目标供水温度仅作冷站值班交接参考，不会自动下发设备指令。
            </p>
          </div>
          {canManageSchedule ? (
            <Button size="sm" onClick={onOpenEditor} className="mt-1">
              <Plus data-icon="inline-start" />
              配置默认周计划
            </Button>
          ) : (
            <p className="text-xs text-subtle-foreground">
              当前账号为只读权限，请联系站点管理员配置周运行计划。
            </p>
          )}
        </div>
      </Panel>
    );
  }

  return (
    <Panel className="overflow-hidden">
      <PanelHeader
        title="周运行计划"
        caption={
          <span className="inline-flex flex-wrap items-center gap-x-3 gap-y-1 tabular-nums">
            <span>
              本周计划供冷 <strong className="font-semibold text-foreground">{totalScheduledHours}</strong> 小时
            </span>
            {overriddenDaysCount > 0 ? (
              <span className="text-info-text">含 {overriddenDaysCount} 天例外覆盖</span>
            ) : null}
            {deviations.length > 0 ? (
              <span className="inline-flex items-center gap-1 rounded-sm bg-warning-soft px-2 py-0.5 text-xs font-medium text-warning-text">
                <span aria-hidden="true">▲</span>
                发现 {deviations.length} 段计划外开机，点击色块核查
              </span>
            ) : hasEvaluatedHistory ? (
              <span className="text-xs text-subtle-foreground">已核查时段无计划外开机</span>
            ) : null}
          </span>
        }
        action={
          <div className="flex flex-wrap items-center gap-3 text-xs text-muted-foreground" aria-label="图例">
            <span className="inline-flex items-center gap-1.5">
              <span className="size-2.5 rounded-[2px] border border-primary/50 bg-primary-soft" aria-hidden="true" />
              常规供冷
            </span>
            <span className="inline-flex items-center gap-1.5">
              <span className="size-2.5 rounded-[2px] border border-info/50 bg-info-soft" aria-hidden="true" />
              例外供冷
            </span>
            <span className="inline-flex items-center gap-1.5">
              <span className="text-[10px] leading-none text-warning-text" aria-hidden="true">
                ▲
              </span>
              计划外开机
            </span>
            {tariff ? (
              <span className="hidden items-center gap-2 border-l border-divider pl-3 xl:inline-flex">
                <span className="text-subtle-foreground">分时底色：</span>
                {(
                  [
                    ['SUPER_PEAK', 'bg-destructive/20'],
                    ['PEAK', 'bg-warning/25'],
                    ['FLAT', 'bg-muted'],
                    ['VALLEY', 'bg-success/20'],
                  ] as const
                ).map(([code, swatch]) => (
                  <span key={code} className="inline-flex items-center gap-1">
                    <span className={cn('size-2 rounded-[2px]', swatch)} aria-hidden="true" />
                    {PERIOD_LABEL[code]}
                  </span>
                ))}
              </span>
            ) : null}
          </div>
        }
      />

      <div className="border-t border-divider">
        {/* Time Ruler Header */}
        <div className="flex border-b border-divider bg-muted/20">
          <div className="w-36 shrink-0 border-r border-divider px-3.5 py-2 text-xs font-medium text-subtle-foreground">
            星期 / 日期
          </div>
          <div className="relative h-8 min-w-0 flex-1 pr-3">
            <div className="relative h-full w-full">
              {HOUR_TICKS.map((hour) => {
                const leftPct = (hour / 24) * 100;
                const alignClass =
                  hour === 0
                    ? 'translate-x-0 pl-1'
                    : hour === 24
                      ? '-translate-x-full pr-0.5'
                      : '-translate-x-1/2';
                return (
                  <span
                    key={hour}
                    style={{ left: `${leftPct}%` }}
                    className={cn(
                      'absolute top-1/2 -translate-y-1/2 text-[11px] text-subtle-foreground tabular-nums',
                      alignClass,
                    )}
                  >
                    {String(hour).padStart(2, '0')}:00
                  </span>
                );
              })}
            </div>
          </div>
        </div>

        {/* 7 Day Rows */}
        <div className="divide-y divide-divider">
          {week.days.map((day) => (
            <DayRow
              key={day.date}
              day={day}
              week={week}
              tariff={tariff}
              deviations={deviations.filter((item) => item.date === day.date)}
              inspectedId={inspectedId}
              isToday={day.date === todayDate}
              currentMinuteOfDay={day.date === todayDate ? currentMinuteOfDay : null}
              onInspect={onInspect}
            />
          ))}
        </div>
      </div>
    </Panel>
  );
}

function DayRow({
  day,
  week,
  tariff,
  deviations,
  inspectedId,
  isToday,
  currentMinuteOfDay,
  onInspect,
}: {
  readonly day: EffectiveScheduleDay;
  readonly week: EffectiveScheduleWeek;
  readonly tariff: SiteTariff | null;
  readonly deviations: readonly ScheduleDeviation[];
  readonly inspectedId: string | undefined;
  readonly isToday: boolean;
  readonly currentMinuteOfDay: number | null;
  readonly onInspect: (id: string) => void;
}) {
  const dayPeriods = tariff
    ? daySchedule(tariff, new Date(day.startMs + 12 * 3_600_000), week.timezone)
    : [];
  const exc = day.approvedException;
  const isFullDayShutdown = exc !== null && day.effectiveSlots.length === 0;
  const shutdownInspectId = exc ? `shutdown:${exc.id}:${day.date}` : '';

  return (
    <div className={cn('flex items-stretch', isToday && 'bg-primary/[0.02]')}>
      {/* Left Day Column */}
      <div className="flex w-36 shrink-0 flex-col justify-center gap-1 border-r border-divider px-3.5 py-2.5">
        <div className="flex items-center gap-1.5">
          <span className="text-sm font-semibold text-foreground">{day.label}</span>
          {isToday ? (
            <span className="rounded-sm bg-primary px-1.5 py-0.2 text-[10px] font-medium text-primary-foreground">
              今日
            </span>
          ) : null}
        </div>
        <span className="text-xs text-muted-foreground tabular-nums">{day.date.slice(5)}</span>
        {exc ? (
          <span
            className={cn(
              'mt-0.5 inline-flex w-fit max-w-full truncate rounded-sm border px-1.5 py-0.5 text-[11px] font-medium',
              EXCEPTION_BADGE_STYLE[exc.kind],
            )}
            title={`${EXCEPTION_KIND_LABELS[exc.kind]}：${exc.title}`}
          >
            {EXCEPTION_KIND_LABELS[exc.kind]}
          </span>
        ) : null}
      </div>

      {/* Right 24h Track */}
      <div className="min-w-0 flex-1 pr-3">
        <div className="relative h-16 w-full">
          {/* Tariff Background Bands */}
          {dayPeriods.map((period) => (
            <div
              key={`${period.periodCode}-${period.startMinute}`}
              style={{
                left: pct(period.startMinute),
                width: spanPct(period.startMinute, period.endMinute),
              }}
              className={cn('pointer-events-none absolute inset-y-0', TARIFF_BAND_STYLE[period.periodCode])}
              aria-hidden="true"
            />
          ))}

          {/* 3-Hour Grid Lines */}
          {HOUR_TICKS.slice(1, -1).map((hour) => (
            <div
              key={hour}
              style={{ left: `${(hour / 24) * 100}%` }}
              className="pointer-events-none absolute inset-y-0 w-px bg-divider/60"
              aria-hidden="true"
            />
          ))}

          {/* Full-Day Shutdown Exception Band */}
          {isFullDayShutdown && exc ? (
            <button
              type="button"
              onClick={() => onInspect(shutdownInspectId)}
              aria-label={`${day.date} ${day.label} ${EXCEPTION_KIND_LABELS[exc.kind]}：${exc.title}（全天停机）`}
              className={cn(
                'absolute inset-x-1 top-2.5 bottom-2.5 z-10 flex items-center justify-center gap-2 rounded-md border border-dashed px-3 text-xs font-medium transition-colors focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none',
                inspectedId === shutdownInspectId
                  ? 'border-primary bg-muted text-foreground ring-1 ring-primary'
                  : 'border-border bg-muted/65 text-muted-foreground hover:border-foreground/30 hover:text-foreground',
              )}
            >
              <span>全天停机 · {exc.title}</span>
            </button>
          ) : null}

          {/* Pre-cool & Coast-down Grace Whiskers */}
          {day.effectiveSlots.map((slot) => {
            const preStart = Math.max(0, slot.startMinute - week.preCoolLeadMinutes);
            const lagEnd = Math.min(1440, slot.endMinute + week.coastDownLagMinutes);
            return (
              <div key={`grace:${slot.id}`} aria-hidden="true">
                {preStart < slot.startMinute ? (
                  <div
                    style={{ left: pct(preStart), width: spanPct(preStart, slot.startMinute) }}
                    className="pointer-events-none absolute top-3.5 bottom-3.5 rounded-l-sm border-y border-l border-dashed border-primary/25 bg-primary/[0.03]"
                  />
                ) : null}
                {lagEnd > slot.endMinute ? (
                  <div
                    style={{ left: pct(slot.endMinute), width: spanPct(slot.endMinute, lagEnd) }}
                    className="pointer-events-none absolute top-3.5 bottom-3.5 rounded-r-sm border-y border-r border-dashed border-primary/25 bg-primary/[0.03]"
                  />
                ) : null}
              </div>
            );
          })}

          {/* Effective Cooling Slots */}
          {day.effectiveSlots.map((slot) => (
            <CoolingSlotButton
              key={slot.id}
              slot={slot}
              dayLabel={day.label}
              selected={inspectedId === slot.id}
              onInspect={onInspect}
            />
          ))}

          {/* Unscheduled Running Deviations */}
          {deviations.map((dev) => {
            const selected = inspectedId === dev.id;
            const timeText = `${formatMinuteOfDay(dev.startMinute)}–${formatMinuteOfDay(dev.endMinute)}`;
            const wideEnoughForText = dev.endMinute - dev.startMinute >= 90;
            return (
              <button
                key={dev.id}
                type="button"
                onClick={() => onInspect(dev.id)}
                style={{
                  left: pct(dev.startMinute),
                  width: spanPct(dev.startMinute, dev.endMinute, 1.8),
                }}
                title={`计划外开机 ${timeText} · ${dev.runningDeviceNames.join('、')}`}
                aria-label={`计划外开机：${day.date} ${day.label} ${timeText}，运行设备 ${dev.runningDeviceNames.join('、')}`}
                className={cn(
                  'absolute top-2 bottom-2 z-20 flex items-center justify-center gap-1 overflow-hidden rounded-md border px-1.5 text-[11px] font-semibold shadow-xs transition-all focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none',
                  selected
                    ? 'border-warning bg-warning/25 text-warning-text ring-2 ring-warning'
                    : 'border-warning/80 bg-warning-soft text-warning-text hover:brightness-95',
                )}
              >
                <span aria-hidden="true" className="shrink-0 text-[10px] leading-none">
                  ▲
                </span>
                {wideEnoughForText ? (
                  <span className="truncate tabular-nums">计划外 {timeText}</span>
                ) : null}
              </button>
            );
          })}

          {/* Current Site-Local Time Marker */}
          {currentMinuteOfDay !== null && currentMinuteOfDay >= 0 && currentMinuteOfDay <= 1440 ? (
            <div
              style={{ left: pct(currentMinuteOfDay) }}
              className="pointer-events-none absolute inset-y-0 z-30 w-0.5 bg-primary"
              aria-hidden="true"
            >
              <span className="absolute -top-1 -left-1 size-2 rounded-full bg-primary" />
            </div>
          ) : null}
        </div>
      </div>
    </div>
  );
}

function CoolingSlotButton({
  slot,
  dayLabel,
  selected,
  onInspect,
}: {
  readonly slot: EffectiveCoolingSlot;
  readonly dayLabel: string;
  readonly selected: boolean;
  readonly onInspect: (id: string) => void;
}) {
  const durationMinutes = slot.endMinute - slot.startMinute;
  const rangeLabel = `${formatMinuteOfDay(slot.startMinute)}–${formatMinuteOfDay(slot.endMinute)}`;
  const isCrossMidnightPart =
    slot.spanGroupId !== null && (slot.endMinute === 1440 || slot.startMinute === 0);
  const sourceLabel = slot.source === 'EXCEPTION' ? `例外供冷（${slot.exceptionTitle ?? ''}）` : '常规供冷';
  const tempLabel = slot.targetSupplyTempC !== null ? `${slot.targetSupplyTempC.toFixed(1)} °C` : null;

  return (
    <button
      type="button"
      onClick={() => onInspect(slot.id)}
      style={{
        left: pct(slot.startMinute),
        width: spanPct(slot.startMinute, slot.endMinute, 1.5),
      }}
      title={`${sourceLabel} ${rangeLabel}${tempLabel ? ` · 交接参考供水 ${tempLabel}` : ''}`}
      aria-label={`${slot.date} ${dayLabel} ${sourceLabel} ${rangeLabel}${tempLabel ? `，参考冷冻水供水温度 ${tempLabel}` : ''}`}
      className={cn(
        'absolute top-2 bottom-2 z-10 flex items-center justify-between gap-1.5 overflow-hidden rounded-md border px-2 text-left text-xs transition-all focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none',
        slot.source === 'EXCEPTION'
          ? selected
            ? 'border-info bg-info/20 text-foreground ring-2 ring-info/60'
            : 'border-info/55 bg-info-soft text-foreground hover:border-info'
          : selected
            ? 'border-primary bg-primary/20 text-foreground ring-2 ring-primary/60'
            : 'border-primary/45 bg-primary-soft text-foreground hover:border-primary',
      )}
    >
      {durationMinutes >= 90 ? (
        <span className="truncate font-medium tabular-nums">
          {rangeLabel}
          {isCrossMidnightPart ? (
            <span className="ml-1 rounded-xs bg-background/70 px-1 py-0.2 text-[10px] font-normal text-muted-foreground">
              跨日
            </span>
          ) : null}
        </span>
      ) : null}
      {durationMinutes >= 210 && tempLabel ? (
        <span className="shrink-0 rounded-xs bg-background/80 px-1.5 py-0.5 text-[11px] font-medium text-muted-foreground tabular-nums">
          {tempLabel}
        </span>
      ) : null}
    </button>
  );
}
