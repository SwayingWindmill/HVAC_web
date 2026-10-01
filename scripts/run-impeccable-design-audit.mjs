#!/usr/bin/env node

import { spawnSync } from 'node:child_process';
import { existsSync, readFileSync } from 'node:fs';
import path from 'node:path';

const ROOT = process.cwd();
const IMPECCABLE_PACKAGE = 'impeccable@3.5.0';
const UI_ROOT = 'apps/hvac-web/src';
const UI_EXTENSIONS = new Set(['.tsx', '.jsx', '.css', '.scss', '.sass', '.less', '.html', '.vue', '.svelte', '.astro', '.ts', '.js']);
const HOOK_CONFIG = path.join(ROOT, '.impeccable', 'config.json');

const args = process.argv.slice(2);
const isPostHook = args.includes('--hook');
const isStopHook = args.includes('--hook-stop');
const scopeArg = args.find((arg) => arg.startsWith('--scope='));
const explicitTargets = args.filter((arg) => !arg.startsWith('--'));

function normalize(relativeOrAbsolute) {
  if (!relativeOrAbsolute) return null;
  const absolute = path.isAbsolute(relativeOrAbsolute)
    ? relativeOrAbsolute
    : path.resolve(ROOT, relativeOrAbsolute);
  return path.relative(ROOT, absolute).replaceAll('\\', '/');
}

function isUiFile(file) {
  const normalized = normalize(file);
  if (!normalized || normalized.startsWith('..')) return false;
  return normalized.startsWith(`${UI_ROOT}/`) && UI_EXTENSIONS.has(path.extname(normalized).toLowerCase());
}

function hookEnabled() {
  if (!existsSync(HOOK_CONFIG)) return true;
  try {
    const config = JSON.parse(readFileSync(HOOK_CONFIG, 'utf8'));
    return config?.hook?.enabled !== false;
  } catch {
    return true;
  }
}

async function readStdinEvent() {
  if (process.stdin.isTTY) return null;
  const chunks = [];
  for await (const chunk of process.stdin) chunks.push(chunk);
  if (chunks.length === 0) return null;
  try {
    return JSON.parse(Buffer.concat(chunks).toString('utf8'));
  } catch {
    return null;
  }
}

function changedUiFiles() {
  const commands = [
    ['diff', '--name-only', '--diff-filter=ACMR', 'HEAD', '--', UI_ROOT],
    ['ls-files', '--others', '--exclude-standard', '--', UI_ROOT],
  ];
  const files = new Set();
  for (const command of commands) {
    const result = spawnSync('git', command, { cwd: ROOT, encoding: 'utf8', windowsHide: true });
    if (result.status !== 0) continue;
    for (const line of result.stdout.split(/\r?\n/)) {
      const normalized = normalize(line.trim());
      if (normalized && isUiFile(normalized)) files.add(normalized);
    }
  }
  return [...files].slice(0, 40);
}

function detectorCommand() {
  const local = path.join(ROOT, 'node_modules', '.bin', 'impeccable');
  if (existsSync(local)) {
    return { command: local, prefix: ['detect', '--json'], engine: IMPECCABLE_PACKAGE };
  }
  return {
    command: process.execPath,
    prefix: [path.join(ROOT, 'scripts', 'impeccable-hvac-detector.mjs'), '--json'],
    engine: 'hvac-impeccable-adapter',
  };
}

function runDetector(targets, timeoutMs) {
  const unique = [...new Set(targets.map(normalize).filter(Boolean))];
  if (unique.length === 0) return { status: 0, stdout: '', stderr: '', engine: 'none' };
  const { command, prefix, engine } = detectorCommand();
  const result = spawnSync(command, [...prefix, ...unique], {
    cwd: ROOT,
    encoding: 'utf8',
    timeout: timeoutMs,
    windowsHide: true,
    maxBuffer: 4 * 1024 * 1024,
  });
  if (result.error) {
    return { status: null, stdout: result.stdout ?? '', stderr: result.error.message, engine };
  }
  return {
    status: result.status,
    stdout: result.stdout ?? '',
    stderr: result.stderr ?? '',
    engine,
  };
}

function extractFindings(stdout) {
  if (!stdout.trim()) return [];
  try {
    const parsed = JSON.parse(stdout);
    if (Array.isArray(parsed)) return parsed;
    if (Array.isArray(parsed?.findings)) return parsed.findings;
    if (Array.isArray(parsed?.issues)) return parsed.issues;
    if (Array.isArray(parsed?.results)) return parsed.results;
  } catch {
    return [];
  }
  return [];
}

function findingText(finding) {
  if (!finding || typeof finding !== 'object') return String(finding);
  const rule = finding.ruleId ?? finding.rule ?? finding.id ?? finding.code ?? 'design';
  const file = finding.file ?? finding.filePath ?? finding.path ?? '';
  const line = finding.line ?? finding.location?.line ?? '';
  const message = finding.message ?? finding.description ?? finding.reason ?? finding.title ?? 'Impeccable finding';
  const where = file ? `${file}${line ? `:${line}` : ''}` : '';
  return `- [${rule}]${where ? ` ${where}` : ''}: ${message}`;
}

function hookContext(eventName, result, targets) {
  if (result.status !== 2) return null;
  const findings = extractFindings(result.stdout);
  const details = findings.slice(0, 6).map(findingText);
  const count = findings.length || 'one or more';
  const targetLabel = targets.length === 1 ? targets[0] : `${targets.length} changed UI files`;
  return [
    `Impeccable detected ${count} frontend design issue(s) in ${targetLabel}.`,
    ...details,
    'Treat these as design-review evidence, not an instruction to blindly change product truth. Fix real issues using PRODUCT.md, DESIGN.md, the matching current surface specification, docs/design-system/shadcn-component-contract.md, docs/design-system/legacy-ui-quarantine.md, and the explicitly approved current reference. Historical Ant Design / ProComponents semantics, screenshots, page anatomy, and migration-only components are not visual authority. If a detector hit is intentional, keep the design and document/scope the exception instead of adding compatibility CSS.',
    'For a focused rerun: npm run web:design:device-center (Device Center) or npm run web:design:detect (full Web source).',
  ].join('\n');
}

function emitAdditionalContext(eventName, text) {
  if (!text) return;
  process.stdout.write(JSON.stringify({
    hookSpecificOutput: {
      hookEventName: eventName,
      additionalContext: text,
    },
  }));
}

function manualTargets() {
  if (explicitTargets.length > 0) return explicitTargets;
  const scope = scopeArg?.slice('--scope='.length) ?? 'web';
  if (scope === 'device-center') return ['apps/hvac-web/src/features/devices'];
  if (scope === 'shell') return ['apps/hvac-web/src/app/ShellChrome.tsx'];
  if (scope === 'changed') return changedUiFiles();
  return [UI_ROOT];
}

if (args.includes('--help')) {
  console.log(`Usage:\n  node scripts/run-impeccable-design-audit.mjs [--scope=web|device-center|shell|changed] [path ...]\n\nPinned detector: ${IMPECCABLE_PACKAGE}\nThe detector is advisory for frontend development; it is not a replacement for browser visual review or product-contract tests.`);
  process.exit(0);
}

if (isPostHook || isStopHook) {
  if (!hookEnabled()) process.exit(0);
  const event = await readStdinEvent();
  if (isStopHook && event?.stop_hook_active === true) process.exit(0);

  let targets = [];
  if (isPostHook) {
    const direct = event?.tool_input?.file_path ?? event?.tool_input?.path ?? null;
    if (direct && isUiFile(direct)) targets = [normalize(direct)];
    else targets = changedUiFiles().slice(0, 12);
  } else {
    targets = changedUiFiles();
  }

  if (targets.length === 0) process.exit(0);
  const result = runDetector(targets, isStopHook ? 25_000 : 8_000);
  const eventName = event?.hook_event_name ?? (isStopHook ? 'Stop' : 'PostToolUse');
  emitAdditionalContext(eventName, hookContext(eventName, result, targets));
  process.exit(0);
}

const targets = manualTargets();
if (targets.length === 0) {
  console.log('No changed HVAC Web UI files to scan.');
  process.exit(0);
}

const result = runDetector(targets, 90_000);
if (result.stdout) process.stdout.write(result.stdout);
if (result.stderr) process.stderr.write(result.stderr);
if (result.status === 0 || result.status === 2) process.exit(result.status);
process.stderr.write(`\nImpeccable detector could not complete (status ${String(result.status)}).\n`);
process.exit(1);
