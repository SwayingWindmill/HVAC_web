import {
  AbstractAgent,
  type BaseEvent,
  type RunAgentInput,
} from '@copilotkit/react-core/v2';
import { Observable } from 'rxjs';
import {
  streamSiteNightEnergyInvestigationEvents,
  type OperationsAgUiEvent,
  type OperationsInvestigationStateSnapshot,
} from '@/api/operations';

export interface OperationsInvestigationAgentOptions {
  readonly organizationId: string;
  readonly siteId: string;
  readonly investigationId: string;
  readonly onSnapshot: (snapshot: OperationsInvestigationStateSnapshot) => void;
  readonly fetchImplementation?: typeof fetch;
  readonly baseUrl?: string;
}

export class OperationsInvestigationAgent extends AbstractAgent {
  private readonly options: OperationsInvestigationAgentOptions;

  constructor(options: OperationsInvestigationAgentOptions) {
    super({
      agentId: 'operations-investigation',
      description: 'Site-scoped Operations Investigation event projection.',
      initialState: {},
    });
    this.options = options;
  }

  run(input: RunAgentInput): Observable<BaseEvent> {
    return new Observable<BaseEvent>((subscriber) => {
      const controller = new AbortController();
      void (async () => {
        try {
          const events = await streamSiteNightEnergyInvestigationEvents(
            this.options.investigationId,
            {
              trustedOrganizationId: this.options.organizationId,
              trustedSiteId: this.options.siteId,
              signal: controller.signal,
              fetchImplementation: this.options.fetchImplementation,
              baseUrl: this.options.baseUrl,
            },
          );
          for (const item of events) {
            if (controller.signal.aborted) return;
            const event: OperationsAgUiEvent = item.event;
            if (event.type === 'STATE_SNAPSHOT') this.options.onSnapshot(event.snapshot);
            const outbound = event.type === 'RUN_STARTED' || event.type === 'RUN_FINISHED'
              ? { ...event, threadId: input.threadId, runId: input.runId }
              : event;
            subscriber.next(outbound as BaseEvent);
          }
          subscriber.complete();
        } catch (error) {
          if (controller.signal.aborted) return;
          subscriber.error(error instanceof Error ? error : new Error(String(error)));
        }
      })();
      return () => controller.abort();
    });
  }
}
