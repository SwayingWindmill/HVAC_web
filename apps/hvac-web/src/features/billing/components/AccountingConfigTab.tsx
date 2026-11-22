import { useEffect, useMemo, useState, type FormEvent, type KeyboardEvent } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { CalendarDays, Pencil, Plus, SlidersHorizontal, Trash2 } from 'lucide-react';
import { toast } from 'sonner';
import { uuidV7Schema } from '@/api/generated/platformGateway.gen';
import { registryAssetModelQuery } from '@/api/registry';
import { Panel, PanelHeader } from '@/blocks/panel';
import { StateCard } from '@/blocks/state/state-card';
import { InfoTip } from '@/components/info-tip';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Field, FieldDescription, FieldError, FieldLabel } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { resolveTelemetryPointLabel } from '@/domain/centralPlantTelemetry';
import { siteDayKey, wallTime } from '@/features/energy-analysis/consumption/model';
import { siteInputValue } from '@/features/savings/savings-model';
import { formatTime } from '@/lib/operator-format';
import { isOneOf } from '@/lib/typed';
import {
  canRetireRatioVersion,
  deriveRatioLifecycleStatus,
  formatWeekdayShort,
  isValidDateKey,
  parseSiteMomentInput,
  previewCloudMultiplier,
  RATIO_APPLICATION_MODES,
  RATIO_APPLICATION_MODE_LABEL,
  RATIO_APPLICATION_MODE_SHORT_LABEL,
  RATIO_READING_UNITS,
  RATIO_READING_UNIT_LABEL,
  SITE_CALENDAR_DAY_TYPES,
  SITE_CALENDAR_DAY_TYPE_DESCRIPTION,
  SITE_CALENDAR_DAY_TYPE_LABEL,
  SITE_CALENDAR_DAY_TYPE_VARIANT,
  validateRatioForm,
} from '../accounting-config-model';
import {
  billingKeys,
  createSiteMeterRatioVersion,
  deleteSiteAccountingHoliday,
  describeAccountingConfigError,
  retireSiteMeterRatioVersion,
  siteAccountingHolidaysQuery,
  siteMeterRatioVersionsQuery,
  upsertSiteAccountingHoliday,
  type MeterRatioVersion,
  type SiteCalendarHoliday,
} from '../query';
import type {
  RatioApplicationMode,
  RatioReadingUnit,
  SiteCalendarDayType,
} from '@/api/accounting-config';

export interface AccountingConfigTabProps {
  readonly siteId: string;
  readonly timezone: string;
  readonly csrfToken: string;
  readonly canManageBilling: boolean;
  readonly now: Date;
}

interface TelemetryPointOption {
  readonly id: string;
  readonly label: string;
  readonly sublabel: string;
  readonly isCounter: boolean;
}

function formatMultiplier(value: number): string {
  if (!Number.isFinite(value)) return '—';
  return value.toLocaleString('zh-CN', { maximumFractionDigits: 6 });
}

function preventImeSubmit(event: KeyboardEvent<HTMLInputElement>) {
  if (event.key === 'Enter' && (event.nativeEvent.isComposing || event.keyCode === 229)) {
    event.preventDefault();
  }
}

export function AccountingConfigTab({
  siteId,
  timezone,
  csrfToken,
  canManageBilling,
  now,
}: AccountingConfigTabProps) {
  const queryClient = useQueryClient();
  const currentYear = useMemo(() => wallTime(now, timezone).year, [now, timezone]);
  const [selectedYear, setSelectedYear] = useState<number>(() => currentYear);
  const [includeSuperseded, setIncludeSuperseded] = useState(false);
  const [configNotice, setConfigNotice] = useState<string | null>(null);

  // Holiday dialog state
  const [holidayDialogOpen, setHolidayDialogOpen] = useState(false);
  const [holidayEditTarget, setHolidayEditTarget] = useState<SiteCalendarHoliday | null>(null);
  const [holidayDateInput, setHolidayDateInput] = useState('');
  const [holidayDayType, setHolidayDayType] = useState<SiteCalendarDayType>('HOLIDAY');
  const [holidayLabelInput, setHolidayLabelInput] = useState('');
  const [holidayFormError, setHolidayFormError] = useState<string | null>(null);
  const [holidaySubmitError, setHolidaySubmitError] = useState<string | null>(null);

  // Holiday delete dialog state
  const [deletingHoliday, setDeletingHoliday] = useState<SiteCalendarHoliday | null>(null);
  const [holidayDeleteError, setHolidayDeleteError] = useState<string | null>(null);

  // Create ratio dialog state
  const [createRatioOpen, setCreateRatioOpen] = useState(false);
  const [ratioPointId, setRatioPointId] = useState('');
  const [manualPointMode, setManualPointMode] = useState(false);
  const [ctRatioInput, setCtRatioInput] = useState('1');
  const [ptRatioInput, setPtRatioInput] = useState('1');
  const [meterMultiplierInput, setMeterMultiplierInput] = useState('1');
  const [ratioApplicationMode, setRatioApplicationMode] =
    useState<RatioApplicationMode>('PLATFORM_APPLIED');
  const [readingUnit, setReadingUnit] = useState<RatioReadingUnit>('kWh');
  const [effectiveFromInput, setEffectiveFromInput] = useState('');
  const [effectiveToInput, setEffectiveToInput] = useState('');
  const [ratioReasonInput, setRatioReasonInput] = useState('');
  const [ratioFieldError, setRatioFieldError] = useState<{
    readonly field: string;
    readonly message: string;
  } | null>(null);
  const [ratioSubmitError, setRatioSubmitError] = useState<string | null>(null);

  // Retire ratio dialog state
  const [retiringRatio, setRetiringRatio] = useState<MeterRatioVersion | null>(null);
  const [retireEffectiveToInput, setRetireEffectiveToInput] = useState('');
  const [retireReasonInput, setRetireReasonInput] = useState('');
  const [retireFieldError, setRetireFieldError] = useState<string | null>(null);
  const [retireSubmitError, setRetireSubmitError] = useState<string | null>(null);

  const yearOptions = useMemo(() => {
    const years = new Set([currentYear - 1, currentYear, currentYear + 1, selectedYear]);
    return [...years].sort((a, b) => b - a);
  }, [currentYear, selectedYear]);

  const holidaysQuery = useQuery(siteAccountingHolidaysQuery(siteId, selectedYear));
  const ratiosQuery = useQuery(siteMeterRatioVersionsQuery(siteId, { includeSuperseded }));
  const assetModelQuery = useQuery({
    ...registryAssetModelQuery(siteId, uuidV7Schema.safeParse(siteId).success),
    retry: false,
  });

  const { pointOptions, pointById } = useMemo<{
    readonly pointOptions: readonly TelemetryPointOption[];
    readonly pointById: ReadonlyMap<string, TelemetryPointOption>;
  }>(() => {
    const model = assetModelQuery.data;
    if (!model) {
      return {
        pointOptions: [],
        pointById: new Map(),
      };
    }
    const deviceNames = new Map(model.devices.map((device) => [device.id, device.displayName]));
    const options: TelemetryPointOption[] = model.telemetryPoints
      .filter((point) => point.status === 'ACTIVE')
      .map((point) => {
        const deviceName = deviceNames.get(point.reportingDeviceId) ?? '';
        const pointLabel = resolveTelemetryPointLabel(point);
        return {
          id: point.id,
          label: deviceName ? `${deviceName} · ${pointLabel}` : pointLabel,
          sublabel: point.unit ? `${point.pointCode} · ${point.unit}` : point.pointCode,
          isCounter: point.pointType === 'COUNTER',
        };
      })
      .sort((left, right) => {
        if (left.isCounter !== right.isCounter) return left.isCounter ? -1 : 1;
        return left.label.localeCompare(right.label, 'zh-CN');
      });

    return {
      pointOptions: options,
      pointById: new Map(options.map((option) => [option.id, option])),
    };
  }, [assetModelQuery.data]);

  const invalidateAfterHolidayMutation = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: billingKeys.holidaysRoot(siteId) }),
      queryClient.invalidateQueries({ queryKey: billingKeys.recalcJobs(siteId) }),
      queryClient.invalidateQueries({ queryKey: ['tariff', siteId] }),
      queryClient.invalidateQueries({ queryKey: billingKeys.monthElectricityRoot(siteId) }),
      queryClient.invalidateQueries({ queryKey: billingKeys.readingWindowElectricityRoot(siteId) }),
      queryClient.invalidateQueries({ queryKey: ['energy-series', siteId] }),
    ]);
  };

  const invalidateAfterRatioMutation = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: billingKeys.meterRatiosRoot(siteId) }),
      queryClient.invalidateQueries({ queryKey: billingKeys.recalcJobs(siteId) }),
      queryClient.invalidateQueries({ queryKey: billingKeys.monthElectricityRoot(siteId) }),
      queryClient.invalidateQueries({ queryKey: billingKeys.readingWindowElectricityRoot(siteId) }),
      queryClient.invalidateQueries({ queryKey: ['energy-series', siteId] }),
    ]);
  };

  const upsertHolidayMutation = useMutation({
    mutationFn: (payload: {
      readonly date: string;
      readonly dayType: SiteCalendarDayType;
      readonly label: string;
    }) => upsertSiteAccountingHoliday(siteId, csrfToken, payload),
    onSuccess: async (saved) => {
      await invalidateAfterHolidayMutation();
      const message = saved.recalcJobId
        ? `已保存 ${saved.holidayDate} 日历覆盖，后台重算任务已加入队列`
        : `已保存 ${saved.holidayDate} 日历覆盖`;
      setConfigNotice(message);
      toast.success(message);
      setHolidayDialogOpen(false);
    },
    onError: (error) => {
      setHolidaySubmitError(
        describeAccountingConfigError(error, '保存站点节假日配置失败，请稍后重试'),
      );
    },
  });

  const deleteHolidayMutation = useMutation({
    mutationFn: (date: string) => deleteSiteAccountingHoliday(siteId, csrfToken, date),
    onSuccess: async (result) => {
      await invalidateAfterHolidayMutation();
      const message = result.recalcJobId
        ? `已删除 ${result.holidayDate} 日历覆盖，后台重算任务已加入队列`
        : `已删除 ${result.holidayDate} 日历覆盖`;
      setConfigNotice(message);
      toast.success(message);
      setDeletingHoliday(null);
    },
    onError: (error) => {
      setHolidayDeleteError(
        describeAccountingConfigError(error, '删除站点节假日配置失败，请稍后重试'),
      );
    },
  });

  const createRatioMutation = useMutation({
    mutationFn: (payload: {
      readonly pointId: string;
      readonly ctRatio: number;
      readonly ptRatio: number;
      readonly meterMultiplier: number;
      readonly ratioApplicationMode: RatioApplicationMode;
      readonly readingUnit: RatioReadingUnit;
      readonly effectiveFrom: string;
      readonly effectiveTo?: string;
      readonly reason?: string;
    }) => createSiteMeterRatioVersion(siteId, csrfToken, payload),
    onSuccess: async (created) => {
      await invalidateAfterRatioMutation();
      const message = created.recalcJobId
        ? '已创建电表倍率版本，后台重算任务已加入队列'
        : '已创建电表倍率版本';
      setConfigNotice(message);
      toast.success(message);
      setCreateRatioOpen(false);
    },
    onError: (error) => {
      setRatioSubmitError(
        describeAccountingConfigError(error, '创建电表倍率版本失败，请稍后重试'),
      );
    },
  });

  const retireRatioMutation = useMutation({
    mutationFn: (payload: {
      readonly versionId: string;
      readonly effectiveTo: string;
      readonly reason?: string;
    }) =>
      retireSiteMeterRatioVersion(siteId, csrfToken, payload.versionId, {
        effectiveTo: payload.effectiveTo,
        ...(payload.reason ? { reason: payload.reason } : {}),
      }),
    onSuccess: async (updated) => {
      await invalidateAfterRatioMutation();
      const message = updated.recalcJobId
        ? '已终止该电表倍率版本，后台重算任务已加入队列'
        : '已终止该电表倍率版本';
      setConfigNotice(message);
      toast.success(message);
      setRetiringRatio(null);
    },
    onError: (error) => {
      setRetireSubmitError(
        describeAccountingConfigError(error, '终止电表倍率版本失败，请稍后重试'),
      );
    },
  });

  const openCreateHolidayDialog = () => {
    const todayKey = siteDayKey(now, timezone);
    const defaultDate = todayKey.startsWith(`${selectedYear}-`) ? todayKey : `${selectedYear}-01-01`;
    setHolidayEditTarget(null);
    setHolidayDateInput(defaultDate);
    setHolidayDayType('HOLIDAY');
    setHolidayLabelInput('');
    setHolidayFormError(null);
    setHolidaySubmitError(null);
    setHolidayDialogOpen(true);
  };

  const openEditHolidayDialog = (holiday: SiteCalendarHoliday) => {
    setHolidayEditTarget(holiday);
    setHolidayDateInput(holiday.holidayDate);
    setHolidayDayType(holiday.dayType);
    setHolidayLabelInput(holiday.label);
    setHolidayFormError(null);
    setHolidaySubmitError(null);
    setHolidayDialogOpen(true);
  };

  const handleHolidaySubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setHolidayFormError(null);
    setHolidaySubmitError(null);

    const trimmedDate = holidayDateInput.trim();
    if (!isValidDateKey(trimmedDate)) {
      setHolidayFormError('请输入有效的日历日期（YYYY-MM-DD）');
      return;
    }

    const trimmedLabel = holidayLabelInput.trim();
    if (trimmedLabel.length > 120) {
      setHolidayFormError('说明不能超过 120 字');
      return;
    }

    upsertHolidayMutation.mutate({
      date: trimmedDate,
      dayType: holidayDayType,
      label: trimmedLabel,
    });
  };

  const openCreateRatioDialog = () => {
    const defaultPointId = pointOptions[0]?.id ?? '';
    setManualPointMode(pointOptions.length === 0);
    setRatioPointId(defaultPointId);
    setCtRatioInput('1');
    setPtRatioInput('1');
    setMeterMultiplierInput('1');
    setRatioApplicationMode('PLATFORM_APPLIED');
    setReadingUnit('kWh');
    setEffectiveFromInput(siteInputValue(now, timezone));
    setEffectiveToInput('');
    setRatioReasonInput('');
    setRatioFieldError(null);
    setRatioSubmitError(null);
    setCreateRatioOpen(true);
  };

  // Sync default point selection when asset model finishes loading after dialog opens.
  useEffect(() => {
    if (createRatioOpen && !manualPointMode && !ratioPointId && pointOptions.length > 0) {
      setRatioPointId(pointOptions[0]?.id ?? '');
    }
  }, [createRatioOpen, manualPointMode, ratioPointId, pointOptions]);

  const liveCloudMultiplier = useMemo(
    () =>
      previewCloudMultiplier({
        ctRatio: Number(ctRatioInput),
        ptRatio: Number(ptRatioInput),
        meterMultiplier: meterMultiplierInput.trim() === '' ? 1 : Number(meterMultiplierInput),
        ratioApplicationMode,
      }),
    [ctRatioInput, ptRatioInput, meterMultiplierInput, ratioApplicationMode],
  );

  const handleCreateRatioSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setRatioFieldError(null);
    setRatioSubmitError(null);

    const validated = validateRatioForm(
      {
        pointId: ratioPointId,
        ctRatio: ctRatioInput,
        ptRatio: ptRatioInput,
        meterMultiplier: meterMultiplierInput,
        ratioApplicationMode,
        readingUnit,
        effectiveFrom: effectiveFromInput,
        effectiveTo: effectiveToInput,
        reason: ratioReasonInput,
      },
      timezone,
    );

    if (!validated.ok) {
      setRatioFieldError({ field: validated.field, message: validated.message });
      return;
    }

    createRatioMutation.mutate(validated.value);
  };

  const openRetireDialog = (version: MeterRatioVersion) => {
    setRetiringRatio(version);
    setRetireEffectiveToInput(siteInputValue(now, timezone));
    setRetireReasonInput('');
    setRetireFieldError(null);
    setRetireSubmitError(null);
  };

  const handleRetireRatioSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!retiringRatio) return;
    setRetireFieldError(null);
    setRetireSubmitError(null);

    const parsedEnd = parseSiteMomentInput(retireEffectiveToInput, timezone);
    if (!parsedEnd) {
      setRetireFieldError('请输入有效的生效结束时间');
      return;
    }
    const startMs = Date.parse(retiringRatio.effectiveFrom);
    if (Number.isFinite(startMs) && parsedEnd.getTime() <= startMs) {
      setRetireFieldError('生效结束时间必须晚于该版本的生效开始时间');
      return;
    }
    const trimmedReason = retireReasonInput.trim();
    if (trimmedReason.length > 500) {
      setRetireFieldError('终止说明不能超过 500 字');
      return;
    }

    retireRatioMutation.mutate({
      versionId: retiringRatio.id,
      effectiveTo: parsedEnd.toISOString(),
      ...(trimmedReason ? { reason: trimmedReason } : {}),
    });
  };

  const resolvePointDisplay = (ratio: MeterRatioVersion): { title: string; subtitle: string | null } => {
    if (ratio.pointId) {
      const matched = pointById.get(ratio.pointId);
      if (matched) {
        return { title: matched.label, subtitle: matched.sublabel };
      }
      const isUuid = uuidV7Schema.safeParse(ratio.pointId).success;
      return {
        title: isUuid ? '已绑定计量测点' : ratio.pointId,
        subtitle: ratio.version !== null ? `版本 v${ratio.version}` : null,
      };
    }
    return {
      title: '站点电表配置',
      subtitle: ratio.version !== null ? `版本 v${ratio.version}` : null,
    };
  };

  const holidays = holidaysQuery.data ?? [];
  const ratioVersions = ratiosQuery.data ?? [];

  return (
    <div className="flex flex-col gap-4">
      {configNotice ? (
        <div
          role="status"
          aria-live="polite"
          data-testid="accounting-config-notice"
          className="flex items-center justify-between gap-3 rounded-lg border border-primary-line bg-primary-soft/60 px-4 py-2.5 text-xs text-foreground"
        >
          <span>{configNotice}</span>
          <Button
            type="button"
            variant="ghost"
            size="xs"
            onClick={() => setConfigNotice(null)}
          >
            关闭提示
          </Button>
        </div>
      ) : null}

      {/* Section 1: 站点节假日日历 */}
      <Panel role="region" aria-label="站点节假日日历">
        <PanelHeader
          title="站点节假日日历"
          className="h-auto min-h-12 flex-wrap py-2"
          action={
            <div className="flex flex-wrap items-center gap-2">
              <Select
                value={String(selectedYear)}
                onValueChange={(value) => {
                  const parsed = Number(value);
                  if (Number.isInteger(parsed) && parsed >= 2000 && parsed <= 2100) {
                    setSelectedYear(parsed);
                  }
                }}
              >
                <SelectTrigger className="h-8 w-28" aria-label="节假日年份">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {yearOptions.map((year) => (
                    <SelectItem key={year} value={String(year)}>
                      {year} 年
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>

              {canManageBilling ? (
                <Button type="button" size="sm" variant="outline" onClick={openCreateHolidayDialog}>
                  <Plus data-icon="inline-start" />
                  新增或更新节假日
                </Button>
              ) : null}
            </div>
          }
        />

        {holidaysQuery.isError ? (
          <StateCard
            tone="danger"
            title={<h3 className="text-base font-semibold">无法读取站点节假日日历</h3>}
            description={describeAccountingConfigError(
              holidaysQuery.error,
              '请检查网络连接或稍后重试',
            )}
          >
            <Button variant="outline" size="sm" onClick={() => void holidaysQuery.refetch()}>
              重试
            </Button>
          </StateCard>
        ) : holidaysQuery.isPending ? (
          <div className="p-5">
            <Skeleton className="h-36 w-full rounded-lg" />
          </div>
        ) : holidays.length === 0 ? (
          <StateCard
            icon={CalendarDays}
            title={
              <h3 className="text-sm font-medium text-foreground">
                {selectedYear} 年尚未配置节假日覆盖
              </h3>
            }
            description="默认按工作日与周末匹配分时电价"
          >
            {canManageBilling ? (
              <Button type="button" size="sm" variant="outline" onClick={openCreateHolidayDialog}>
                <Plus data-icon="inline-start" />
                新增或更新节假日
              </Button>
            ) : null}
          </StateCard>
        ) : (
          <Table aria-label="节假日日历列表">
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead className="pl-5">日期</TableHead>
                <TableHead>日期类型</TableHead>
                <TableHead>说明</TableHead>
                <TableHead>最近更新</TableHead>
                {canManageBilling ? <TableHead className="pr-5 text-right">操作</TableHead> : null}
              </TableRow>
            </TableHeader>
            <TableBody>
              {holidays.map((item) => {
                const badgeMeta = SITE_CALENDAR_DAY_TYPE_VARIANT[item.dayType];
                const weekday = formatWeekdayShort(item.holidayDate);
                return (
                  <TableRow key={item.id}>
                    <TableCell className="pl-5 font-mono text-xs font-medium tabular-nums whitespace-nowrap">
                      {item.holidayDate}
                      {weekday ? (
                        <span className="ml-2 font-sans font-normal text-muted-foreground">
                          {weekday}
                        </span>
                      ) : null}
                    </TableCell>
                    <TableCell className="whitespace-nowrap">
                      <Badge variant={badgeMeta.variant}>
                        {badgeMeta.marker} {SITE_CALENDAR_DAY_TYPE_LABEL[item.dayType]}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-foreground">
                      {item.label.trim() ? item.label : <span className="text-muted-foreground">—</span>}
                    </TableCell>
                    <TableCell className="font-mono text-xs text-muted-foreground tabular-nums whitespace-nowrap">
                      {formatTime(item.updatedAt, timezone)}
                    </TableCell>
                    {canManageBilling ? (
                      <TableCell className="pr-5 text-right whitespace-nowrap">
                        <div className="inline-flex items-center justify-end gap-1">
                          <Button
                            type="button"
                            size="sm"
                            variant="ghost"
                            onClick={() => openEditHolidayDialog(item)}
                          >
                            <Pencil data-icon="inline-start" />
                            编辑
                          </Button>
                          <Button
                            type="button"
                            size="sm"
                            variant="ghost"
                            className="text-destructive-text hover:text-destructive-text"
                            onClick={() => {
                              setHolidayDeleteError(null);
                              setDeletingHoliday(item);
                            }}
                          >
                            <Trash2 data-icon="inline-start" />
                            删除
                          </Button>
                        </div>
                      </TableCell>
                    ) : null}
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        )}
      </Panel>

      {/* Section 2: 电表 CT/PT 倍率版本 */}
      <Panel role="region" aria-label="电表 CT/PT 倍率版本">
        <PanelHeader
          title={
            <span className="inline-flex items-center gap-1.5">
              电表 CT/PT 倍率版本
              <InfoTip>
                按测点与生效时间段维护电流互感器（CT）、电压互感器（PT）变比及计量单位；同一测点的生效时间段不允许重叠。
              </InfoTip>
            </span>
          }
          className="h-auto min-h-12 flex-wrap py-2"
          action={
            <div className="flex flex-wrap items-center gap-3">
              <label
                htmlFor="ratio-include-superseded"
                className="inline-flex cursor-pointer items-center gap-2 py-1 text-xs font-medium text-muted-foreground select-none hover:text-foreground"
              >
                <Checkbox
                  id="ratio-include-superseded"
                  checked={includeSuperseded}
                  onCheckedChange={(checked) => setIncludeSuperseded(checked === true)}
                />
                <span>显示历史/已失效版本</span>
              </label>

              {canManageBilling ? (
                <Button type="button" size="sm" variant="outline" onClick={openCreateRatioDialog}>
                  <Plus data-icon="inline-start" />
                  新增倍率版本
                </Button>
              ) : null}
            </div>
          }
        />

        {ratiosQuery.isError ? (
          <StateCard
            tone="danger"
            title={<h3 className="text-base font-semibold">无法读取电表倍率版本</h3>}
            description={describeAccountingConfigError(
              ratiosQuery.error,
              '请检查网络连接或稍后重试',
            )}
          >
            <Button variant="outline" size="sm" onClick={() => void ratiosQuery.refetch()}>
              重试
            </Button>
          </StateCard>
        ) : ratiosQuery.isPending ? (
          <div className="p-5">
            <Skeleton className="h-44 w-full rounded-lg" />
          </div>
        ) : ratioVersions.length === 0 ? (
          <StateCard
            icon={SlidersHorizontal}
            title={
              <h3 className="text-sm font-medium text-foreground">
                {includeSuperseded ? '尚未配置任何电表倍率版本' : '暂无当前生效的电表倍率版本'}
              </h3>
            }
            description={
              includeSuperseded
                ? '新增后将按生效时段自动折算电量'
                : '可勾选上方开关查看历史版本'
            }
          >
            {canManageBilling ? (
              <Button type="button" size="sm" variant="outline" onClick={openCreateRatioDialog}>
                <Plus data-icon="inline-start" />
                新增倍率版本
              </Button>
            ) : null}
          </StateCard>
        ) : (
          <Table aria-label="电表倍率版本">
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead className="pl-5">测点与设备</TableHead>
                <TableHead>CT / PT / 附加乘数</TableHead>
                <TableHead className="text-right">云端生效乘数</TableHead>
                <TableHead>生效时段</TableHead>
                <TableHead>状态</TableHead>
                {canManageBilling ? <TableHead className="pr-5 text-right">操作</TableHead> : null}
              </TableRow>
            </TableHeader>
            <TableBody>
              {ratioVersions.map((version) => {
                const pointInfo = resolvePointDisplay(version);
                const lifecycle = deriveRatioLifecycleStatus(version, now);
                const canRetire = canRetireRatioVersion(version);
                return (
                  <TableRow key={version.id}>
                    <TableCell className="pl-5 align-top">
                      <div className="font-medium text-foreground">{pointInfo.title}</div>
                      {pointInfo.subtitle ? (
                        <div className="mt-0.5 text-xs text-muted-foreground">
                          {pointInfo.subtitle}
                        </div>
                      ) : null}
                    </TableCell>
                    <TableCell className="align-top font-mono text-xs tabular-nums whitespace-nowrap">
                      <div>
                        CT {formatMultiplier(version.ctRatio)} · PT {formatMultiplier(version.ptRatio)}
                      </div>
                      <div className="mt-0.5 text-muted-foreground">
                        附加乘数 {formatMultiplier(version.meterMultiplier)}
                      </div>
                    </TableCell>
                    <TableCell className="text-right align-top tabular-nums whitespace-nowrap">
                      <div className="font-mono font-semibold text-foreground">
                        ×{formatMultiplier(version.cloudMultiplier)}
                      </div>
                      <div className="mt-0.5 text-xs text-muted-foreground">
                        {RATIO_APPLICATION_MODE_SHORT_LABEL[version.ratioApplicationMode]} ·{' '}
                        {version.readingUnit}
                      </div>
                    </TableCell>
                    <TableCell className="align-top font-mono text-xs tabular-nums whitespace-nowrap">
                      <div>{formatTime(version.effectiveFrom, timezone)} 起</div>
                      <div className="mt-0.5 font-sans text-muted-foreground">
                        {version.effectiveTo
                          ? `至 ${formatTime(version.effectiveTo, timezone)}`
                          : '长期有效'}
                      </div>
                    </TableCell>
                    <TableCell className="align-top whitespace-nowrap">
                      <Badge variant={lifecycle.variant}>{lifecycle.label}</Badge>
                    </TableCell>
                    {canManageBilling ? (
                      <TableCell className="pr-5 text-right align-top whitespace-nowrap">
                        {canRetire ? (
                          <Button
                            type="button"
                            size="sm"
                            variant="outline"
                            onClick={() => openRetireDialog(version)}
                          >
                            终止生效
                          </Button>
                        ) : (
                          <span className="text-xs text-muted-foreground">—</span>
                        )}
                      </TableCell>
                    ) : null}
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        )}
      </Panel>

      {/* Dialog 1: 配置站点节假日 */}
      <Dialog
        open={holidayDialogOpen}
        onOpenChange={(nextOpen) => {
          if (upsertHolidayMutation.isPending) return;
          setHolidayDialogOpen(nextOpen);
        }}
      >
        <DialogContent
          showCloseButton={!upsertHolidayMutation.isPending}
          onEscapeKeyDown={(event) => {
            if (upsertHolidayMutation.isPending) event.preventDefault();
          }}
          onPointerDownOutside={(event) => {
            if (upsertHolidayMutation.isPending) event.preventDefault();
          }}
          className="sm:max-w-md"
        >
          <DialogHeader>
            <DialogTitle>配置站点节假日</DialogTitle>
            <DialogDescription>
              指定日期覆盖默认星期类型后，系统将按对应分时电价重新计算该日及关联时段电费。
            </DialogDescription>
          </DialogHeader>

          <form className="space-y-4" onSubmit={handleHolidaySubmit}>
            {holidaySubmitError ? (
              <Alert variant="destructive">
                <AlertTitle>无法保存节假日配置</AlertTitle>
                <AlertDescription>{holidaySubmitError}</AlertDescription>
              </Alert>
            ) : null}

            <Field data-invalid={Boolean(holidayFormError) || undefined}>
              <FieldLabel htmlFor="holiday-date-input">日期</FieldLabel>
              <Input
                id="holiday-date-input"
                type="text"
                inputMode="numeric"
                placeholder="YYYY-MM-DD"
                value={holidayDateInput}
                readOnly={Boolean(holidayEditTarget)}
                disabled={upsertHolidayMutation.isPending}
                className={
                  holidayEditTarget
                    ? 'bg-muted text-muted-foreground tabular-nums'
                    : 'tabular-nums'
                }
                onChange={(event) => setHolidayDateInput(event.target.value)}
                onKeyDown={preventImeSubmit}
              />
              <FieldDescription>按站点本地日期填写，格式如 2026-10-01。</FieldDescription>
              {holidayFormError ? <FieldError>{holidayFormError}</FieldError> : null}
            </Field>

            <Field>
              <FieldLabel htmlFor="holiday-day-type-select">日期类型</FieldLabel>
              <Select
                value={holidayDayType}
                disabled={upsertHolidayMutation.isPending}
                onValueChange={(value) => {
                  if (isOneOf(SITE_CALENDAR_DAY_TYPES, value)) {
                    setHolidayDayType(value);
                  }
                }}
              >
                <SelectTrigger id="holiday-day-type-select" aria-label="日期类型">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {SITE_CALENDAR_DAY_TYPES.map((type) => (
                    <SelectItem key={type} value={type}>
                      {SITE_CALENDAR_DAY_TYPE_DESCRIPTION[type]}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>

            <Field>
              <FieldLabel htmlFor="holiday-label-input">说明</FieldLabel>
              <Input
                id="holiday-label-input"
                type="text"
                placeholder="例如：国庆节放假、春节调休补班"
                value={holidayLabelInput}
                disabled={upsertHolidayMutation.isPending}
                onChange={(event) => setHolidayLabelInput(event.target.value)}
                onKeyDown={preventImeSubmit}
              />
            </Field>

            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                disabled={upsertHolidayMutation.isPending}
                onClick={() => setHolidayDialogOpen(false)}
              >
                取消
              </Button>
              <Button type="submit" disabled={upsertHolidayMutation.isPending}>
                {upsertHolidayMutation.isPending ? '正在保存…' : '保存节假日'}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      {/* Dialog 2: 确认删除节假日 */}
      <Dialog
        open={Boolean(deletingHoliday)}
        onOpenChange={(nextOpen) => {
          if (deleteHolidayMutation.isPending) return;
          if (!nextOpen) setDeletingHoliday(null);
        }}
      >
        <DialogContent
          showCloseButton={!deleteHolidayMutation.isPending}
          onEscapeKeyDown={(event) => {
            if (deleteHolidayMutation.isPending) event.preventDefault();
          }}
          onPointerDownOutside={(event) => {
            if (deleteHolidayMutation.isPending) event.preventDefault();
          }}
          className="sm:max-w-md"
        >
          <DialogHeader>
            <DialogTitle>确认删除节假日</DialogTitle>
            <DialogDescription>
              {deletingHoliday
                ? `删除 ${deletingHoliday.holidayDate}（${SITE_CALENDAR_DAY_TYPE_LABEL[deletingHoliday.dayType]}）的覆盖配置后，该日将恢复按常规星期类型匹配分时电价，并触发后台重算。`
                : ''}
            </DialogDescription>
          </DialogHeader>

          {holidayDeleteError ? (
            <Alert variant="destructive">
              <AlertTitle>无法删除节假日</AlertTitle>
              <AlertDescription>{holidayDeleteError}</AlertDescription>
            </Alert>
          ) : null}

          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              disabled={deleteHolidayMutation.isPending}
              onClick={() => setDeletingHoliday(null)}
            >
              取消
            </Button>
            <Button
              type="button"
              variant="destructive"
              disabled={deleteHolidayMutation.isPending || !deletingHoliday}
              onClick={() => {
                if (!deletingHoliday) return;
                setHolidayDeleteError(null);
                deleteHolidayMutation.mutate(deletingHoliday.holidayDate);
              }}
            >
              {deleteHolidayMutation.isPending ? '正在删除…' : '确认删除'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Dialog 3: 新增电表倍率版本 */}
      <Dialog
        open={createRatioOpen}
        onOpenChange={(nextOpen) => {
          if (createRatioMutation.isPending) return;
          setCreateRatioOpen(nextOpen);
        }}
      >
        <DialogContent
          showCloseButton={!createRatioMutation.isPending}
          onEscapeKeyDown={(event) => {
            if (createRatioMutation.isPending) event.preventDefault();
          }}
          onPointerDownOutside={(event) => {
            if (createRatioMutation.isPending) event.preventDefault();
          }}
          className="sm:max-w-xl"
        >
          <DialogHeader>
            <DialogTitle>新增电表倍率版本</DialogTitle>
            <DialogDescription>
              为指定计量测点配置互感器变比、单位与生效时间段。同一测点的已发布生效时间段不能重叠。
            </DialogDescription>
          </DialogHeader>

          <form className="space-y-4" onSubmit={handleCreateRatioSubmit}>
            {ratioSubmitError ? (
              <Alert variant="destructive">
                <AlertTitle>无法创建倍率版本</AlertTitle>
                <AlertDescription>{ratioSubmitError}</AlertDescription>
              </Alert>
            ) : null}

            <Field
              data-invalid={ratioFieldError?.field === 'pointId' ? true : undefined}
            >
              <div className="flex items-center justify-between gap-2">
                <FieldLabel htmlFor="ratio-point-id">测点 ID</FieldLabel>
                {pointOptions.length > 0 ? (
                  <Button
                    type="button"
                    variant="ghost"
                    size="xs"
                    disabled={createRatioMutation.isPending}
                    onClick={() => setManualPointMode((prev) => !prev)}
                  >
                    {manualPointMode ? '从站点测点选择' : '手动填写测点 ID'}
                  </Button>
                ) : null}
              </div>

              {!manualPointMode && pointOptions.length > 0 ? (
                <Select
                  value={ratioPointId}
                  disabled={createRatioMutation.isPending}
                  onValueChange={setRatioPointId}
                >
                  <SelectTrigger id="ratio-point-id" aria-label="测点 ID" className="w-full">
                    <SelectValue placeholder="请选择电表或冷量测点" />
                  </SelectTrigger>
                  <SelectContent>
                    {pointOptions.map((option) => (
                      <SelectItem key={option.id} value={option.id}>
                        {option.label}（{option.sublabel}）
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              ) : (
                <Input
                  id="ratio-point-id"
                  type="text"
                  placeholder="请输入测点编号（UUID）"
                  value={ratioPointId}
                  disabled={createRatioMutation.isPending}
                  className="font-mono text-xs"
                  onChange={(event) => setRatioPointId(event.target.value)}
                  onKeyDown={preventImeSubmit}
                />
              )}
              {ratioFieldError?.field === 'pointId' ? (
                <FieldError>{ratioFieldError.message}</FieldError>
              ) : null}
            </Field>

            <div className="grid gap-3 sm:grid-cols-3">
              <Field data-invalid={ratioFieldError?.field === 'ctRatio' ? true : undefined}>
                <FieldLabel htmlFor="ratio-ct-input">CT 变比</FieldLabel>
                <Input
                  id="ratio-ct-input"
                  type="text"
                  inputMode="decimal"
                  value={ctRatioInput}
                  disabled={createRatioMutation.isPending}
                  className="tabular-nums"
                  onChange={(event) => setCtRatioInput(event.target.value)}
                  onKeyDown={preventImeSubmit}
                />
                {ratioFieldError?.field === 'ctRatio' ? (
                  <FieldError>{ratioFieldError.message}</FieldError>
                ) : null}
              </Field>

              <Field data-invalid={ratioFieldError?.field === 'ptRatio' ? true : undefined}>
                <FieldLabel htmlFor="ratio-pt-input">PT 变比</FieldLabel>
                <Input
                  id="ratio-pt-input"
                  type="text"
                  inputMode="decimal"
                  value={ptRatioInput}
                  disabled={createRatioMutation.isPending}
                  className="tabular-nums"
                  onChange={(event) => setPtRatioInput(event.target.value)}
                  onKeyDown={preventImeSubmit}
                />
                {ratioFieldError?.field === 'ptRatio' ? (
                  <FieldError>{ratioFieldError.message}</FieldError>
                ) : null}
              </Field>

              <Field
                data-invalid={ratioFieldError?.field === 'meterMultiplier' ? true : undefined}
              >
                <FieldLabel htmlFor="ratio-mult-input">附加乘数</FieldLabel>
                <Input
                  id="ratio-mult-input"
                  type="text"
                  inputMode="decimal"
                  value={meterMultiplierInput}
                  disabled={createRatioMutation.isPending}
                  className="tabular-nums"
                  onChange={(event) => setMeterMultiplierInput(event.target.value)}
                  onKeyDown={preventImeSubmit}
                />
                {ratioFieldError?.field === 'meterMultiplier' ? (
                  <FieldError>{ratioFieldError.message}</FieldError>
                ) : null}
              </Field>
            </div>

            <div className="grid gap-3 sm:grid-cols-2">
              <Field
                data-invalid={
                  ratioFieldError?.field === 'ratioApplicationMode' ? true : undefined
                }
              >
                <FieldLabel htmlFor="ratio-app-mode-select">应用层级</FieldLabel>
                <Select
                  value={ratioApplicationMode}
                  disabled={createRatioMutation.isPending}
                  onValueChange={(value) => {
                    if (isOneOf(RATIO_APPLICATION_MODES, value)) {
                      setRatioApplicationMode(value);
                    }
                  }}
                >
                  <SelectTrigger id="ratio-app-mode-select" aria-label="应用层级" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {RATIO_APPLICATION_MODES.map((mode) => (
                      <SelectItem key={mode} value={mode}>
                        {RATIO_APPLICATION_MODE_LABEL[mode]}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                {ratioFieldError?.field === 'ratioApplicationMode' ? (
                  <FieldError>{ratioFieldError.message}</FieldError>
                ) : null}
              </Field>

              <Field data-invalid={ratioFieldError?.field === 'readingUnit' ? true : undefined}>
                <FieldLabel htmlFor="ratio-unit-select">计量单位</FieldLabel>
                <Select
                  value={readingUnit}
                  disabled={createRatioMutation.isPending}
                  onValueChange={(value) => {
                    if (isOneOf(RATIO_READING_UNITS, value)) {
                      setReadingUnit(value);
                    }
                  }}
                >
                  <SelectTrigger id="ratio-unit-select" aria-label="计量单位" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {RATIO_READING_UNITS.map((unit) => (
                      <SelectItem key={unit} value={unit}>
                        {RATIO_READING_UNIT_LABEL[unit]}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                {ratioFieldError?.field === 'readingUnit' ? (
                  <FieldError>{ratioFieldError.message}</FieldError>
                ) : null}
              </Field>
            </div>

            <div className="flex items-center justify-between gap-3 rounded-lg border border-border bg-muted/40 px-3.5 py-2.5">
              <div>
                <div className="text-xs font-medium text-foreground">云端生效乘数</div>
                <div className="text-xs text-muted-foreground">
                  {ratioApplicationMode === 'PLATFORM_APPLIED'
                    ? '按 CT 变比 × PT 变比 × 附加乘数在云端折算'
                    : '表端或边缘已完成变比计算，云端按 ×1 折算单位'}
                </div>
              </div>
              <div
                data-testid="cloud-multiplier-preview"
                className="font-mono text-base font-semibold text-foreground tabular-nums"
              >
                {liveCloudMultiplier !== null ? `×${formatMultiplier(liveCloudMultiplier)}` : '—'}
              </div>
            </div>

            <div className="grid gap-3 sm:grid-cols-2">
              <Field data-invalid={ratioFieldError?.field === 'effectiveFrom' ? true : undefined}>
                <FieldLabel htmlFor="ratio-effective-from">生效开始时间</FieldLabel>
                <Input
                  id="ratio-effective-from"
                  type="datetime-local"
                  value={effectiveFromInput}
                  disabled={createRatioMutation.isPending}
                  className="tabular-nums"
                  onChange={(event) => setEffectiveFromInput(event.target.value)}
                />
                {ratioFieldError?.field === 'effectiveFrom' ? (
                  <FieldError>{ratioFieldError.message}</FieldError>
                ) : null}
              </Field>

              <Field data-invalid={ratioFieldError?.field === 'effectiveTo' ? true : undefined}>
                <FieldLabel htmlFor="ratio-effective-to">生效结束时间（选填）</FieldLabel>
                <Input
                  id="ratio-effective-to"
                  type="datetime-local"
                  value={effectiveToInput}
                  disabled={createRatioMutation.isPending}
                  className="tabular-nums"
                  onChange={(event) => setEffectiveToInput(event.target.value)}
                />
                {ratioFieldError?.field === 'effectiveTo' ? (
                  <FieldError>{ratioFieldError.message}</FieldError>
                ) : null}
              </Field>
            </div>

            <Field data-invalid={ratioFieldError?.field === 'reason' ? true : undefined}>
              <FieldLabel htmlFor="ratio-reason-input">变更说明（选填）</FieldLabel>
              <Input
                id="ratio-reason-input"
                type="text"
                placeholder="例如：更换进线电流互感器，变比由 100 调整为 200"
                value={ratioReasonInput}
                disabled={createRatioMutation.isPending}
                onChange={(event) => setRatioReasonInput(event.target.value)}
                onKeyDown={preventImeSubmit}
              />
              {ratioFieldError?.field === 'reason' ? (
                <FieldError>{ratioFieldError.message}</FieldError>
              ) : null}
            </Field>

            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                disabled={createRatioMutation.isPending}
                onClick={() => setCreateRatioOpen(false)}
              >
                取消
              </Button>
              <Button type="submit" disabled={createRatioMutation.isPending}>
                {createRatioMutation.isPending ? '正在创建…' : '创建倍率版本'}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      {/* Dialog 4: 终止倍率版本 */}
      <Dialog
        open={Boolean(retiringRatio)}
        onOpenChange={(nextOpen) => {
          if (retireRatioMutation.isPending) return;
          if (!nextOpen) setRetiringRatio(null);
        }}
      >
        <DialogContent
          showCloseButton={!retireRatioMutation.isPending}
          onEscapeKeyDown={(event) => {
            if (retireRatioMutation.isPending) event.preventDefault();
          }}
          onPointerDownOutside={(event) => {
            if (retireRatioMutation.isPending) event.preventDefault();
          }}
          className="sm:max-w-md"
        >
          <DialogHeader>
            <DialogTitle>终止倍率版本</DialogTitle>
            <DialogDescription>
              {retiringRatio
                ? `设置该版本（自 ${formatTime(retiringRatio.effectiveFrom, timezone)} 起生效，云端乘数 ×${formatMultiplier(retiringRatio.cloudMultiplier)}）的生效结束时间；终止后可为后续时段创建新的倍率版本。`
                : ''}
            </DialogDescription>
          </DialogHeader>

          <form className="space-y-4" onSubmit={handleRetireRatioSubmit}>
            {retireSubmitError ? (
              <Alert variant="destructive">
                <AlertTitle>无法终止倍率版本</AlertTitle>
                <AlertDescription>{retireSubmitError}</AlertDescription>
              </Alert>
            ) : null}

            <Field data-invalid={Boolean(retireFieldError) || undefined}>
              <FieldLabel htmlFor="retire-effective-to">生效结束时间</FieldLabel>
              <Input
                id="retire-effective-to"
                type="datetime-local"
                value={retireEffectiveToInput}
                disabled={retireRatioMutation.isPending}
                className="tabular-nums"
                onChange={(event) => setRetireEffectiveToInput(event.target.value)}
              />
              <FieldDescription>
                按站点时区（{timezone}）填写，采用左闭右开生效边界。
              </FieldDescription>
              {retireFieldError ? <FieldError>{retireFieldError}</FieldError> : null}
            </Field>

            <Field>
              <FieldLabel htmlFor="retire-reason-input">变更说明（选填）</FieldLabel>
              <Input
                id="retire-reason-input"
                type="text"
                placeholder="例如：现场更换新互感器，终止旧倍率"
                value={retireReasonInput}
                disabled={retireRatioMutation.isPending}
                onChange={(event) => setRetireReasonInput(event.target.value)}
                onKeyDown={preventImeSubmit}
              />
            </Field>

            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                disabled={retireRatioMutation.isPending}
                onClick={() => setRetiringRatio(null)}
              >
                取消
              </Button>
              <Button
                type="submit"
                variant="destructive"
                disabled={retireRatioMutation.isPending || !retiringRatio}
              >
                {retireRatioMutation.isPending ? '正在终止…' : '确认终止'}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </div>
  );
}
