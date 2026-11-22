export interface RealtimeDemandController {
  acquire(): () => void;
  reset(): void;
  isActive(): boolean;
  count(): number;
}

export interface RealtimeDemandOptions<THandle = ReturnType<typeof globalThis.setTimeout>> {
  readonly graceMs: number;
  readonly onChange: (active: boolean) => void;
  readonly setTimer?: (callback: () => void, ms: number) => THandle;
  readonly clearTimer?: (handle: THandle) => void;
}

/**
 * Coordinates demand-driven activation across route handoffs inside one Site workspace.
 * Immediate activation on 0 -> 1 consumers; delayed deactivation on 1 -> 0 so moving
 * between live routes avoids tearing down and reconnecting the realtime stream.
 */
export function createRealtimeDemandController<THandle = ReturnType<typeof globalThis.setTimeout>>({
  graceMs,
  onChange,
  setTimer,
  clearTimer,
}: RealtimeDemandOptions<THandle>): RealtimeDemandController {
  let consumers = 0;
  let active = false;
  let cancelPending: (() => void) | null = null;

  const clearPending = () => {
    if (!cancelPending) return;
    cancelPending();
    cancelPending = null;
  };

  const scheduleTimer = (callback: () => void, ms: number): (() => void) => {
    if (setTimer && clearTimer) {
      const handle = setTimer(callback, ms);
      return () => clearTimer(handle);
    }
    const handle = globalThis.setTimeout(callback, ms);
    return () => globalThis.clearTimeout(handle);
  };

  const deactivate = () => {
    cancelPending = null;
    if (consumers === 0 && active) {
      active = false;
      onChange(false);
    }
  };

  return {
    acquire() {
      consumers += 1;
      clearPending();
      if (!active) {
        active = true;
        onChange(true);
      }
      let released = false;
      return () => {
        if (released) return;
        released = true;
        if (consumers > 0) consumers -= 1;
        if (consumers === 0 && active && !cancelPending) {
          if (graceMs <= 0) deactivate();
          else cancelPending = scheduleTimer(deactivate, graceMs);
        }
      };
    },
    reset() {
      clearPending();
      consumers = 0;
      if (active) {
        active = false;
        onChange(false);
      }
    },
    isActive: () => active,
    count: () => consumers,
  };
}
