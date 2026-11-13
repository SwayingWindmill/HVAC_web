import { spawnSync } from 'node:child_process';
import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import { resolveLinuxBrowserExecutable } from './lib/browser-runtime.mjs';

export interface NodeVersionTuple {
  readonly major: number;
  readonly minor: number;
  readonly patch: number;
}

export interface VerifyRuntimeReport {
  readonly platform: NodeJS.Platform;
  readonly nodeVersion: string;
  readonly minimumNodeVersion: string;
  readonly supportedNode: boolean;
  readonly browserExecutable: string;
  readonly checkedPorts: readonly number[];
  readonly listeningPorts: readonly number[];
}

export interface ParsedVerifyArgs {
  readonly help: boolean;
  readonly ports: readonly number[];
}

export interface CollectReportOptions {
  readonly platform?: NodeJS.Platform;
  readonly nodeVersion?: string;
  readonly resolveBrowser?: () => string;
  readonly procPaths?: readonly string[];
}

export const MINIMUM_SUPPORTED_NODE: NodeVersionTuple = {
  major: 22,
  minor: 22,
  patch: 0,
};

export const DEFAULT_VERIFY_PORTS: readonly number[] = [5190, 5194, 8443];

const TCP_LISTEN_STATE = '0A';
const DEFAULT_PROC_TCP_PATHS = ['/proc/net/tcp', '/proc/net/tcp6'] as const;

function formatVersionTuple(version: NodeVersionTuple): string {
  return `v${version.major}.${version.minor}.${version.patch}`;
}

function isMissingOptionalTcp6Error(path: string, error: unknown): boolean {
  return path === '/proc/net/tcp6'
    && typeof error === 'object'
    && error !== null
    && 'code' in error
    && error.code === 'ENOENT';
}

/** Parse a Node.js semver tag such as `v24.16.0` into numeric components. */
export function parseNodeVersion(rawVersion: string): NodeVersionTuple {
  const match = /^v?(\d+)\.(\d+)\.(\d+)/u.exec(rawVersion.trim());
  if (!match) {
    throw new Error(`Unable to parse Node.js version string: ${rawVersion}`);
  }
  return {
    major: Number(match[1]),
    minor: Number(match[2]),
    patch: Number(match[3]),
  };
}

/** Return whether the running Node.js version satisfies the minimum supported version. */
export function isSupportedNodeVersion(
  actual: NodeVersionTuple,
  minimum: NodeVersionTuple = MINIMUM_SUPPORTED_NODE,
): boolean {
  if (actual.major !== minimum.major) return actual.major > minimum.major;
  if (actual.minor !== minimum.minor) return actual.minor > minimum.minor;
  return actual.patch >= minimum.patch;
}

/** Convert a Windows drive-letter path (`E:\...`) to its WSL mount path (`/mnt/e/...`). */
export function windowsPathToWsl(windowsPath: string): string {
  const normalized = windowsPath.trim().replaceAll('\\', '/');
  const driveMatch = /^([A-Za-z]):(?:\/(.*))?$/u.exec(normalized);
  if (!driveMatch) {
    throw new Error(`Expected Windows drive-letter path to bridge into WSL, received: ${windowsPath}`);
  }
  const driveLetter = driveMatch[1].toLowerCase();
  const remainder = driveMatch[2] ?? '';
  return remainder ? `/mnt/${driveLetter}/${remainder}` : `/mnt/${driveLetter}`;
}

function appendPortList(rawList: string, destination: number[]): void {
  for (const rawPort of rawList.split(',')) {
    const trimmed = rawPort.trim();
    if (!/^\d+$/u.test(trimmed)) {
      throw new Error(`Invalid port value in --ports: ${trimmed}`);
    }
    const port = Number(trimmed);
    if (port < 1 || port > 65535) {
      throw new Error(`Port out of range (1-65535) in --ports: ${trimmed}`);
    }
    if (!destination.includes(port)) {
      destination.push(port);
    }
  }
}

/** Parse CLI arguments (`--ports=5190,5194,8443`, positional ports such as `5190`, `--help`). */
export function parseVerifyArguments(argv: readonly string[]): ParsedVerifyArgs {
  let help = false;
  let sawPortArgument = false;
  const collectedPorts: number[] = [];

  for (const rawArgument of argv) {
    if (rawArgument === '--help' || rawArgument === '-h') {
      help = true;
      continue;
    }
    if (rawArgument.startsWith('--ports=')) {
      sawPortArgument = true;
      const rawList = rawArgument.slice('--ports='.length).trim();
      if (rawList) {
        appendPortList(rawList, collectedPorts);
      }
      continue;
    }
    if (!rawArgument.startsWith('-') && /^\d+(?:,\s*\d+)*$/u.test(rawArgument.trim())) {
      sawPortArgument = true;
      appendPortList(rawArgument, collectedPorts);
      continue;
    }
    throw new Error(`Unknown argument: ${rawArgument}`);
  }

  return {
    help,
    ports: sawPortArgument ? collectedPorts : DEFAULT_VERIFY_PORTS,
  };
}

/** Extract sorted unique TCP listening ports (`st == 0A`) from `/proc/net/tcp{,6}` contents. */
export function parseListeningPortsFromProcNet(
  procNetContent: string,
  targetPorts?: readonly number[],
): number[] {
  const allowed = targetPorts ? new Set(targetPorts) : null;
  const listening = new Set<number>();

  for (const rawLine of procNetContent.split(/\r?\n/u)) {
    const trimmed = rawLine.trim();
    if (!trimmed || trimmed.startsWith('sl')) continue;
    const columns = trimmed.split(/\s+/u);
    if (columns.length < 4) continue;

    const localAddress = columns[1];
    const socketState = columns[3].toUpperCase();
    if (socketState !== TCP_LISTEN_STATE) continue;

    const colonIndex = localAddress.lastIndexOf(':');
    if (colonIndex === -1) continue;
    const portHex = localAddress.slice(colonIndex + 1);
    if (!/^[0-9a-fA-F]+$/u.test(portHex)) continue;

    const port = Number.parseInt(portHex, 16);
    if (port < 1 || port > 65535) continue;
    if (allowed && !allowed.has(port)) continue;
    listening.add(port);
  }

  return [...listening].sort((left, right) => left - right);
}

/** Read listening TCP ports from Linux `/proc/net/tcp` and `/proc/net/tcp6` without external shells. */
export async function readListeningPorts(
  targetPorts: readonly number[],
  procPaths: readonly string[] = DEFAULT_PROC_TCP_PATHS,
): Promise<number[]> {
  if (targetPorts.length === 0) {
    return [];
  }
  const combined = new Set<number>();
  for (const procPath of procPaths) {
    try {
      const content = await readFile(procPath, 'utf8');
      for (const port of parseListeningPortsFromProcNet(content, targetPorts)) {
        combined.add(port);
      }
    } catch (error) {
      // IPv6 kernels disabled at boot omit /proc/net/tcp6; treat only that ENOENT as empty.
      if (isMissingOptionalTcp6Error(procPath, error)) {
        continue;
      }
      throw error;
    }
  }
  return [...combined].sort((left, right) => left - right);
}

/** Collect Linux browser verification diagnostics (Node version, Chromium binary, open ports). */
export async function collectLinuxVerifyRuntimeReport(
  argv: readonly string[],
  options: CollectReportOptions = {},
): Promise<VerifyRuntimeReport> {
  const platform = options.platform ?? process.platform;
  if (platform !== 'linux') {
    throw new Error(`Linux verification runtime check requires platform "linux", received "${platform}".`);
  }

  const parsed = parseVerifyArguments(argv);
  const nodeVersion = options.nodeVersion ?? process.version;
  const supportedNode = isSupportedNodeVersion(parseNodeVersion(nodeVersion));
  const browserExecutable = (options.resolveBrowser ?? resolveLinuxBrowserExecutable)();
  const listeningPorts = await readListeningPorts(parsed.ports, options.procPaths);

  return {
    platform,
    nodeVersion,
    minimumNodeVersion: formatVersionTuple(MINIMUM_SUPPORTED_NODE),
    supportedNode,
    browserExecutable,
    checkedPorts: [...parsed.ports],
    listeningPorts,
  };
}

function bridgeThroughWsl(scriptPath: string, argv: readonly string[]): number {
  const wslScriptPath = windowsPathToWsl(scriptPath);
  const result = spawnSync(
    'wsl',
    [
      '-e',
      'bash',
      '-lc',
      // Use a login shell so ~/.local/bin or nvm Node installs enter PATH, while passing arguments as positional parameters to avoid shell quoting bugs.
      'exec node --experimental-strip-types "$@"',
      '_',
      wslScriptPath,
      ...argv,
    ],
    {
      stdio: 'inherit',
      windowsHide: true,
    },
  );
  if (result.error) {
    throw new Error(`Failed to run WSL verify runtime check: ${result.error.message}`);
  }
  return result.status ?? 1;
}

async function runCli(argv: readonly string[], scriptPath: string): Promise<void> {
  const parsed = parseVerifyArguments(argv);
  if (parsed.help) {
    process.stdout.write('Usage: node --experimental-strip-types scripts/check-verify-runtime.ts [--ports=5190,5194,8443 | 5190]\n');
    return;
  }

  if (process.platform === 'win32') {
    process.exitCode = bridgeThroughWsl(scriptPath, argv);
    return;
  }

  const report = await collectLinuxVerifyRuntimeReport(argv);
  process.stdout.write(`${JSON.stringify(report, null, 2)}\n`);
  if (!report.supportedNode) {
    process.stderr.write(
      `Unsupported Node.js version ${report.nodeVersion}; require ${report.minimumNodeVersion} or newer inside WSL/Linux.\n`,
    );
    process.exitCode = 1;
  }
}

const currentScriptPath = fileURLToPath(import.meta.url);
const invokedScriptPath = process.argv[1] ? resolve(process.argv[1]) : null;
if (invokedScriptPath === resolve(currentScriptPath)) {
  runCli(process.argv.slice(2), currentScriptPath).catch((error: unknown) => {
    const message = error instanceof Error ? error.message : String(error);
    process.stderr.write(`${message}\n`);
    process.exitCode = 1;
  });
}
