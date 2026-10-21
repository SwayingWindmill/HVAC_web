// UTF-8 bytes to text, chunk by chunk: WeChat has no TextDecoder, and wx.request with enableChunked
// cuts a response anywhere, even inside a character, which is kept for the next chunk.

const REPLACEMENT = 0xfffd;

export function utf8Reader() {
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
