import { spawnSync } from 'node:child_process';
import { existsSync, readdirSync, readFileSync, unlinkSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

export const CHANGELOG_FRAGMENTS_DIR = 'changelog.d';
export const CHANGELOG_README_PATH = 'changelog.d/README.md';
export const CHANGELOG_CATEGORIES = ['新增', '改进', '变更', '修复'] as const;

export type ChangelogCategory = (typeof CHANGELOG_CATEGORIES)[number];

export interface ChangelogFragment {
  path: string;
  date: string;
  category: ChangelogCategory;
  pr?: number;
  body: string;
}

const FRAGMENT_PATH_RE = /^changelog\.d\/(\d{4}-\d{2}-\d{2})-[a-z0-9][a-z0-9-]*\.md$/u;
const DATE_RE = /^(\d{4})-(\d{2})-(\d{2})$/u;
const CONFLICT_MARKER_RE = /^(?:<{7}|={7}|>{7})(?:\s|$)/mu;
const HEADING_RE = /^#{1,6}\s/mu;
const UNRELEASED_HEADING_RE = /^## 未发布[ \t]*(?:\n|$)/mu;
const RELEASED_HEADING_RE = /^## [^\n]+/mu;
const DATE_HEADING_RE = /^### (\d{4}-\d{2}-\d{2})[ \t]*$/gmu;
const CATEGORY_HEADING_RE = /^#### ([^\n]+?)[ \t]*$/gmu;

const normalizePath = (value: string): string => value.replaceAll('\\', '/');

const isValidCalendarDate = (dateText: string): boolean => {
  const match = DATE_RE.exec(dateText);
  if (!match) return false;
  const year = Number(match[1]);
  const month = Number(match[2]);
  const day = Number(match[3]);
  const utc = new Date(Date.UTC(year, month - 1, day));
  return utc.getUTCFullYear() === year && utc.getUTCMonth() === month - 1 && utc.getUTCDate() === day;
};

const parseCategory = (raw: string): ChangelogCategory | undefined =>
  CHANGELOG_CATEGORIES.find((candidate) => candidate === raw);

const unquoteYamlScalar = (raw: string): string => {
  const trimmed = raw.trim();
  if (
    trimmed.length >= 2 &&
    ((trimmed.startsWith('"') && trimmed.endsWith('"')) || (trimmed.startsWith("'") && trimmed.endsWith("'")))
  ) {
    return trimmed.slice(1, -1).trim();
  }
  return trimmed;
};

export function isChangelogFragmentPath(filePath: string): boolean {
  return FRAGMENT_PATH_RE.test(normalizePath(filePath));
}

export function parseChangelogFragment(
  filePath: string,
  content: string,
): { fragment?: ChangelogFragment; violations: string[] } {
  const normalizedPath = normalizePath(filePath);
  const violations: string[] = [];

  if (content.includes('\r')) {
    violations.push(`${normalizedPath}: CRLF line endings are not allowed; convert to LF`);
  }

  const pathMatch = FRAGMENT_PATH_RE.exec(normalizedPath);
  let filenameDate = '';
  if (!pathMatch) {
    violations.push(
      `${normalizedPath}: invalid changelog fragment filename; expected changelog.d/YYYY-MM-DD-<lowercase-slug>.md (see ${CHANGELOG_README_PATH})`,
    );
  } else {
    filenameDate = pathMatch[1];
    if (!isValidCalendarDate(filenameDate)) {
      violations.push(`${normalizedPath}: invalid calendar date "${filenameDate}" in filename`);
    }
  }

  const lfContent = content.replaceAll('\r\n', '\n').replaceAll('\r', '\n');
  const fmMatch = /^---\n([\s\S]*?)\n---(?:\n([\s\S]*))?$/u.exec(lfContent);
  if (!fmMatch) {
    violations.push(
      `${normalizedPath}: missing YAML frontmatter (expected ---\\ndate: YYYY-MM-DD\\ncategory: ${CHANGELOG_CATEGORIES.join('|')}\\n---)`,
    );
    return { violations };
  }

  const rawFrontmatter = fmMatch[1];
  const rawBody = fmMatch[2] ?? '';

  const seenKeys = new Set<string>();
  let dateValue: string | undefined;
  let categoryValue: ChangelogCategory | undefined;
  let prValue: number | undefined;

  for (const rawLine of rawFrontmatter.split('\n')) {
    const line = rawLine.trim();
    if (!line || line.startsWith('#')) continue;
    const kvMatch = /^([A-Za-z0-9_-]+):\s*(.*)$/u.exec(line);
    if (!kvMatch) {
      violations.push(`${normalizedPath}: invalid frontmatter line "${line}"; expected "key: value"`);
      continue;
    }
    const key = kvMatch[1];
    const value = unquoteYamlScalar(kvMatch[2]);
    if (seenKeys.has(key)) {
      violations.push(`${normalizedPath}: duplicate frontmatter key "${key}"`);
      continue;
    }
    seenKeys.add(key);

    if (key === 'date') {
      if (!isValidCalendarDate(value)) {
        violations.push(`${normalizedPath}: invalid frontmatter date "${value}"; expected valid YYYY-MM-DD`);
      } else {
        dateValue = value;
      }
    } else if (key === 'category') {
      const parsed = parseCategory(value);
      if (!parsed) {
        violations.push(
          `${normalizedPath}: invalid category "${value}"; expected one of ${CHANGELOG_CATEGORIES.join('、')}`,
        );
      } else {
        categoryValue = parsed;
      }
    } else if (key === 'pr') {
      if (!/^[1-9]\d*$/u.test(value)) {
        violations.push(`${normalizedPath}: invalid frontmatter pr "${value}"; expected a positive integer`);
      } else {
        prValue = Number(value);
      }
    } else {
      violations.push(
        `${normalizedPath}: unknown frontmatter key "${key}" (allowed keys: date, category, pr)`,
      );
    }
  }

  if (!seenKeys.has('date')) {
    violations.push(`${normalizedPath}: missing required frontmatter key "date"`);
  } else if (dateValue && filenameDate && dateValue !== filenameDate) {
    violations.push(
      `${normalizedPath}: frontmatter date "${dateValue}" must match filename date prefix "${filenameDate}"`,
    );
  }

  if (!seenKeys.has('category')) {
    violations.push(
      `${normalizedPath}: missing required frontmatter key "category" (${CHANGELOG_CATEGORIES.join('、')})`,
    );
  }

  const body = rawBody.trim();
  if (!body) {
    violations.push(`${normalizedPath}: changelog fragment body cannot be empty; add at least one "- ..." item`);
  } else {
    if (!body.startsWith('- ')) {
      violations.push(`${normalizedPath}: changelog fragment body must start with "- "`);
    }
    if (CONFLICT_MARKER_RE.test(body)) {
      violations.push(`${normalizedPath}: unresolved git conflict marker found in changelog fragment`);
    }
    if (HEADING_RE.test(body)) {
      violations.push(
        `${normalizedPath}: Markdown headings are not allowed inside fragment body (category is set via frontmatter)`,
      );
    }
  }

  if (violations.length > 0 || !dateValue || !categoryValue) {
    return { violations };
  }

  return {
    fragment: {
      path: normalizedPath,
      date: dateValue,
      category: categoryValue,
      ...(prValue !== undefined ? { pr: prValue } : {}),
      body,
    },
    violations: [],
  };
}

export function findChangelogFragmentViolations({
  root,
  trackedFiles,
}: {
  root: string;
  trackedFiles: readonly string[];
}): string[] {
  const violations: string[] = [];
  const candidatePaths = trackedFiles
    .map(normalizePath)
    .filter((filePath) => filePath.startsWith(`${CHANGELOG_FRAGMENTS_DIR}/`))
    .filter((filePath) => filePath !== CHANGELOG_README_PATH)
    .sort();

  for (const filePath of candidatePaths) {
    if (!isChangelogFragmentPath(filePath)) {
      violations.push(
        `${filePath}: invalid changelog fragment filename; rename to changelog.d/YYYY-MM-DD-<lowercase-slug>.md (see ${CHANGELOG_README_PATH})`,
      );
      continue;
    }
    const absPath = join(root, filePath);
    if (!existsSync(absPath)) {
      violations.push(`${filePath}: tracked changelog fragment is missing on disk`);
      continue;
    }
    const parsed = parseChangelogFragment(filePath, readFileSync(absPath, 'utf8'));
    violations.push(...parsed.violations);
  }

  return violations;
}

export function extractUnreleasedSection(markdown: string): string {
  const normalized = markdown.replaceAll('\r\n', '\n').replaceAll('\r', '\n');
  const match = UNRELEASED_HEADING_RE.exec(normalized);
  if (!match || match.index === undefined) return normalized;
  return normalized.slice(match.index);
}

export function findChangelogDirectEditViolations({
  baseChangelog,
  headChangelog,
  baseFragmentPaths,
  headFragmentPaths,
}: {
  baseChangelog: string;
  headChangelog: string;
  baseFragmentPaths: readonly string[];
  headFragmentPaths: readonly string[];
}): string[] {
  if (extractUnreleasedSection(baseChangelog) === extractUnreleasedSection(headChangelog)) {
    return [];
  }

  const headFragmentSet = new Set(headFragmentPaths.map(normalizePath).filter(isChangelogFragmentPath));
  const deletedBaseFragments = baseFragmentPaths
    .map(normalizePath)
    .filter(isChangelogFragmentPath)
    .filter((fragmentPath) => !headFragmentSet.has(fragmentPath));

  if (deletedBaseFragments.length > 0) {
    return [];
  }

  return [
    `CHANGELOG.md: do not edit entries under "## 未发布" directly in feature/fix PRs (parallel PRs conflict on CHANGELOG.md). Move your entry to a standalone file changelog.d/YYYY-MM-DD-<slug>.md (see ${CHANGELOG_README_PATH}) and revert CHANGELOG.md; reserve "## 未发布" updates for \`npm run changelog:release\` rollup PRs.`,
  ];
}

interface ParsedDateBlock {
  date: string;
  rawText: string;
}

interface ParsedCategorySubsection {
  categoryName: string;
  rawText: string;
}

function splitUnreleasedDateBlocks(unreleasedBody: string): {
  preamble: string;
  blocks: ParsedDateBlock[];
} {
  const matches = [...unreleasedBody.matchAll(DATE_HEADING_RE)];
  if (matches.length === 0) {
    return { preamble: unreleasedBody.trim(), blocks: [] };
  }

  const firstIndex = matches[0].index ?? 0;
  const preamble = unreleasedBody.slice(0, firstIndex).trim();
  const blocks: ParsedDateBlock[] = [];

  for (let i = 0; i < matches.length; i += 1) {
    const start = matches[i].index ?? 0;
    const end = i + 1 < matches.length ? (matches[i + 1].index ?? unreleasedBody.length) : unreleasedBody.length;
    blocks.push({
      date: matches[i][1],
      rawText: unreleasedBody.slice(start, end).trim(),
    });
  }

  return { preamble, blocks };
}

function mergeDateBlock(
  date: string,
  existingBlockRaw: string | undefined,
  byCategory: ReadonlyMap<ChangelogCategory, readonly ChangelogFragment[]>,
): string {
  const renderSection = (category: ChangelogCategory, fragments: readonly ChangelogFragment[]): string =>
    `#### ${category}\n\n${fragments.map((fragment) => fragment.body).join('\n')}`;

  if (!existingBlockRaw) {
    const sections: string[] = [];
    for (const category of CHANGELOG_CATEGORIES) {
      const group = byCategory.get(category);
      if (group && group.length > 0) {
        sections.push(renderSection(category, group));
      }
    }
    return `### ${date}\n\n${sections.join('\n\n')}`;
  }

  const headingEnd = existingBlockRaw.indexOf('\n');
  const headerLine = headingEnd === -1 ? existingBlockRaw.trim() : existingBlockRaw.slice(0, headingEnd).trim();
  const blockRest = headingEnd === -1 ? '' : existingBlockRaw.slice(headingEnd + 1);
  const catMatches = [...blockRest.matchAll(CATEGORY_HEADING_RE)];

  const leadingText = catMatches.length > 0 ? blockRest.slice(0, catMatches[0].index ?? 0).trim() : blockRest.trim();
  const subsections: ParsedCategorySubsection[] = [];
  for (let i = 0; i < catMatches.length; i += 1) {
    const start = catMatches[i].index ?? 0;
    const end = i + 1 < catMatches.length ? (catMatches[i + 1].index ?? blockRest.length) : blockRest.length;
    subsections.push({
      categoryName: catMatches[i][1].trim(),
      rawText: blockRest.slice(start, end).trim(),
    });
  }

  const orderIndex = (name: string): number => {
    const idx = CHANGELOG_CATEGORIES.findIndex((category) => category === name);
    return idx === -1 ? CHANGELOG_CATEGORIES.length : idx;
  };

  for (const category of CHANGELOG_CATEGORIES) {
    const group = byCategory.get(category);
    if (!group || group.length === 0) continue;
    const combinedNewBody = group.map((fragment) => fragment.body).join('\n');
    const existingIdx = subsections.findIndex((sub) => sub.categoryName === category);

    if (existingIdx !== -1) {
      const existing = subsections[existingIdx];
      const firstBreak = existing.rawText.indexOf('\n');
      const existingHeading = firstBreak === -1 ? `#### ${category}` : existing.rawText.slice(0, firstBreak).trim();
      const existingBody = firstBreak === -1 ? '' : existing.rawText.slice(firstBreak + 1).trim();
      const mergedBody = existingBody ? `${combinedNewBody}\n${existingBody}` : combinedNewBody;
      subsections[existingIdx] = {
        categoryName: category,
        rawText: `${existingHeading}\n\n${mergedBody}`,
      };
      continue;
    }

    const newSection: ParsedCategorySubsection = {
      categoryName: category,
      rawText: renderSection(category, group),
    };
    const targetOrder = orderIndex(category);
    const insertAt = subsections.findIndex((sub) => {
      const existingOrder = orderIndex(sub.categoryName);
      return existingOrder < CHANGELOG_CATEGORIES.length && existingOrder > targetOrder;
    });
    if (insertAt === -1) {
      subsections.push(newSection);
    } else {
      subsections.splice(insertAt, 0, newSection);
    }
  }

  const parts = [headerLine];
  if (leadingText) parts.push(leadingText);
  for (const subsection of subsections) {
    parts.push(subsection.rawText);
  }
  return parts.join('\n\n');
}

export function foldFragmentsIntoChangelog(
  existingChangelog: string,
  fragments: readonly ChangelogFragment[],
): string {
  const normalized = existingChangelog.replaceAll('\r\n', '\n').replaceAll('\r', '\n');
  if (fragments.length === 0) {
    return normalized.endsWith('\n') ? normalized : `${normalized}\n`;
  }

  const categoryOrder = (category: ChangelogCategory): number => CHANGELOG_CATEGORIES.indexOf(category);
  const sortedFragments = [...fragments].sort((left, right) => {
    if (left.date !== right.date) return right.date.localeCompare(left.date);
    const categoryDiff = categoryOrder(left.category) - categoryOrder(right.category);
    if (categoryDiff !== 0) return categoryDiff;
    return left.path.localeCompare(right.path);
  });

  const unreleasedMatch = UNRELEASED_HEADING_RE.exec(normalized);
  if (!unreleasedMatch || unreleasedMatch.index === undefined) {
    throw new Error('CHANGELOG.md: missing "## 未发布" heading');
  }

  const prefixEnd = unreleasedMatch.index + unreleasedMatch[0].length;
  const prefix = normalized.slice(0, prefixEnd).trimEnd();
  const afterUnreleased = normalized.slice(prefixEnd);
  const tailMatch = RELEASED_HEADING_RE.exec(afterUnreleased);

  const unreleasedRaw = tailMatch && tailMatch.index !== undefined
    ? afterUnreleased.slice(0, tailMatch.index)
    : afterUnreleased;
  const releasedTail = tailMatch && tailMatch.index !== undefined
    ? afterUnreleased.slice(tailMatch.index).trim()
    : '';

  const { preamble, blocks } = splitUnreleasedDateBlocks(unreleasedRaw);

  const groupedByDate = new Map<string, Map<ChangelogCategory, ChangelogFragment[]>>();
  for (const fragment of sortedFragments) {
    let byCategory = groupedByDate.get(fragment.date);
    if (!byCategory) {
      byCategory = new Map();
      groupedByDate.set(fragment.date, byCategory);
    }
    const list = byCategory.get(fragment.category) ?? [];
    list.push(fragment);
    byCategory.set(fragment.category, list);
  }

  const mergedBlocks = [...blocks];
  const targetDates = [...groupedByDate.keys()].sort((a, b) => b.localeCompare(a));

  for (const date of targetDates) {
    const byCategory = groupedByDate.get(date);
    if (!byCategory) continue;
    const existingIdx = mergedBlocks.findIndex((block) => block.date === date);
    if (existingIdx !== -1) {
      mergedBlocks[existingIdx] = {
        date,
        rawText: mergeDateBlock(date, mergedBlocks[existingIdx].rawText, byCategory),
      };
      continue;
    }
    const newBlock: ParsedDateBlock = {
      date,
      rawText: mergeDateBlock(date, undefined, byCategory),
    };
    const insertAt = mergedBlocks.findIndex((block) => block.date < date);
    if (insertAt === -1) {
      mergedBlocks.push(newBlock);
    } else {
      mergedBlocks.splice(insertAt, 0, newBlock);
    }
  }

  const sections = [prefix];
  if (preamble) sections.push(preamble);
  for (const block of mergedBlocks) {
    sections.push(block.rawText);
  }
  if (releasedTail) sections.push(releasedTail);

  return `${sections.join('\n\n')}\n`;
}

function listWorkingTreeChangelogPaths(root: string): string[] {
  const dir = join(root, CHANGELOG_FRAGMENTS_DIR);
  if (!existsSync(dir)) return [];

  const collected: string[] = [];
  const walk = (absDir: string, relPrefix: string): void => {
    for (const entry of readdirSync(absDir, { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name))) {
      const rel = `${relPrefix}/${entry.name}`;
      if (entry.isDirectory()) {
        walk(join(absDir, entry.name), rel);
      } else if (entry.isFile()) {
        collected.push(rel);
      }
    }
  };
  walk(dir, CHANGELOG_FRAGMENTS_DIR);
  return collected;
}

function runGit(root: string, args: readonly string[]): string {
  const result = spawnSync('git', [...args], {
    cwd: root,
    encoding: 'utf8',
    maxBuffer: 64 << 20,
    windowsHide: true,
  });
  if (result.error || result.status !== 0) {
    throw new Error(`git ${args.join(' ')} failed: ${result.error?.message ?? result.stderr.trim()}`);
  }
  return result.stdout;
}

function loadWorkingTreeFragments(root: string): {
  fragments: ChangelogFragment[];
  violations: string[];
  workingTreePaths: string[];
} {
  const workingTreePaths = listWorkingTreeChangelogPaths(root);
  const violations = findChangelogFragmentViolations({ root, trackedFiles: workingTreePaths });
  if (violations.length > 0) {
    return { fragments: [], violations, workingTreePaths };
  }

  const fragments: ChangelogFragment[] = [];
  for (const filePath of workingTreePaths) {
    if (filePath === CHANGELOG_README_PATH) continue;
    const parsed = parseChangelogFragment(filePath, readFileSync(join(root, filePath), 'utf8'));
    violations.push(...parsed.violations);
    if (parsed.fragment) fragments.push(parsed.fragment);
  }
  return { fragments, violations, workingTreePaths };
}

function runCheckCli(root: string): number {
  const { violations, workingTreePaths } = loadWorkingTreeFragments(root);

  const baseRef = process.env.GITHUB_BASE_SHA || 'origin/main';
  const forkPoint = runGit(root, ['merge-base', baseRef, 'HEAD']).trim();
  const baseChangelog = runGit(root, ['show', `${forkPoint}:CHANGELOG.md`]);
  const baseFragmentPaths = runGit(root, ['ls-tree', '-r', '--name-only', forkPoint, '--', CHANGELOG_FRAGMENTS_DIR])
    .split(/\r?\n/u)
    .map((line) => line.trim())
    .filter(Boolean);
  const headChangelog = readFileSync(join(root, 'CHANGELOG.md'), 'utf8');

  violations.push(
    ...findChangelogDirectEditViolations({
      baseChangelog,
      headChangelog,
      baseFragmentPaths,
      headFragmentPaths: workingTreePaths,
    }),
  );

  if (violations.length > 0) {
    console.error(`Changelog check failed:\n- ${violations.join('\n- ')}`);
    return 1;
  }

  console.log('Changelog fragments and rollup policy check passed.');
  return 0;
}

function runReleaseCli(root: string, dryRun: boolean): number {
  const { fragments, violations } = loadWorkingTreeFragments(root);
  if (violations.length > 0) {
    console.error(`Changelog fragment validation failed:\n- ${violations.join('\n- ')}`);
    return 1;
  }
  if (fragments.length === 0) {
    console.log('No changelog fragments to fold.');
    return 0;
  }

  const changelogPath = join(root, 'CHANGELOG.md');
  const nextChangelog = foldFragmentsIntoChangelog(readFileSync(changelogPath, 'utf8'), fragments);

  if (dryRun) {
    console.log(
      `[dry-run] Would fold ${fragments.length} changelog fragment(s) into CHANGELOG.md:\n- ${fragments.map((f) => f.path).join('\n- ')}`,
    );
    return 0;
  }

  writeFileSync(changelogPath, nextChangelog, 'utf8');
  for (const fragment of fragments) {
    unlinkSync(join(root, fragment.path));
  }
  console.log(`Folded ${fragments.length} changelog fragment(s) into CHANGELOG.md and removed folded fragments.`);
  return 0;
}

const invokedPath = process.argv[1] ? pathToFileURL(resolve(process.argv[1])).href : '';
if (invokedPath === import.meta.url) {
  const root = process.cwd();
  const args = process.argv.slice(2);
  if (args.includes('--check')) {
    process.exitCode = runCheckCli(root);
  } else if (args.includes('--release')) {
    const dryRun = args.includes('--dry-run') || process.env.npm_config_dry_run === 'true';
    process.exitCode = runReleaseCli(root, dryRun);
  } else {
    console.error('Usage: node --experimental-strip-types scripts/changelog-fragments.ts (--check | --release [--dry-run])');
    process.exitCode = 1;
  }
}
