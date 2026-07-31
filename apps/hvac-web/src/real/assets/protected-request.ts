import type { ProtectedScopeRequestToken } from '../protected-scope.ts';

function linkedAbortSignal(...signals: readonly AbortSignal[]): { signal: AbortSignal; dispose(): void } {
  const controller = new AbortController();
  const abort = (event: Event) => {
    const source = event.target as AbortSignal;
    if (!controller.signal.aborted) controller.abort(source.reason);
  };
  for (const signal of signals) {
    if (signal.aborted) {
      controller.abort(signal.reason);
      break;
    }
    signal.addEventListener('abort', abort, { once: true });
  }
  return {
    signal: controller.signal,
    dispose: () => signals.forEach((signal) => signal.removeEventListener('abort', abort)),
  };
}

export async function runRealAssetsProtectedRequest<T>(
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
