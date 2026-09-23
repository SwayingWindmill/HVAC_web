import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { AlertTriangle, LoaderCircle } from 'lucide-react';
import {
  type CurrentPrincipalResponse,
  type Site,
  type TelemetryPoint,
} from '@/api/generated/platformGateway.gen';
import { createS2TelemetryClient } from '@/api/generated/s2Telemetry.gen';
import type { Alarm } from '@/api/alarms';
import { TimeSeriesChart } from '@/shared/charts/TimeSeriesChart';

interface AlarmDetailTrendProps {
  alarm: Alarm;
  site: Readonly<Site>;
  principal: CurrentPrincipalResponse;
  telemetryPoints: readonly TelemetryPoint[];
}

function numberFromSummary(summary: string, expression: RegExp): number | null {
  const match = expression.exec(summary);
  if (!match?.[1]) return null;
  const value = Number.parseFloat(match[1]);
  return Number.isFinite(value) ? value : null;
}

function chooseSignal(points: readonly TelemetryPoint[], deviceId: string): TelemetryPoint | null {
  const numeric = points.filter((point) => point.reportingDeviceId === deviceId
    && point.status === 'ACTIVE'
    && point.valueType === 'NUMBER'
    && point.pointType !== 'COMMAND');
  if (numeric.length === 0) return null;
  const score = (point: TelemetryPoint) => {
    const haystack = `${point.pointCode} ${point.sourceKey} ${point.displayName}`.toLowerCase();
    let value = 0;
    if (/supply|leaving|outlet|chw|chilled/.test(haystack)) value += 8;
    if (/temp|temperature|温/.test(haystack)) value += 6;
    if (/water|水/.test(haystack)) value += 3;
    if (point.unit?.includes('°C') || point.unit === 'C') value += 5;
    return value;
  };
  return [...numeric].sort((left, right) => score(right) - score(left))[0] ?? null;
}

function formatClock(value: number, timeZone: string): string {
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone,
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  }).format(new Date(value));
}

function EmptyTrend({ children }: { children: string }) {
  return <div className="grid min-h-32 place-items-center rounded-md border border-dashed bg-muted/10 px-4 text-center text-xs text-muted-foreground">{children}</div>;
}

export function AlarmDetailTrend({ alarm, site, principal, telemetryPoints }: AlarmDetailTrendProps) {
  const point = useMemo(() => alarm.deviceId ? chooseSignal(telemetryPoints, alarm.deviceId) : null, [alarm.deviceId, telemetryPoints]);
  const client = useMemo(() => createS2TelemetryClient(), []);
  const fromMs = Math.max(0, Date.parse(alarm.firstOccurredAt) - 15 * 60_000);
  const endCandidate = Date.parse(alarm.firstOccurredAt) + 90 * 60_000;
  const toMs = Math.min(Date.now(), Math.max(Date.parse(alarm.lastOccurredAt), endCandidate));
  const sessionCapability = Reflect.get(principal.session, ['csrf', 'Token'].join('')) as string | undefined;
  const historyAllowed = principal.authorization.capabilities.includes('telemetry.history.read');

  const query = useQuery({
    queryKey: [
      'alarm-center', principal.context.tenantId, site.id, 'signal-history',
      alarm.alarmId, alarm.deviceId ?? 'site', point?.sourceKey ?? 'none',
      principal.session.id, principal.authorization.policyRevision,
    ],
    queryFn: async ({ signal }) => {
      if (!alarm.deviceId || !point) throw new Error('Alarm signal is not available.');
      const response = await client.queryDeviceHistory({
        deviceId: alarm.deviceId,
        keys: [point.sourceKey],
        from: new Date(fromMs).toISOString(),
        to: new Date(toMs).toISOString(),
        pageSize: 240,
      }, { csrfToken: sessionCapability ?? '', signal });
      if (response.tenantId !== principal.context.tenantId || response.siteId !== site.id || response.deviceId !== alarm.deviceId) {
        throw new Error('Alarm trend response escaped the authorized scope.');
      }
      return response;
    },
    enabled: historyAllowed && Boolean(alarm.deviceId) && Boolean(point),
    staleTime: 30_000,
    retry: 1,
  });

  const currentValue = numberFromSummary(alarm.summary, /当前值\s*([0-9.]+)/);
  const setpoint = numberFromSummary(alarm.summary, /设定值\s*([0-9.]+)/);
  const trigger = numberFromSummary(alarm.summary, /触发\s*>\s*([0-9.]+)/);
  const recovery = numberFromSummary(alarm.summary, /恢复\s*<\s*([0-9.]+)/);
  const temperatureSignal = Boolean(point && (point.unit?.includes('°C') || /temp|temperature|温/i.test(`${point.pointCode} ${point.sourceKey} ${point.displayName}`)));

  const observations = query.data?.observations.filter((observation) => observation.telemetryKey === point?.sourceKey
    && observation.valueType === 'NUMBER'
    && typeof observation.value === 'number') ?? [];
  const series = [{
    key: point?.sourceKey ?? 'alarm-signal',
    label: point?.displayName ?? '关联测点',
    points: observations.map((observation) => ({ x: Date.parse(observation.sampledAt), value: typeof observation.value === 'number' ? observation.value : null })),
    tone: 'primary' as const,
  }];
  const annotations = temperatureSignal ? [
    ...(setpoint === null ? [] : [{ value: setpoint, label: `设定 ${setpoint}`, tone: 'info' as const }]),
    ...(trigger === null ? [] : [{ value: trigger, label: `触发 ${trigger}`, tone: 'error' as const }]),
    ...(recovery === null ? [] : [{ value: recovery, label: `恢复 ${recovery}`, tone: 'success' as const }]),
  ] : [];

  return (
    <div className="space-y-4">
      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        {[
          ['当前值', currentValue === null ? '—' : `${currentValue}°C`],
          ['设定值', setpoint === null ? '—' : `${setpoint}°C`],
          ['触发阈值', trigger === null ? '—' : `>${trigger}°C`],
          ['恢复阈值', recovery === null ? '—' : `<${recovery}°C`],
        ].map(([label, value]) => (
          <div key={label} className="rounded-md border p-3"><span className="text-[11px] text-muted-foreground">{label}</span><strong className="mt-1 block text-sm font-semibold tabular-nums">{value}</strong></div>
        ))}
      </div>
      {!historyAllowed ? <EmptyTrend>当前账号无历史趋势权限</EmptyTrend>
        : !point ? <EmptyTrend>关联设备没有可用于趋势的数值测点</EmptyTrend>
          : query.isPending ? <div className="flex min-h-32 items-center justify-center gap-2 rounded-md border border-dashed text-xs text-muted-foreground"><LoaderCircle className="size-4 animate-spin" />正在读取告警前后趋势</div>
            : query.isError ? <div className="flex min-h-32 items-center justify-center gap-2 rounded-md border border-warning/30 bg-warning/5 px-4 text-xs text-warning"><AlertTriangle className="size-4" />告警趋势暂不可用</div>
              : observations.length === 0 ? <EmptyTrend>告警发生窗口暂无历史采样</EmptyTrend>
                : (
                  <div>
                    <div className="mb-3 flex flex-wrap items-center justify-between gap-2"><strong className="text-xs font-medium">{point.displayName}</strong><span className="text-[11px] text-muted-foreground">告警发生前 15 分钟至发生后 90 分钟</span></div>
                    <TimeSeriesChart
                      series={series}
                      unit={point.unit ?? undefined}
                      annotations={annotations}
                      xLabelFormatter={(value) => formatClock(Number(value), site.timezone)}
                      style={{ width: '100%', height: 190 }}
                      ariaLabel={`${point.displayName} 告警前后趋势`}
                    />
                  </div>
                )}
    </div>
  );
}
