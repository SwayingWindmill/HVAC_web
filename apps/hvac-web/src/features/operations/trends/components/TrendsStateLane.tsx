import { formatTime } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import {
  resolveTrackStateAt,
  type TrendStateKind,
  type TrendStateTrack,
} from '../trends-model';

const SEGMENT_TONE: Readonly<Record<TrendStateKind, string>> = {
  RUNNING: 'bg-success/85',
  STOPPED: 'bg-subtle-foreground/35',
  FAULT: 'bg-destructive/85',
  OTHER: 'bg-primary/70',
};

const BADGE_TONE: Readonly<Record<TrendStateKind, string>> = {
  RUNNING: 'bg-success-soft text-success-text',
  STOPPED: 'bg-muted text-muted-foreground',
  FAULT: 'bg-destructive-soft text-destructive-text',
  OTHER: 'bg-primary-soft text-primary',
};

export function TrendsStateLane({
  tracks,
  fromMs,
  toMs,
  timezone,
  hoverAt,
  onHoverAt,
}: {
  readonly tracks: readonly TrendStateTrack[];
  readonly fromMs: number;
  readonly toMs: number;
  readonly timezone: string;
  readonly hoverAt: number | null;
  readonly onHoverAt?: (atMs: number | null) => void;
}) {
  if (tracks.length === 0) return null;

  const spanMs = Math.max(1, toMs - fromMs);
  const cursorPercent =
    hoverAt !== null && hoverAt >= fromMs && hoverAt <= toMs
      ? ((hoverAt - fromMs) / spanMs) * 100
      : null;

  return (
    <section
      role="region"
      aria-label="设备启停状态"
      className="flex flex-col gap-2.5 border-t border-divider bg-muted/15 px-5 py-3.5"
    >
      <div className="flex flex-wrap items-center justify-between gap-2 text-xs">
        <div className="flex items-center gap-2">
          <span className="font-semibold text-foreground">设备启停状态</span>
          {hoverAt !== null ? (
            <span className="font-mono text-muted-foreground">
              游标 {formatTime(new Date(hoverAt).toISOString(), timezone).slice(-5)}
            </span>
          ) : null}
        </div>
        <div className="flex items-center gap-3 text-[11px] text-muted-foreground">
          <span className="inline-flex items-center gap-1.5">
            <span className="h-2 w-2.5 rounded-[2px] bg-success/85" aria-hidden="true" />
            运行
          </span>
          <span className="inline-flex items-center gap-1.5">
            <span className="h-2 w-2.5 rounded-[2px] bg-subtle-foreground/35" aria-hidden="true" />
            停止 / 待机
          </span>
          <span className="inline-flex items-center gap-1.5">
            <span className="h-2 w-2.5 rounded-[2px] border border-dashed border-border bg-background" aria-hidden="true" />
            无读数
          </span>
        </div>
      </div>

      <div className="flex flex-col gap-2">
        {tracks.map((track) => {
          const activeState = resolveTrackStateAt(track, hoverAt);
          const runningPct = Math.round(track.runningRatio * 100);

          return (
            <div
              key={track.ref}
              className="grid grid-cols-[160px_minmax(0,1fr)_96px] items-center gap-3 text-xs"
            >
              <div className="flex min-w-0 flex-col">
                <span className="truncate font-medium text-foreground">{track.deviceName}</span>
                <span className="truncate text-[11px] text-muted-foreground">
                  {track.pointLabel} · 运行 {runningPct}%
                </span>
              </div>

              <div
                className="relative h-5 overflow-hidden rounded-md border border-divider bg-background"
                onMouseMove={(event) => {
                  if (!onHoverAt) return;
                  const rect = event.currentTarget.getBoundingClientRect();
                  if (rect.width <= 0) return;
                  const ratio = Math.min(1, Math.max(0, (event.clientX - rect.left) / rect.width));
                  onHoverAt(Math.round(fromMs + ratio * spanMs));
                }}
                onMouseLeave={() => onHoverAt?.(null)}
              >
                {track.segments.map((segment) => (
                  <div
                    key={`${segment.startMs}-${segment.endMs}`}
                    data-state-kind={segment.kind}
                    title={`${track.deviceName} · ${segment.label}`}
                    className={cn('absolute inset-y-0.5 rounded-[2px]', SEGMENT_TONE[segment.kind])}
                    style={{
                      left: `${segment.startPercent}%`,
                      width: `${Math.max(0.4, segment.widthPercent)}%`,
                    }}
                  />
                ))}
                {cursorPercent !== null ? (
                  <div
                    aria-hidden="true"
                    className="pointer-events-none absolute inset-y-0 w-px bg-foreground/70"
                    style={{ left: `${cursorPercent}%` }}
                  />
                ) : null}
              </div>

              <div className="flex justify-end">
                {activeState ? (
                  <span
                    data-testid={`state-readout-${track.deviceName}`}
                    className={cn(
                      'inline-flex items-center rounded-md px-2 py-0.5 text-[11px] font-medium',
                      BADGE_TONE[activeState.kind],
                    )}
                  >
                    {activeState.label}
                  </span>
                ) : (
                  <span
                    data-testid={`state-readout-${track.deviceName}`}
                    className="inline-flex items-center rounded-md bg-muted px-2 py-0.5 text-[11px] text-muted-foreground"
                  >
                    无读数
                  </span>
                )}
              </div>
            </div>
          );
        })}
      </div>
    </section>
  );
}
