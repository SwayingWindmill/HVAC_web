import type { ProtectedScopeRequestToken } from '@/app/protected-scope.ts';

function linkedAbortSignal(...signals: readonly AbortSignal[]): { signal: AbortSignal; dispose(): void } {
  const controller = new AbortController();
  const listeners: Array<() => void> = [];
  for (const signal of signals) {
    if (signal.aborted) {
      controller.abort(signal.reason);
      break;
    }
    const abort = () => {
      if (!controller.signal.aborted) controller.abort(signal.reason);
    };
    signal.addEventListener('abort', abort, { once: true });
    listeners.push(() => signal.removeEventListener('abort', abort));
  }
  return {
    signal: controller.signal,
    dispose: () => listeners.forEach((remove) => remove()),
  };
}

export async function runAssetsProtectedRequest<T>(
  scopeGuard: ProtectedScopeRequestToken,
  querySignal: AbortSignal,
  operation: (signal: AbortSignal) => Promise<T>,
): Promise<T> {
  const linked = linkedAbortSignal(scopeGuard.signal, querySignal);
  try {
    const result = await operation(linked.signal);
    if (linked.signal.aborted) {
      throw linked.signal.reason instanceof Error
        ? linked.signal.reason
        : new DOMException('Protected request was cancelled.', 'AbortError');
    }
    let accepted: T | undefined;
    if (!scopeGuard.commit(() => { accepted = result; })) {
      throw new DOMException('Protected Site scope changed before the response could commit.', 'AbortError');
    }
    return accepted!;
  } finally {
    linked.dispose();
  }
}
