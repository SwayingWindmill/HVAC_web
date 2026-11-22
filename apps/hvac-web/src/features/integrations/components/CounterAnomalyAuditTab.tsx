import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { ShieldCheck } from 'lucide-react';
import { siteAssetModelQuery } from '@/api/site-asset-model';
import { Panel } from '@/blocks/panel';
import { StateCard } from '@/blocks/state/state-card';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Label } from '@/components/ui/label';
import { PeriodTabs, type PeriodOption } from '@/components/ui/period-tabs';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import { Switch } from '@/components/ui/switch';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import {
  buildCounterAuditKpiMetrics,
  COUNTER_TRANSITION_FILTER_OPTIONS,
  counterQualityMeta,
  counterTransitionMeta,
  formatCounterQualityReasons,
  type CounterTransitionFilterValue,
} from '@/features/billing/accounting-config-model';
import { describeAccountingConfigError, siteCounterAuditQuery } from '@/features/billing/query';
import {
  ENERGY_PERIODS,
  periodWindow,
  type EnergyPeriod,
} from '@/features/energy-analysis/consumption/model';
import { useNow } from '@/hooks/use-now';
import { formatDecimal, formatTime } from '@/lib/operator-format';
import { isOneOf } from '@/lib/typed';
import { cn } from '@/lib/utils';

const PERIOD_OPTIONS: readonly PeriodOption<EnergyPeriod>[] = (
  ['today', '7d', 'month', 'year'] as const
).map((value) => ({ value, label: ENERGY_PERIODS[value] }));

const COUNTER_TRANSITION_FILTER_VALUES = [
  'ALL',
  'INVALID_ZERO_DROP',
  'INVALID_SPIKE',
  'INVALID_DECREASE',
  'RESET',
  'ROLLOVER',
  'RECOVERY',
  'METER_REPLACEMENT',
  'INCREASE',
] as const satisfies readonly CounterTransitionFilterValue[];

export interface CounterAnomalyAuditTabProps {
  readonly siteId: string | null;
  readonly timeZone: string;
}

export function CounterAnomalyAuditTab({ siteId, timeZone }: CounterAnomalyAuditTabProps) {
  const [period, setPeriod] = useState<EnergyPeriod>('today');
  const [anomaliesOnly, setAnomaliesOnly] = useState(true);
  const [transitionFilter, setTransitionFilter] = useState<CounterTransitionFilterValue>('ALL');

  const nowMs = useNow(60_000);
  const bucketNow = useMemo(() => new Date(Math.floor(nowMs / 60_000) * 60_000), [nowMs]);
  const { from } = useMemo(() => periodWindow(period, bucketNow, timeZone), [period, bucketNow, timeZone]);

  const auditQuery = useQuery(
    siteCounterAuditQuery(
      siteId ?? '',
      {
        from: from.toISOString(),
        to: bucketNow.toISOString(),
        anomaliesOnly,
        limit: 200,
      },
      Boolean(siteId),
    ),
  );

  const assetModelQuery = useQuery(
    siteAssetModelQuery(siteId, {
      enabled: Boolean(siteId),
      retry: false,
    }),
  );

  const { deviceById, pointById } = useMemo(() => {
    const model = assetModelQuery.data;
    if (!model) {
      return {
        deviceById: new Map<string, string>(),
        pointById: new Map<string, { readonly displayName: string; readonly pointCode: string }>(),
      };
    }
    return {
      deviceById: new Map(model.devices.map((device) => [device.id, device.displayName])),
      pointById: new Map(
        model.telemetryPoints.map((point) => [
          point.id,
          { displayName: point.displayName, pointCode: point.pointCode },
        ]),
      ),
    };
  }, [assetModelQuery.data]);

  const auditData = auditQuery.data;
  const kpiMetrics = useMemo(
    () => (auditData ? buildCounterAuditKpiMetrics(auditData.summary) : []),
    [auditData],
  );

  const filteredItems = useMemo(() => {
    const items = auditData?.items ?? [];
    if (transitionFilter === 'ALL') return items;
    return items.filter((item) => item.transitionType === transitionFilter);
  }, [auditData, transitionFilter]);

  const resolveTelemetryDisplay = (item: {
    readonly pointId: string;
    readonly deviceId: string;
    readonly telemetryKey: string;
  }): { readonly title: string; readonly subtitle: string | null } => {
    const deviceName = item.deviceId ? deviceById.get(item.deviceId) : undefined;
    const pointMeta = pointById.get(item.pointId);
    if (pointMeta) {
      return {
        title: deviceName ? `${deviceName} · ${pointMeta.displayName}` : pointMeta.displayName,
        subtitle: pointMeta.pointCode,
      };
    }
    const cleanKey = item.telemetryKey.trim();
    if (cleanKey) {
      return {
        title: deviceName ? `${deviceName} · ${cleanKey}` : cleanKey,
        subtitle: null,
      };
    }
    return {
      title: deviceName ? `${deviceName} · 累计表计` : '累计电表测点',
      subtitle: null,
    };
  };

  if (!siteId) {
    return (
      <Panel aria-label="异常清洗留痕" data-testid="counter-anomaly-audit">
        <StateCard
          icon={ShieldCheck}
          title={<h3 className="text-base font-semibold">当前没有可选站点</h3>}
          description="请先选择一个授权站点，再查看累计表计的异常清洗留痕。"
        />
      </Panel>
    );
  }

  if (auditQuery.isError) {
    return (
      <Panel aria-label="异常清洗留痕" data-testid="counter-anomaly-audit">
        <StateCard
          tone="danger"
          title={<h3 className="text-base font-semibold">无法读取表计异常清洗记录</h3>}
          description={describeAccountingConfigError(
            auditQuery.error,
            '请检查网络连接或稍后重试',
          )}
        >
          <Button variant="outline" size="sm" onClick={() => void auditQuery.refetch()}>
            重试
          </Button>
        </StateCard>
      </Panel>
    );
  }

  return (
    <Panel
      aria-label="异常清洗留痕"
      data-testid="counter-anomaly-audit"
      className="flex flex-col"
    >
      {auditQuery.isPending || !auditData ? (
        <div className="grid grid-cols-2 divide-x divide-y divide-divider border-b border-divider sm:grid-cols-3 lg:grid-cols-6 lg:divide-y-0">
          {Array.from({ length: 6 }, (_, index) => (
            <div key={index} className="flex flex-col gap-2 px-5 py-3.5">
              <Skeleton className="h-3.5 w-16" />
              <Skeleton className="h-6 w-12" />
            </div>
          ))}
        </div>
      ) : (
        <div className="grid grid-cols-2 divide-x divide-y divide-divider border-b border-divider sm:grid-cols-3 lg:grid-cols-6 lg:divide-y-0">
          {kpiMetrics.map((metric) => (
            <div key={metric.key} className="flex min-w-0 flex-col gap-1 px-5 py-3.5">
              <span className="truncate text-xs font-medium text-muted-foreground">
                {metric.label}
              </span>
              <span
                className={cn(
                  'text-xl leading-7 font-semibold tracking-tight tabular-nums',
                  metric.tone === 'destructive'
                    ? 'text-destructive-text'
                    : metric.tone === 'warning'
                      ? 'text-warning-text'
                      : 'text-foreground',
                )}
              >
                {formatDecimal(metric.value, 0)}
              </span>
            </div>
          ))}
        </div>
      )}

      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-divider bg-muted/20 px-5 py-3">
        <div className="flex flex-wrap items-center gap-4">
          <PeriodTabs
            aria-label="统计期间"
            options={PERIOD_OPTIONS}
            value={period}
            onValueChange={setPeriod}
          />

          <div className="flex items-center gap-2">
            <Switch
              id="counter-audit-anomalies-only"
              checked={anomaliesOnly}
              onCheckedChange={(checked) => setAnomaliesOnly(checked)}
              aria-label="仅看异常"
            />
            <Label
              htmlFor="counter-audit-anomalies-only"
              className="cursor-pointer text-xs font-medium text-foreground"
            >
              仅看异常
            </Label>
          </div>

          <Select
            value={transitionFilter}
            onValueChange={(value) => {
              if (!isOneOf(COUNTER_TRANSITION_FILTER_VALUES, value)) return;
              setTransitionFilter(value);
              if (value === 'INCREASE' && anomaliesOnly) {
                setAnomaliesOnly(false);
              }
            }}
          >
            <SelectTrigger className="h-8 w-44" aria-label="清洗类型筛选">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {COUNTER_TRANSITION_FILTER_OPTIONS.map((option) => (
                <SelectItem key={option.value} value={option.value}>
                  {option.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        {auditData ? (
          <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground tabular-nums">
            <span>共 {filteredItems.length} 条记录</span>
            {auditData.summary.invalidDecreaseCount > 0 ? (
              <span className="text-destructive-text">
                · 异常回退 {auditData.summary.invalidDecreaseCount} 次
              </span>
            ) : null}
            {auditData.truncated ? (
              <span className="text-warning-text">
                · 仅展示前 {auditData.items.length} 条，可缩短统计期间查看全部
              </span>
            ) : null}
          </div>
        ) : null}
      </div>

      {auditQuery.isPending || !auditData ? (
        <div className="p-5">
          <Skeleton className="h-44 w-full rounded-lg" />
        </div>
      ) : filteredItems.length === 0 ? (
        <StateCard
          icon={ShieldCheck}
          tone={auditData.items.length === 0 && anomaliesOnly ? 'success' : 'neutral'}
          title={
            <h3 className="text-sm font-medium text-foreground">
              {auditData.items.length === 0 && anomaliesOnly
                ? '所选时段内未检测到异常或换表清洗事件'
                : auditData.items.length === 0
                  ? '所选时段暂无表计采样记录'
                  : '当前筛选条件下没有匹配的清洗记录'}
            </h3>
          }
          description={
            auditData.items.length === 0 && anomaliesOnly
              ? '期间内累计表计示数均平稳递进，可关闭「仅看异常」开关查看全部正常采样记录。'
              : auditData.items.length === 0
                ? '等待表计持续上报累计示数后，这里会展示每段读数的清洗与校验轨迹。'
                : '可切换上方清洗类型筛选或关闭「仅看异常」开关查看其他记录。'
          }
        >
          {auditData.items.length === 0 && anomaliesOnly ? (
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={() => setAnomaliesOnly(false)}
            >
              查看全部采样
            </Button>
          ) : auditData.items.length > 0 ? (
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={() => {
                setTransitionFilter('ALL');
                setAnomaliesOnly(false);
              }}
            >
              重置筛选条件
            </Button>
          ) : null}
        </StateCard>
      ) : (
        <Table aria-label="表计异常清洗明细">
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead className="whitespace-nowrap pl-5">时间窗口</TableHead>
              <TableHead className="whitespace-nowrap">测点与设备</TableHead>
              <TableHead className="whitespace-nowrap">清洗分类</TableHead>
              <TableHead className="whitespace-nowrap text-right">原始读数变化</TableHead>
              <TableHead className="whitespace-nowrap text-right">清洗后净增量</TableHead>
              <TableHead className="whitespace-nowrap pr-5">质量与判定原因</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {filteredItems.map((item, index) => {
              const target = resolveTelemetryDisplay(item);
              const transition = counterTransitionMeta(item.transitionType);
              const quality = counterQualityMeta(item.quality);
              const reasonText = formatCounterQualityReasons(item.qualityReasons);
              return (
                <TableRow
                  key={`${item.pointId}-${item.periodEnd}-${item.transitionType}-${index}`}
                >
                  <TableCell className="whitespace-nowrap pl-5 font-mono text-xs tabular-nums">
                    <span>{item.periodStart ? formatTime(item.periodStart, timeZone) : '—'}</span>
                    <span className="mx-1 text-muted-foreground">→</span>
                    <span>{formatTime(item.periodEnd, timeZone)}</span>
                  </TableCell>
                  <TableCell className="min-w-36">
                    <div className="font-medium text-foreground">{target.title}</div>
                    {target.subtitle ? (
                      <div className="mt-0.5 font-mono text-xs text-muted-foreground">
                        {target.subtitle}
                      </div>
                    ) : null}
                  </TableCell>
                  <TableCell className="whitespace-nowrap">
                    <Badge variant={transition.variant}>
                      {transition.marker} {transition.label}
                    </Badge>
                  </TableCell>
                  <TableCell className="whitespace-nowrap text-right font-mono text-xs tabular-nums">
                    <span>
                      {item.rawPrevious !== null ? formatDecimal(item.rawPrevious, 2) : '—'}
                    </span>
                    <span className="mx-1 text-muted-foreground">→</span>
                    <span className="font-medium text-foreground">
                      {formatDecimal(item.rawCurrent, 2)}
                    </span>
                    <span className="ml-1 text-subtle-foreground">{item.unit}</span>
                  </TableCell>
                  <TableCell className="whitespace-nowrap text-right font-mono font-medium tabular-nums">
                    {item.cleanedDeltaKWh !== null ? (
                      <>
                        {formatDecimal(item.cleanedDeltaKWh, 2)}
                        <span className="ml-1 text-xs font-normal text-subtle-foreground">
                          kWh
                        </span>
                      </>
                    ) : (
                      <span className="text-muted-foreground">—</span>
                    )}
                  </TableCell>
                  <TableCell className="pr-5">
                    <div className="flex items-center gap-2">
                      <Badge variant={quality.variant} className="shrink-0">
                        {quality.label}
                      </Badge>
                      {reasonText ? (
                        <span
                          className="max-w-44 truncate text-xs text-muted-foreground"
                          title={reasonText}
                        >
                          {reasonText}
                        </span>
                      ) : null}
                    </div>
                  </TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      )}
    </Panel>
  );
}
