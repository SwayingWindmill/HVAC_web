import type {
  BACnetObjectType,
  DriverChannelDataType,
  DriverSafetyBoundary,
} from './gateway-driver-constants.ts';

export function isValidHostPortEndpoint(endpoint: string): boolean {
  if (!endpoint || endpoint.length > 256 || /\s/.test(endpoint)) {
    return false;
  }
  let host: string;
  let portStr: string;
  if (endpoint.startsWith('[')) {
    const closeBracket = endpoint.indexOf(']');
    if (closeBracket <= 1 || endpoint[closeBracket + 1] !== ':') {
      return false;
    }
    host = endpoint.slice(1, closeBracket);
    portStr = endpoint.slice(closeBracket + 2);
    if (host.includes('[') || host.includes('/')) {
      return false;
    }
  } else {
    const colonIdx = endpoint.lastIndexOf(':');
    if (colonIdx <= 0) {
      return false;
    }
    host = endpoint.slice(0, colonIdx);
    portStr = endpoint.slice(colonIdx + 1);
    if (host.includes(':') || host.includes('/')) {
      return false;
    }
  }
  if (!/^[1-9]\d{0,4}$/.test(portStr)) {
    return false;
  }
  const port = Number(portStr);
  return port >= 1 && port <= 65535;
}

export function isValidOpcuaEndpoint(endpoint: string): boolean {
  if (!endpoint || endpoint.length > 256 || /\s/.test(endpoint)) {
    return false;
  }
  let parsed: URL;
  try {
    parsed = new URL(endpoint);
  } catch {
    return false;
  }
  if (parsed.protocol.toLowerCase() !== 'opc.tcp:') {
    return false;
  }
  return isValidHostPortEndpoint(parsed.host);
}

function splitIdentifierTokens(raw: string): string[] {
  return raw
    .replace(/([a-z0-9])([A-Z])/g, '$1_$2')
    .replace(/([A-Z]+)([A-Z][a-z])/g, '$1_$2')
    .toLowerCase()
    .split(/[^a-z0-9]+/)
    .filter((token) => token.length > 0);
}

function hasAdjacentTokenPair(tokens: readonly string[], first: string, second: string): boolean {
  for (let i = 0; i + 1 < tokens.length; i += 1) {
    if (tokens[i] === first && tokens[i + 1] === second) return true;
  }
  return false;
}

function hasAnyToken(tokens: readonly string[], candidates: readonly string[]): boolean {
  return candidates.some((candidate) => tokens.includes(candidate));
}

export function isHardRealtimeProtectionPoint(
  pointCode: string,
  semanticChannel: string,
  safetyBoundary: DriverSafetyBoundary | string | undefined,
): boolean {
  if (safetyBoundary === 'HARD_REALTIME_SAFETY') return true;
  const tokens = [
    ...splitIdentifierTokens(pointCode),
    ...splitIdentifierTokens(semanticChannel),
  ];
  if (tokens.length === 0) return false;
  if (
    tokens.includes('estop') ||
    hasAdjacentTokenPair(tokens, 'e', 'stop') ||
    (tokens.includes('plc') && hasAnyToken(tokens, ['emergency', 'estop', 'shutdown']))
  ) {
    return true;
  }
  if (
    tokens.includes('fire') &&
    hasAnyToken(tokens, [
      'interlock', 'trip', 'alarm', 'bypass', 'override', 'mask', 'cutout', 'relay',
      'shutoff', 'shutdown', 'linkage', 'damper', 'safety', 'protect', 'protection',
      'panel', 'signal', 'link', 'stop',
    ])
  ) {
    return true;
  }
  if (
    hasAdjacentTokenPair(tokens, 'safety', 'trip') ||
    hasAdjacentTokenPair(tokens, 'hard', 'trip') ||
    hasAdjacentTokenPair(tokens, 'trip', 'bypass') ||
    hasAdjacentTokenPair(tokens, 'trip', 'override') ||
    hasAdjacentTokenPair(tokens, 'trip', 'mask')
  ) {
    return true;
  }
  const hasPressureAnchor =
    hasAdjacentTokenPair(tokens, 'high', 'pressure') ||
    tokens.includes('hp') ||
    hasAdjacentTokenPair(tokens, 'discharge', 'pressure') ||
    hasAdjacentTokenPair(tokens, 'relief', 'valve') ||
    tokens.includes('breaker');
  if (
    hasPressureAnchor &&
    hasAnyToken(tokens, ['trip', 'cutout', 'interlock', 'switch', 'bypass', 'override', 'mask', 'protect', 'protection'])
  ) {
    return true;
  }
  const hasFreezeAnchor =
    tokens.includes('freezestat') ||
    ((hasAnyToken(tokens, ['freeze', 'antifreeze', 'frost']) ||
      hasAdjacentTokenPair(tokens, 'anti', 'freeze') ||
      (hasAdjacentTokenPair(tokens, 'low', 'temp') && tokens.includes('cutout'))) &&
      hasAnyToken(tokens, ['protect', 'protection', 'stat', 'trip', 'interlock', 'cutout', 'bypass', 'override', 'mask', 'switch', 'alarm', 'hard']));
  if (hasFreezeAnchor) return true;
  const hardwarePairs: ReadonlyArray<readonly [string, string]> = [
    ['hard', 'realtime'],
    ['plc', 'safety'],
    ['plc', 'protect'],
    ['plc', 'protection'],
    ['plc', 'interlock'],
    ['safety', 'plc'],
    ['safety', 'relay'],
    ['safety', 'circuit'],
    ['safety', 'bypass'],
    ['safety', 'override'],
    ['safety', 'mask'],
    ['hard', 'interlock'],
    ['hard', 'safety'],
    ['hardware', 'interlock'],
    ['hardware', 'protection'],
    ['protection', 'bypass'],
    ['protection', 'override'],
    ['protection', 'mask'],
  ];
  return hardwarePairs.some(([first, second]) => hasAdjacentTokenPair(tokens, first, second));
}

const OPCUA_GUID_PATTERN = /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/;

export function parseOPCUANodeId(
  raw: string,
): { readonly ok: true; readonly namespaceIndex: number; readonly canonical: string } | { readonly ok: false; readonly error: string } {
  const trimmed = raw.trim();
  if (!trimmed || trimmed.length > 512 || /[\u0000-\u001f\u007f]/.test(trimmed)) {
    return { ok: false, error: 'OPC-UA 节点 ID 不能为空、不超过 512 个字符且不能含控制字符' };
  }
  let namespaceIndex = 0;
  let rest = trimmed;
  if (rest.startsWith('ns=')) {
    const semi = rest.indexOf(';');
    if (semi <= 3 || semi === rest.length - 1) {
      return { ok: false, error: 'OPC-UA 节点 ID 命名空间前缀须为 ns=<整数>;' };
    }
    const nsRaw = rest.slice(3, semi);
    if (!/^\d+$/.test(nsRaw)) {
      return { ok: false, error: `无效的命名空间索引 "${nsRaw}"` };
    }
    namespaceIndex = Number(nsRaw);
    if (!Number.isInteger(namespaceIndex) || namespaceIndex < 0 || namespaceIndex > 65535) {
      return { ok: false, error: 'OPC-UA 命名空间索引须为 0 至 65535 的整数' };
    }
    rest = rest.slice(semi + 1);
  }
  if (rest.length < 3 || rest[1] !== '=') {
    return { ok: false, error: `无效的 OPC-UA 节点 ID "${raw}"（示例：ns=2;s=Chiller.SupplyTemp）` };
  }
  const kind = rest[0]!;
  const body = rest.slice(2);
  switch (kind) {
    case 'i': {
      if (!/^(?:0|[1-9]\d*)$/.test(body)) {
        return { ok: false, error: `数字型 OPC-UA 节点 ID "${raw}" 须为无前导零的非负整数` };
      }
      const num = Number(body);
      if (!Number.isInteger(num) || num > 4_294_967_295) {
        return { ok: false, error: `数字型 OPC-UA 节点 ID "${raw}" 超出 32 位无符号整数范围` };
      }
      return { ok: true, namespaceIndex, canonical: `ns=${namespaceIndex};i=${num}` };
    }
    case 's': {
      if (!body || [...body].length > 256) {
        return { ok: false, error: '字符串型 OPC-UA 节点 ID 内容须为 1 至 256 个字符' };
      }
      return { ok: true, namespaceIndex, canonical: `ns=${namespaceIndex};s=${body}` };
    }
    case 'g': {
      if (!OPCUA_GUID_PATTERN.test(body)) {
        return { ok: false, error: `GUID 型 OPC-UA 节点 ID "${raw}" 须为标准 UUID 格式` };
      }
      return { ok: true, namespaceIndex, canonical: `ns=${namespaceIndex};g=${body.toLowerCase()}` };
    }
    case 'b': {
      try {
        const binary = atob(body);
        if (binary.length === 0 || binary.length > 256) {
          return { ok: false, error: '字节流 OPC-UA 节点 ID 解码后须为 1 至 256 字节' };
        }
        return { ok: true, namespaceIndex, canonical: `ns=${namespaceIndex};b=${btoa(binary)}` };
      } catch {
        return { ok: false, error: `字节流 OPC-UA 节点 ID "${raw}" 须为有效 Base64 编码` };
      }
    }
    default:
      return { ok: false, error: `不支持的 OPC-UA 节点标识前缀 "${kind}="（支持 i=/s=/g=/b=）` };
  }
}

export function defaultBACnetDataType(objectType: BACnetObjectType): DriverChannelDataType {
  if (objectType === 'BINARY_INPUT' || objectType === 'BINARY_OUTPUT' || objectType === 'BINARY_VALUE') {
    return 'BOOLEAN';
  }
  if (objectType === 'MULTI_STATE_INPUT' || objectType === 'MULTI_STATE_OUTPUT' || objectType === 'MULTI_STATE_VALUE') {
    return 'INTEGER';
  }
  return 'DOUBLE';
}
