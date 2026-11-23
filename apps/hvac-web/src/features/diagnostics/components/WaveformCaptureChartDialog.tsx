import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Download } from 'lucide-react';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Skeleton } from '@/components/ui/skeleton';
import {
  WAVEFORM_SOURCE_LABELS,
  WAVEFORM_STATUS_LABELS,
  buildWaveformCSV,
  describeRemoteDiagError,
  gatewayRemoteDiagApi,
  gatewayWaveformCaptureDetailKey,
  triggerTextDownload,
} from '@/features/connectivity/gateway-remote-diagnostics-api';

const SERIES_COLORS = [
  'var(--chart-1)',
  'var(--chart-2)',
  'var(--chart-3)',
  'var(--chart-4)',
  'var(--chart-5)',
] as const;

export function WaveformCaptureChartDialog({
  gatewayId,
  captureId,
  timeZone,
  onClose,
}: {
  readonly gatewayId: string;
  readonly captureId: string | null;
  readonly timeZone: string;
  readonly onClose: () => void;
}) {
  const detailQuery = useQuery({
    queryKey: gatewayWaveformCaptureDetailKey(gatewayId, captureId ?? ''),
    queryFn: ({ signal }) => gatewayRemoteDiagApi.getWaveformCapture(gatewayId, captureId!, signal),
    enabled: Boolean(captureId),
  });

  const [hiddenSeries, setHiddenSeries] = useState<ReadonlySet<string>>(() => new Set());

  const capture = detailQuery.data ?? null;
  const seriesKeys = useMemo<readonly string[]>(() => {
    if (!capture) return [];
    const set = new Set<string>();
    for (const row of capture.samples) {
      for (const k of Object.keys(row.values)) set.add(k);
    }
    return Array.from(set).sort();
  }, [capture]);

  const activeKeys = useMemo(
    () => seriesKeys.filter((k) => !hiddenSeries.has(k)),
    [seriesKeys, hiddenSeries],
  );

  const chartGeometry = useMemo(() => {
    if (!capture || capture.samples.length === 0 || activeKeys.length === 0) return null;
    const width = 760;
    const height = 240;
    const padLeft = 48;
    const padRight = 18;
    const padTop = 18;
    const padBottom = 30;
    const plotW = width - padLeft - padRight;
    const plotH = height - padTop - padBottom;

    const minMs = capture.samples[0]!.sampledAtMs;
    const maxMs = capture.samples[capture.samples.length - 1]!.sampledAtMs;
    const spanMs = Math.max(maxMs - minMs, 1);

    let minY = Infinity;
    let maxY = -Infinity;
    for (const row of capture.samples) {
      for (const key of activeKeys) {
        const val = row.values[key];
        if (val !== undefined && Number.isFinite(val)) {
          if (val < minY) minY = val;
          if (val > maxY) maxY = val;
        }
      }
    }
    if (!Number.isFinite(minY) || !Number.isFinite(maxY)) return null;
    if (minY === maxY) {
      minY -= 1;
      maxY += 1;
    }
    const ySpan = maxY - minY;
    const triggerMs = Date.parse(capture.triggeredAt);
    const triggerX =
      Number.isFinite(triggerMs) && triggerMs >= minMs && triggerMs <= maxMs
        ? padLeft + ((triggerMs - minMs) / spanMs) * plotW
        : null;

    const paths = activeKeys.map((key, idx) => {
      const points = capture.samples
        .map((row) => {
          const val = row.values[key];
          if (val === undefined || !Number.isFinite(val)) return null;
          const x = padLeft + ((row.sampledAtMs - minMs) / spanMs) * plotW;
          const y = padTop + (1 - (val - minY) / ySpan) * plotH;
          return `${x.toFixed(1)},${y.toFixed(1)}`;
        })
        .filter((pt): pt is string => pt !== null)
        .join(' ');
      return { key, color: SERIES_COLORS[idx % SERIES_COLORS.length]!, points };
    });

    return { width, height, padLeft, padTop, plotW, plotH, minY, maxY, minMs, maxMs, triggerX, paths };
  }, [capture, activeKeys]);

  const formatTimeLabel = (ms: number) =>
    new Intl.DateTimeFormat('zh-CN', {
      timeZone,
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hour12: false,
    }).format(new Date(ms));

  return (
    <Dialog open={Boolean(captureId)} onOpenChange={(open) => { if (!open) onClose(); }}>
      <DialogContent className="sm:max-w-[840px]">
        <DialogHeader>
          <DialogTitle>瞬态高频波形曲线分析</DialogTitle>
          <DialogDescription>
            秒级环形缓存抓拍回流样本，独立存档且不计入常规小时能耗与在线率口径。
          </DialogDescription>
        </DialogHeader>

        {detailQuery.isPending ? (
          <Skeleton className="h-64 rounded-lg" />
        ) : detailQuery.isError ? (
          <Alert variant="destructive">
            <AlertDescription>{describeRemoteDiagError(detailQuery.error, '波形回流详情暂时读不到')}</AlertDescription>
          </Alert>
        ) : capture ? (
          <div className="flex flex-col gap-4">
            <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border bg-muted/40 px-3.5 py-2.5 text-xs">
              <div className="flex flex-wrap items-center gap-2">
                <Badge variant="outline">{WAVEFORM_SOURCE_LABELS[capture.triggerSource]}</Badge>
                <Badge variant="outline">{WAVEFORM_STATUS_LABELS[capture.status]}</Badge>
                <span className="font-medium text-foreground">{capture.triggerReason}</span>
              </div>
              <div className="font-mono text-muted-foreground tabular-nums">
                {capture.sampleRateHz} Hz · {capture.sampleCount} 样本
              </div>
            </div>

            {seriesKeys.length > 0 ? (
              <div className="flex flex-wrap items-center gap-2">
                {seriesKeys.map((key, idx) => {
                  const active = !hiddenSeries.has(key);
                  return (
                    <button
                      key={key}
                      type="button"
                      onClick={() =>
                        setHiddenSeries((prev) => {
                          const next = new Set(prev);
                          if (next.has(key)) next.delete(key);
                          else next.add(key);
                          return next;
                        })
                      }
                      className={`inline-flex items-center gap-1.5 rounded-md border px-2.5 py-1 text-xs font-mono transition-colors ${active ? 'bg-background text-foreground' : 'bg-muted/40 text-muted-foreground opacity-60'}`}
                    >
                      <span
                        className="size-2 rounded-full"
                        style={{ backgroundColor: SERIES_COLORS[idx % SERIES_COLORS.length] }}
                        aria-hidden="true"
                      />
                      {key}
                    </button>
                  );
                })}
              </div>
            ) : null}

            {chartGeometry ? (
              <div className="rounded-lg border bg-background p-3">
                <svg
                  role="img"
                  aria-label="瞬态高频波形多点位曲线图"
                  viewBox={`0 0 ${chartGeometry.width} ${chartGeometry.height}`}
                  className="h-60 w-full"
                >
                  <line
                    x1={chartGeometry.padLeft}
                    y1={chartGeometry.padTop}
                    x2={chartGeometry.padLeft}
                    y2={chartGeometry.padTop + chartGeometry.plotH}
                    stroke="var(--border)"
                    strokeWidth="1"
                  />
                  <line
                    x1={chartGeometry.padLeft}
                    y1={chartGeometry.padTop + chartGeometry.plotH}
                    x2={chartGeometry.padLeft + chartGeometry.plotW}
                    y2={chartGeometry.padTop + chartGeometry.plotH}
                    stroke="var(--border)"
                    strokeWidth="1"
                  />
                  <text x={4} y={chartGeometry.padTop + 10} fill="var(--muted-foreground)" fontSize="11">
                    {chartGeometry.maxY.toFixed(1)}
                  </text>
                  <text x={4} y={chartGeometry.padTop + chartGeometry.plotH} fill="var(--muted-foreground)" fontSize="11">
                    {chartGeometry.minY.toFixed(1)}
                  </text>
                  <text x={chartGeometry.padLeft} y={chartGeometry.height - 6} fill="var(--muted-foreground)" fontSize="11">
                    {formatTimeLabel(chartGeometry.minMs)}
                  </text>
                  <text x={chartGeometry.padLeft + chartGeometry.plotW - 56} y={chartGeometry.height - 6} fill="var(--muted-foreground)" fontSize="11">
                    {formatTimeLabel(chartGeometry.maxMs)}
                  </text>
                  {chartGeometry.triggerX !== null ? (
                    <line
                      x1={chartGeometry.triggerX}
                      y1={chartGeometry.padTop}
                      x2={chartGeometry.triggerX}
                      y2={chartGeometry.padTop + chartGeometry.plotH}
                      stroke="var(--destructive)"
                      strokeDasharray="4 3"
                      strokeWidth="1.5"
                    />
                  ) : null}
                  {chartGeometry.paths.map((series) => (
                    <polyline
                      key={series.key}
                      fill="none"
                      stroke={series.color}
                      strokeWidth="2"
                      points={series.points}
                    />
                  ))}
                </svg>
              </div>
            ) : (
              <p className="rounded-lg border border-dashed px-4 py-8 text-center text-xs text-muted-foreground">
                暂无可视化的高采样波形点位数据
              </p>
            )}

            <div className="flex flex-wrap items-center justify-end gap-2">
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={capture.samples.length === 0}
                onClick={() =>
                  triggerTextDownload(
                    `waveform-${capture.captureId.slice(-8)}.csv`,
                    'text/csv;charset=utf-8',
                    buildWaveformCSV(capture),
                  )
                }
              >
                <Download data-icon="inline-start" aria-hidden="true" />
                导出 CSV
              </Button>
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={capture.samples.length === 0}
                onClick={() =>
                  triggerTextDownload(
                    `waveform-${capture.captureId.slice(-8)}.json`,
                    'application/json;charset=utf-8',
                    JSON.stringify(capture, null, 2),
                  )
                }
              >
                <Download data-icon="inline-start" aria-hidden="true" />
                导出 JSON
              </Button>
            </div>
          </div>
        ) : null}
      </DialogContent>
    </Dialog>
  );
}
