import { readdir, rm, stat } from 'node:fs/promises';
import path from 'node:path';

const root = process.cwd();

const removableRootPatterns = [
  /^\.tmp-/,
  /^\.ruff_cache$/,
  /^CodeHVAC_web\.tmp-/,
  /^CodeHVAC_webouttoolsgo.*\.tar\.gz$/,
];

async function exists(target) {
  try {
    await stat(target);
    return true;
  } catch {
    return false;
  }
}

async function remove(target, label = path.relative(root, target)) {
  if (!(await exists(target))) return false;
  await rm(target, { recursive: true, force: true });
  console.log(`[repo:clean:local] removed ${label}`);
  return true;
}

async function removeIfEmpty(relativePath) {
  const target = path.join(root, relativePath);
  if (!(await exists(target))) return false;
  const entries = await readdir(target);
  if (entries.length > 0) return false;
  await rm(target, { recursive: true, force: true });
  console.log(`[repo:clean:local] removed empty ${relativePath}`);
  return true;
}

async function treeHasFiles(target) {
  for (const entry of await readdir(target, { withFileTypes: true })) {
    if (entry.isFile() || entry.isSymbolicLink()) return true;
    if (entry.isDirectory() && await treeHasFiles(path.join(target, entry.name))) return true;
  }
  return false;
}

async function removeEmptyHiddenAdapterDirs() {
  const protectedNames = new Set([
    '.agents', '.ai-bridge', '.codegraph', '.codex', '.github', '.impeccable',
    '.ruff_cache', '.scratch', '.workbuddy', '.worktrees',
  ]);
  let count = 0;
  for (const entry of await readdir(root, { withFileTypes: true })) {
    if (!entry.isDirectory() || !entry.name.startsWith('.') || protectedNames.has(entry.name)) continue;
    const target = path.join(root, entry.name);
    if (await treeHasFiles(target)) continue;
    await rm(target, { recursive: true, force: true });
    console.log(`[repo:clean:local] removed empty adapter ${entry.name}`);
    count += 1;
  }
  return count;
}

async function removeEmptyDirectoriesRecursively(relativePath) {
  const target = path.join(root, relativePath);
  if (!(await exists(target))) return 0;
  let count = 0;
  for (const entry of await readdir(target, { withFileTypes: true })) {
    if (!entry.isDirectory()) continue;
    count += await removeEmptyDirectoriesRecursively(path.join(relativePath, entry.name));
  }
  if ((await readdir(target)).length === 0) {
    await rm(target, { recursive: true, force: true });
    console.log(`[repo:clean:local] removed empty source dir ${relativePath}`);
    count += 1;
  }
  return count;
}

let removed = 0;
for (const entry of await readdir(root)) {
  if (!removableRootPatterns.some((pattern) => pattern.test(entry))) continue;
  removed += Number(await remove(path.join(root, entry), entry));
}

for (const relativePath of ['agent/skills', 'data/skills', 'skills']) {
  removed += Number(await removeIfEmpty(relativePath));
}
for (const relativePath of ['agent', 'data']) {
  removed += Number(await removeIfEmpty(relativePath));
}
removed += await removeEmptyHiddenAdapterDirs();
removed += await removeEmptyDirectoriesRecursively('apps/hvac-web/src');

console.log(`[repo:clean:local] complete; removed ${removed} local artifact(s).`);
