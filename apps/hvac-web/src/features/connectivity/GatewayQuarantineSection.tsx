import { useQuery } from '@tanstack/react-query';
import { ChevronRight, Copy, RefreshCw } from 'lucide-react';
import { notifyError, notifySuccess } from '@/lib/toast';
import type {
  GatewayCapturedPacket,
  GatewayQuarantineRecord,
} from '@/api/generated/platformGateway.gen';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { cn } from '@/lib/utils';
import {
  describeGatewayDiagnosticsError,
  gatewayDiagnosticsApi,
  gatewayQuarantineKey,
} from './gateway-credentials';

export type DiagnosticTone = 'success' | 'warning' | 'danger' | 'info' | 'neutral';

const TONE_STYLE: Readonly<Record<DiagnosticTone, { readonly chip: string; readonly dot: string }>> = {
  success: { chip: 'bg-success-soft text-success-text', dot: 'bg-success' },
  warning: { chip: 'bg-warning-soft text-warning-text', dot: 'bg-warning' },
  danger: { chip: 'bg-destructive-soft text-destructive-text', dot: 'bg-destructive' },
  info: { chip: 'bg-primary-soft text-primary', dot: 'bg-primary' },
  neutral: { chip: 'bg-secondary text-secondary-foreground', dot: 'bg-subtle-foreground' },
};

export const PAYLOAD_ENCODING_LABELS: Readonly<Record<GatewayCapturedPacket['payloadEncoding'], string>> = {
  utf8: 'UTF-8',
  hex: '十六进制',
  empty: '空报文',
};

const QUARANTINE_REASONS: Readonly<
  Record<GatewayQuarantineRecord['reasonCode'], { readonly label: string; readonly tone: DiagnosticTone }>
> = {
  GATEWAY_CREDENTIAL_INACTIVE: { label: '证书未生效或已吊销', tone: 'danger' },
  MESSAGE_INVALID: { label: '报文格式或字段不合法', tone: 'warning' },
  GATEWAY_UNKNOWN: { label: '网关未登记', tone: 'neutral' },
  RATE_LIMITED: { label: '上报速率超限', tone: 'warning' },
};

const dateTimeFormatter = new Intl.DateTimeFormat('zh-CN', {
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
  hour12: false,
});

function formatDateTime(value: string): string {
  const parsed = Date.parse(value);
  return Number.isNaN(parsed) ? value : dateTimeFormatter.format(parsed);
}

export function DiagnosticChip({
  label,
  tone,
}: {
  readonly label: string;
  readonly tone: DiagnosticTone;
}) {
  const style = TONE_STYLE[tone];
  return (
    <span
      className={cn(
        'inline-flex h-5.5 shrink-0 items-center gap-1.5 rounded-md px-1.5 text-xs font-medium whitespace-nowrap',
        style.chip,
      )}
    >
      <span className={cn('size-1.5 rounded-full', style.dot)} aria-hidden="true" />
      {label}
    </span>
  );
}

export function copyPayloadText(value: string, successText: string) {
  if (!navigator.clipboard) {
    notifyError('当前环境不支持复制到剪贴板');
    return;
  }
  void navigator.clipboard.writeText(value).then(
    () => notifySuccess(successText),
    () => notifyError('无法复制内容，请手动选中后复制'),
  );
}

/** Recent uplink quarantine records rejected before entering telemetry state. */
export function GatewayQuarantineSection({ gatewayId }: { readonly gatewayId: string }) {
  const quarantineQuery = useQuery({
    queryKey: gatewayQuarantineKey(gatewayId),
    queryFn: ({ signal }) => gatewayDiagnosticsApi.listQuarantine(gatewayId, signal),
    retry: false,
  });

  const items = quarantineQuery.data?.items ?? [];

  return (
    <section aria-label="接收隔离" className="flex flex-col gap-3 border-b border-divider px-6 py-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="text-sm font-semibold">接收隔离</h3>
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={quarantineQuery.isFetching}
          onClick={() => void quarantineQuery.refetch()}
        >
          <RefreshCw
            data-icon="inline-start"
            className={quarantineQuery.isFetching ? 'motion-safe:animate-spin' : undefined}
            aria-hidden="true"
          />
          刷新隔离记录
        </Button>
      </div>

      {quarantineQuery.isPending ? (
        <div aria-busy="true" aria-label="正在读取接收隔离记录">
          <Skeleton className="h-24 rounded-lg" />
        </div>
      ) : quarantineQuery.isError ? (
        <Alert variant="destructive">
          <AlertDescription className="flex flex-wrap items-center justify-between gap-2">
            <span>
              {describeGatewayDiagnosticsError(
                quarantineQuery.error,
                '接收隔离记录暂时读不到，请确认权限和服务状态后重试。',
              )}
            </span>
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={quarantineQuery.isFetching}
              onClick={() => void quarantineQuery.refetch()}
            >
              重试
            </Button>
          </AlertDescription>
        </Alert>
      ) : items.length === 0 ? (
        <p className="rounded-lg border border-dashed px-4 py-3 text-xs text-muted-foreground">
          最近 7 天暂无接收隔离记录
        </p>
      ) : (
        <ul aria-label="接收隔离记录" className="flex flex-col gap-2">
          {items.map((item) => {
            const reason = QUARANTINE_REASONS[item.reasonCode];
            return (
              <li
                key={item.id}
                className="flex flex-col gap-2 rounded-lg border bg-muted/40 px-3.5 py-3"
              >
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <div className="flex flex-wrap items-center gap-2">
                    <DiagnosticChip label={reason.label} tone={reason.tone} />
                    <span className="text-xs font-medium text-foreground tabular-nums">
                      累计 {item.occurrenceCount} 次
                    </span>
                  </div>
                  <span className="text-xs text-muted-foreground">
                    最近收到{' '}
                    <span className="font-mono tabular-nums">
                      {formatDateTime(item.lastSeenAt)}
                    </span>
                  </span>
                </div>
                <div className="break-all font-mono text-xs text-foreground">
                  {item.topic}
                </div>
                <p className="break-words text-xs text-muted-foreground">{item.detail}</p>
                <details className="group">
                  <summary className="inline-flex cursor-pointer list-none items-center gap-1.5 rounded-md px-1 py-0.5 text-xs font-medium text-primary outline-none select-none hover:underline focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring-soft [&::-webkit-details-marker]:hidden">
                    <ChevronRight
                      className="size-3.5 shrink-0 transition-transform duration-150 group-open:rotate-90 motion-reduce:transition-none"
                      aria-hidden="true"
                    />
                    查看原始报文片段
                  </summary>
                  <div className="mt-2 flex flex-col gap-2 border-t border-divider pt-2.5">
                    <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
                      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
                        <span>内容编码：{PAYLOAD_ENCODING_LABELS[item.payloadEncoding]}</span>
                        <span className="font-mono tabular-nums">{item.payloadBytes} B</span>
                        <span className="font-mono tabular-nums">
                          内容指纹 {item.payloadSha256.slice(0, 12)}
                        </span>
                        {item.payloadTruncated ? (
                          <span className="text-warning-text">已截断至 4 KiB</span>
                        ) : null}
                      </div>
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        aria-label={`复制隔离报文 ${item.id}`}
                        disabled={item.payloadEncoding === 'empty' && !item.payloadPreview}
                        onClick={() => copyPayloadText(item.payloadPreview, '隔离报文片段已复制')}
                      >
                        <Copy data-icon="inline-start" aria-hidden="true" />
                        复制片段
                      </Button>
                    </div>
                    <pre
                      role="region"
                      aria-label={`隔离报文 ${item.id} 原始内容`}
                      tabIndex={0}
                      className="max-h-40 overflow-auto rounded-md border bg-background p-2.5 font-mono text-xs leading-relaxed break-all whitespace-pre-wrap"
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
