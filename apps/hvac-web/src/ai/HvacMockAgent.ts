import {
  AbstractAgent,
  EventType,
  type BaseEvent,
  type RunAgentInput,
} from '@copilotkit/react-core/v2';
import { Observable } from 'rxjs';
import {
  buildAiMockAnswerFromSnapshot,
  readAiSnapshot,
  type AiTelemetrySnapshot,
} from '@/api/ai';
import { fddList } from '@/store/ops';

const pause = (milliseconds: number) => new Promise<void>((resolve) => setTimeout(resolve, milliseconds));

type MockToolCall = {
  name: 'render_asset_status_card' | 'render_energy_anomaly_card' | 'render_fdd_evidence_card';
  args: Record<string, unknown>;
};

function getLatestUserText(input: RunAgentInput): string {
  const message = [...input.messages].reverse().find((item) => item.role === 'user');
  if (!message || typeof message.content !== 'string') return '总结当前系统状态。';
  return message.content.trim() || '总结当前系统状态。';
}

function buildMockToolCall(text: string, snapshot: AiTelemetrySnapshot): MockToolCall | null {
  const normalized = text.toLowerCase();
  const asks = (...keywords: string[]) => keywords.some((keyword) => normalized.includes(keyword));

  if (asks('诊断', 'fdd', '故障', '根因', '证据', '排查')) {
    const diagnosis = fddList.find((item) => item.severity === 'critical') ?? fddList[0];
    return {
      name: 'render_fdd_evidence_card',
      args: {
        diagnosisId: diagnosis.id,
        deviceName: diagnosis.device,
        title: diagnosis.phenomenon,
        severity: diagnosis.severity,
        confidence: diagnosis.confidence,
        completeness: 0.96,
        assetId: diagnosis.linkedAssetId,
        evidence: diagnosis.evidence.map((item, index) => ({
          label: item.name,
          value: item.value,
          verified: index < diagnosis.evidence.length - 1 || diagnosis.evidence.length === 1,
        })),
      },
    };
  }

  if (asks('能耗', '功率', '用电', '峰谷', '增量', '基线')) {
    const comparedToBaseline = Math.round(Math.max(8.6, (snapshot.load - 52) * 0.48) * 10) / 10;
    return {
      name: 'render_energy_anomaly_card',
      args: {
        period: '当前运行周期',
        comparedToBaseline,
        extraEnergy: Math.round(snapshot.power * 2.35),
        primaryCause: `${snapshot.weakest.name} 能效下降与空调末端延时运行，是当前增量的主要来源。`,
        contributors: [
          { label: '冷水机组', share: 44.8 },
          { label: '空调末端', share: 31.2 },
          { label: '水泵系统', share: 16.4 },
        ],
      },
    };
  }

  if (asks('cop', '能效', '效率', '设备', '哪台')) {
    const baselineDelta = Math.round(((snapshot.weakest.cop / 4.5) - 1) * 1000) / 10;
    return {
      name: 'render_asset_status_card',
      args: {
        deviceId: snapshot.weakest.id,
        deviceName: snapshot.weakest.name,
        status: snapshot.weakest.cop < 4.5 ? 'attention' : 'running',
        load: snapshot.load,
        cop: snapshot.weakest.cop,
        baselineDelta,
        issue: snapshot.weakest.cop < 4.5
          ? 'COP 低于健康阈值，建议优先检查换热效率、流量与负荷匹配。'
          : '当前能效处于健康区间，继续关注负荷变化即可。',
        diagnosisId: 'FDD-77',
      },
    };
  }

  return null;
}

async function emitText(
  subscriber: { next: (event: BaseEvent) => void },
  messageId: string,
  answer: string,
  isCancelled: () => boolean,
) {
  subscriber.next({
    type: EventType.TEXT_MESSAGE_START,
    messageId,
    role: 'assistant',
  });

  const chunks = answer.match(/[\s\S]{1,4}/g) ?? [answer];
  for (const delta of chunks) {
    if (isCancelled()) return;
    subscriber.next({
      type: EventType.TEXT_MESSAGE_CONTENT,
      messageId,
      delta,
    });
    await pause(10);
  }

  if (!isCancelled()) {
    subscriber.next({
      type: EventType.TEXT_MESSAGE_END,
      messageId,
    });
  }
}

export class HvacMockAgent extends AbstractAgent {
  constructor() {
    super({
      agentId: 'default',
      description: '泉来禾智慧能源只读本地演示 Agent。',
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

          const text = getLatestUserText(input);
          const snapshot = await readAiSnapshot();
          const answer = buildAiMockAnswerFromSnapshot(text, snapshot);
          const toolCall = buildMockToolCall(text, snapshot);

          await emitText(subscriber, messageId, answer, () => cancelled);
          if (cancelled) return;

          if (toolCall) {
            const toolCallId = `tool-${Date.now()}`;
            subscriber.next({
              type: EventType.TOOL_CALL_START,
              toolCallId,
              toolCallName: toolCall.name,
              parentMessageId: messageId,
            });
            subscriber.next({
              type: EventType.TOOL_CALL_ARGS,
              toolCallId,
              delta: JSON.stringify(toolCall.args),
            });
            subscriber.next({
              type: EventType.TOOL_CALL_END,
              toolCallId,
            });
            await pause(24);
          }

          if (cancelled) return;
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
