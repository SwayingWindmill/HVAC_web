import {
  AbstractAgent,
  type BaseEvent,
  type RunAgentInput,
} from '@copilotkit/react-core/v2';
import { Observable } from 'rxjs';
import {
  OperationsApiError,
  streamSiteNightEnergyInvestigationEvents,
  type OperationsAgUiEvent,
  type OperationsAgUiStreamRecovery,
  type OperationsInvestigationStateSnapshot,
} from '@/api/operations';

export type OperationsInvestigationConnectionStatus =
  | 'CONNECTING'
  | 'LIVE'
  | 'RETRYING'
  | 'TERMINAL';

export interface OperationsInvestigationConnectionState {
  readonly status: OperationsInvestigationConnectionStatus;
  readonly attempt: number;
  readonly recovery?: OperationsAgUiStreamRecovery;
  readonly error?: Error;
}

export interface OperationsInvestigationAgentOptions {
  readonly organizationId: string;
  readonly siteId: string;
  readonly investigationId: string;
  readonly onSnapshot: (snapshot: OperationsInvestigationStateSnapshot) => void;
  readonly onConnectionState?: (state: OperationsInvestigationConnectionState) => void;
  readonly fetchImplementation?: typeof fetch;
  readonly baseUrl?: string;
  readonly reconnectDelayMs?: number;
  readonly maximumRetryDelayMs?: number;
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

const durableEventKey = (event: OperationsAgUiEvent): string | null => {
  if (event.type === 'TOOL_CALL_START'
    || event.type === 'TOOL_CALL_ARGS'
    || event.type === 'TOOL_CALL_END') {
    return `${event.type}:${event.toolCallId}`;
  }
  return null;
};

const isRetryableStreamError = (error: unknown): boolean => (
  error instanceof OperationsApiError ? error.retryable : error instanceof TypeError
);

export class OperationsInvestigationAgent extends AbstractAgent {
  private readonly options: OperationsInvestigationAgentOptions;
  private recoveryPosition: string | undefined;
  private readonly deliveredDurableEvents = new Set<string>();

  constructor(options: OperationsInvestigationAgentOptions) {
    super({
      agentId: 'operations',
      description: 'Site-scoped Operations Investigation event projection.',
      initialState: {},
    });
    this.options = options;
  }

  run(input: RunAgentInput): Observable<BaseEvent> {
    return new Observable<BaseEvent>((subscriber) => {
      const controller = new AbortController();
      const reconnectDelay = Math.max(25, this.options.reconnectDelayMs ?? 750);
      const maximumRetryDelay = Math.max(reconnectDelay, this.options.maximumRetryDelayMs ?? 4_000);
      void (async () => {
        let attempt = 0;
        let retryDelay = reconnectDelay;
        let started = false;
        try {
          while (!controller.signal.aborted) {
            attempt += 1;
            if (!started) {
              this.options.onConnectionState?.({ status: 'CONNECTING', attempt });
            }
            try {
              const batch = await streamSiteNightEnergyInvestigationEvents(
                this.options.investigationId,
                {
                  trustedOrganizationId: this.options.organizationId,
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

              const startEvent = batch.events[0]?.event;
              if (!started && startEvent?.type === 'RUN_STARTED') {
                subscriber.next({ ...startEvent, threadId: input.threadId, runId: input.runId } as BaseEvent);
                started = true;
              }
              subscriber.next(snapshotFrame.event as BaseEvent);
              for (const item of batch.events) {
                const event = item.event;
                const key = durableEventKey(event);
                if (key === null || this.deliveredDurableEvents.has(key)) continue;
                this.deliveredDurableEvents.add(key);
                subscriber.next(event as BaseEvent);
              }
              this.recoveryPosition = batch.recovery.latestPosition;
              retryDelay = reconnectDelay;

              if (terminalStatuses.has(snapshot.investigation.status)) {
                const finishEvent = batch.events.at(-1)?.event;
                if (finishEvent?.type === 'RUN_FINISHED') {
                  subscriber.next({ ...finishEvent, threadId: input.threadId, runId: input.runId } as BaseEvent);
                }
                this.options.onConnectionState?.({
                  status: 'TERMINAL',
                  attempt,
                  recovery: batch.recovery,
                });
                subscriber.complete();
                return;
              }
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
        } catch (error) {
          if (controller.signal.aborted) return;
          subscriber.error(error instanceof Error ? error : new Error(String(error)));
        }
      })();
      return () => controller.abort();
    });
  }
}
