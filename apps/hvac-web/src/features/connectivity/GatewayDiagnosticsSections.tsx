import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ChevronRight, Copy, RefreshCw } from 'lucide-react';
import { notifySuccess } from '@/lib/toast';
import type { GatewayCapturedPacket } from '@/api/generated/platformGateway.gen';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { cn } from '@/lib/utils';
import {
  describeGatewayDiagnosticsError,
  gatewayDiagnosticsApi,
  gatewayPacketCaptureKey,
} from './gateway-credentials';
import {
  copyPayloadText,
  DiagnosticChip,
  GatewayQuarantineSection,
  PAYLOAD_ENCODING_LABELS,
  type DiagnosticTone,
} from './GatewayQuarantineSection';

export { GatewayQuarantineSection };

const DURATION_OPTIONS = [
  { value: '60', seconds: 60, label: '1 分钟' },
  { value: '300', seconds: 300, label: '5 分钟' },
  { value: '900', seconds: 900, label: '15 分钟' },
] as const;

const PACKET_FILTERS = [
  { value: 'all', label: '全部' },
  { value: 'UPLINK', label: '上行' },
  { value: 'DOWNLINK', label: '下行' },
  { value: 'QUARANTINE', label: '接收隔离' },
] as const;

type PacketFilter = (typeof PACKET_FILTERS)[number]['value'];

const PACKET_DIRECTION_LABELS: Readonly<Record<GatewayCapturedPacket['direction'], string>> = {
  UPLINK: '上行',
  DOWNLINK: '下行',
};

const PACKET_CHANNEL_META: Readonly<
  Record<GatewayCapturedPacket['channel'], { readonly label: string; readonly tone: DiagnosticTone }>
> = {
  TELEMETRY: { label: '上行读数', tone: 'neutral' },
  EVENT: { label: '上行事件', tone: 'neutral' },
  COMMAND: { label: '下行指令', tone: 'info' },
  COMMAND_REPLY: { label: '指令返回结果', tone: 'success' },
  CONCENTRATOR: { label: '采集器配置', tone: 'info' },
  QUARANTINE: { label: '接收隔离', tone: 'danger' },
};

const shortTimeFormatter = new Intl.DateTimeFormat('zh-CN', {
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
  hour12: false,
});

function formatShortTime(value: string): string {
  const parsed = Date.parse(value);
  return Number.isNaN(parsed) ? value : shortTimeFormatter.format(parsed);
}

function formatRemainingCountdown(expiresAt: string, nowMs: number): string {
  const expiryMs = Date.parse(expiresAt);
  if (Number.isNaN(expiryMs)) return '00:00';
  const remainingSeconds = Math.max(0, Math.ceil((expiryMs - nowMs) / 1000));
  const minutes = Math.floor(remainingSeconds / 60);
  const seconds = remainingSeconds % 60;
  return `${String(minutes).padStart(2, '0')}:${String(seconds).padStart(2, '0')}`;
}

function matchesPacketFilter(packet: GatewayCapturedPacket, filter: PacketFilter): boolean {
  switch (filter) {
    case 'all':
      return true;
    case 'UPLINK':
      return packet.direction === 'UPLINK';
    case 'DOWNLINK':
      return packet.direction === 'DOWNLINK';
    case 'QUARANTINE':
      return packet.channel === 'QUARANTINE';
  }
}

/** Live bounded-TTL packet capture console inside the gateway detail sheet. */
export function GatewayPacketCaptureSection({
  gatewayId,
  csrfToken,
  canWrite,
}: {
  readonly gatewayId: string;
  readonly csrfToken: string;
  readonly canWrite: boolean;
}) {
  const queryClient = useQueryClient();
  const [selectedDuration, setSelectedDuration] = useState<(typeof DURATION_OPTIONS)[number]['value']>('300');
  const [filter, setFilter] = useState<PacketFilter>('all');
  const [nowMs, setNowMs] = useState(() => Date.now());

  const captureQuery = useQuery({
    queryKey: gatewayPacketCaptureKey(gatewayId),
    queryFn: ({ signal }) => gatewayDiagnosticsApi.getPacketCapture(gatewayId, signal),
    retry: false,
    refetchInterval: (query) => (query.state.data?.active ? 2000 : false),
  });

  const capture = captureQuery.data;
  const activeExpiresAt = capture?.active ? capture.expiresAt : undefined;

  useEffect(() => {
    if (!activeExpiresAt) return undefined;
    setNowMs(Date.now());
    const timer = window.setInterval(() => {
      setNowMs(Date.now());
    }, 1000);
    return () => window.clearInterval(timer);
  }, [activeExpiresAt]);

  const startMutation = useMutation({
    mutationFn: (seconds: number) =>
      gatewayDiagnosticsApi.startPacketCapture(gatewayId, seconds, csrfToken),
    onSuccess: (snapshot) => {
      queryClient.setQueryData(gatewayPacketCaptureKey(gatewayId), snapshot);
      notifySuccess('已开启通信抓包');
    },
  });

  const stopMutation = useMutation({
    mutationFn: () => gatewayDiagnosticsApi.stopPacketCapture(gatewayId, csrfToken),
    onSuccess: (snapshot) => {
      queryClient.setQueryData(gatewayPacketCaptureKey(gatewayId), snapshot);
      notifySuccess('已停止通信抓包');
    },
  });

  const mutating = startMutation.isPending || stopMutation.isPending;
  const activeOption = DURATION_OPTIONS.find((item) => item.value === selectedDuration) ?? DURATION_OPTIONS[1];
  const packets = capture?.items ?? [];
  const filteredPackets = packets.filter((item) => matchesPacketFilter(item, filter));

  return (
    <section aria-label="通信抓包" className="flex flex-col gap-3 border-b border-divider px-6 py-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <h3 className="text-sm font-semibold">通信抓包</h3>
          {capture ? (
            <span
              className={cn(
                'inline-flex h-5.5 shrink-0 items-center gap-1.5 rounded-md px-1.5 text-xs font-medium whitespace-nowrap',
                capture.active
                  ? 'bg-success-soft text-success-text'
                  : 'bg-secondary text-secondary-foreground',
              )}
            >
              <span
                className={cn(
                  'size-1.5 rounded-full',
                  capture.active ? 'bg-success' : 'bg-subtle-foreground',
                )}
                aria-hidden="true"
              />
              {capture.active ? '抓包中' : '未开启'}
            </span>
          ) : null}
          {capture?.active && capture.expiresAt ? (
            <span className="font-mono text-xs text-muted-foreground tabular-nums">
              剩余 {formatRemainingCountdown(capture.expiresAt, nowMs)}
            </span>
          ) : null}
        </div>
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={captureQuery.isFetching}
          onClick={() => void captureQuery.refetch()}
        >
          <RefreshCw
            data-icon="inline-start"
            className={captureQuery.isFetching ? 'motion-safe:animate-spin' : undefined}
            aria-hidden="true"
          />
          刷新报文
        </Button>
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <label htmlFor="packet-capture-duration" className="text-xs font-medium text-muted-foreground">
          抓包时长
        </label>
        <Select
          value={selectedDuration}
          disabled={!canWrite || mutating || Boolean(capture?.active)}
          onValueChange={(next) => {
            const matched = DURATION_OPTIONS.find((item) => item.value === next);
            if (matched) setSelectedDuration(matched.value);
          }}
        >
          <SelectTrigger id="packet-capture-duration" className="h-7 min-w-28 text-xs">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {DURATION_OPTIONS.map((option) => (
              <SelectItem key={option.value} value={option.value}>
                {option.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>

        {capture?.active ? (
          <Button
            type="button"
            size="sm"
            disabled={!canWrite || mutating}
            onClick={() => {
              startMutation.reset();
              stopMutation.mutate();
            }}
          >
            {stopMutation.isPending ? '正在停止…' : '停止抓包'}
          </Button>
        ) : (
          <Button
            type="button"
            size="sm"
            disabled={!canWrite || mutating || captureQuery.isPending}
            onClick={() => {
              stopMutation.reset();
              startMutation.mutate(activeOption.seconds);
            }}
          >
            {startMutation.isPending ? '正在开启…' : '开启抓包'}
          </Button>
        )}
      </div>

      {!canWrite ? (
        <p className="text-xs text-muted-foreground">
          当前账号仅有查看权限，不可启停抓包。
        </p>
      ) : null}

      {startMutation.isError || stopMutation.isError ? (
        <Alert variant="destructive">
          <AlertDescription>
            {describeGatewayDiagnosticsError(
              startMutation.error ?? stopMutation.error,
              '抓包状态没有更新，请确认权限和网关状态后重试。',
            )}
          </AlertDescription>
        </Alert>
      ) : null}

      {capture && capture.droppedCount > 0 ? (
        <p className="text-xs text-muted-foreground tabular-nums">
          缓冲区最多保留 {capture.maxPackets} 条，已覆盖最早 {capture.droppedCount} 条。
        </p>
      ) : null}

      <div className="flex flex-wrap items-center justify-between gap-2">
        <ToggleGroup
          type="single"
          size="sm"
          variant="outline"
          value={filter}
          onValueChange={(next) => {
            const matched = PACKET_FILTERS.find((item) => item.value === next);
            if (matched) setFilter(matched.value);
          }}
          aria-label="报文方向筛选"
        >
          {PACKET_FILTERS.map((item) => (
            <ToggleGroupItem
              key={item.value}
              value={item.value}
              className="text-xs data-[state=on]:border-primary-line data-[state=on]:bg-primary-soft data-[state=on]:text-primary"
            >
              {item.label}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
        {capture ? (
          <span className="text-xs text-muted-foreground tabular-nums">
            共 {filteredPackets.length} 条
          </span>
        ) : null}
      </div>

      {captureQuery.isPending ? (
        <div aria-busy="true" aria-label="正在读取抓包记录">
          <Skeleton className="h-24 rounded-lg" />
        </div>
      ) : captureQuery.isError ? (
        <Alert variant="destructive">
          <AlertDescription className="flex flex-wrap items-center justify-between gap-2">
            <span>
              {describeGatewayDiagnosticsError(
                captureQuery.error,
                '抓包记录暂时读不到，请确认权限和服务状态后重试。',
              )}
            </span>
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={captureQuery.isFetching}
              onClick={() => void captureQuery.refetch()}
            >
              重试
            </Button>
          </AlertDescription>
        </Alert>
      ) : packets.length === 0 ? (
        <p className="rounded-lg border border-dashed px-4 py-3 text-xs text-muted-foreground">
          暂无抓包记录，点击“开启抓包”后可查看该网关实时收发的原始报文。
        </p>
      ) : filteredPackets.length === 0 ? (
        <p className="rounded-lg border border-dashed px-4 py-3 text-xs text-muted-foreground">
          当前筛选条件下暂无报文，可切换为“全部”查看已捕获记录。
        </p>
      ) : (
        <ul aria-label="抓包记录" className="flex flex-col gap-2">
          {filteredPackets.map((item) => {
            const channelMeta = PACKET_CHANNEL_META[item.channel];
            return (
              <li key={item.sequence}>
                <details className="group rounded-lg border bg-muted/40">
                  <summary className="flex cursor-pointer list-none flex-wrap items-center gap-2 rounded-lg px-3.5 py-2.5 text-xs outline-none select-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring-soft [&::-webkit-details-marker]:hidden">
                    <ChevronRight
                      className="size-3.5 shrink-0 text-muted-foreground transition-transform duration-150 group-open:rotate-90 motion-reduce:transition-none"
                      aria-hidden="true"
                    />
                    <DiagnosticChip label={channelMeta.label} tone={channelMeta.tone} />
                    <span className="font-medium text-foreground">
                      {PACKET_DIRECTION_LABELS[item.direction]}
                    </span>
                    <span className="font-mono text-muted-foreground tabular-nums">
                      {formatShortTime(item.capturedAt)}
                    </span>
                    <span className="font-mono text-muted-foreground tabular-nums">
                      {item.payloadBytes} B
                    </span>
                    <span className="min-w-0 flex-1 truncate font-mono text-muted-foreground">
                      {item.topic}
                    </span>
                  </summary>
                  <div className="flex flex-col gap-2 border-t border-divider px-3.5 py-3">
                    <div className="break-all font-mono text-xs text-foreground">
                      {item.topic}
                    </div>
                    <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
                      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
                        <span>内容编码：{PAYLOAD_ENCODING_LABELS[item.payloadEncoding]}</span>
                        <span className="font-mono tabular-nums">
                          内容指纹 {item.payloadSha256.slice(0, 12)}
                        </span>
                        {item.traceId ? (
                          <span className="font-mono tabular-nums">
                            链路标识 {item.traceId.slice(0, 12)}
                          </span>
                        ) : null}
                        {item.payloadTruncated ? (
                          <span className="text-warning-text">已截断至 4 KiB</span>
                        ) : null}
                      </div>
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        aria-label={`复制报文 ${item.sequence}`}
                        disabled={item.payloadEncoding === 'empty' && !item.payloadPreview}
                        onClick={() => copyPayloadText(item.payloadPreview, '报文内容已复制')}
                      >
                        <Copy data-icon="inline-start" aria-hidden="true" />
                        复制报文
                      </Button>
                    </div>
                    <pre
                      role="region"
                      aria-label={`报文 ${item.sequence} 原始内容`}
                      tabIndex={0}
                      className="max-h-48 overflow-auto rounded-md border bg-background p-2.5 font-mono text-xs leading-relaxed break-all whitespace-pre-wrap"
                    >
                      <code>
                        {item.payloadEncoding === 'empty' ? '（空报文）' : item.payloadPreview}
                      </code>
                    </pre>
                  </div>
                </details>
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}
