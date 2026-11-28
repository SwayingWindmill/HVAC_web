import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync } from 'node:fs';
import { dirname, join, resolve, sep } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

import { findChangelogFragmentViolations } from './changelog-fragments.ts';
import { findGoFileLineViolations } from './check-go-file-lines.ts';
import { findGoTestHygieneViolations } from './check-go-test-hygiene.ts';
import { goExecutableName, readGoWorkspace } from './lib/go-workspace.ts';

const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const maximumInlineCommands = 4;
export const maximumHandoffBytes = 28 * 1024;
const javascriptToolingBaselinePath = 'scripts/javascript-tooling-baseline.json';
const requiredLinguistExclusions = Object.freeze([
  'scripts/** -linguist-detectable',
  'services/operations-agent-service/test/** -linguist-detectable',
  '.agents/** -linguist-detectable',
]);

const forbiddenTrackedPrefixes = Object.freeze([
  '.ai-bridge/',
  '.audit/',
  '.clones/',
  '.codegraph/',
  '.workbuddy/',
  '.worktrees/',
  'hvac-backend/logs/',
  'out/',
  'prototype/',
]);

const forbiddenTrackedSuffixes = Object.freeze([
  '.bak',
  '.log',
  '.orig',
  '.swp',
  '.tmp',
]);

const forbiddenTrackedBasenames = new Set(['.DS_Store', 'Thumbs.db']);

const normalizePath = (value: string): string => value.split(sep).join('/').replaceAll('\\', '/');
const countInlineCommands = (command: string): number => command.split(/\s*&&\s*/u).filter(Boolean).length;

type JavaScriptToolingFile = { path: string; bytes: number };
type JavaScriptRootBaseline = { fileCount: number; maxBytes: number };
type JavaScriptToolingBaseline = {
  schemaVersion: number;
  policy: string;
  trackedExtensions: string[];
  allowedLegacyRoots: Record<string, JavaScriptRootBaseline>;
  fileCount: number;
  maxBytes: number;
};

export const findPackageScriptViolations = (scripts: Record<string, string>) => {
  const violations: string[] = [];

  const historicalStageScripts = Object.keys(scripts).filter((name) => /^s\d+:/u.test(name));
  for (const name of historicalStageScripts) {
    violations.push(`package.json: historical stage task \`${name}\` must be expressed through a stable domain task`);
  }

  for (const [name, command] of Object.entries(scripts)) {
    const commandCount = countInlineCommands(command);
    if (commandCount > maximumInlineCommands) {
      violations.push(`package.json: script \`${name}\` contains ${commandCount} inline commands; move orchestration into domain-task-matrix or a script`);
    }
  }

  return violations;
};

export const findTrackedArtifactViolations = (trackedFiles: readonly string[]): string[] => {
  const violations: string[] = [];
  for (const rawPath of trackedFiles) {
    const path = normalizePath(rawPath);
    const basename = path.slice(path.lastIndexOf('/') + 1);
    if (forbiddenTrackedPrefixes.some((prefix) => path.startsWith(prefix))) {
      violations.push(`${path}: generated, local coordination, or archived runtime content is tracked`);
      continue;
    }
    if (forbiddenTrackedBasenames.has(basename)
      || forbiddenTrackedSuffixes.some((suffix) => basename.endsWith(suffix))) {
      violations.push(`${path}: transient file type is tracked`);
    }
  }
  return violations;
};

export const findJavascriptToolingViolations = ({ files, baseline }: { files: JavaScriptToolingFile[]; baseline: JavaScriptToolingBaseline }) => {
  if (baseline?.schemaVersion !== 1 || baseline?.policy !== 'legacy-js-ratchet'
    || !Array.isArray(baseline.trackedExtensions)
    || !baseline.allowedLegacyRoots || typeof baseline.allowedLegacyRoots !== 'object'
    || !Number.isInteger(baseline.fileCount) || !Number.isInteger(baseline.maxBytes)) {
    return [`${javascriptToolingBaselinePath}: invalid or unsupported baseline schema`];
  }

  const normalizedFiles = files
    .map(({ path, bytes }) => ({ path: normalizePath(path), bytes }))
    .filter(({ path }) => baseline.trackedExtensions.some((extension) => path.endsWith(extension)))
    .sort((left, right) => left.path.localeCompare(right.path));
  const violations = [];
  const roots = Object.keys(baseline.allowedLegacyRoots);
  const rootTotals = Object.fromEntries(roots.map((root) => [root, { fileCount: 0, bytes: 0 }]));

  for (const file of normalizedFiles) {
    const root = roots.find((candidate) => file.path.startsWith(candidate));
    if (!root) {
      violations.push(`${file.path}: JavaScript is outside reviewed legacy tooling roots; use TypeScript for new repository or product code`);
      continue;
    }
    rootTotals[root].fileCount += 1;
    rootTotals[root].bytes += file.bytes;
  }

  const totalBytes = normalizedFiles.reduce((sum, file) => sum + file.bytes, 0);
  if (normalizedFiles.length > baseline.fileCount) {
    violations.push(`${javascriptToolingBaselinePath}: JavaScript file count grew beyond ${baseline.fileCount} to ${normalizedFiles.length}; migrate tooling to TypeScript instead of growing legacy JavaScript`);
  } else if (normalizedFiles.length < baseline.fileCount) {
    violations.push(`${javascriptToolingBaselinePath}: JavaScript file count dropped from ${baseline.fileCount} to ${normalizedFiles.length}; tighten ${javascriptToolingBaselinePath}`);
  }
  if (totalBytes > baseline.maxBytes) {
    violations.push(`${javascriptToolingBaselinePath}: JavaScript bytes grew from ${baseline.maxBytes} to ${totalBytes}; migrate tooling to TypeScript instead of growing legacy JavaScript`);
  } else if (totalBytes < baseline.maxBytes) {
    violations.push(`${javascriptToolingBaselinePath}: JavaScript bytes dropped from ${baseline.maxBytes} to ${totalBytes}; tighten ${javascriptToolingBaselinePath}`);
  }

  for (const [root, expected] of Object.entries(baseline.allowedLegacyRoots)) {
    const actual = rootTotals[root];
    if (!actual) continue;
    if (actual.fileCount > expected.fileCount) {
      violations.push(`${javascriptToolingBaselinePath}: ${root} JavaScript file count grew beyond ${expected.fileCount} to ${actual.fileCount}`);
    } else if (actual.fileCount < expected.fileCount) {
      violations.push(`${javascriptToolingBaselinePath}: ${root} JavaScript file count dropped from ${expected.fileCount} to ${actual.fileCount}; tighten ${javascriptToolingBaselinePath}`);
    }
    if (actual.bytes > expected.maxBytes) {
      violations.push(`${javascriptToolingBaselinePath}: ${root} JavaScript bytes grew from ${expected.maxBytes} to ${actual.bytes}`);
    } else if (actual.bytes < expected.maxBytes) {
      violations.push(`${javascriptToolingBaselinePath}: ${root} JavaScript bytes dropped from ${expected.maxBytes} to ${actual.bytes}; tighten ${javascriptToolingBaselinePath}`);
    }
  }

  return violations;
};

export const findLinguistViolations = (gitattributes: string): string[] => requiredLinguistExclusions
  .filter((entry) => !gitattributes.includes(entry))
  .map((entry) => `.gitattributes: missing ancillary Linguist exclusion \`${entry}\``);

export const findWorkflowViolations = (workflow: string): string[] => {
  const required = 'node --experimental-strip-types scripts/run-pr-gate.ts --gate=static';
  return workflow.includes(`- run: ${required}`)
    ? []
    : [`.github/workflows/pr-gates.yml: missing canonical static gate \`${required}\``];
};

const requiredGuardrailFiles = Object.freeze([
  '.agents/hooks.json',
  '.claude/settings.json',
  'scripts/hooks/pre-tool-guard.ts',
  'tsconfig.scripts.json',
  'scripts/web-as-cast-baseline.json',
  'scripts/web-file-lines-baseline.json',
  'scripts/coverage-baseline.json',
  'scripts/check-coverage-regression.ts',
  'scripts/verify-live-web.ts',
  '.agents/skills/command-safety/SKILL.md',
  '.claude/skills/command-safety/SKILL.md',
  'scripts/run-command-formal-check.ts',
  'scripts/migration-conventions-baseline.json',
  'scripts/check-migration-conventions.ts',
  'scripts/sync-phase1-migrations.ts',
  'scripts/test-sync-phase1-migrations.ts',
  'scripts/check-clickhouse-sql.ts',
  'scripts/test-clickhouse-sql.ts',
  'scripts/check-go-test-hygiene.ts',
  'scripts/go-test-sleep-baseline.json',
  'scripts/test-go-test-hygiene.ts',
  'scripts/check-go-file-lines.ts',
  'scripts/go-file-lines-baseline.json',
  'scripts/test-go-file-lines.ts',
  'scripts/run-preflight.ts',
  'changelog.d/README.md',
  'scripts/changelog-fragments.ts',
  'scripts/test-changelog-fragments.ts',
]);

const debugMarkerExtensions = Object.freeze(['.go', '.ts', '.tsx', '.mjs', '.js', '.cjs']);
const debugMarkerPattern = /\[DEBUG-[A-Za-z0-9_-]+\]/;

export const findDebugMarkerViolations = ({
  root,
  trackedFiles,
}: {
  root: string;
  trackedFiles: readonly string[];
}): string[] => {
  const violations: string[] = [];
  for (const rawPath of trackedFiles) {
    const path = normalizePath(rawPath);
    if (!debugMarkerExtensions.some((extension) => path.endsWith(extension))) continue;
    const absPath = join(root, path);
    if (!existsSync(absPath)) continue;
    const match = debugMarkerPattern.exec(readFileSync(absPath, 'utf8'));
    if (match) {
      violations.push(`${path}: leftover debug marker ${match[0]}; remove temporary debug instrumentation before committing`);
    }
  }
  return violations;
};

const allowedAgentsSkillRootFiles = new Set(['PSTACK-LICENSE.txt']);
const allowedClaudeSkillRootFiles = new Set(['README.md']);

export const findRequiredGuardrailFileViolations = (trackedFiles: string[]) => {
  const trackedSet = new Set(trackedFiles.map(normalizePath));
  return requiredGuardrailFiles
    .filter((path) => !trackedSet.has(path))
    .map((path) => `${path}: required repository safety hook file is missing from git tracking`);
};

const collectSkillMirrorEntries = ({
  trackedFiles,
  prefix,
  allowedRootFiles,
  violations,
}: {
  trackedFiles: string[];
  prefix: string;
  allowedRootFiles: Set<string>;
  violations: string[];
}) => {
  const entries = new Map<string, string>();
  for (const rawPath of trackedFiles) {
    const path = normalizePath(rawPath);
    if (!path.startsWith(prefix)) continue;
    const rel = path.slice(prefix.length);
    if (!rel.includes('/')) {
      if (!allowedRootFiles.has(rel)) {
        violations.push(`${path}: unexpected root file under ${prefix}`);
      }
      continue;
    }
    entries.set(rel, path);
  }
  return entries;
};

export const findSkillMirrorViolations = ({
  root,
  trackedFiles,
}: {
  root: string;
  trackedFiles: string[];
}) => {
  const violations: string[] = [];
  const agentsSkillsPrefix = '.agents/skills/';
  const claudeSkillsPrefix = '.claude/skills/';
  const agentsSkillFiles = collectSkillMirrorEntries({
    trackedFiles,
    prefix: agentsSkillsPrefix,
    allowedRootFiles: allowedAgentsSkillRootFiles,
    violations,
  });
  const claudeSkillFiles = collectSkillMirrorEntries({
    trackedFiles,
    prefix: claudeSkillsPrefix,
    allowedRootFiles: allowedClaudeSkillRootFiles,
    violations,
  });

  for (const [rel, agentsPath] of [...agentsSkillFiles.entries()].sort(([a], [b]) => a.localeCompare(b))) {
    if (!claudeSkillFiles.has(rel)) {
      violations.push(`${agentsPath}: missing mirrored counterpart at ${claudeSkillsPrefix}${rel}; mirror both skill directories 1:1`);
    }
  }

  for (const [rel, claudePath] of [...claudeSkillFiles.entries()].sort(([a], [b]) => a.localeCompare(b))) {
    if (!agentsSkillFiles.has(rel)) {
      violations.push(`${claudePath}: missing mirrored counterpart at ${agentsSkillsPrefix}${rel}; mirror both skill directories 1:1`);
    }
  }

  for (const [rel, agentsPath] of [...agentsSkillFiles.entries()].sort(([a], [b]) => a.localeCompare(b))) {
    const claudePath = claudeSkillFiles.get(rel);
    if (!claudePath) continue;
    const agentsAbs = join(root, agentsPath);
    const claudeAbs = join(root, claudePath);
    if (!existsSync(agentsAbs)) {
      violations.push(`${agentsPath}: tracked skill file is missing on disk`);
      continue;
    }
    if (!existsSync(claudeAbs)) {
      violations.push(`${claudePath}: tracked skill file is missing on disk`);
      continue;
    }
    if (Buffer.compare(readFileSync(agentsAbs), readFileSync(claudeAbs)) !== 0) {
      violations.push(`${agentsPath}: byte content drifted from ${claudePath}; sync both skill files verbatim`);
    }
  }

  return violations;
};

const verifyFeaturesPrefix = '.agents/skills/verify/features/';
const verifyFeaturesIndex = '.agents/skills/verify/features/README.md';
const verifyE2eSpecPrefix = 'apps/hvac-web/e2e/';
const verifyE2eSpecSuffix = '.spec.ts';
const requiredVerifyFeatureHeadings = Object.freeze([
  '## Sub-features',
  '## How to get to it (user POV)',
  '## Driving it with Playwright',
  '## Gotchas',
]);

const escapeRegExp = (value: string): string => value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

export const findVerifyFeatureMapViolations = ({
  root,
  trackedFiles,
}: {
  root: string;
  trackedFiles: string[];
}): string[] => {
  const violations: string[] = [];
  const featureMaps = trackedFiles
    .filter(
      (file) =>
        file.startsWith(verifyFeaturesPrefix) &&
        file.endsWith('.md') &&
        file !== verifyFeaturesIndex,
    )
    .sort();
  const e2eSpecs = trackedFiles
    .filter((file) => file.startsWith(verifyE2eSpecPrefix) && file.endsWith(verifyE2eSpecSuffix))
    .sort();

  const hasVerifyIndex = trackedFiles.includes(verifyFeaturesIndex);
  if (!hasVerifyIndex && featureMaps.length === 0 && e2eSpecs.length === 0) {
    return violations;
  }

  let readmeContent = '';
  const readmeAbs = join(root, verifyFeaturesIndex);
  if (!hasVerifyIndex || !existsSync(readmeAbs)) {
    violations.push(`${verifyFeaturesIndex}: missing verify features index README.md`);
  } else {
    readmeContent = readFileSync(readmeAbs, 'utf8');
  }

  const referencedSpecs = new Set<string>();
  for (const mapPath of featureMaps) {
    const mapAbs = join(root, mapPath);
    if (!existsSync(mapAbs)) {
      violations.push(`${mapPath}: tracked verify feature map is missing on disk`);
      continue;
    }

    const basename = mapPath.slice(verifyFeaturesPrefix.length);
    if (readmeContent) {
      const linkPattern = new RegExp(`\\[[^\\]]+\\]\\((?:\\./)?${escapeRegExp(basename)}\\)`);
      if (!linkPattern.test(readmeContent)) {
        violations.push(`${mapPath}: not indexed in ${verifyFeaturesIndex}`);
      }
    }

    const content = readFileSync(mapAbs, 'utf8');
    for (const heading of requiredVerifyFeatureHeadings) {
      const headingPattern = new RegExp(`^${escapeRegExp(heading)}\\s*$`, 'm');
      if (!headingPattern.test(content)) {
        violations.push(`${mapPath}: missing required heading "${heading}"`);
      }
    }

    for (const specPath of e2eSpecs) {
      const specName = specPath.slice(verifyE2eSpecPrefix.length);
      const specPattern = new RegExp(`(?:^|[^A-Za-z0-9_.-])${escapeRegExp(specName)}(?:$|[^A-Za-z0-9_.-])`);
      if (specPattern.test(content)) {
        referencedSpecs.add(specPath);
      }
    }
  }

  for (const specPath of e2eSpecs) {
    if (!referencedSpecs.has(specPath)) {
      violations.push(
        `${specPath}: unmapped Playwright spec; document it under ${verifyFeaturesPrefix} and index it in ${verifyFeaturesIndex}`,
      );
    }
  }

  return violations;
};

export const findHandoffBudgetViolations = ({
  bytes,
  maxBytes = maximumHandoffBytes,
}: {
  bytes: number;
  maxBytes?: number;
}) => (bytes <= maxBytes
  ? []
  : [
    `docs/HANDOFF.md: ${bytes} bytes exceeds ${maxBytes}-byte single-read budget; keep only live server state, site constraints, and local environment deviations in docs/HANDOFF.md, move runbooks to docs/operations/, and encode recurring rules in checks (/correct)`,
  ]);

const composePortScanRoots = Object.freeze(['infra/', 'deploy/', 'semantic/']);
const composePortAllowedPublicBindings = new Map<string, ReadonlySet<string>>([
  ['deploy/platform/phase1/compose.yaml', new Set(['${HTTPS_PORT:-443}:443', '${MQTT_TLS_PORT:-8883}:8883'])],
]);

type ComposePortMapping = {
  line: number;
  kind: 'short' | 'long';
  spec: string;
};

const isComposePortPolicyTarget = (path: string): boolean =>
  composePortScanRoots.some((prefix) => path.startsWith(prefix))
  && /(?:^|\/).*(?:compose|override)[^/]*\.ya?ml$/u.test(path);

const stripYamlComment = (line: string): string => {
  let quote: '"' | "'" | null = null;
  for (let index = 0; index < line.length; index += 1) {
    const ch = line[index];
    if (quote === "'") {
      if (ch === "'" && line[index + 1] === "'") {
        index += 1;
      } else if (ch === "'") {
        quote = null;
      }
      continue;
    }
    if (quote === '"') {
      if (ch === '\\') {
        index += 1;
      } else if (ch === '"') {
        quote = null;
      }
      continue;
    }
    if (ch === '"' || ch === "'") {
      quote = ch;
    } else if (ch === '#' && (index === 0 || /\s/u.test(line[index - 1]))) {
      return line.slice(0, index);
    }
  }
  return line;
};

const unquoteYamlScalar = (raw: string): string => {
  const trimmed = raw.trim();
  if (trimmed.length >= 2 && trimmed.startsWith('"') && trimmed.endsWith('"')) {
    return trimmed.slice(1, -1).replace(/\\"/g, '"').replace(/\\\\/g, '\\');
  }
  if (trimmed.length >= 2 && trimmed.startsWith("'") && trimmed.endsWith("'")) {
    return trimmed.slice(1, -1).replace(/''/g, "'");
  }
  return trimmed;
};

const splitInlineYamlList = (inner: string): string[] => {
  const items: string[] = [];
  let current = '';
  let quote: '"' | "'" | null = null;
  let braceDepth = 0;
  for (let index = 0; index < inner.length; index += 1) {
    const ch = inner[index];
    if (quote === "'") {
      current += ch;
      if (ch === "'" && inner[index + 1] === "'") {
        current += inner[index + 1];
        index += 1;
      } else if (ch === "'") {
        quote = null;
      }
      continue;
    }
    if (quote === '"') {
      current += ch;
      if (ch === '\\' && index + 1 < inner.length) {
        current += inner[index + 1];
        index += 1;
      } else if (ch === '"') {
        quote = null;
      }
      continue;
    }
    if (ch === '"' || ch === "'") {
      quote = ch;
      current += ch;
    } else if (ch === '{') {
      braceDepth += 1;
      current += ch;
    } else if (ch === '}') {
      braceDepth = Math.max(0, braceDepth - 1);
      current += ch;
    } else if (ch === ',' && braceDepth === 0) {
      if (current.trim()) items.push(current.trim());
      current = '';
    } else {
      current += ch;
    }
  }
  if (current.trim()) items.push(current.trim());
  return items;
};

const extractInlineObjectHostIp = (rawObject: string): string | null => {
  const match = /\bhost_ip\s*:\s*([^,}\s]+)/u.exec(rawObject);
  return match ? unquoteYamlScalar(match[1]) : null;
};

const isLoopbackPortBinding = (spec: string): boolean => {
  if (!spec.startsWith('127.0.0.1:')) return false;
  const remainder = spec.slice('127.0.0.1:'.length);
  return remainder.includes(':');
};

const collectComposePortMappings = (content: string): ComposePortMapping[] => {
  const lines = content.replace(/\r\n?/gu, '\n').split('\n');
  const mappings: ComposePortMapping[] = [];

  for (let index = 0; index < lines.length; index += 1) {
    const cleaned = stripYamlComment(lines[index]);
    const portsMatch = /^(\s*)ports:\s*(.*)$/u.exec(cleaned);
    if (!portsMatch) continue;

    const portsIndent = portsMatch[1].length;
    const inlineValue = portsMatch[2].trim();
    if (inlineValue.startsWith('[')) {
      let combined = inlineValue;
      let cursor = index;
      while (!combined.includes(']') && cursor + 1 < lines.length) {
        cursor += 1;
        combined += ` ${stripYamlComment(lines[cursor]).trim()}`;
      }
      const closeIndex = combined.indexOf(']');
      const inner = closeIndex === -1 ? combined.slice(1) : combined.slice(1, closeIndex);
      for (const item of splitInlineYamlList(inner)) {
        if (item.startsWith('{')) {
          const hostIp = extractInlineObjectHostIp(item);
          mappings.push({
            line: index + 1,
            kind: hostIp === '127.0.0.1' ? 'short' : 'long',
            spec: hostIp === '127.0.0.1' ? '127.0.0.1::' : item,
          });
        } else {
          mappings.push({ line: index + 1, kind: 'short', spec: unquoteYamlScalar(item) });
        }
      }
      index = cursor;
      continue;
    }

    if (inlineValue.length > 0) continue;

    let listItemIndent: number | null = null;
    let cursor = index + 1;
    while (cursor < lines.length) {
      const nextCleaned = stripYamlComment(lines[cursor]);
      const nextTrimmed = nextCleaned.trim();
      if (!nextTrimmed) {
        cursor += 1;
        continue;
      }
      const nextIndent = nextCleaned.length - nextCleaned.trimStart().length;
      if (nextIndent < portsIndent || (nextIndent === portsIndent && !nextTrimmed.startsWith('-'))) {
        break;
      }
      if (listItemIndent === null) {
        if (!nextTrimmed.startsWith('-')) break;
        listItemIndent = nextIndent;
      }
      if (nextIndent === listItemIndent && (nextTrimmed === '-' || /^-\s+/u.test(nextTrimmed))) {
        const itemLine = cursor + 1;
        const afterDash = nextTrimmed.slice(1).trim();
        if (afterDash.startsWith('{')) {
          const hostIp = extractInlineObjectHostIp(afterDash);
          mappings.push({
            line: itemLine,
            kind: hostIp === '127.0.0.1' ? 'short' : 'long',
            spec: hostIp === '127.0.0.1' ? '127.0.0.1::' : afterDash,
          });
          cursor += 1;
          continue;
        }
        const keyValMatch = /^([A-Za-z0-9_-]+)(?::\s+(.*)|:\s*)$/u.exec(afterDash);
        if (afterDash && !keyValMatch) {
          mappings.push({ line: itemLine, kind: 'short', spec: unquoteYamlScalar(afterDash) });
          cursor += 1;
          continue;
        }
        const fields = new Map<string, string>();
        if (keyValMatch) {
          fields.set(keyValMatch[1], unquoteYamlScalar(keyValMatch[2]));
        }
        let scalarFallback: string | null = null;
        cursor += 1;
        while (cursor < lines.length) {
          const subCleaned = stripYamlComment(lines[cursor]);
          const subTrimmed = subCleaned.trim();
          if (!subTrimmed) {
            cursor += 1;
            continue;
          }
          const subIndent = subCleaned.length - subCleaned.trimStart().length;
          if (subIndent <= listItemIndent) break;
          const subKeyMatch = /^([A-Za-z0-9_-]+)\s*:\s*(.*)$/u.exec(subTrimmed);
          if (subKeyMatch) {
            fields.set(subKeyMatch[1], unquoteYamlScalar(subKeyMatch[2]));
          } else if (fields.size === 0 && !scalarFallback) {
            scalarFallback = unquoteYamlScalar(subTrimmed);
          }
          cursor += 1;
        }
        if (fields.size === 0 && scalarFallback !== null) {
          mappings.push({ line: itemLine, kind: 'short', spec: scalarFallback });
        } else {
          const hostIp = fields.get('host_ip') ?? '';
          if (hostIp === '127.0.0.1') {
            mappings.push({ line: itemLine, kind: 'short', spec: '127.0.0.1::' });
          } else {
            const target = fields.get('target') ?? '<unset>';
            mappings.push({
              line: itemLine,
              kind: 'long',
              spec: `host_ip=${hostIp || '<unset>'} target=${target}`,
            });
          }
        }
        continue;
      }
      cursor += 1;
    }
    index = cursor - 1;
  }

  return mappings;
};

export const findComposePortExposureViolations = ({
  root,
  trackedFiles,
}: {
  root: string;
  trackedFiles: readonly string[];
}): string[] => {
  const violations: string[] = [];
  for (const rawPath of trackedFiles) {
    const path = normalizePath(rawPath);
    if (!isComposePortPolicyTarget(path)) continue;
    const absPath = join(root, path);
    if (!existsSync(absPath)) continue;
    const allowedBindings = composePortAllowedPublicBindings.get(path);
    const mappings = collectComposePortMappings(readFileSync(absPath, 'utf8'));
    for (const mapping of mappings) {
      if (allowedBindings?.has(mapping.spec)) continue;
      if (mapping.kind === 'short' && isLoopbackPortBinding(mapping.spec)) continue;
      violations.push(
        `${path}:${mapping.line}: non-loopback Compose port binding "${mapping.spec}"; bind host ports with 127.0.0.1:<host>:<container>`,
      );
    }
  }
  return violations;
};

const resolveGitListFilesInvocation = (root: string) => ({
  gitExecutable: 'git',
  args: ['-C', root, 'ls-files', '-z'],
});

const listTrackedFiles = (root: string): string[] => {
  const { gitExecutable, args } = resolveGitListFilesInvocation(root);
  const output = execFileSync(gitExecutable, args, {
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  return output.split('\0').filter(Boolean);
};

const listTrackedJavaScriptFiles = (
  root: string,
  trackedFiles: readonly string[],
  extensions: readonly string[],
): JavaScriptToolingFile[] => trackedFiles
  .map(normalizePath)
  .filter((path) => extensions.some((extension) => path.endsWith(extension)))
  .filter((path) => existsSync(join(root, path)))
  .map((path) => ({
    path,
    bytes: readFileSync(join(root, path)).byteLength,
  }));

const collectJavascriptToolingViolations = (
  root: string,
  trackedFiles: readonly string[] = listTrackedFiles(root),
): string[] => {
  const baseline = JSON.parse(readFileSync(join(root, javascriptToolingBaselinePath), 'utf8')) as JavaScriptToolingBaseline;
  return [
    ...findJavascriptToolingViolations({
      files: listTrackedJavaScriptFiles(root, trackedFiles, baseline.trackedExtensions ?? []),
      baseline,
    }),
    ...findLinguistViolations(readFileSync(join(root, '.gitattributes'), 'utf8')),
  ];
};

// Go sources are formatted by the gofmt from the Go version in go.work. PATH gofmt can be an older
// system Go than the toolchain `go` runs. Go 1.27 reformats a multi-value return of composite
// literals, so the two disagree. `go env GOROOT` with GOTOOLCHAIN set to the go.work version is
// the same gofmt CI's setup-go puts on PATH.
const workspaceGofmt = (root: string): string => {
  const { goVersion } = readGoWorkspace(root);
  const goroot = execFileSync('go', ['env', 'GOROOT'], {
    cwd: root,
    encoding: 'utf8',
    env: { ...process.env, GOTOOLCHAIN: `go${goVersion}` },
  }).trim();
  const binary = join(goroot, 'bin', goExecutableName('gofmt'));
  if (!existsSync(binary)) {
    throw new Error(`gofmt for go${goVersion} is not at ${binary}`);
  }
  return binary;
};

export const splitArgsByLength = (args: readonly string[], maxChars = 16_000): string[][] => {
  const batches: string[][] = [];
  let current: string[] = [];
  let currentChars = 0;
  for (const arg of args) {
    const nextChars = currentChars + arg.length + 1;
    if (current.length > 0 && nextChars > maxChars) {
      batches.push(current);
      current = [arg];
      currentChars = arg.length;
    } else {
      current.push(arg);
      currentChars = nextChars;
    }
  }
  if (current.length > 0) batches.push(current);
  return batches;
};

const findGoFormatViolations = (root: string, trackedFiles: readonly string[]): string[] => {
  const goFiles = trackedFiles.map(normalizePath).filter((path) => path.endsWith('.go') && existsSync(join(root, path)));
  if (goFiles.length === 0) return [];
  const gofmt = workspaceGofmt(root);
  const unformatted = splitArgsByLength(goFiles).flatMap((batch) =>
    execFileSync(gofmt, ['-l', ...batch], { cwd: root, encoding: 'utf8', maxBuffer: 16 * 1024 * 1024 })
      .split(/\r?\n/u)
      .filter(Boolean),
  );
  return unformatted.map((path) => `${path}: not gofmt-formatted (run ${gofmt} -w ${path})`);
};

const approvedGoWorkspaceModules = Object.freeze(['.', 'cmd/wecom-alert-adapter']);

export const findGoWorkspaceModuleListViolations = (modules: readonly string[]): string[] => {
  const violations: string[] = [];
  const approved = new Set<string>(approvedGoWorkspaceModules);
  for (const mod of modules) {
    if (!approved.has(mod)) {
      violations.push(`go.work: unapproved Go submodule "${mod}"; merge internal packages into root go.mod instead of spawning parallel submodules`);
    }
  }
  for (const required of approvedGoWorkspaceModules) {
    if (!modules.includes(required)) {
      violations.push(`go.work: missing required workspace module "${required}"`);
    }
  }
  return violations;
};

const findGoWorkspaceViolations = (root: string): string[] => {
  const { goVersion, modules } = readGoWorkspace(root);
  const violations = findGoWorkspaceModuleListViolations(modules);
  for (const mod of modules) {
    if (mod === '.') continue;
    try {
      execFileSync('go', ['vet', './...'], {
        cwd: join(root, mod),
        encoding: 'utf8',
        env: { ...process.env, GOWORK: 'off', GOTOOLCHAIN: `go${goVersion}` },
      });
    } catch (error) {
      const detail = error instanceof Error ? error.message : String(error);
      violations.push(`${mod}: GOWORK=off go vet ./... failed (${detail})`);
    }
  }
  return violations;
};

export const checkRepositoryGovernance = (root = repositoryRoot) => {
  const packageJson = JSON.parse(readFileSync(join(root, 'package.json'), 'utf8')) as { scripts?: Record<string, string> };
  const trackedFiles = listTrackedFiles(root);
  const violations = [
    ...findTrackedArtifactViolations(trackedFiles),
    ...findRequiredGuardrailFileViolations(trackedFiles),
    ...findDebugMarkerViolations({ root, trackedFiles }),
    ...findSkillMirrorViolations({ root, trackedFiles }),
    ...findVerifyFeatureMapViolations({ root, trackedFiles }),
    ...findComposePortExposureViolations({ root, trackedFiles }),
    ...collectJavascriptToolingViolations(root, trackedFiles),
    ...findPackageScriptViolations(packageJson.scripts ?? {}),
    ...findWorkflowViolations(readFileSync(join(root, '.github/workflows/pr-gates.yml'), 'utf8')),
    ...findHandoffBudgetViolations({ bytes: readFileSync(join(root, 'docs/HANDOFF.md')).byteLength }),
    ...findGoWorkspaceViolations(root),
    ...findGoFormatViolations(root, trackedFiles),
    ...findGoFileLineViolations(root, trackedFiles),
    ...findGoTestHygieneViolations(root, trackedFiles),
    ...findChangelogFragmentViolations({ root, trackedFiles }),
  ];

  if (violations.length > 0) {
    throw new Error(`Repository governance check failed:\n- ${violations.join('\n- ')}`);
  }
};

const invokedPath = process.argv[1] ? pathToFileURL(resolve(process.argv[1])).href : '';
if (invokedPath === import.meta.url) {
  try {
    checkRepositoryGovernance();
    console.log('Repository governance check passed.');
  } catch (error) {
    console.error(error instanceof Error ? error.message : error);
    process.exitCode = 1;
  }
}
