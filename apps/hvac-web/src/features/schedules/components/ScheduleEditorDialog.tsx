import { useEffect, useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Copy, Plus, RefreshCw, Trash2 } from 'lucide-react';
import { toast } from 'sonner';
import type { OperatingSchedule, OperatingScheduleSlotInput } from '@/api/generated/platformGateway.gen';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import { Textarea } from '@/components/ui/textarea';
import { isRevisionConflictError, saveOperatingSchedule, scheduleErrorMessage, scheduleKeys } from '../query';
import {
  createClientUuidV7,
  DAY_OF_WEEK_LABELS,
  formatMinuteOfDay,
  mergeWeeklyScheduleSpans,
  parseTimeInputToMinute,
  splitScheduleSlotDraft,
} from '../schedule-model';

interface SpanFormRow {
  readonly key: string;
  readonly dayOfWeek: number;
  readonly startTime: string;
  readonly endTime: string;
  readonly crossesMidnight: boolean;
  readonly targetSupplyTempInput: string;
  readonly spanGroupId?: string;
}

function toFormRows(schedule: OperatingSchedule): SpanFormRow[] {
  const merged = mergeWeeklyScheduleSpans(schedule.slots);
  return merged.map((span) => ({
    key: span.key,
    dayOfWeek: span.dayOfWeek,
    startTime: formatMinuteOfDay(span.startMinute),
    endTime: formatMinuteOfDay(span.endMinute),
    crossesMidnight: span.crossesMidnight,
    targetSupplyTempInput:
      span.targetSupplyTempC !== undefined ? span.targetSupplyTempC.toFixed(1) : '',
    ...(span.spanGroupId ? { spanGroupId: span.spanGroupId } : {}),
  }));
}

let rowCounter = 1;
function nextRowKey(): string {
  rowCounter += 1;
  return `draft-row-${rowCounter}`;
}

export interface ScheduleEditorDialogProps {
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
  readonly siteId: string;
  readonly csrfToken: string;
  readonly schedule: OperatingSchedule;
  readonly onReloadLatest: () => Promise<OperatingSchedule | undefined>;
}

export function ScheduleEditorDialog({
  open,
  onOpenChange,
  siteId,
  csrfToken,
  schedule,
  onReloadLatest,
}: ScheduleEditorDialogProps) {
  const queryClient = useQueryClient();
  const [name, setName] = useState(schedule.name || '标准供冷周计划');
  const [preCoolInput, setPreCoolInput] = useState(String(schedule.preCoolLeadMinutes));
  const [coastDownInput, setCoastDownInput] = useState(String(schedule.coastDownLagMinutes));
  const [rows, setRows] = useState<SpanFormRow[]>(() => toFormRows(schedule));
  const [reason, setReason] = useState('');
  const [validationError, setValidationError] = useState<string | null>(null);
  const [conflictDetected, setConflictDetected] = useState(false);

  useEffect(() => {
    if (!open) return;
    setName(schedule.name || '标准供冷周计划');
    setPreCoolInput(String(schedule.preCoolLeadMinutes));
    setCoastDownInput(String(schedule.coastDownLagMinutes));
    setRows(toFormRows(schedule));
    setReason('');
    setValidationError(null);
    setConflictDetected(false);
  }, [open, schedule]);

  const mutation = useMutation({
    mutationFn: (slots: OperatingScheduleSlotInput[]) =>
      saveOperatingSchedule(
        siteId,
        {
          name: name.trim(),
          preCoolLeadMinutes: Number(preCoolInput),
          coastDownLagMinutes: Number(coastDownInput),
          slots,
          meta: {
            expectedRevision: schedule.revision,
            idempotencyKey: `sched-save-${createClientUuidV7()}`,
            reason: reason.trim(),
          },
        },
        csrfToken,
      ),
    onSuccess: (saved) => {
      queryClient.setQueryData(scheduleKeys.schedule(siteId), {
        data: saved,
        requestId: null,
        traceparent: null,
        auditMessageId: null,
        routePolicyRevision: null,
        location: null,
      });
      void queryClient.invalidateQueries({ queryKey: scheduleKeys.schedule(siteId) });
      toast.success('已保存周运行计划');
      onOpenChange(false);
    },
    onError: (error) => {
      if (isRevisionConflictError(error)) {
        setConflictDetected(true);
      }
      setValidationError(scheduleErrorMessage(error, '保存周运行计划失败'));
    },
  });

  const handleAddRow = () => {
    setRows((prev) => [
      ...prev,
      {
        key: nextRowKey(),
        dayOfWeek: 1,
        startTime: '08:00',
        endTime: '21:00',
        crossesMidnight: false,
        targetSupplyTempInput: '7.0',
      },
    ]);
  };

  const handleFillWorkdayPreset = () => {
    setRows(
      [1, 2, 3, 4, 5].map((dayOfWeek) => ({
        key: nextRowKey(),
        dayOfWeek,
        startTime: '08:00',
        endTime: '21:00',
        crossesMidnight: false,
        targetSupplyTempInput: '7.0',
      })),
    );
    setValidationError(null);
  };

  const handleCopyToWorkdays = (sourceRow: SpanFormRow) => {
    setRows((prev) => {
      const keptWeekend = prev.filter((item) => item.dayOfWeek >= 6);
      const copied = [1, 2, 3, 4, 5].map((dayOfWeek) => ({
        key: nextRowKey(),
        dayOfWeek,
        startTime: sourceRow.startTime,
        endTime: sourceRow.endTime,
        crossesMidnight: sourceRow.crossesMidnight,
        targetSupplyTempInput: sourceRow.targetSupplyTempInput,
      }));
      return [...copied, ...keptWeekend];
    });
    toast.info('已将该供冷时段复制到周一至周五');
  };

  const updateRow = (key: string, patch: Partial<SpanFormRow>) => {
    setRows((prev) => prev.map((row) => (row.key === key ? { ...row, ...patch } : row)));
  };

  const removeRow = (key: string) => {
    setRows((prev) => prev.filter((row) => row.key !== key));
  };

  const handleReloadConflict = async () => {
    const latest = await onReloadLatest();
    if (latest) {
      setName(latest.name || '标准供冷周计划');
      setPreCoolInput(String(latest.preCoolLeadMinutes));
      setCoastDownInput(String(latest.coastDownLagMinutes));
      setRows(toFormRows(latest));
      setConflictDetected(false);
      setValidationError(null);
      toast.info('已加载最新周运行计划，请核对后重新保存');
    }
  };

  const handleSubmit = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setValidationError(null);

    const trimmedName = name.trim();
    if (!trimmedName || trimmedName.length > 100) {
      setValidationError('请填写 1–100 字以内的周运行计划名称');
      return;
    }

    const preCool = Number(preCoolInput);
    if (!Number.isInteger(preCool) || preCool < 0 || preCool > 120) {
      setValidationError('预冷提前时间须为 0–120 之间的整数分钟');
      return;
    }

    const coastDown = Number(coastDownInput);
    if (!Number.isInteger(coastDown) || coastDown < 0 || coastDown > 60) {
      setValidationError('停机余冷滞后时间须为 0–60 之间的整数分钟');
      return;
    }

    const trimmedReason = reason.trim();
    if (!trimmedReason || trimmedReason.length > 500) {
      setValidationError('请填写本次调整原因（最多 500 字），以便写入运行审计记录');
      return;
    }

    const compiledSlots: OperatingScheduleSlotInput[] = [];
    for (let idx = 0; idx < rows.length; idx += 1) {
      const row = rows[idx];
      const dayLabel = DAY_OF_WEEK_LABELS[row.dayOfWeek] ?? `第 ${idx + 1} 行`;
      const startMinute = parseTimeInputToMinute(row.startTime, false);
      const endMinute = parseTimeInputToMinute(row.endTime, !row.crossesMidnight);

      if (startMinute === null || endMinute === null) {
        setValidationError(`${dayLabel} 的起止时间格式无效，请使用 HH:MM（结束时间可填 24:00）`);
        return;
      }

      if (!row.crossesMidnight && startMinute >= endMinute) {
        setValidationError(`${dayLabel} 的结束时间须晚于开始时间；若需跨午夜运行至次日凌晨，请开启「跨日」`);
        return;
      }
      if (row.crossesMidnight && (endMinute <= 0 || endMinute >= 1440)) {
        setValidationError(`${dayLabel} 开启跨日后，次日结束时间须在 00:01 至 23:59 之间`);
        return;
      }

      let targetTemp: number | undefined;
      if (row.targetSupplyTempInput.trim() !== '') {
        const parsedTemp = Number(row.targetSupplyTempInput.trim());
        if (!Number.isFinite(parsedTemp) || parsedTemp < 4 || parsedTemp > 20) {
          setValidationError(`${dayLabel} 的交接参考供水温度须在 4.0–20.0 °C 之间`);
          return;
        }
        targetTemp = Number(parsedTemp.toFixed(1));
      }

      compiledSlots.push(
        ...splitScheduleSlotDraft({
          dayOfWeek: row.dayOfWeek,
          startMinute,
          endMinute,
          crossesMidnight: row.crossesMidnight,
          ...(targetTemp !== undefined ? { targetSupplyTempC: targetTemp } : {}),
          ...(row.spanGroupId ? { spanGroupId: row.spanGroupId } : {}),
        }),
      );
    }

    // Validate intra-day non-overlap across 1..7 (including cross-midnight spillovers).
    for (let day = 1; day <= 7; day += 1) {
      const daySlots = compiledSlots
        .filter((slot) => slot.dayOfWeek === day)
        .sort((a, b) => a.startMinute - b.startMinute);
      for (let i = 1; i < daySlots.length; i += 1) {
        if (daySlots[i].startMinute < daySlots[i - 1].endMinute) {
          setValidationError(
            `${DAY_OF_WEEK_LABELS[day]} 的供冷时段存在重叠（${formatMinuteOfDay(daySlots[i - 1].startMinute)}–${formatMinuteOfDay(daySlots[i - 1].endMinute)} 与 ${formatMinuteOfDay(daySlots[i].startMinute)}–${formatMinuteOfDay(daySlots[i].endMinute)}），请调整后再保存`,
          );
          return;
        }
      }
    }

    mutation.mutate(compiledSlots);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>{schedule.revision === 0 ? '配置默认周运行计划' : '编辑周运行计划'}</DialogTitle>
          <DialogDescription>
            按站点时区（{schedule.timezone}）设定每周各日预期供冷窗口。目标冷冻水供水温度仅作冷站值班交接参考，平台不会据此自动下发设备指令。
          </DialogDescription>
        </DialogHeader>

        <form className="space-y-4" onSubmit={handleSubmit}>
          {conflictDetected ? (
            <Alert variant="destructive">
              <AlertTitle>检测到版本冲突</AlertTitle>
              <AlertDescription className="flex flex-wrap items-center justify-between gap-2">
                <span>其他成员刚刚更新了本站点的周运行计划。</span>
                <Button type="button" size="sm" variant="outline" onClick={() => void handleReloadConflict()}>
                  <RefreshCw data-icon="inline-start" />
                  刷新最新计划
                </Button>
              </AlertDescription>
            </Alert>
          ) : null}

          <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
            <div className="space-y-1.5 sm:col-span-1">
              <Label htmlFor="schedule-name">计划名称</Label>
              <Input
                id="schedule-name"
                value={name}
                maxLength={100}
                onChange={(event) => setName(event.target.value)}
                placeholder="例如：标准供冷周计划"
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="schedule-precool">预冷提前窗（0–120 分钟）</Label>
              <Input
                id="schedule-precool"
                type="number"
                min={0}
                max={120}
                step={5}
                value={preCoolInput}
                onChange={(event) => setPreCoolInput(event.target.value)}
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="schedule-coastdown">停机滞后窗（0–60 分钟）</Label>
              <Input
                id="schedule-coastdown"
                type="number"
                min={0}
                max={60}
                step={5}
                value={coastDownInput}
                onChange={(event) => setCoastDownInput(event.target.value)}
              />
            </div>
          </div>

          <div className="space-y-2">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div>
                <Label>供冷时段配置</Label>
                <p className="text-xs text-muted-foreground">
                  未添加供冷时段的星期默认全天停机；开启「跨日」后时段将延续至次日凌晨并在保存时自动拆分为两段。
                </p>
              </div>
              <div className="flex items-center gap-2">
                {rows.length === 0 ? (
                  <Button type="button" variant="outline" size="sm" onClick={handleFillWorkdayPreset}>
                    填充工作日 08:00–21:00
                  </Button>
                ) : null}
                <Button type="button" variant="outline" size="sm" onClick={handleAddRow}>
                  <Plus data-icon="inline-start" />
                  添加供冷时段
                </Button>
              </div>
            </div>

            {rows.length === 0 ? (
              <div className="rounded-md border border-dashed border-border px-4 py-6 text-center text-sm text-muted-foreground">
                当前未配置任何供冷时段（全周预期停机）。点击上方按钮添加供冷时段。
              </div>
            ) : (
              <div className="space-y-2 rounded-md border border-border p-3">
                {rows.map((row, index) => (
                  <div
                    key={row.key}
                    className="grid grid-cols-1 items-center gap-2 border-b border-divider pb-2 last:border-b-0 last:pb-0 sm:grid-cols-[104px_108px_108px_92px_132px_1fr]"
                  >
                    <Select
                      value={String(row.dayOfWeek)}
                      onValueChange={(val) => updateRow(row.key, { dayOfWeek: Number(val) })}
                    >
                      <SelectTrigger aria-label={`第 ${index + 1} 行星期`}>
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {[1, 2, 3, 4, 5, 6, 7].map((day) => (
                          <SelectItem key={day} value={String(day)}>
                            {DAY_OF_WEEK_LABELS[day]}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>

                    <Input
                      aria-label={`第 ${index + 1} 行开始时间`}
                      value={row.startTime}
                      onChange={(event) => updateRow(row.key, { startTime: event.target.value })}
                      placeholder="08:00"
                      className="tabular-nums"
                    />

                    <Input
                      aria-label={`第 ${index + 1} 行结束时间`}
                      value={row.endTime}
                      onChange={(event) => updateRow(row.key, { endTime: event.target.value })}
                      placeholder={row.crossesMidnight ? '02:00' : '21:00'}
                      className="tabular-nums"
                    />

                    <label className="inline-flex cursor-pointer items-center gap-1.5 text-xs text-muted-foreground">
                      <Switch
                        checked={row.crossesMidnight}
                        onCheckedChange={(checked) => updateRow(row.key, { crossesMidnight: checked })}
                        aria-label={`第 ${index + 1} 行跨日`}
                      />
                      <span>跨日</span>
                    </label>

                    <Input
                      aria-label={`第 ${index + 1} 行参考供水温度`}
                      type="number"
                      min={4}
                      max={20}
                      step={0.1}
                      value={row.targetSupplyTempInput}
                      onChange={(event) =>
                        updateRow(row.key, { targetSupplyTempInput: event.target.value })
                      }
                      placeholder="参考 °C"
                      className="tabular-nums"
                    />

                    <div className="flex items-center justify-end gap-1">
                      <Button
                        type="button"
                        variant="ghost"
                        size="sm"
                        title="将该时段复制到周一至周五"
                        onClick={() => handleCopyToWorkdays(row)}
                      >
                        <Copy className="size-3.5" aria-hidden="true" />
                        <span className="sr-only sm:not-sr-only sm:ml-1 sm:text-xs">工作日</span>
                      </Button>
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon-sm"
                        aria-label={`删除第 ${index + 1} 行时段`}
                        onClick={() => removeRow(row.key)}
                      >
                        <Trash2 className="size-4 text-muted-foreground hover:text-destructive" />
                      </Button>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="schedule-reason">变更原因</Label>
            <Textarea
              id="schedule-reason"
              rows={2}
              maxLength={500}
              value={reason}
              onChange={(event) => setReason(event.target.value)}
              placeholder="例如：按园区夏季作息将工作日供冷时间定为 08:00–21:00"
            />
          </div>

          {validationError ? <p className="text-sm text-destructive">{validationError}</p> : null}

          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              取消
            </Button>
            <Button type="submit" disabled={mutation.isPending}>
              {mutation.isPending ? '正在保存…' : '保存周计划'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
