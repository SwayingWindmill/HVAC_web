import { useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { Activity, ChevronRight, Copy, Download, FileText, RefreshCw, ShieldCheck } from 'lucide-react';
import { toast } from 'sonner';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import {
  describeRemoteDiagError,
  gatewayHostMetricsKey,
  gatewayRemoteDiagApi,
  triggerTextDownload,
} from './gateway-remote-diagnostics-api';

const MAX_BYTE_OPTIONS = [
  { value: '16384', label: '16 KiB' },
  { value: '65536', label: '64 KiB' },
  { value: '131072', label: '128 KiB' },
  { value: '262144', label: '256 KiB（上限）' },
] as const;

const SINCE_MINUTE_OPTIONS = [
  { value: '15', label: '最近 15 分钟' },
  { value: '60', label: '最近 1 小时' },
  { value: '240', label: '最近 4 小时' },
  { value: '1440', label: '最近 24 小时' },
] as const;

function formatUptime(seconds: number): string {
  const hrs = Math.floor(seconds / 3600);
  const mins = Math.floor((seconds % 3600) / 60);
  if (hrs >= 24) return `${Math.floor(hrs / 24)} 天 ${hrs % 24} 小时`;
  if (hrs > 0) return `${hrs} 小时 ${mins} 分钟`;
  return `${mins} 分钟`;
}

function formatDateTimeShort(value: string): string {
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  }).format(new Date(value));
}

export function GatewayHostMetricsSection({ gatewayId }: { readonly gatewayId: string }) {
  const metricsQuery = useQuery({
    queryKey: gatewayHostMetricsKey(gatewayId),
    queryFn: ({ signal }) => gatewayRemoteDiagApi.getHostMetrics(gatewayId, 12, signal),
  });

  const latest = metricsQuery.data?.latest ?? null;

  return (
    <section aria-label="网关宿主及链路健康" className="border-b border-divider px-6 py-4">
      <details open className="group">
        <summary className="flex cursor-pointer list-none items-center justify-between gap-2 text-sm font-semibold outline-none select-none [&::-webkit-details-marker]:hidden">
          <span className="inline-flex items-center gap-1.5">
            <ChevronRight className="size-4 text-muted-foreground transition-transform duration-150 group-open:rotate-90 motion-reduce:transition-none" aria-hidden="true" />
            <Activity className="size-4 text-primary" aria-hidden="true" />
            网关宿主及链路健康
          </span>
          {latest ? (
            <Badge variant="outline" className="font-mono text-[11px]">
              {latest.mqttTlsVersion || 'TLSv1.3'} · {latest.mqttRttMs.toFixed(1)} ms
            </Badge>
          ) : null}
        </summary>
        <div className="mt-3 flex flex-col gap-3">
          <div className="flex items-center justify-between gap-2 text-xs text-muted-foreground">
            <span>
              {latest ? `最近采样 ${formatDateTimeShort(latest.collectedAt)} · 连续运行 ${formatUptime(latest.uptimeSeconds)}` : '包含边缘宿主机资源、RS485 总线误码与 MQTT 链路延迟'}
            </span>
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={metricsQuery.isFetching}
              onClick={() => void metricsQuery.refetch()}
            >
              <RefreshCw data-icon="inline-start" aria-hidden="true" />
              刷新指标
            </Button>
          </div>

          {metricsQuery.isPending ? (
            <Skeleton className="h-24 rounded-lg" />
          ) : metricsQuery.isError ? (
            <Alert variant="destructive">
              <AlertDescription>{describeRemoteDiagError(metricsQuery.error, '网关宿主指标暂时不可用。')}</AlertDescription>
            </Alert>
          ) : !latest ? (
            <p className="rounded-lg border border-dashed px-4 py-3 text-xs text-muted-foreground">
              暂无宿主机与南向链路健康快照上报
            </p>
          ) : (
            <div className="grid grid-cols-2 gap-2.5 sm:grid-cols-3">
              <div className="rounded-lg border bg-muted/40 px-3 py-2">
                <div className="text-[11px] text-muted-foreground">CPU / 内存</div>
                <div className="mt-0.5 font-mono text-sm font-semibold tabular-nums">
                  {latest.cpuPercent.toFixed(1)}% / {latest.memoryUsedPercent.toFixed(1)}%
                </div>
              </div>
              <div className="rounded-lg border bg-muted/40 px-3 py-2">
                <div className="text-[11px] text-muted-foreground">磁盘使用率</div>
                <div className="mt-0.5 font-mono text-sm font-semibold tabular-nums">
                  {latest.diskUsedPercent.toFixed(1)}%
                </div>
              </div>
              <div className="rounded-lg border bg-muted/40 px-3 py-2">
                <div className="text-[11px] text-muted-foreground">RS485 误码率</div>
                <div className="mt-0.5 font-mono text-sm font-semibold tabular-nums">
                  {latest.rs485ErrorRatePpm.toFixed(1)} PPM
                </div>
                <div className="text-[11px] text-muted-foreground tabular-nums">
                  错帧 {latest.rs485RxErrors} / 总帧 {latest.rs485TxFrames}
                </div>
              </div>
              <div className="rounded-lg border bg-muted/40 px-3 py-2">
                <div className="text-[11px] text-muted-foreground">MQTT 链路 RTT</div>
                <div className="mt-0.5 font-mono text-sm font-semibold tabular-nums">
                  {latest.mqttRttMs.toFixed(1)} ms
                </div>
                <div className="text-[11px] text-muted-foreground tabular-nums">
                  重连 {latest.mqttReconnectCount} 次
                </div>
              </div>
              <div className="col-span-2 rounded-lg border bg-muted/40 px-3 py-2">
                <div className="text-[11px] text-muted-foreground">1Hz 高频环形缓存</div>
                <div className="mt-0.5 font-mono text-sm font-semibold tabular-nums">
                  {latest.activeRingPoints} 点位 · {Math.round(latest.ringBufferBytes / 1024)} KiB
                </div>
              </div>
            </div>
          )}
        </div>
      </details>
    </section>
  );
}

export function GatewayLogPullSection({
  gatewayId,
  csrfToken,
  canWrite,
}: {
  readonly gatewayId: string;
  readonly csrfToken: string;
  readonly canWrite: boolean;
}) {
  const [maxBytes, setMaxBytes] = useState('65536');
  const [sinceMinutes, setSinceMinutes] = useState('60');

  const pullMutation = useMutation({
    mutationFn: () =>
      gatewayRemoteDiagApi.pullLogs(
        gatewayId,
        { maxBytes: Number(maxBytes), sinceMinutes: Number(sinceMinutes) },
        csrfToken,
      ),
    onSuccess: (res) => {
      toast.success(`已安全拉取 ${res.returnedBytes} B 脱敏日志（脱敏 ${res.redactedCount} 处）`);
    },
    onError: (err) => {
      toast.error(describeRemoteDiagError(err, '端侧日志拉取失败，请检查权限或网关在线状态'));
    },
  });

  const bundle = pullMutation.data ?? null;

  return (
    <section aria-label="端侧日志按需拉取" className="border-b border-divider px-6 py-4">
      <details className="group">
        <summary className="flex cursor-pointer list-none items-center justify-between gap-2 text-sm font-semibold outline-none select-none [&::-webkit-details-marker]:hidden">
          <span className="inline-flex items-center gap-1.5">
            <ChevronRight className="size-4 text-muted-foreground transition-transform duration-150 group-open:rotate-90 motion-reduce:transition-none" aria-hidden="true" />
            <FileText className="size-4 text-primary" aria-hidden="true" />
            端侧日志按需拉取
          </span>
          <Badge variant="outline" className="text-[11px]">
            <ShieldCheck className="mr-1 size-3" aria-hidden="true" />
            受控只读 · 自动脱敏
          </Badge>
        </summary>

        <div className="mt-3 flex flex-col gap-3">
          <p className="text-xs text-muted-foreground">
            严格限定字节上限与回溯时间范围，并在端侧和平台双端剥离密钥与凭据后写入操作审计（禁止远程交互 Shell）。
          </p>

          <div className="flex flex-wrap items-center gap-2">
            <Select value={maxBytes} onValueChange={setMaxBytes} disabled={!canWrite || pullMutation.isPending}>
              <SelectTrigger aria-label="日志字节上限" className="h-8 w-36">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {MAX_BYTE_OPTIONS.map((opt) => (
                  <SelectItem key={opt.value} value={opt.value}>{opt.label}</SelectItem>
                ))}
              </SelectContent>
            </Select>

            <Select value={sinceMinutes} onValueChange={setSinceMinutes} disabled={!canWrite || pullMutation.isPending}>
              <SelectTrigger aria-label="日志时间范围" className="h-8 w-36">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {SINCE_MINUTE_OPTIONS.map((opt) => (
                  <SelectItem key={opt.value} value={opt.value}>{opt.label}</SelectItem>
                ))}
              </SelectContent>
            </Select>

            <Button
              type="button"
              size="sm"
              disabled={!canWrite || pullMutation.isPending}
              onClick={() => pullMutation.mutate()}
            >
              {pullMutation.isPending ? '正在拉取…' : '按需拉取脱敏日志'}
            </Button>
          </div>

          {!canWrite ? (
            <p className="text-xs text-muted-foreground">
              当前账号缺少设备写入诊断权限（`device.write`），仅可查看网关基础状态，拉取请求将被服务端拦截（403）。
            </p>
          ) : null}

          {pullMutation.isError ? (
            <Alert variant="destructive">
              <AlertDescription>{describeRemoteDiagError(pullMutation.error, '端侧日志拉取失败。')}</AlertDescription>
            </Alert>
          ) : null}

          {bundle ? (
            <div className="flex flex-col gap-2 rounded-lg border bg-muted/40 p-3">
              <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-mono tabular-nums">{bundle.returnedBytes} B</span>
                  <Badge variant="outline">已脱敏 {bundle.redactedCount} 处凭据</Badge>
                  {bundle.truncated ? <span className="text-warning-text">已按字节上限截断最早日志</span> : null}
                </div>
                <div className="flex items-center gap-1.5">
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => {
                      void navigator.clipboard.writeText(bundle.content);
                      toast.success('脱敏日志片段已复制');
                    }}
                  >
                    <Copy data-icon="inline-start" aria-hidden="true" />
                    复制日志
                  </Button>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() =>
                      triggerTextDownload(
                        `gateway-${gatewayId.slice(-8)}-diagnostics.log`,
                        'text/plain;charset=utf-8',
                        bundle.content,
                      )
                    }
                  >
                    <Download data-icon="inline-start" aria-hidden="true" />
                    下载文件
                  </Button>
                </div>
              </div>
              <pre
                role="region"
                aria-label="端侧脱敏日志内容"
                tabIndex={0}
                className="max-h-52 overflow-auto rounded-md border bg-background p-2.5 font-mono text-xs leading-relaxed break-all whitespace-pre-wrap"
              >
                <code>{bundle.content || '（所选时间窗内无新增日志输出）'}</code>
              </pre>
            </div>
          ) : null}
        </div>
      </details>
    </section>
  );
}
