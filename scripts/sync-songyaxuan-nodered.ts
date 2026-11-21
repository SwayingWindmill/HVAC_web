#!/usr/bin/env node
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { stripTypeScriptTypes } from 'node:module';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  PROTECTED_FLOW_TAB_LABELS,
  validateProtectedFlowTabs,
  type NodeRedFlowItem,
} from '../edge/node-red/gateway-nodered-tunnel.ts';

export const DEFAULT_NODERED_URL = 'http://127.0.0.1:18800';
export const DEFAULT_NODERED_USERNAME = 'admin';
export const DEFAULT_PASSWORD_FILE = '/opt/hvac-platform/runtime/secrets/songyaxuan-nodered-admin';
export const TARGET_ENVELOPE_NODE_NAME = '平台·形成信封';
export const MAX_ENVELOPE_BYTES = 12288;

export interface SyncSongyaxuanOptions {
  baseUrl?: string;
  envelopePath?: string;
  envelopeSource?: string;
  token?: string;
  username?: string;
  password?: string;
  passwordFile?: string;
  backupPath?: string | null;
  fetchImpl?: typeof fetch;
}

export interface SyncSongyaxuanResult {
  ok: true;
  baseUrl: string;
  envelopeBytes: number;
  envelopeSha256: string;
  targetNodeId: string;
  targetAlreadyMatched: boolean;
  nodeCount: number;
  revBefore: string;
  revAfter: string;
  verifiedMatch: true;
  tabs: Array<{ id: string; label: string; disabled: boolean }>;
}

interface FlowsV2Payload {
  rev: string;
  flows: NodeRedFlowItem[];
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function parseFlowItem(raw: unknown): NodeRedFlowItem {
  if (!isRecord(raw) || typeof raw.id !== 'string' || typeof raw.type !== 'string') {
    throw new Error('GET /flows returned an invalid flow node entry without string id/type');
  }
  return {
    ...raw,
    id: raw.id,
    type: raw.type,
    ...(typeof raw.label === 'string' ? { label: raw.label } : {}),
    ...(typeof raw.name === 'string' ? { name: raw.name } : {}),
    ...(typeof raw.disabled === 'boolean' ? { disabled: raw.disabled } : {}),
  };
}

function parseFlowsPayload(raw: unknown): FlowsV2Payload {
  if (!isRecord(raw) || typeof raw.rev !== 'string' || !Array.isArray(raw.flows)) {
    throw new Error('GET /flows did not return a valid v2 { rev, flows } payload');
  }
  return {
    rev: raw.rev,
    flows: raw.flows.map(parseFlowItem),
  };
}

function defaultEnvelopePath(): string {
  const scriptDir = dirname(fileURLToPath(import.meta.url));
  return resolve(scriptDir, '../edge/node-red/songyaxuan-platform-envelope.js');
}

function loadEnvelopeSource(options: SyncSongyaxuanOptions): { sourceText: string; bytes: number; sha256: string } {
  const sourceText = options.envelopeSource ?? readFileSync(options.envelopePath ?? defaultEnvelopePath(), 'utf8');
  const bytes = Buffer.byteLength(sourceText, 'utf8');
  if (bytes === 0 || bytes > MAX_ENVELOPE_BYTES) {
    throw new Error(`Envelope size ${bytes} bytes is outside allowed 1..${MAX_ENVELOPE_BYTES} byte cap`);
  }
  const sha256 = createHash('sha256').update(sourceText, 'utf8').digest('hex');
  return { sourceText, bytes, sha256 };
}

function resolvePasswordSecret(options: SyncSongyaxuanOptions): string | undefined {
  const direct = options.password?.trim() || process.env.NODERED_PASSWORD?.trim();
  if (direct) return direct;

  const explicitFile = options.passwordFile?.trim() || process.env.NODERED_PASSWORD_FILE?.trim();
  if (explicitFile) {
    const content = readFileSync(explicitFile, 'utf8').trim();
    if (!content) throw new Error(`Password file is empty: ${explicitFile}`);
    return content;
  }

  if (existsSync(DEFAULT_PASSWORD_FILE)) {
    const content = readFileSync(DEFAULT_PASSWORD_FILE, 'utf8').trim();
    if (content) return content;
  }
  return undefined;
}

async function exchangePasswordForToken(
  fetchFn: typeof fetch,
  baseUrl: string,
  username: string,
  password: string,
): Promise<string> {
  const body = new URLSearchParams({
    client_id: 'node-red-admin',
    grant_type: 'password',
    scope: '*',
    username,
    password,
  });
  const res = await fetchFn(`${baseUrl}/auth/token`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body,
  });
  if (!res.ok) {
    throw new Error(`POST /auth/token failed with HTTP ${res.status}`);
  }
  const raw: unknown = await res.json();
  if (!isRecord(raw) || typeof raw.access_token !== 'string' || !raw.access_token) {
    throw new Error('POST /auth/token response lacked a string access_token');
  }
  return raw.access_token;
}

async function fetchFlowsV2(
  fetchFn: typeof fetch,
  baseUrl: string,
  token?: string,
): Promise<{ status: number; payload?: FlowsV2Payload }> {
  const headers: Record<string, string> = {
    'Node-RED-API-Version': 'v2',
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
  };
  const res = await fetchFn(`${baseUrl}/flows`, { headers });
  if (res.status === 401) {
    return { status: 401 };
  }
  if (!res.ok) {
    throw new Error(`GET /flows failed with HTTP ${res.status}`);
  }
  const raw: unknown = await res.json();
  return { status: res.status, payload: parseFlowsPayload(raw) };
}

export async function syncSongyaxuanEnvelopeViaApi(options: SyncSongyaxuanOptions = {}): Promise<SyncSongyaxuanResult> {
  const baseUrl = (options.baseUrl ?? process.env.NODERED_URL?.trim() ?? DEFAULT_NODERED_URL).replace(/\/+$/u, '');
  const username = options.username?.trim() || process.env.NODERED_USERNAME?.trim() || DEFAULT_NODERED_USERNAME;
  const fetchFn = options.fetchImpl ?? fetch;
  const { sourceText, bytes: envelopeBytes, sha256: envelopeSha256 } = loadEnvelopeSource(options);

  let activeToken = options.token?.trim() || process.env.NODERED_TOKEN?.trim() || undefined;
  let beforeAttempt = await fetchFlowsV2(fetchFn, baseUrl, activeToken);

  if (!activeToken && beforeAttempt.status === 401) {
    const password = resolvePasswordSecret(options);
    if (!password) {
      throw new Error(
        `Node-RED at ${baseUrl} returned HTTP 401 and no credential was found (set NODERED_TOKEN, NODERED_PASSWORD, --password-file, or provision ${DEFAULT_PASSWORD_FILE})`,
      );
    }
    activeToken = await exchangePasswordForToken(fetchFn, baseUrl, username, password);
    beforeAttempt = await fetchFlowsV2(fetchFn, baseUrl, activeToken);
  }

  if (beforeAttempt.status === 401 || !beforeAttempt.payload) {
    throw new Error(`GET /flows rejected with HTTP ${beforeAttempt.status}`);
  }

  const before = beforeAttempt.payload;
  if (options.backupPath) {
    writeFileSync(options.backupPath, `${JSON.stringify(before, null, 2)}\n`, { encoding: 'utf8', mode: 0o600 });
  }

  // Validate that protected tabs already exist and stay enabled before mutating flows.
  const requiredProtectedTabs: NodeRedFlowItem[] = PROTECTED_FLOW_TAB_LABELS.map((label, idx) => ({
    id: `required-tab-${idx}`,
    type: 'tab',
    label,
    disabled: false,
  }));
  validateProtectedFlowTabs(requiredProtectedTabs, before.flows);

  const matches = before.flows.filter(
    (item) => item.type === 'function' && item.name === TARGET_ENVELOPE_NODE_NAME && !item.disabled,
  );
  if (matches.length !== 1) {
    throw new Error(`Expected exactly 1 enabled function node "${TARGET_ENVELOPE_NODE_NAME}", found ${matches.length}`);
  }
  const targetNode = matches[0];
  const targetAlreadyMatched = targetNode.func === sourceText;

  const updatedFlows: NodeRedFlowItem[] = before.flows.map((item) =>
    item.id === targetNode.id ? { ...item, func: sourceText } : item,
  );
  validateProtectedFlowTabs(before.flows, updatedFlows);

  const postRes = await fetchFn(`${baseUrl}/flows`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'Node-RED-API-Version': 'v2',
      'Node-RED-Deployment-Type': 'nodes',
      ...(activeToken ? { Authorization: `Bearer ${activeToken}` } : {}),
    },
    body: JSON.stringify({ rev: before.rev, flows: updatedFlows }),
  });
  if (!postRes.ok) {
    const detail = await postRes.text().catch(() => '');
    throw new Error(`POST /flows failed with HTTP ${postRes.status}: ${detail}`);
  }
  const postRaw: unknown = await postRes.json().catch(() => ({}));
  const postRev = isRecord(postRaw) && typeof postRaw.rev === 'string' ? postRaw.rev : undefined;

  const afterAttempt = await fetchFlowsV2(fetchFn, baseUrl, activeToken);
  if (afterAttempt.status === 401 || !afterAttempt.payload) {
    throw new Error(`Readback GET /flows failed with HTTP ${afterAttempt.status}`);
  }
  const after = afterAttempt.payload;
  const reloaded = after.flows.find((item) => item.id === targetNode.id);
  if (!reloaded || reloaded.func !== sourceText) {
    throw new Error('Readback verification failed: reloaded function node body does not match reference script');
  }
  if (after.flows.length !== before.flows.length) {
    throw new Error(`Unexpected Node-RED node count change: ${before.flows.length} -> ${after.flows.length}`);
  }

  return {
    ok: true,
    baseUrl,
    envelopeBytes,
    envelopeSha256,
    targetNodeId: targetNode.id,
    targetAlreadyMatched,
    nodeCount: after.flows.length,
    revBefore: before.rev,
    revAfter: postRev ?? after.rev,
    verifiedMatch: true,
    tabs: after.flows
      .filter((item) => item.type === 'tab')
      .map((item) => ({
        id: item.id,
        label: item.label ?? '',
        disabled: Boolean(item.disabled),
      })),
  };
}

export function buildRemoteModulePayload(options: {
  baseUrl: string;
  envelopeSource: string;
  username: string;
  passwordFile: string;
  backupPath?: string | null;
}): string {
  const scriptDir = dirname(fileURLToPath(import.meta.url));
  const tunnelTs = readFileSync(resolve(scriptDir, '../edge/node-red/gateway-nodered-tunnel.ts'), 'utf8');
  const syncTs = readFileSync(resolve(scriptDir, './sync-songyaxuan-nodered.ts'), 'utf8');

  const namespaceImports = new Map<string, string>();
  const namedImports = new Map<string, Set<string>>();
  const rawImports: string[] = [];
  const bodies: string[] = [];
  for (const source of [tunnelTs, syncTs]) {
    const stripped = stripTypeScriptTypes(source.replace(/^#![^\n]*\n/, ''), { mode: 'strip' });
    const body = stripped.replace(/^import\s+([\s\S]*?)\s+from\s+['"]([^'"]+)['"];?\s*$/gm, (_stmt, clause: string, spec: string) => {
      if (spec.startsWith('node:')) {
        const trimmed = clause.replace(/\s+/g, ' ').trim();
        if (trimmed.startsWith('* as ')) {
          namespaceImports.set(`${spec}:${trimmed}`, `import ${trimmed} from '${spec}';`);
        } else if (trimmed.startsWith('{') && trimmed.endsWith('}')) {
          const names = namedImports.get(spec) ?? new Set<string>();
          for (const part of trimmed.slice(1, -1).split(',')) {
            const binding = part.trim();
            if (binding) names.add(binding);
          }
          namedImports.set(spec, names);
        } else {
          const stmtClean = `import ${trimmed} from '${spec}';`;
          if (!rawImports.includes(stmtClean)) rawImports.push(stmtClean);
        }
      }
      return '';
    });
    bodies.push(body);
  }
  const imports: string[] = [
    ...namespaceImports.values(),
    ...Array.from(namedImports.entries()).map(
      ([spec, names]) => `import { ${Array.from(names).join(', ')} } from '${spec}';`,
    ),
    ...rawImports,
  ];

  return [
    ...imports,
    ...bodies,
    `const __remoteResult = await syncSongyaxuanEnvelopeViaApi(${JSON.stringify({
      baseUrl: options.baseUrl,
      envelopeSource: options.envelopeSource,
      username: options.username,
      passwordFile: options.passwordFile,
      ...(options.backupPath ? { backupPath: options.backupPath } : {}),
    })});`,
    'console.log(JSON.stringify(__remoteResult, null, 2));',
  ].join('\n');
}

function syncViaSsh(args: {
  sshTarget: string;
  baseUrl: string;
  envelopePath?: string;
  username: string;
  passwordFile: string;
  backupPath?: string | null;
}): void {
  const { sourceText } = loadEnvelopeSource({ envelopePath: args.envelopePath });
  const remoteScript = buildRemoteModulePayload({
    baseUrl: args.baseUrl,
    envelopeSource: sourceText,
    username: args.username,
    passwordFile: args.passwordFile,
    backupPath: args.backupPath,
  });

  const proc = spawnSync(
    'ssh',
    ['-o', 'BatchMode=yes', args.sshTarget, 'node', '--input-type=module', '-', '--embedded-remote'],
    {
      input: remoteScript,
      encoding: 'utf8',
      stdio: ['pipe', 'pipe', 'inherit'],
    },
  );
  if (proc.error) throw proc.error;
  if (proc.status !== 0) {
    throw new Error(`ssh remote sync exited with code ${proc.status ?? 1}`);
  }
  process.stdout.write(proc.stdout);
}

function parseCliArgs(argv: string[]): {
  sshTarget?: string;
  baseUrl: string;
  envelopePath?: string;
  username: string;
  passwordFile?: string;
  backupPath?: string;
} {
  const parsed: {
    sshTarget?: string;
    baseUrl: string;
    envelopePath?: string;
    username: string;
    passwordFile?: string;
    backupPath?: string;
  } = {
    baseUrl: process.env.NODERED_URL?.trim() || DEFAULT_NODERED_URL,
    username: process.env.NODERED_USERNAME?.trim() || DEFAULT_NODERED_USERNAME,
  };

  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    const next = argv[i + 1];
    if (arg === '--embedded-remote') continue;
    if (arg === '--ssh' && next) {
      parsed.sshTarget = next;
      i += 1;
    } else if (arg === '--url' && next) {
      parsed.baseUrl = next;
      i += 1;
    } else if (arg === '--envelope' && next) {
      parsed.envelopePath = next;
      i += 1;
    } else if (arg === '--username' && next) {
      parsed.username = next;
      i += 1;
    } else if (arg === '--password-file' && next) {
      parsed.passwordFile = next;
      i += 1;
    } else if (arg === '--backup' && next) {
      parsed.backupPath = next;
      i += 1;
    } else if (arg === '--help' || arg === '-h') {
      console.log(
        'Usage: node --experimental-strip-types scripts/sync-songyaxuan-nodered.ts [--ssh ubuntu@139.199.58.47] [--url http://127.0.0.1:18800] [--envelope <path>] [--username admin] [--password-file <path>] [--backup <path>]',
      );
      process.exit(0);
    } else {
      throw new Error(`Unknown or incomplete CLI argument: ${arg}`);
    }
  }
  return parsed;
}

const isCliEntry =
  !process.argv.includes('--embedded-remote') &&
  Boolean(process.argv[1]) &&
  import.meta.url.startsWith('file:') &&
  resolve(process.argv[1]) === resolve(fileURLToPath(import.meta.url));

if (isCliEntry) {
  const cli = parseCliArgs(process.argv.slice(2));
  if (cli.sshTarget) {
    syncViaSsh({
      sshTarget: cli.sshTarget,
      baseUrl: cli.baseUrl,
      envelopePath: cli.envelopePath,
      username: cli.username,
      passwordFile: cli.passwordFile ?? DEFAULT_PASSWORD_FILE,
      backupPath: cli.backupPath,
    });
  } else {
    const result = await syncSongyaxuanEnvelopeViaApi({
      baseUrl: cli.baseUrl,
      envelopePath: cli.envelopePath,
      username: cli.username,
      passwordFile: cli.passwordFile,
      backupPath: cli.backupPath,
    });
    console.log(JSON.stringify(result, null, 2));
  }
}
