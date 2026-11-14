#!/usr/bin/env node
import { execFileSync } from "node:child_process";
import { createHash, randomUUID } from "node:crypto";
import { lstatSync, mkdirSync, readFileSync, realpathSync, renameSync, rmSync, statSync, writeFileSync } from "node:fs";
import { dirname, isAbsolute, join, relative, resolve, sep } from "node:path";
import { parseArgs } from "node:util";

type ProjectId = { worktree: string; gitDir: string };
type FileDigest = { path: string; sha256: string };
type CheckpointRecord = {
  schemaVersion: 1;
  gitDir: string;
  worktree: string;
  branch: string | null;
  createdAt: string;
  note: FileDigest;
  artifacts: FileDigest[];
};

function git(project: string, ...args: string[]): string {
  return execFileSync("git", ["-C", project, ...args], {
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
  }).trim();
}

function identity(project: string): ProjectId {
  return {
    worktree: realpathSync(git(project, "rev-parse", "--show-toplevel")),
    gitDir: realpathSync(git(project, "rev-parse", "--path-format=absolute", "--git-common-dir")),
  };
}

function inside(root: string, file: string): boolean {
  const rel = relative(root, file);
  return rel === "" || (!isAbsolute(rel) && rel !== ".." && !rel.startsWith(`..${sep}`));
}

function asString(value: unknown, name: string): string {
  if (typeof value !== "string" || !value) throw new Error(`missing ${name}`);
  return value;
}

function asObject(value: unknown, name: string): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error(`invalid ${name}`);
  return value as Record<string, unknown>;
}

function fileInfo(filePath: string, roots: string[]): FileDigest {
  const canonical = realpathSync(filePath);
  if (!roots.some((root) => inside(root, canonical)) || !statSync(canonical).isFile()) {
    throw new Error(`checkpoint file must belong to this project: ${filePath}`);
  }
  return { path: canonical, sha256: createHash("sha256").update(readFileSync(canonical)).digest("hex") };
}

function verifyFile(value: unknown, roots: string[]): FileDigest {
  const record = asObject(value, "file record");
  const file = fileInfo(asString(record.path, "file path"), roots);
  if (file.sha256 !== asString(record.sha256, "file digest")) {
    throw new Error(`checkpoint file changed: ${file.path}`);
  }
  return file;
}

const linkRule =
  "Link each local file with an inline Markdown link whose destination is the path, written bare or as `<path>`, with no square brackets in the link text and no title after the path. Reference-style links are not read. The path is relative to the note or absolute, and it is percent-decoded, so write a literal `%` as `%25`. Wrap a path that contains spaces or parentheses in angle brackets, or percent-encode it. Links inside code spans and fenced code blocks are ignored.";

function resolveLink(note: FileDigest, link: string, target: string): string {
  let decoded: string;
  try {
    decoded = decodeURIComponent(target.split("#")[0]);
  } catch {
    throw new Error(`link ${link} has malformed percent-encoding. ${linkRule}`);
  }
  try {
    return realpathSync(resolve(dirname(note.path), decoded));
  } catch (error) {
    const code = error && typeof error === "object" && "code" in error ? String(error.code) : "ERR";
    throw new Error(`link ${link} does not resolve (${code}). ${linkRule}`);
  }
}

function verifyLinks(note: FileDigest, artifacts: FileDigest[]): void {
  const prose = readFileSync(note.path, "utf8")
    .replace(/^\uFEFF/, "")
    .replaceAll("\r\n", "\n")
    .replace(/^ {0,3}((`|~)\2{2,}).*\n[\s\S]*?(?:^ {0,3}\1\2*[ \t]*$|(?![\s\S]))/gm, " ")
    .replace(/(?<!`)(`+)(?!`)[\s\S]*?(?<!`)\1(?!`)/g, " ");
  const links = [...prose.matchAll(/\[[^\]]*\]\((?:<([^<>]+)>|([^)]+))\)/g)]
    .filter(([, angled, bare]) => !/^(?:[a-z]{2,}:|#)/i.test(angled ?? bare ?? ""))
    .map(([link, angled, bare]) => ({ link, path: resolveLink(note, link, angled ?? bare ?? "") }));
  for (const artifact of artifacts) {
    if (!links.some(({ path }) => path === artifact.path)) {
      throw new Error(`note must link requested artifact: ${artifact.path}. ${linkRule}`);
    }
  }
  for (const { link, path } of links) {
    if (path !== note.path && !artifacts.some((artifact) => artifact.path === path)) {
      throw new Error(`register every local note link with --artifact: ${link} resolves to ${path}. ${linkRule}`);
    }
  }
}

function load(store: string, project: ProjectId): CheckpointRecord {
  const pointer = join(store, "latest.json");
  fileInfo(pointer, [store]);
  const record = asObject(JSON.parse(readFileSync(pointer, "utf8")), "checkpoint");
  if (record.schemaVersion !== 1 || record.gitDir !== project.gitDir) {
    throw new Error("checkpoint project mismatch");
  }
  const worktree = asString(record.worktree, "worktree");
  const roots = [store, project.worktree];
  if (worktree !== project.worktree) {
    try {
      if (identity(worktree).gitDir === project.gitDir) roots.push(realpathSync(worktree));
    } catch {
      /* Removed worktrees cannot supply artifacts; store files remain readable. */
    }
  }
  const note = verifyFile(record.note, [store]);
  if (!Array.isArray(record.artifacts)) throw new Error("invalid artifact list");
  const artifacts = record.artifacts.map((value) => verifyFile(value, roots));
  const branch = record.branch === null ? null : asString(record.branch, "branch");
  const createdAt = asString(record.createdAt, "timestamp");
  if (!Number.isFinite(Date.parse(createdAt))) throw new Error("invalid checkpoint timestamp");
  verifyLinks(note, artifacts);
  return { schemaVersion: 1, gitDir: project.gitDir, worktree, branch, createdAt, note, artifacts };
}

try {
  const { values, positionals } = parseArgs({
    allowPositionals: true,
    options: {
      project: { type: "string", default: "." },
      note: { type: "string" },
      artifact: { type: "string", multiple: true, default: [] },
    },
  });
  const [operation] = positionals;
  if (positionals.length !== 1 || !["begin", "publish", "read"].includes(operation ?? "")) {
    throw new Error("usage: resume.ts begin|publish|read --project <directory> [--note <file> --artifact <file> ...]");
  }
  if (operation !== "publish" && (values.note || (values.artifact?.length ?? 0) > 0)) {
    throw new Error("only publish accepts files");
  }
  const project = identity(values.project ?? ".");
  const store = join(project.gitDir, "pstack", "resume");
  if (operation === "read") {
    try {
      lstatSync(join(store, "latest.json"));
    } catch (error) {
      const code = error && typeof error === "object" && "code" in error ? error.code : undefined;
      if (code !== "ENOENT") throw error;
      console.log(JSON.stringify({ kind: "missing" }));
      process.exit(2);
    }
    console.log(JSON.stringify({ kind: "checkpoint", checkpoint: load(store, project) }));
  } else {
    mkdirSync(store, { recursive: true, mode: 0o700 });
    if (!inside(project.gitDir, realpathSync(store))) throw new Error("resume store escapes the Git directory");
    if (operation === "begin") {
      const directory = join(store, randomUUID());
      mkdirSync(directory, { mode: 0o700 });
      console.log(JSON.stringify({ kind: "directory", directory }));
    } else {
      const note = fileInfo(asString(values.note, "--note"), [store]);
      const artifacts = (values.artifact ?? []).map((filePath) => fileInfo(filePath, [store, project.worktree]));
      if (new Set(artifacts.map((file) => file.path)).size !== artifacts.length) {
        throw new Error("duplicate artifact");
      }
      verifyLinks(note, artifacts);
      const checkpoint: CheckpointRecord = {
        schemaVersion: 1,
        ...project,
        branch: git(project.worktree, "branch", "--show-current") || null,
        createdAt: new Date().toISOString(),
        note,
        artifacts,
      };
      const temporary = join(store, `.latest-${randomUUID()}.json`);
      try {
        writeFileSync(temporary, `${JSON.stringify(checkpoint)}\n`, { flag: "wx", mode: 0o600 });
        renameSync(temporary, join(store, "latest.json"));
      } finally {
        rmSync(temporary, { force: true });
      }
      console.log(JSON.stringify({ kind: "checkpoint", checkpoint: load(store, project) }));
    }
  }
} catch (error) {
  const detail = error instanceof Error ? error.message : String(error);
  console.log(JSON.stringify({ kind: "unavailable", detail }));
  process.exitCode = 1;
}
