import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

import {
  CHANGELOG_README_PATH,
  extractUnreleasedSection,
  findChangelogDirectEditViolations,
  findChangelogFragmentViolations,
  foldFragmentsIntoChangelog,
  isChangelogFragmentPath,
  parseChangelogFragment,
} from './changelog-fragments.ts';

test('findChangelogDirectEditViolations blocks parallel-PR edits under ## 未发布 and explains changelog.d workflow', () => {
  const baseChangelog = [
    '# 更新记录',
    '',
    '项目尚未正式发布。',
    '',
    '## 未发布',
    '',
    '### 2026-11-19',
    '',
    '#### 新增',
    '',
    '- 既有条目 A',
    '',
  ].join('\n');

  const conflictingPrHead = [
    '# 更新记录',
    '',
    '项目尚未正式发布。',
    '',
    '## 未发布',
    '',
    '### 2026-11-19',
    '',
    '#### 新增',
    '',
    '- PR #918/#919/#915 各自直接往顶部塞的新条目',
    '- 既有条目 A',
    '',
  ].join('\n');

  const directViolation = findChangelogDirectEditViolations({
    baseChangelog,
    headChangelog: conflictingPrHead,
    baseFragmentPaths: [],
    headFragmentPaths: [],
  });
  assert.equal(directViolation.length, 1);
  assert.match(directViolation[0], /do not edit entries under "## 未发布" directly/);
  assert.match(directViolation[0], /changelog\.d\/YYYY-MM-DD-<slug>\.md/);
  assert.match(directViolation[0], /npm run changelog:release/);

  const premptiveFoldInFeaturePr = findChangelogDirectEditViolations({
    baseChangelog,
    headChangelog: conflictingPrHead,
    baseFragmentPaths: [],
    headFragmentPaths: ['changelog.d/2026-11-20-feature.md'],
  });
  assert.equal(premptiveFoldInFeaturePr.length, 1);

  const headerOnlyHead = [
    '# 更新记录',
    '',
    '改为使用 changelog.d 碎片文件。',
    '',
    '## 未发布',
    '',
    '### 2026-11-19',
    '',
    '#### 新增',
    '',
    '- 既有条目 A',
    '',
  ].join('\n');
  assert.deepEqual(
    findChangelogDirectEditViolations({
      baseChangelog,
      headChangelog: headerOnlyHead,
      baseFragmentPaths: [],
      headFragmentPaths: ['changelog.d/2026-11-20-changelog-fragments.md'],
    }),
    [],
  );

  assert.deepEqual(
    findChangelogDirectEditViolations({
      baseChangelog,
      headChangelog: conflictingPrHead,
      baseFragmentPaths: ['changelog.d/2026-11-19-merged-pr.md'],
      headFragmentPaths: [],
    }),
    [],
  );

  assert.equal(extractUnreleasedSection(headerOnlyHead), extractUnreleasedSection(baseChangelog));
});

test('fragment parser validates naming, real calendar dates, frontmatter keys, LF endings, and body safety', () => {
  assert.equal(isChangelogFragmentPath('changelog.d/2026-11-20-fix-tariff.md'), true);
  assert.equal(isChangelogFragmentPath('changelog.d/BadCase.md'), false);
  assert.equal(isChangelogFragmentPath('changelog.d/nested/2026-11-20-fix.md'), false);

  const validContent = [
    '---',
    'date: 2026-11-20',
    'category: 修复',
    'pr: 925',
    '---',
    '- 修复电价账单展示与合计精度：',
    '  - 深谷时段费用不再计入平段。',
    '',
  ].join('\n');
  const parsedValid = parseChangelogFragment('changelog.d/2026-11-20-tariff-fix.md', validContent);
  assert.deepEqual(parsedValid.violations, []);
  assert.deepEqual(parsedValid.fragment, {
    path: 'changelog.d/2026-11-20-tariff-fix.md',
    date: '2026-11-20',
    category: '修复',
    pr: 925,
    body: '- 修复电价账单展示与合计精度：\n  - 深谷时段费用不再计入平段。',
  });

  const mismatchDate = parseChangelogFragment(
    'changelog.d/2026-11-20-mismatch.md',
    '---\ndate: 2026-11-19\ncategory: 新增\n---\n- 条目\n',
  );
  assert.ok(mismatchDate.violations.some((v) => v.includes('must match filename date prefix')));

  const fakeCalendarDate = parseChangelogFragment(
    'changelog.d/2026-02-31-bad-date.md',
    '---\ndate: 2026-02-31\ncategory: 新增\n---\n- 条目\n',
  );
  assert.ok(fakeCalendarDate.violations.some((v) => v.includes('invalid calendar date')));

  const unknownKeyAndCat = parseChangelogFragment(
    'changelog.d/2026-11-20-unknown.md',
    '---\ndate: 2026-11-20\ncategory: 其他\nauthor: bot\n---\n- 条目\n',
  );
  assert.ok(unknownKeyAndCat.violations.some((v) => v.includes('invalid category')));
  assert.ok(unknownKeyAndCat.violations.some((v) => v.includes('unknown frontmatter key "author"')));

  const crlfBody = parseChangelogFragment(
    'changelog.d/2026-11-20-crlf.md',
    '---\r\ndate: 2026-11-20\r\ncategory: 变更\r\n---\r\n- 条目\r\n',
  );
  assert.ok(crlfBody.violations.some((v) => v.includes('CRLF')));

  const badBody = parseChangelogFragment(
    'changelog.d/2026-11-20-bad-body.md',
    '---\ndate: 2026-11-20\ncategory: 改进\n---\n### 禁止标题\n<<<<<<< HEAD\n',
  );
  assert.ok(badBody.violations.some((v) => v.includes('must start with "- "')));
  assert.ok(badBody.violations.some((v) => v.includes('conflict marker')));
  assert.ok(badBody.violations.some((v) => v.includes('Markdown headings are not allowed')));

  const sandbox = mkdtempSync(join(tmpdir(), 'hvac-changelog-dir-'));
  try {
    mkdirSync(join(sandbox, 'changelog.d'), { recursive: true });
    writeFileSync(join(sandbox, CHANGELOG_README_PATH), '# guide\n');
    writeFileSync(join(sandbox, 'changelog.d/2026-11-20-good.md'), validContent);
    writeFileSync(join(sandbox, 'changelog.d/notes.txt'), 'stray');
    const dirViolations = findChangelogFragmentViolations({
      root: sandbox,
      trackedFiles: [CHANGELOG_README_PATH, 'changelog.d/2026-11-20-good.md', 'changelog.d/notes.txt'],
    });
    assert.equal(dirViolations.length, 1);
    assert.match(dirViolations[0], /notes\.txt: invalid changelog fragment filename/);
  } finally {
    rmSync(sandbox, { recursive: true, force: true });
  }
});

test('foldFragmentsIntoChangelog merges categories deterministically while preserving older date blocks', () => {
  const existing = [
    '# 更新记录',
    '',
    '说明文字。',
    '',
    '## 未发布',
    '',
    '### 2026-11-19',
    '',
    '#### 新增与架构演进',
    '',
    '- 既有新功能',
    '',
    '#### 修复',
    '',
    '- 既有修复 A',
    '',
    '### 2026-11-18',
    '',
    '#### 新增',
    '',
    '- 前一日原文保持逐字节不变',
    '',
    '## [0.1.0] - 2026-10-01',
    '',
    '- 历史已发布记录',
    '',
  ].join('\n');

  const fragments = [
    parseChangelogFragment(
      'changelog.d/2026-11-20-fix-ui.md',
      '---\ndate: 2026-11-20\ncategory: 修复\n---\n- 修复移动端滚动条溢出\n',
    ).fragment!,
    parseChangelogFragment(
      'changelog.d/2026-11-20-add-feature.md',
      '---\ndate: 2026-11-20\ncategory: 新增\n---\n- 新增一键巡检导出\n',
    ).fragment!,
    parseChangelogFragment(
      'changelog.d/2026-11-19-improve-copy.md',
      '---\ndate: 2026-11-19\ncategory: 改进\n---\n- 优化总览卡片文案\n',
    ).fragment!,
    parseChangelogFragment(
      'changelog.d/2026-11-19-fix-race.md',
      '---\ndate: 2026-11-19\ncategory: 修复\n---\n- 修复首屏站点发现竞态\n',
    ).fragment!,
  ];

  const merged = foldFragmentsIntoChangelog(existing, fragments);
  assert.equal(
    merged,
    [
      '# 更新记录',
      '',
      '说明文字。',
      '',
      '## 未发布',
      '',
      '### 2026-11-20',
      '',
      '#### 新增',
      '',
      '- 新增一键巡检导出',
      '',
      '#### 修复',
      '',
      '- 修复移动端滚动条溢出',
      '',
      '### 2026-11-19',
      '',
      '#### 新增与架构演进',
      '',
      '- 既有新功能',
      '',
      '#### 改进',
      '',
      '- 优化总览卡片文案',
      '',
      '#### 修复',
      '',
      '- 修复首屏站点发现竞态',
      '- 既有修复 A',
      '',
      '### 2026-11-18',
      '',
      '#### 新增',
      '',
      '- 前一日原文保持逐字节不变',
      '',
      '## [0.1.0] - 2026-10-01',
      '',
      '- 历史已发布记录',
      '',
    ].join('\n'),
  );
  assert.equal(merged.match(/^#### 修复$/gmu)?.length, 2);
});

test('CLI --check enforces policy against fork point and --release --dry-run passes on repository', () => {
  const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
  const scriptPath = join(repoRoot, 'scripts/changelog-fragments.ts');
  const tracked = spawnSync('git', ['ls-files'], { cwd: repoRoot, encoding: 'utf8', windowsHide: true })
    .stdout.split(/\r?\n/u)
    .filter(Boolean);
  assert.deepEqual(findChangelogFragmentViolations({ root: repoRoot, trackedFiles: tracked }), []);

  const dryRunResult = spawnSync(
    process.execPath,
    ['--experimental-strip-types', scriptPath, '--release', '--dry-run'],
    { cwd: repoRoot, encoding: 'utf8', windowsHide: true },
  );
  assert.equal(dryRunResult.status, 0, dryRunResult.stderr || dryRunResult.stdout);
  assert.match(readFileSync(join(repoRoot, 'CHANGELOG.md'), 'utf8'), /^## 未发布$/mu);

  const repo = mkdtempSync(join(tmpdir(), 'hvac-changelog-cli-'));
  const env: NodeJS.ProcessEnv = { ...process.env, GITHUB_BASE_SHA: 'main' };
  delete env.GIT_DIR;
  delete env.GIT_WORK_TREE;
  const git = (...args: string[]): void => {
    const result = spawnSync('git', ['-c', 'user.name=test', '-c', 'user.email=test@example.com', ...args], {
      cwd: repo,
      encoding: 'utf8',
      env,
      windowsHide: true,
    });
    assert.equal(result.status, 0, result.stderr);
  };
  const runCheck = () =>
    spawnSync(process.execPath, ['--experimental-strip-types', scriptPath, '--check'], {
      cwd: repo,
      encoding: 'utf8',
      env,
      windowsHide: true,
    });

  try {
    git('init', '--quiet', '--initial-branch=main');
    mkdirSync(join(repo, 'changelog.d'), { recursive: true });
    writeFileSync(join(repo, CHANGELOG_README_PATH), '# guide\n');
    writeFileSync(join(repo, 'CHANGELOG.md'), '# 更新记录\n\n## 未发布\n\n### 2026-11-19\n\n#### 修复\n\n- 既有条目\n');
    git('add', '.');
    git('commit', '--quiet', '-m', 'base');
    git('switch', '--quiet', '-c', 'feature');

    writeFileSync(
      join(repo, 'changelog.d/2026-11-20-feature.md'),
      '---\ndate: 2026-11-20\ncategory: 新增\n---\n- 新增合法碎片\n',
    );
    assert.equal(runCheck().status, 0);

    writeFileSync(
      join(repo, 'CHANGELOG.md'),
      '# 更新记录\n\n## 未发布\n\n### 2026-11-19\n\n#### 修复\n\n- 直接修改未发布\n- 既有条目\n',
    );
    const badCheck = runCheck();
    assert.equal(badCheck.status, 1);
    assert.match(badCheck.stderr, /do not edit entries under "## 未发布" directly/u);
  } finally {
    rmSync(repo, { recursive: true, force: true });
  }
});
