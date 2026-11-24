function abortError(signal?: AbortSignal): Error {
  if (signal?.reason instanceof Error) return signal.reason;
  if (typeof DOMException !== 'undefined') {
    return new DOMException('The operation was aborted.', 'AbortError');
  }
  const error = new Error('The operation was aborted.');
  error.name = 'AbortError';
  return error;
}

export interface ConcurrencyLimiter {
  run<T>(task: () => Promise<T>, signal?: AbortSignal): Promise<T>;
}

/**
 * Limits concurrent async work (e.g., per-device fan-out requests) without starving queued aborts.
 */
export function createConcurrencyLimiter(maxConcurrent: number): ConcurrencyLimiter {
  const limit = Math.max(1, Math.trunc(maxConcurrent));
  let active = 0;
  const queue: (() => void)[] = [];

  const wakeNext = () => {
    while (active < limit && queue.length > 0) {
      const next = queue.shift();
      if (next) next();
    }
  };

  return {
    run<T>(task: () => Promise<T>, signal?: AbortSignal): Promise<T> {
      if (signal?.aborted) {
        return Promise.reject(abortError(signal));
      }

      const execute = async (): Promise<T> => {
        active += 1;
        try {
          return await task();
        } finally {
          active -= 1;
          wakeNext();
        }
      };

      if (active < limit) {
        return execute();
      }

      return new Promise<T>((resolve, reject) => {
        let settled = false;
        const onAbort = () => {
          if (settled) return;
          settled = true;
          const idx = queue.indexOf(start);
          if (idx >= 0) queue.splice(idx, 1);
          reject(abortError(signal));
        };
        const start = () => {
          if (settled) return;
          settled = true;
          signal?.removeEventListener('abort', onAbort);
          if (signal?.aborted) {
            reject(abortError(signal));
            wakeNext();
            return;
          }
          execute().then(resolve, reject);
        };
        signal?.addEventListener('abort', onAbort, { once: true });
        queue.push(start);
      });
    },
  };
}
