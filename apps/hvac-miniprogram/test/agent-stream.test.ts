import assert from 'node:assert/strict';
import test from 'node:test';
// The Operations Agent behind the Gateway, as the web's browser tests run it.
import { AssistantOwner } from '../../hvac-web/e2e/assistant-owner.ts';
import type { AgentSessionEvent } from '../miniprogram/lib/agent-types.ts';
import { agentEventReader } from '../miniprogram/lib/agent-stream.ts';
import { followSession } from '../miniprogram/lib/agent.ts';
import { createGateway } from '../miniprogram/lib/gateway.ts';

const SITE = '018f3e00-1000-7000-8000-000000000001';

/** A question asked, and the event stream the Gateway sends for its Run. */
function askedAndStreamed(question: string): string {
  const owner = new AssistantOwner();
  const created = owner.respond(SITE, 'POST', `/api/v1/sites/${SITE}/operations/agent-sessions`, { message: question });
  const sessionId = (created?.body as { session: { id: string } }).session.id;
  return owner.respond(SITE, 'GET', `/api/v1/sites/${SITE}/operations/agent-sessions/${sessionId}/events`, null)!.stream!;
}

test('the event stream reads whole, however WeChat cuts it into chunks, even through a Chinese character', () => {
  const bytes = new TextEncoder().encode(askedAndStreamed('这一小时为什么用电多？'));
  for (const size of [1, 2, 3, 7, 64, bytes.length]) {
    const events: AgentSessionEvent[] = [];
    const read = agentEventReader((event) => events.push(event));
    for (let offset = 0; offset < bytes.length; offset += size) {
      const piece = bytes.slice(offset, offset + size);
      read(piece.buffer.slice(piece.byteOffset, piece.byteOffset + piece.byteLength));
    }
    assert.deepEqual(events.map((event) => event.type), [
      'session.snapshot', 'tool.started', 'tool.completed', 'tool.started', 'tool.completed',
      'assistant.delta', 'assistant.delta', 'artifact.created', 'run.completed', 'session.snapshot',
    ], `chunks of ${size} bytes`);
    const answer = events.flatMap((event) => (event.type === 'assistant.delta' ? [event.payload.delta] : [])).join('');
    assert.equal(answer, '这一小时比前一天同一时段多用电 18 kWh，主要来自 2 号机组。', `chunks of ${size} bytes`);
  }
});

test('following a conversation ends with the snapshot that settles it; a stream cut before that is reported', async () => {
  const stream = askedAndStreamed('今天能效怎么样？');
  const gatewayStreaming = (body: string) => createGateway({
    origin: 'https://hvac.example.test',
    transport: async () => { throw new Error('not used'); },
    chunked: (request) => ({
      done: (async () => {
        request.onHeaders(200);
        request.onChunk(new TextEncoder().encode(body).buffer as ArrayBuffer);
      })(),
      abort: () => {},
    }),
    session: { get: () => 'session-1', set: () => {}, clear: () => {} },
    onSignedOut: () => {},
  });

  const types: string[] = [];
  const ended = await new Promise<boolean>((resolve) => {
    followSession(gatewayStreaming(stream), SITE, 'session-0001', (event) => types.push(event.type), resolve);
  });
  assert.equal(ended, false);
  assert.equal(types.at(-1), 'session.snapshot');

  const cut = stream.slice(0, stream.indexOf('event: artifact.created'));
  const failed = await new Promise<boolean>((resolve) => {
    followSession(gatewayStreaming(cut), SITE, 'session-0001', () => {}, resolve);
  });
  assert.equal(failed, true);
});

test('stopping a conversation on purpose ends it quietly; an event that cannot be read ends it as a failure', async () => {
  let aborted = 0;
  let release: () => void = () => {};
  const held = createGateway({
    origin: 'https://hvac.example.test',
    transport: async () => { throw new Error('not used'); },
    chunked: (request) => ({
      done: new Promise<void>((resolve) => {
        request.onHeaders(200);
        release = resolve;
      }),
      abort: () => { aborted += 1; release(); },
    }),
    session: { get: () => 'session-1', set: () => {}, clear: () => {} },
    onSignedOut: () => {},
  });
  const ends: boolean[] = [];
  const stop = followSession(held, SITE, 'session-0001', () => {}, (failed) => ends.push(failed));
  stop();
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.deepEqual([aborted, ends], [1, []]);

  let feed: (text: string) => void = () => {};
  const garbled = createGateway({
    origin: 'https://hvac.example.test',
    transport: async () => { throw new Error('not used'); },
    chunked: (request) => ({
      done: new Promise<void>(() => {
        request.onHeaders(200);
        feed = (text) => request.onChunk(new TextEncoder().encode(text).buffer as ArrayBuffer);
      }),
      abort: () => {},
    }),
    session: { get: () => 'session-1', set: () => {}, clear: () => {} },
    onSignedOut: () => {},
  });
  const failed = new Promise<boolean>((resolve) => { followSession(garbled, SITE, 'session-0001', () => {}, resolve); });
  feed('event: session.snapshot\ndata: {"broken\n\n');
  assert.equal(await failed, true);
});
