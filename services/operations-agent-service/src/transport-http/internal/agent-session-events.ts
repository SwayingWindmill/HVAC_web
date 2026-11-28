import type {
  AgentSessionAccessContext,
  AgentSessionEvent,
  AgentSessionService,
} from '../../application/index.js';

const headers = Object.freeze({
  'Cache-Control': 'no-store, no-transform',
  'Content-Type': 'text/event-stream; charset=utf-8',
  'X-Accel-Buffering': 'no',
});

const encodeEvent = (event: AgentSessionEvent): Uint8Array => new TextEncoder().encode(
  `event: ${event.type}\ndata: ${JSON.stringify(event)}\n\n`,
);

const isTerminalSnapshot = (event: AgentSessionEvent): boolean => (
  event.type === 'session.snapshot' && event.payload.snapshot.session.status !== 'ACTIVE'
);

export const createAgentSessionEventStreamResponse = async (
  service: AgentSessionService,
  context: AgentSessionAccessContext,
  sessionId: string,
): Promise<Response> => {
  let unsubscribe = (): void => undefined;
  const stream = new ReadableStream<Uint8Array>({
    async start(controller) {
      let closed = false;
      const close = (): void => {
        if (closed) return;
        closed = true;
        controller.close();
        unsubscribe();
      };
      try {
        unsubscribe = await service.subscribe(context, sessionId, (event) => {
          if (closed) return;
          controller.enqueue(encodeEvent(event));
          if (isTerminalSnapshot(event)) close();
        });
      } catch (error) {
        if (!closed) controller.error(error);
      }
    },
    cancel() {
      unsubscribe();
    },
  });
  return new Response(stream, { status: 200, headers });
};
