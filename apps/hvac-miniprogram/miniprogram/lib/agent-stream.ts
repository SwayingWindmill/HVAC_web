// Reads the Agent Session event stream from the raw chunks wx.request hands over with
// enableChunked (WeChat has no EventSource). The Gateway writes each event as exactly
// `event: <type>` and `data: <json>` lines ended by a blank line (forwardAgentSessionEventStream),
// after checking it against the contract.

import type { AgentSessionEvent } from './agent-types';
import { utf8Reader } from './utf8';

/** A reader to hand each chunk to; it calls `onEvent` for every complete event, in order. */
export function agentEventReader(onEvent: (event: AgentSessionEvent) => void): (chunk: ArrayBuffer) => void {
  const decode = utf8Reader();
  let buffered = '';
  return (chunk) => {
    buffered += decode(chunk);
    let end = buffered.indexOf('\n\n');
    while (end >= 0) {
      const block = buffered.slice(0, end);
      buffered = buffered.slice(end + 2);
      const data = block.split('\n').find((line) => line.startsWith('data: '));
      if (data !== undefined) onEvent(JSON.parse(data.slice('data: '.length)) as AgentSessionEvent);
      end = buffered.indexOf('\n\n');
    }
  };
}
