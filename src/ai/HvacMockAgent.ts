import {
  AbstractAgent,
  EventType,
  type BaseEvent,
  type RunAgentInput,
} from '@copilotkit/react-core/v2';
import { Observable } from 'rxjs';
import { buildAiMockAnswer } from '@/api/ai';

const pause = (milliseconds: number) => new Promise<void>((resolve) => setTimeout(resolve, milliseconds));

function getLatestUserText(input: RunAgentInput): string {
  const message = [...input.messages].reverse().find((item) => item.role === 'user');
  if (!message || typeof message.content !== 'string') return '总结当前系统状态。';
  return message.content.trim() || '总结当前系统状态。';
}

export class HvacMockAgent extends AbstractAgent {
  constructor() {
    super({
      agentId: 'default',
      description: 'HVAC 智慧能源只读本地演示 Agent。',
      initialState: {},
    });
  }

  run(input: RunAgentInput): Observable<BaseEvent> {
    return new Observable<BaseEvent>((subscriber) => {
      let cancelled = false;
      const messageId = `assistant-${Date.now()}`;

      void (async () => {
        try {
          subscriber.next({
            type: EventType.RUN_STARTED,
            threadId: input.threadId,
            runId: input.runId,
          });
          subscriber.next({
            type: EventType.TEXT_MESSAGE_START,
            messageId,
            role: 'assistant',
          });

          const answer = await buildAiMockAnswer(getLatestUserText(input));
          const chunks = answer.match(/[\s\S]{1,4}/g) ?? [answer];
          for (const delta of chunks) {
            if (cancelled) return;
            subscriber.next({
              type: EventType.TEXT_MESSAGE_CONTENT,
              messageId,
              delta,
            });
            await pause(12);
          }

          if (cancelled) return;
          subscriber.next({
            type: EventType.TEXT_MESSAGE_END,
            messageId,
          });
          subscriber.next({
            type: EventType.RUN_FINISHED,
            threadId: input.threadId,
            runId: input.runId,
            outcome: { type: 'success' },
          });
          subscriber.complete();
        } catch (error) {
          subscriber.error(error instanceof Error ? error : new Error(String(error)));
        }
      })();

      return () => {
        cancelled = true;
      };
    });
  }
}
