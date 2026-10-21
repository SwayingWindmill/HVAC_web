// Reads the Agent Session event stream as wx.request hands it over with enableChunked: raw bytes
// cut anywhere, even inside a character. WeChat has neither EventSource nor TextDecoder, so the
// bytes are decoded here. The Gateway writes each event as exactly `event: <type>` and
// `data: <json>` lines ended by a blank line (forwardAgentSessionEventStream), after checking it
// against the contract.

import type { AgentSessionEvent } from './agent-types';

const REPLACEMENT = 0xfffd;

/** UTF-8 to text, keeping a character cut at the end of a chunk for the next one. */
function utf8Reader() {
  let carried: number[] = [];
  return (chunk: ArrayBuffer): string => {
    const bytes = carried.length === 0 ? new Uint8Array(chunk) : Uint8Array.from([...carried, ...new Uint8Array(chunk)]);
    const codes: number[] = [];
    let index = 0;
    while (index < bytes.length) {
      const lead = bytes[index];
      const length = lead < 0x80 ? 1 : lead >= 0xf0 ? 4 : lead >= 0xe0 ? 3 : lead >= 0xc0 ? 2 : 0;
      if (length === 0) {
        // A stray continuation byte.
        codes.push(REPLACEMENT);
        index += 1;
        continue;
      }
      if (index + length > bytes.length) break;
      let code = length === 1 ? lead : lead & (0xff >> (length + 1));
      for (let next = 1; next < length; next += 1) code = (code << 6) | (bytes[index + next] & 0x3f);
      codes.push(code);
      index += length;
    }
    carried = Array.from(bytes.subarray(index));
    return codes.map((code) => String.fromCodePoint(code)).join('');
  };
}

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
