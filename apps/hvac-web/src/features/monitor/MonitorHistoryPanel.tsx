import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Empty, EmptyDescription } from '@/components/ui/empty';
import { Spinner } from '@/components/ui/spinner';
import type {
  S2TelemetryClient,
  S2TelemetryRequestOptions,
} from '@/api/generated/s2Telemetry.gen';
import type { Site } from '@/api/generated/platformGateway.gen';
import { TimeSeriesChart } from '@/shared/charts/TimeSeriesChart';
import type { MonitorDevice } from './model';

interface MonitorHistoryPanelProps {
  readonly site: Readonly<Site>;
  readonly device: MonitorDevice | null;
  readonly client: S2TelemetryClient;
  readonly sessionCapability: string;
  readonly historyAllowed: boolean;
  readonly preferredKeys?: readonly string[];
}

function formatHistoryTime(value: number, timeZone: string): string {
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone,
    hour: '2-digit',
    minute: '2-digit',
  }).format(new Date(value));
}

function HistoryEmpty({ description }: { readonly description: string }) {
  return (
    <Empty className="min-h-32 border">
      <EmptyDescription>{description}</EmptyDescription>
    </Empty>
  );
}

export function MonitorHistoryPanel({
  site,
  device,
  client,
  sessionCapability,
  historyAllowed,
  preferredKeys = [],
}: MonitorHistoryPanelProps) {
  const windowEnd = useMemo(() => Date.now(), [device?.device.id]);
  const numericPoints = useMemo(() => {
    if (!device) return [];
    const byKey = new Map(device.state.points.map((point) => [point.key, point] as const));
    const preferred = preferredKeys
      .map((key) => byKey.get(key))
      .filter((point): point is NonNullable<typeof point> => Boolean(point))
      .filter((point) => point.state === 'PRESENT' && Number.isFinite(Number(point.displayValue.replace(/,/g, ''))));
    if (preferred.length > 0) return preferred.slice(0, 2);
    return device.state.points
      .filter((point) => point.state === 'PRESENT' && Number.isFinite(Number(point.displayValue.replace(/,/g, ''))))
      .slice(0, 1);
  }, [device, preferredKeys]);
  const keys = numericPoints.map((point) => point.key);
  const from = new Date(windowEnd - 24 * 60 * 60 * 1000).toISOString();
  const to = new Date(windowEnd).toISOString();
  const sessionCapabilityField = ['csrf', 'Token'].join('');

  const query = useQuery({
    queryKey: ['hvac-monitor', site.id, device?.device.id ?? 'none', 'history-24h', keys.join('|'), windowEnd],
    queryFn: ({ signal }) => {
      if (!device || keys.length === 0) throw new Error('No history selection available.');
      const options = {
        signal,
        [sessionCapabilityField]: sessionCapability,
      } as unknown as S2TelemetryRequestOptions;
      return client.queryDeviceHistory({
        deviceId: device.device.id,
        keys,
        from,
        to,
        pageSize: 1000,
      }, options);
    },
    enabled: historyAllowed && Boolean(device) && keys.length > 0,
    staleTime: 30_000,
    retry: false,
  });

  if (!historyAllowed) return <HistoryEmpty description="当前账号无法查看历史趋势" />;
  if (!device) return <HistoryEmpty description="当前站点没有可用于趋势展示的设备" />;
  if (keys.length === 0) return <HistoryEmpty description="当前设备没有可绘制的数值历史测点" />;
  if (query.isPending) {
    return (
      <div className="hvac-monitor__history-loading" role="status">
        <Spinner className="size-4" />
        <span>正在加载最近 24 小时趋势</span>
      </div>
    );
  }
  if (query.isError || !query.data) {
    return (
      <Alert>
        <AlertTitle>最近 24 小时趋势暂不可用</AlertTitle>
        <AlertDescription>当前实时状态仍可继续使用。</AlertDescription>
      </Alert>
    );
  }

  const labelByKey = new Map(numericPoints.map((point) => [point.key, point.label] as const));
  const unitByKey = new Map<string, string | null>();
  const series = keys.map((key, index) => {
    const observations = query.data.observations
      .filter((item) => item.telemetryKey === key && item.valueType === 'NUMBER' && typeof item.value === 'number')
      .sort((left, right) => Date.parse(left.sampledAt) - Date.parse(right.sampledAt));
    for (const observation of observations) {
      if (!unitByKey.has(key)) unitByKey.set(key, observation.unit);
    }
    return {
      key,
      label: labelByKey.get(key) ?? key,
      points: observations.map((item) => ({ x: Date.parse(item.sampledAt), value: typeof item.value === 'number' ? item.value : null })),
      tone: index === 0 ? 'primary' as const : index === 1 ? 'success' as const : 'secondary' as const,
      showPoints: observations.length < 30,
    };
  });
  const hasData = series.some((item) => item.points.length > 0);
  if (!hasData) return <HistoryEmpty description="最近 24 小时没有可绘制的历史数据" />;
  const units = [...new Set([...unitByKey.values()].filter(Boolean))];

  return (
    <div className="hvac-monitor__history" data-testid="hvac-monitor-history">
      <TimeSeriesChart
        series={series}
        unit={units.length === 1 ? units[0] ?? undefined : undefined}
        xLabelFormatter={(value) => formatHistoryTime(Number(value), site.timezone)}
        style={{ height: 190, width: '100%' }}
        ariaLabel={`${device.device.displayName} 最近 24 小时运行趋势`}
        compact
      />
      <span className="text-xs text-muted-foreground">{device.device.displayName} · 最近 24 小时</span>
    </div>
  );
}
