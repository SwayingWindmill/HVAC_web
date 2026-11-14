#!/usr/bin/env node
// Read-only worktree prune audit. Classifies every git worktree by size, merge
// state, uncommitted work, ignored files outside build and cache directories,
// commits only its HEAD reflog reaches, remote/PR state, and the most recent chat
// that operated in it, then prints a table sorted by size with a suggested bucket.
// Never deletes anything; deletion stays a human-gated step in the playbook.
//
// Usage: node --disable-warning=ExperimentalWarning worktree-audit.ts [repo-path] [transcripts-path ...]

import { execFileSync } from "node:child_process";
import { lstatSync, readdirSync, readFileSync, realpathSync, statSync } from "node:fs";
import { homedir } from "node:os";
import { basename, dirname, join } from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";

type Fact<T> = { known: true; value: T } | { known: false };
type WorktreeEntry = { path: string; prunable: boolean; locked: string | null };
type PrEntry = { number: number; state: string; headRefName: string; headRefOid: string };
type DirtyCounts = { wip: number; untracked: number; ignored: number };

const known = <T>(value: T): Fact<T> => ({ known: true, value });
const UNKNOWN: Fact<never> = Object.freeze({ known: false });
const probe = <T>(read: () => T): Fact<T> => {
  try {
    return known(read());
  } catch {
    return UNKNOWN;
  }
};
const bind = <A, B>(fact: Fact<A>, next: (value: A) => Fact<B>): Fact<B> => (fact.known ? next(fact.value) : UNKNOWN);

const DAY = 86400;
const RECENT_DAYS = 4;
const HEADER = ["SIZE", "AGE", "MERGED", "DIRTY", "REMOTE", "PR", "LAST_CHAT", "BUCKET", "LOCKED", "WORKTREE"];
const GH_PR_LIST = ["pr", "list", "--author", "@me", "--state", "all", "--limit", "1000", "--json", "number,state,headRefName,headRefOid"];

export const DISPOSABLE = new Set([
  ".build",
  ".cache",
  ".dart_tool",
  ".DS_Store",
  ".gradle",
  ".lake",
  ".lycheecache",
  ".mypy_cache",
  ".next",
  ".nuxt",
  ".parcel-cache",
  ".pytest_cache",
  ".ruff_cache",
  ".svelte-kit",
  ".tox",
  ".turbo",
  ".venv",
  "__pycache__",
  "build",
  "coverage",
  "DerivedData",
  "dist",
  "node_modules",
  "target",
  "venv",
]);

function git(cwd: string, ...args: string[]): string {
  return execFileSync("git", args, {
    cwd,
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
    maxBuffer: Infinity,
  }).replace(/\n+$/, "");
}

const runGh = (args: string[], cwd: string): string =>
  execFileSync("gh", args, { cwd, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] });

function parseWorktrees(output: string): WorktreeEntry[] {
  const worktrees: WorktreeEntry[] = [];
  for (const field of output.split("\0")) {
    if (field.startsWith("worktree ")) worktrees.push({ path: field.slice("worktree ".length), prunable: false, locked: null });
    else if (field.startsWith("prunable") && worktrees.length) worktrees.at(-1)!.prunable = true;
    else if (field.startsWith("locked") && worktrees.length) worktrees.at(-1)!.locked = field.slice("locked ".length);
  }
  return worktrees;
}

export function defaultTranscriptRoots(): string[] {
  const home = homedir();
  const candidates = [
    process.env.ANTIGRAVITY_BRAIN_DIR || join(home, ".gemini", "antigravity", "brain"),
    join(home, ".gemini", "antigravity-cli", "brain"),
    join(home, ".gemini", "antigravity-ide", "brain"),
    join(process.env.CLAUDE_CONFIG_DIR || join(home, ".claude"), "projects"),
    join(process.env.CODEX_HOME || join(home, ".codex"), "sessions"),
    join(process.env.CODEX_HOME || join(home, ".codex"), "archived_sessions"),
  ];
  if (process.platform === "linux") {
    try {
      const skip = new Set(["All Users", "Default", "Default User", "Public"]);
      for (const entry of readdirSync("/mnt/c/Users", { withFileTypes: true })) {
        if (!entry.isDirectory() || skip.has(entry.name)) continue;
        candidates.push(join("/mnt/c/Users", entry.name, ".gemini", "antigravity", "brain"));
        candidates.push(join("/mnt/c/Users", entry.name, ".gemini", "antigravity-cli", "brain"));
        candidates.push(join("/mnt/c/Users", entry.name, ".gemini", "antigravity-ide", "brain"));
        candidates.push(join("/mnt/c/Users", entry.name, ".claude", "projects"));
      }
    } catch {
      /* Not WSL or /mnt/c/Users unreadable; keep native roots. */
    }
  }
  const found = candidates.filter((root) => {
    try {
      return statSync(root, { throwIfNoEntry: false })?.isDirectory() ?? false;
    } catch (error) {
      const code = error && typeof error === "object" && "code" in error ? String(error.code) : "";
      return !["ENOENT", "ENOTDIR"].includes(code);
    }
  });
  return [...new Set(found.length ? found : [candidates[0]])];
}

function rethrowUnlessRemoved(error: unknown): void {
  const code = error && typeof error === "object" && "code" in error ? error.code : undefined;
  if (code !== "ENOENT") throw error;
}

function listTranscriptFiles(root: string, maxDepth = 4): Array<{ path: string; mtime: number }> {
  const files: string[] = [];
  const walk = (dir: string, depth: number) => {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      const full = join(dir, entry.name);
      if (entry.isFile() && entry.name.endsWith(".jsonl")) files.push(full);
      else if (entry.isDirectory() && depth < maxDepth) {
        try {
          walk(full, depth + 1);
        } catch (error) {
          rethrowUnlessRemoved(error);
        }
      }
    }
  };
  walk(root, 0);
  return files
    .flatMap((filePath) => {
      const stat = statSync(filePath, { throwIfNoEntry: false });
      return stat ? [{ path: filePath, mtime: stat.mtimeMs }] : [];
    })
    .sort((a, b) => b.mtime - a.mtime);
}

const fileId = (filePath: string): string => {
  const { dev, ino } = statSync(filePath, { bigint: true });
  return `${dev}:${ino}`;
};

function symlinkTargets(dir: string): Array<[string, { id: string; name: string | undefined }]> {
  return readdirSync(dir, { withFileTypes: true })
    .filter((entry) => entry.isSymbolicLink())
    .flatMap((entry) => {
      const link = join(dir, entry.name);
      try {
        const resolved = probe(() => realpathSync(link));
        return [[link, { id: fileId(link), name: resolved.known ? resolved.value : undefined }]];
      } catch (error) {
        const code = error && typeof error === "object" && "code" in error ? String(error.code) : "";
        if (["ENOENT", "ELOOP"].includes(code)) return [];
        throw error;
      }
    });
}

function pathSpellings(targetPath: string, linksIn: (dir: string) => Array<[string, { id: string; name: string | undefined }]>): string[] {
  let resolved: string;
  try {
    resolved = realpathSync(targetPath);
  } catch (error) {
    const code = error && typeof error === "object" && "code" in error ? error.code : undefined;
    if (code === "ENOENT") return [targetPath];
    throw error;
  }
  const ancestors: string[] = [];
  for (let dir = dirname(resolved); ; dir = dirname(dir)) {
    ancestors.unshift(dir);
    if (dir === dirname(dir)) break;
  }
  const onPath = [...ancestors, resolved];
  const links = ancestors.flatMap((dir) =>
    linksIn(dir).flatMap(([link, { id, name }]) =>
      onPath.filter((t) => t === name || fileId(t) === id).map((t) => [dir, link, t] as const),
    ),
  );
  const spelled = new Map<string, string[]>();
  for (const dir of onPath) {
    const parent = dirname(dir);
    const forms = new Set(parent === dir ? [dir] : (spelled.get(parent) ?? []).map((form) => join(form, basename(dir))));
    for (const [homeDir, link, target] of links) {
      if (target === dir) {
        for (const form of spelled.get(homeDir) ?? [homeDir]) forms.add(join(form, basename(link)));
      }
    }
    spelled.set(dir, [...forms]);
  }
  return [...new Set([targetPath, ...(spelled.get(resolved) ?? [])])];
}

const BOUNDARY = new Set(Buffer.from("/\\\"' `:;),|&<>]}?!"));
const DOT = ".".charCodeAt(0);
const bounded = (text: Buffer, at: number) => at === text.length || BOUNDARY.has(text[at]);

function mentions(text: Buffer, needle: Buffer): boolean {
  for (let at = text.indexOf(needle); at !== -1; at = text.indexOf(needle, at + 1)) {
    const end = at + needle.length;
    if (bounded(text, end) || (text[end] === DOT && bounded(text, end + 1))) return true;
  }
  return false;
}

function addDriveAndUriVariants(forms: string[], drive: string, slashRest: string): void {
  const cleanRest = slashRest.replace(/^\/+/, "");
  const backRest = cleanRest.replaceAll("/", "\\");
  const encodedRest = cleanRest.split("/").map((segment) => encodeURIComponent(segment)).join("/");
  const uriRests = encodedRest === cleanRest ? [cleanRest] : [cleanRest, encodedRest];
  for (const letter of [drive.toLowerCase(), drive.toUpperCase()]) {
    forms.push(`${letter}:/${cleanRest}`, `${letter}:\\${backRest}`);
    for (const uriRest of uriRests) {
      forms.push(`file:///${letter}:/${uriRest}`, `file:///${letter}%3A/${uriRest}`, `file:///${letter}%3a/${uriRest}`);
    }
  }
}

export function transcriptNeedles(spellings: string[]): Buffer[] {
  const expanded = spellings.flatMap((spelling) => {
    const forms = [spelling];
    const wslMatch = /^\/mnt\/([a-z])\/(.*)$/i.exec(spelling);
    if (wslMatch) {
      addDriveAndUriVariants(forms, wslMatch[1], wslMatch[2]);
    }
    const winMatch = /^([a-z]):[\\/](.*)$/i.exec(spelling);
    if (winMatch) {
      const slashRest = winMatch[2].replaceAll("\\", "/");
      forms.push(`/mnt/${winMatch[1].toLowerCase()}/${slashRest}`);
      addDriveAndUriVariants(forms, winMatch[1], slashRest);
    } else if (/^(?:\\\\|\/\/)/.test(spelling)) {
      forms.push(spelling.replaceAll("\\", "/"), spelling.replaceAll("/", "\\"));
    }
    return forms;
  });
  return [...new Set(expanded)].map((form) => Buffer.from(JSON.stringify(form).slice(1, -1)));
}

function lastChats(roots: string[], spellings: Map<string, string[]>): Map<string, number> {
  const needles = [...spellings].map(([p, forms]) => [p, transcriptNeedles(forms)] as const);
  const latest = new Map<string, number>();
  for (const { path: file } of roots.flatMap((root) => listTranscriptFiles(root))) {
    let text: Buffer;
    let mtime: number;
    try {
      text = readFileSync(file);
      mtime = Math.floor(statSync(file).mtimeMs / 1000);
    } catch (error) {
      rethrowUnlessRemoved(error);
      continue;
    }
    for (const [wtPath, forms] of needles) {
      if (mtime > (latest.get(wtPath) ?? 0) && forms.some((needle) => mentions(text, needle))) {
        latest.set(wtPath, mtime);
      }
    }
  }
  return latest;
}

function trunkName(repo: string): string {
  const advertised = probe(() => git(repo, "ls-remote", "--symref", "origin", "HEAD"));
  return (advertised.known && /^ref: refs\/heads\/(.+)\tHEAD$/m.exec(advertised.value)?.[1]) || "main";
}

function isAncestor(repo: string, head: string, trunk: string): boolean {
  try {
    git(repo, "merge-base", "--is-ancestor", head, `refs/remotes/origin/${trunk}`);
    return true;
  } catch (error) {
    const status = error && typeof error === "object" && "status" in error ? error.status : undefined;
    if (status === 1) return false;
    throw error;
  }
}

function dirtyState(wtPath: string): DirtyCounts {
  const lines = git(wtPath, "status", "--porcelain", "--untracked-files=all", "--ignored=matching", "--ignore-submodules=none")
    .split("\n")
    .filter(Boolean);
  const untracked = lines.filter((line) => line.startsWith("??")).length;
  const ignoredLines = lines.filter((line) => line.startsWith("!!"));
  const ignored = ignoredLines.filter((line) => {
    const parts = line.slice(3).replace(/^"|"$/g, "").split("/");
    return !parts.some((part) => DISPOSABLE.has(part));
  }).length;
  return { wip: lines.length - untracked - ignoredLines.length, untracked, ignored };
}

function reflogOnly(wtPath: string, branchLogged: Set<string>): number {
  const visited = new Set(git(wtPath, "rev-list", "--walk-reflogs", "HEAD").split("\n").filter(Boolean));
  const input = [...visited, ...[...branchLogged].map((sha) => `^${sha}`)].join("\n");
  return Number(
    execFileSync("git", ["rev-list", "--count", "--not", "--branches", "--tags", "--remotes", "--stdin"], {
      cwd: wtPath,
      encoding: "utf8",
      input: `${input}\n`,
      stdio: ["pipe", "pipe", "pipe"],
      maxBuffer: Infinity,
    }).trim(),
  );
}

function remoteState(wtPath: string, branch: string, head: string): string {
  const sha = git(wtPath, "for-each-ref", "--format=%(objectname)", `refs/remotes/origin/${branch}`);
  if (!sha) return "no-remote";
  if (sha === head) return "pushed";
  const [behind, ahead] = git(wtPath, "rev-list", "--left-right", "--count", `refs/remotes/origin/${branch}...HEAD`)
    .split(/\s+/)
    .map(Number);
  return [ahead > 0 && `ahead${ahead}`, behind > 0 && `behind${behind}`].filter(Boolean).join(",");
}

function formatHumanBytes(bytes: number): string {
  const units = ["B", "K", "M", "G", "T", "P", "E"];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  return unit === 0 ? `${Math.round(value)}${units[unit]}` : `${value >= 10 ? value.toFixed(0) : value.toFixed(1)}${units[unit]}`;
}

function size(wtPath: string): string {
  try {
    return execFileSync("du", ["-sh", wtPath], { encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] })
      .trim()
      .split(/\s/)[0];
  } catch {
    let total = 0;
    let entries = 0;
    const maxEntries = 100_000;
    const stack = [wtPath];
    try {
      while (stack.length > 0) {
        const dir = stack.pop()!;
        for (const entry of readdirSync(dir, { withFileTypes: true })) {
          if (++entries > maxEntries) return "?";
          const full = join(dir, entry.name);
          if (entry.isSymbolicLink()) continue;
          if (entry.isDirectory()) stack.push(full);
          else if (entry.isFile()) total += lstatSync(full).size;
        }
      }
      return formatHumanBytes(total);
    } catch {
      return "?";
    }
  }
}

function sizeKey(label: string): number {
  const match = /^(\d+(?:\.\d+)?)([KMGTPE]?)/.exec(label);
  if (!match) return 0;
  return Number(match[1]) * 1024 ** (match[2] ? "KMGTPE".indexOf(match[2]) + 1 : 0);
}

function dirtyLabel({ wip, untracked, ignored }: DirtyCounts, orphans: Fact<number>): string {
  return (
    [
      wip > 0 && `wip:${wip}`,
      untracked > 0 && `untracked:${untracked}`,
      ignored > 0 && `ignored:${ignored}`,
      orphans.known && orphans.value > 0 && `reflog:${orphans.value}`,
    ]
      .filter(Boolean)
      .join(",") || "clean"
  );
}

function lockedLabel(locked: string | null): string {
  if (locked === null) return "-";
  return locked.replace(/\s+/g, " ").trim() || "locked";
}

function classify(facts: {
  trunk: Fact<string>;
  head: Fact<string>;
  age: Fact<number>;
  ancestry: Fact<boolean>;
  dirty: Fact<DirtyCounts>;
  orphans: Fact<number>;
  remote: Fact<string>;
  pr: Fact<PrEntry | null>;
  recent: Fact<boolean>;
  locked: Fact<string | null>;
}): string {
  const { locked, dirty, orphans, pr, recent, ancestry, head } = facts;
  if (locked.known && locked.value !== null) return "hold-locked";
  if (dirty.known && dirty.value.wip > 0) return "hold-wip";
  if (dirty.known && dirty.value.untracked > 0) return "hold-untracked";
  if (orphans.known && orphans.value > 0) return "hold-reflog";
  if (dirty.known && dirty.value.ignored > 0) return "hold-ignored";
  if (pr.known && pr.value?.state === "OPEN") return "hold-open-pr";
  if (recent.known && recent.value) return "verify-recent-chat";
  if (Object.values(facts).some((fact) => !fact.known)) return "review";
  if (ancestry.known && ancestry.value) return "safe";
  if (pr.known && pr.value?.state === "MERGED" && head.known && pr.value.headRefOid === head.value) return "safe";
  return "review";
}

export function resolveHostWorktreePath(
  wtPath: string,
  platform = process.platform,
): { hostPath: string; isWindowsPath: boolean; existsOnHost: boolean } {
  const winMatch = /^([a-z]):[\\/](.*)$/i.exec(wtPath);
  if (!winMatch) {
    return {
      hostPath: wtPath,
      isWindowsPath: false,
      existsOnHost: statSync(wtPath, { throwIfNoEntry: false })?.isDirectory() ?? false,
    };
  }
  const hostPath =
    platform === "linux" ? `/mnt/${winMatch[1].toLowerCase()}/${winMatch[2].replaceAll("\\", "/")}` : wtPath;
  return {
    hostPath,
    isWindowsPath: true,
    existsOnHost: statSync(hostPath, { throwIfNoEntry: false })?.isDirectory() ?? false,
  };
}

function auditWorktree(
  {
    worktree: { path: wtPath, locked },
    hostPath,
    isWindowsPathOnWsl,
  }: {
    worktree: WorktreeEntry;
    hostPath: string;
    isWindowsPathOnWsl: boolean;
  },
  ctx: {
    repo: string;
    trunk: string;
    fetched: Fact<string>;
    prs: Fact<PrEntry[]>;
    chats: Map<string, Fact<number | null>>;
    branchLogs: Fact<Set<string>>;
    now: number;
    warn: (line: string) => void;
  },
): string[] {
  const head = probe(() => git(hostPath, "rev-parse", "HEAD"));
  if (isWindowsPathOnWsl && !head.known) {
    ctx.warn(
      `warn: ${wtPath} is a live Windows worktree (${hostPath}); WSL git cannot read its Windows .git link — inspect or prune from Windows instead of WSL`,
    );
  }
  const age = bind(head, () => probe(() => Math.trunc((ctx.now - Number(git(hostPath, "log", "-1", "--format=%ct", "HEAD"))) / DAY)));
  const ancestry = bind(head, (sha) => probe(() => isAncestor(ctx.repo, sha, ctx.trunk)));
  const dirty = probe(() => dirtyState(hostPath));
  const orphans = bind(head, () => bind(ctx.branchLogs, (logged) => probe(() => reflogOnly(hostPath, logged))));
  const branch = probe(() => {
    const name = git(hostPath, "rev-parse", "--abbrev-ref", "HEAD");
    return name === "HEAD" ? null : name;
  });
  const remote = bind(branch, (name) =>
    name === null ? known("detached") : bind(head, (sha) => probe(() => remoteState(hostPath, name, sha))),
  );
  const pr = bind(ctx.prs, (list) =>
    bind(branch, (name) => {
      const matching = list.filter((entry) => name !== null && entry.headRefName === name);
      return known(matching.find((entry) => entry.state === "OPEN") ?? matching[0] ?? null);
    }),
  );
  const lastChat = ctx.chats.get(wtPath) ?? UNKNOWN;
  const recent = bind(lastChat, (ts) => known(ts !== null && Math.trunc((ctx.now - ts) / DAY) <= RECENT_DAYS));
  const bucket = classify({ trunk: ctx.fetched, head, age, ancestry, dirty, orphans, remote, pr, recent, locked: known(locked) });
  return [
    size(hostPath),
    age.known ? `${age.value}d` : "?",
    ancestry.known ? (ancestry.value ? "YES" : "no") : "?",
    dirty.known ? dirtyLabel(dirty.value, orphans) : "unknown",
    remote.known ? remote.value : "unknown",
    pr.known && pr.value ? `#${pr.value.number}/${pr.value.state}` : "-",
    lastChat.known && lastChat.value !== null ? new Date(lastChat.value * 1000).toISOString().slice(0, 10) : "-",
    bucket,
    lockedLabel(locked),
    wtPath,
  ];
}

function audit(repo: string, transcripts: string[], now = Math.floor(Date.now() / 1000)): string {
  const warn = (line: string) => console.error(line);
  const discover = <T>(read: () => T, message: string): Fact<T> => {
    try {
      return known(read());
    } catch (error) {
      const stderr = error && typeof error === "object" && "stderr" in error ? String(error.stderr ?? "").trim() : "";
      const detail = error instanceof Error ? error.message : String(error);
      warn(`warn: ${message}: ${stderr || detail}`);
      return UNKNOWN;
    }
  };
  const trunk = trunkName(repo);
  const fetched = discover(
    () => git(repo, "fetch", "origin", `+refs/heads/${trunk}:refs/remotes/origin/${trunk}`),
    `could not fetch origin/${trunk}; merged column may be stale`,
  );
  const prs = discover(() => {
    const list = JSON.parse(runGh(GH_PR_LIST, repo)) as unknown;
    if (!Array.isArray(list)) throw new Error("gh returned JSON that is not an array");
    return list as PrEntry[];
  }, "gh pr list failed; PR column will be empty");

  const resolvedWorktrees = parseWorktrees(git(repo, "worktree", "list", "--porcelain", "-z"))
    .slice(1)
    .map((worktree) => {
      const { hostPath, isWindowsPath, existsOnHost } = resolveHostWorktreePath(worktree.path);
      const isWindowsPathOnWsl = process.platform === "linux" && isWindowsPath && existsOnHost;
      // WSL git flags live Windows drive worktrees (E:/...) as prunable because it cannot stat DOS paths.
      const effectivePrunable = worktree.prunable && !isWindowsPathOnWsl;
      return { worktree, hostPath, isWindowsPathOnWsl, effectivePrunable };
    });
  const live = resolvedWorktrees.filter((item) => !item.effectivePrunable);
  const links = new Map<string, Array<[string, { id: string; name: string | undefined }]>>();
  const linksIn = (dir: string) => {
    const cached = links.get(dir);
    if (cached) return cached;
    const loaded = symlinkTargets(dir);
    links.set(dir, loaded);
    return loaded;
  };
  const spellings = new Map(
    live.map((item) => [
      item.worktree.path,
      discover(
        () => [...new Set([item.worktree.path, ...pathSpellings(item.hostPath, linksIn)])],
        `could not resolve spellings of ${item.worktree.path}; LAST_CHAT column will be empty`,
      ),
    ]),
  );
  const scanFailed = "transcript scan failed; LAST_CHAT column will be empty";
  const missing = discover(
    () => transcripts.filter((root) => !statSync(root, { throwIfNoEntry: false })?.isDirectory()),
    scanFailed,
  );
  const found = bind(missing, (roots) => {
    for (const root of roots) warn(`warn: ${root} not found; LAST_CHAT column will be empty`);
    if (roots.length) return UNKNOWN;
    const knownSpellings = new Map(
      [...spellings]
        .filter((pair): pair is [string, { known: true; value: string[] }] => pair[1].known)
        .map(([wtPath, fact]) => [wtPath, fact.value]),
    );
    return discover(() => lastChats(transcripts, knownSpellings), scanFailed);
  });
  const chats = new Map(
    live.map((item) => [
      item.worktree.path,
      bind(spellings.get(item.worktree.path) ?? UNKNOWN, () => bind(found, (latest) => known(latest.get(item.worktree.path) ?? null))),
    ]),
  );
  const branchLogs = discover(
    () => new Set(git(repo, "rev-list", "--walk-reflogs", "--branches").split("\n").filter(Boolean)),
    "could not read branch reflogs; no worktree can reach safe",
  );
  const context = { repo, trunk, fetched, prs, chats, branchLogs, now, warn };
  const rows = resolvedWorktrees.map((item) =>
    item.effectivePrunable
      ? ["-", "?", "-", "-", "-", "-", "-", "prunable", "-", item.worktree.path]
      : auditWorktree(item, context),
  );
  rows.sort((a, b) => sizeKey(b[0]) - sizeKey(a[0]) || (a.join("\t") < b.join("\t") ? 1 : -1));
  return [HEADER, ...rows].map((row) => `${row.join("\t")}\n`).join("");
}

function invokedDirectly(): boolean {
  if (!process.argv[1]) return false;
  try {
    return fileURLToPath(import.meta.url) === realpathSync(process.argv[1]);
  } catch {
    return false;
  }
}

if (invokedDirectly()) {
  const [repoArg, ...transcriptsArgs] = process.argv.slice(2);
  const repo = probe(() => git(repoArg || process.cwd(), "rev-parse", "--show-toplevel"));
  if (!repo.known) {
    console.error("not in a git repo; pass a repo path");
    process.exitCode = 1;
  } else {
    const transcripts = transcriptsArgs.length ? transcriptsArgs : defaultTranscriptRoots();
    process.stdout.write(audit(repo.value, transcripts));
  }
}
