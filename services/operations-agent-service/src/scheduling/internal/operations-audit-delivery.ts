import type {
  OperationsAuditDeliveryRepository,
  OperationsAuditEventV1,
} from '../../application/index.js';

export interface OperationsAuditDeliveryClient {
  deliver(event: OperationsAuditEventV1): Promise<void>;
}

export interface OperationsAuditHttpClientOptions {
  readonly endpoint: string;
  readonly fetchImplementation?: typeof fetch;
  readonly timeoutMs?: number;
}

export interface OperationsAuditDeliveryWorkerOptions {
  readonly repository: OperationsAuditDeliveryRepository;
  readonly client: OperationsAuditDeliveryClient;
  readonly now?: () => number;
  readonly batchSize?: number;
  readonly leaseDurationMs?: number;
  readonly retryBaseMs?: number;
  readonly retryMaximumMs?: number;
}

export interface OperationsAuditDeliveryRunResult {
  readonly claimed: number;
  readonly delivered: number;
  readonly failed: number;
}

type OperationsAuditFailureClass = Parameters<
  OperationsAuditDeliveryRepository['markFailed']
>[0]['failureClass'];

class OperationsAuditDeliveryError extends Error {
  readonly failureClass: OperationsAuditFailureClass;

  constructor(failureClass: OperationsAuditFailureClass, message: string, options?: ErrorOptions) {
    super(message, options);
    this.name = 'OperationsAuditDeliveryError';
    this.failureClass = failureClass;
  }
}

const positiveInteger = (value: number, name: string, maximum: number): number => {
  if (!Number.isSafeInteger(value) || value <= 0 || value > maximum) {
    throw new Error(`${name} must be between 1 and ${maximum}.`);
  }
  return value;
};

const retryDelay = (
  attemptCount: number,
  baseMs: number,
  maximumMs: number,
): number => Math.min(maximumMs, baseMs * (2 ** Math.min(Math.max(0, attemptCount - 1), 10)));

export const createOperationsAuditHttpClient = (
  options: OperationsAuditHttpClientOptions,
): OperationsAuditDeliveryClient => {
  const parsed = new URL(options.endpoint);
  if ((parsed.protocol !== 'http:' && parsed.protocol !== 'https:')
    || parsed.username !== ''
    || parsed.password !== ''
    || parsed.search !== ''
    || parsed.hash !== '') {
    throw new Error('Operations Audit endpoint must be an HTTP URL without credentials or query data.');
  }
  const endpoint = parsed.pathname.endsWith('/internal/v1/audit/operations-events')
    ? parsed.toString()
    : new URL(
      `${parsed.pathname.replace(/\/+$/u, '')}/internal/v1/audit/operations-events`,
      parsed.origin,
    ).toString();
  const fetchImplementation = options.fetchImplementation ?? globalThis.fetch.bind(globalThis);
  const timeoutMs = positiveInteger(options.timeoutMs ?? 1_000, 'Audit HTTP timeout', 30_000);
  return Object.freeze({
    async deliver(event: OperationsAuditEventV1) {
      const controller = new AbortController();
      const timer = setTimeout(() => controller.abort(), timeoutMs);
      try {
        let response: Response;
        try {
          response = await fetchImplementation(endpoint, {
            method: 'POST',
            headers: {
              'Content-Type': 'application/json',
              'Idempotency-Key': event.eventId,
            },
            body: JSON.stringify(event),
            signal: controller.signal,
          });
        } catch (cause) {
          if (controller.signal.aborted) {
            throw new OperationsAuditDeliveryError('TIMEOUT', 'Operations Audit delivery timed out.');
          }
          throw new OperationsAuditDeliveryError(
            'UNAVAILABLE',
            'Operations Audit Ledger is unavailable.',
            { cause },
          );
        }
        if (response.status !== 204) {
          await response.body?.cancel().catch(() => undefined);
          throw new OperationsAuditDeliveryError(
            response.status >= 500 ? 'UNAVAILABLE' : 'REJECTED',
            'Operations Audit Ledger rejected the event.',
          );
        }
        await response.body?.cancel().catch(() => undefined);
      } finally {
        clearTimeout(timer);
      }
    },
  });
};

export const createOperationsAuditDeliveryWorker = (
  options: OperationsAuditDeliveryWorkerOptions,
): { runOnce(): Promise<OperationsAuditDeliveryRunResult> } => {
  const now = options.now ?? Date.now;
  const batchSize = positiveInteger(options.batchSize ?? 25, 'Audit batch size', 100);
  const leaseDurationMs = positiveInteger(
    options.leaseDurationMs ?? 30_000,
    'Audit lease duration',
    300_000,
  );
  const retryBaseMs = positiveInteger(options.retryBaseMs ?? 1_000, 'Audit retry base', 60_000);
  const retryMaximumMs = positiveInteger(
    options.retryMaximumMs ?? 60_000,
    'Audit retry maximum',
    3_600_000,
  );
  if (retryMaximumMs < retryBaseMs) {
    throw new Error('Audit retry maximum cannot be smaller than the retry base.');
  }
  return Object.freeze({
    async runOnce() {
      const claimed = await options.repository.claim({
        at: now(),
        limit: batchSize,
        leaseDurationMs,
      });
      let delivered = 0;
      let failed = 0;
      for (const record of claimed) {
        try {
          await options.client.deliver(record.event);
          await options.repository.markDelivered(record.event.eventId, now());
          delivered += 1;
        } catch (error) {
          const failureClass = error instanceof OperationsAuditDeliveryError
            ? error.failureClass
            : 'INVALID_RESPONSE';
          const failedAt = now();
          await options.repository.markFailed({
            eventId: record.event.eventId,
            failedAt,
            retryAt: failedAt + retryDelay(record.attemptCount, retryBaseMs, retryMaximumMs),
            failureClass,
          });
          failed += 1;
        }
      }
      return Object.freeze({ claimed: claimed.length, delivered, failed });
    },
  });
};
