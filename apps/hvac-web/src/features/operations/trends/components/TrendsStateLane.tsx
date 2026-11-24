import { memo, useCallback, useSyncExternalStore } from 'react';
import { formatTime } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import type { TrendsHoverStore } from '../trends-hover-store';
import {
  resolveTrackStateAt,
  type TrendStateKind,
  type TrendStateTrack,
} from '../trends-model';

const NOOP_SUBSCRIBE = () => () => {};
const NULL_HOVER_SNAPSHOT = () => null;

function useTrendsHoverAt(hoverStore?: TrendsHoverStore): number | null {
  return useSyncExternalStore(
    hoverStore ? hoverStore.subscribe : NOOP_SUBSCRIBE,
    hoverStore ? hoverStore.getSnapshot : NULL_HOVER_SNAPSHOT,
    NULL_HOVER_SNAPSHOT,
  );
}

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

const StateLaneHeaderClock = memo(function StateLaneHeaderClock({
  timezone,
  hoverStore,
}: {
  readonly timezone: string;
  readonly hoverStore?: TrendsHoverStore;
}) {
  const hoverAt = useTrendsHoverAt(hoverStore);
  if (hoverAt === null) return null;
  return (
    <span className="font-mono text-muted-foreground tabular-nums">
      时间 {formatTime(new Date(hoverAt).toISOString(), timezone).slice(-5)}
    </span>
  );
});

const StateTrackReadout = memo(function StateTrackReadout({
  track,
  hoverStore,
}: {
  readonly track: TrendStateTrack;
  readonly hoverStore?: TrendsHoverStore;
}) {
  const hoverAt = useTrendsHoverAt(hoverStore);
  const activeState = resolveTrackStateAt(track, hoverAt);
  const activeSlot =
    hoverAt !== null
      ? track.slots.find((slot) => hoverAt >= slot.startMs && hoverAt < slot.endMs)
      : undefined;
  const runningPct = Math.round(track.runningRatio * 100);

  return (
    <div className="mr-3 ml-12 flex items-center justify-between gap-2">
      <div className="flex min-w-0 items-center gap-1.5">
        <span className="truncate font-medium text-foreground">{track.deviceName}</span>
        <span className="truncate text-[11px] text-muted-foreground tabular-nums">
          {track.pointLabel} · 运行 {runningPct}%
          {activeSlot?.breakdown ? ` (${activeSlot.breakdown})` : ''}
        </span>
      </div>
      {activeState ? (
        <span
          data-testid={`state-readout-${track.deviceName}`}
          className={cn(
            'inline-flex shrink-0 items-center rounded-md px-2 py-0.5 text-[11px] font-medium',
            BADGE_TONE[activeState.kind],
          )}
        >
          {activeState.label}
        </span>
      ) : (
        <span
          data-testid={`state-readout-${track.deviceName}`}
          className="inline-flex shrink-0 items-center rounded-md bg-muted px-2 py-0.5 text-[11px] text-muted-foreground"
        >
          无读数
        </span>
      )}
    </div>
  );
});

const StateTrackCursor = memo(function StateTrackCursor({
  fromMs,
  toMs,
  hoverStore,
}: {
  readonly fromMs: number;
  readonly toMs: number;
  readonly hoverStore?: TrendsHoverStore;
}) {
  const hoverAt = useTrendsHoverAt(hoverStore);
  if (hoverAt === null || hoverAt < fromMs || hoverAt > toMs) return null;
  const cursorPercent = ((hoverAt - fromMs) / Math.max(1, toMs - fromMs)) * 100;
  return (
    <div
      aria-hidden="true"
      className="pointer-events-none absolute inset-y-0 w-px bg-foreground/70"
      style={{ left: `${cursorPercent}%` }}
    />
  );
});

const StateTrackSegments = memo(function StateTrackSegments({
  track,
}: {
  readonly track: TrendStateTrack;
}) {
  return (
    <>
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
    </>
  );
});

export const TrendsStateLane = memo(function TrendsStateLane({
  tracks,
  fromMs,
  toMs,
  timezone,
  hoverStore,
}: {
  readonly tracks: readonly TrendStateTrack[];
  readonly fromMs: number;
  readonly toMs: number;
  readonly timezone: string;
  readonly hoverStore?: TrendsHoverStore;
}) {
  const spanMs = Math.max(1, toMs - fromMs);
  const handleMouseMove = useCallback(
    (event: React.MouseEvent<HTMLDivElement>) => {
      if (!hoverStore) return;
      const rect = event.currentTarget.getBoundingClientRect();
      if (rect.width <= 0) return;
      const ratio = Math.min(1, Math.max(0, (event.clientX - rect.left) / rect.width));
      hoverStore.setHoverAt(Math.round(fromMs + ratio * spanMs));
    },
    [fromMs, hoverStore, spanMs],
  );
  const handleMouseLeave = useCallback(() => hoverStore?.setHoverAt(null), [hoverStore]);

  if (tracks.length === 0) return null;

  return (
    <section
      role="region"
      aria-label="设备启停状态"
      className="flex flex-col gap-2.5 border-t border-divider bg-muted/15 px-5 py-3.5"
    >
      <div className="flex flex-wrap items-center justify-between gap-2 text-xs">
        <div className="flex items-center gap-2">
          <span className="font-semibold text-foreground">设备启停状态</span>
          <StateLaneHeaderClock timezone={timezone} hoverStore={hoverStore} />
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

      <div className="flex flex-col gap-2.5">
        {tracks.map((track) => (
          <div key={track.ref} className="flex flex-col gap-1 text-xs">
            <StateTrackReadout track={track} hoverStore={hoverStore} />
            <div
              className="relative mr-3 ml-12 h-5 overflow-hidden rounded-md border border-divider bg-background"
              onMouseMove={handleMouseMove}
              onMouseLeave={handleMouseLeave}
            >
              <StateTrackSegments track={track} />
              <StateTrackCursor fromMs={fromMs} toMs={toMs} hoverStore={hoverStore} />
            </div>
          </div>
        ))}
      </div>
    </section>
  );
});
