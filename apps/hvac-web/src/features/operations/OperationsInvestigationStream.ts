import {
  OperationsApiError,
  streamSiteNightEnergyInvestigationEvents,
  type OperationsAgentStreamRecovery,
  type OperationsInvestigationStateSnapshot,
} from '@/api/operations';
import {
  createOperationsInvestigationRecoveryPositionStore,
  type OperationsInvestigationRecoveryPositionStore,
  type OperationsInvestigationRecoveryScope,
} from './operations-recovery-position';

export type OperationsInvestigationConnectionStatus =
  | 'CONNECTING'
  | 'LIVE'
  | 'RETRYING'
  | 'TERMINAL';

export interface OperationsInvestigationConnectionState {
  readonly status: OperationsInvestigationConnectionStatus;
  readonly attempt: number;
  readonly recovery?: OperationsAgentStreamRecovery;
  readonly error?: Error;
}

export interface OperationsInvestigationStreamOptions {
  readonly tenantId: string;
  readonly siteId: string;
  readonly investigationId: string;
  readonly onSnapshot: (snapshot: OperationsInvestigationStateSnapshot) => void;
  readonly onConnectionState?: (state: OperationsInvestigationConnectionState) => void;
  readonly fetchImplementation?: typeof fetch;
  readonly baseUrl?: string;
  readonly reconnectDelayMs?: number;
  readonly maximumRetryDelayMs?: number;
  readonly recoveryPositionStore?: OperationsInvestigationRecoveryPositionStore;
}

const terminalStatuses = new Set(['PAUSED', 'COMPLETED', 'FAILED', 'CANCELLED']);

const delayUntil = (milliseconds: number, signal: AbortSignal): Promise<void> => new Promise((resolve) => {
  if (signal.aborted) {
    resolve();
    return;
  }
  let settled = false;
  let timer: ReturnType<typeof globalThis.setTimeout>;
  const finish = () => {
    if (settled) return;
    settled = true;
    signal.removeEventListener('abort', onAbort);
    resolve();
  };
  const onAbort = () => {
    globalThis.clearTimeout(timer);
    finish();
  };
  timer = globalThis.setTimeout(finish, milliseconds);
  signal.addEventListener('abort', onAbort, { once: true });
});

const isRetryableStreamError = (error: unknown): boolean => (
  error instanceof OperationsApiError ? error.retryable : error instanceof TypeError
);

export class OperationsInvestigationStream {
  private readonly options: OperationsInvestigationStreamOptions;
  private readonly recoveryScope: OperationsInvestigationRecoveryScope;
  private readonly recoveryPositionStore: OperationsInvestigationRecoveryPositionStore;
  private recoveryPosition: string | undefined;
  private controller: AbortController | null = null;

  constructor(options: OperationsInvestigationStreamOptions) {
    this.options = options;
    this.recoveryScope = Object.freeze({
      tenantId: options.tenantId,
      siteId: options.siteId,
      investigationId: options.investigationId,
    });
    this.recoveryPositionStore = options.recoveryPositionStore
      ?? createOperationsInvestigationRecoveryPositionStore();
    this.recoveryPosition = this.recoveryPositionStore.load(this.recoveryScope);
  }

  abort(): void {
    this.controller?.abort();
    this.controller = null;
  }

  purgeRecoveryPosition(): void {
    this.recoveryPosition = undefined;
    this.recoveryPositionStore.clear(this.recoveryScope);
  }

  purgeSiteRecoveryPositions(): void {
    this.recoveryPosition = undefined;
    if (this.recoveryPositionStore.clearSite) {
      this.recoveryPositionStore.clearSite(this.recoveryScope);
    } else {
      this.recoveryPositionStore.clear(this.recoveryScope);
    }
  }

  async start(): Promise<void> {
    this.abort();
    const controller = new AbortController();
    this.controller = controller;
    const reconnectDelay = Math.max(25, this.options.reconnectDelayMs ?? 750);
    const maximumRetryDelay = Math.max(reconnectDelay, this.options.maximumRetryDelayMs ?? 4_000);
    let attempt = 0;
    let retryDelay = reconnectDelay;
    let connected = false;

    try {
      while (!controller.signal.aborted) {
        attempt += 1;
        if (!connected) {
          this.options.onConnectionState?.({ status: 'CONNECTING', attempt });
        }
        try {
          const batch = await streamSiteNightEnergyInvestigationEvents(
            this.options.investigationId,
            {
              trustedTenantId: this.options.tenantId,
              trustedSiteId: this.options.siteId,
              signal: controller.signal,
              recoveryPosition: this.recoveryPosition,
              fetchImplementation: this.options.fetchImplementation,
              baseUrl: this.options.baseUrl,
            },
          );
          if (controller.signal.aborted) return;
          const snapshotFrame = batch.events.find((item) => item.event.type === 'STATE_SNAPSHOT');
          if (snapshotFrame?.event.type !== 'STATE_SNAPSHOT') {
            throw new OperationsApiError(
              502,
              'OPERATIONS_STREAM_INVALID',
              'Operations Agent 事件流缺少权威 snapshot。',
              true,
            );
          }
          const snapshot = snapshotFrame.event.snapshot;
          this.options.onSnapshot(snapshot);
          this.options.onConnectionState?.({
            status: 'LIVE',
            attempt,
            recovery: batch.recovery,
          });
          connected = true;
          this.recoveryPosition = batch.recovery.latestPosition;
          retryDelay = reconnectDelay;

          if (terminalStatuses.has(snapshot.investigation.status)) {
            this.purgeRecoveryPosition();
            this.options.onConnectionState?.({
              status: 'TERMINAL',
              attempt,
              recovery: batch.recovery,
            });
            return;
          }
          this.recoveryPositionStore.save(this.recoveryScope, this.recoveryPosition);
          await delayUntil(reconnectDelay, controller.signal);
        } catch (error) {
          if (controller.signal.aborted) return;
          const normalized = error instanceof Error ? error : new Error(String(error));
          if (!isRetryableStreamError(error)) throw normalized;
          this.options.onConnectionState?.({
            status: 'RETRYING',
            attempt,
            error: normalized,
          });
          await delayUntil(retryDelay, controller.signal);
          retryDelay = Math.min(maximumRetryDelay, retryDelay * 2);
        }
      }
    } finally {
      if (this.controller === controller) this.controller = null;
    }
  }
}
