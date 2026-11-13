import { spawnSync } from 'node:child_process';
import { dirname, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

export interface CommandFormalSummary {
  ok: boolean;
  package: string;
  passedTests: string[];
  failedTests: string[];
  counterexamples: string[];
  exitCode: number;
  rawOutput: string;
}

const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const formalPackage = './modules/command/pkg/commandformal/...';

export const parseCommandFormalOptions = (args: readonly string[]): { json: boolean } => {
  let json = false;
  for (const arg of args) {
    if (arg === '--json') {
      json = true;
    } else if (arg.trim() !== '') {
      throw new Error(`Unknown flag "${arg}" for run-command-formal-check.ts`);
    }
  }
  return { json };
};

export const summarizeCommandFormalOutput = (rawOutput: string, exitCode: number): CommandFormalSummary => {
  const passedTests: string[] = [];
  const failedTests: string[] = [];
  const counterexamples: string[] = [];

  for (const line of rawOutput.split(/\r?\n/u)) {
    const passMatch = /^\s*--- PASS:\s+(\S+)/u.exec(line);
    if (passMatch) {
      passedTests.push(passMatch[1]);
      continue;
    }
    const failMatch = /^\s*--- FAIL:\s+(\S+)/u.exec(line);
    if (failMatch) {
      failedTests.push(failMatch[1]);
      continue;
    }
    const trimmed = line.trim();
    if (/\[(SingleCommandProtocol|MultiCommandDeviceExclusivity|ReplyRecoveryQueueDrain)\/P[1-6]\]/u.test(trimmed)) {
      counterexamples.push(trimmed);
    }
  }

  return {
    ok: exitCode === 0 && failedTests.length === 0 && passedTests.length > 0,
    package: formalPackage,
    passedTests,
    failedTests,
    counterexamples,
    exitCode,
    rawOutput,
  };
};

export const runCommandFormalCheck = (root = repositoryRoot): CommandFormalSummary => {
  const result = spawnSync(process.execPath, ['scripts/run-go.mjs', 'test', '-count=1', '-v', formalPackage], {
    cwd: root,
    encoding: 'utf8',
    windowsHide: true,
  });
  const combined = [result.stdout ?? '', result.stderr ?? ''].filter(Boolean).join('');
  return summarizeCommandFormalOutput(combined, result.status ?? 1);
};

const main = (): void => {
  const { json } = parseCommandFormalOptions(process.argv.slice(2));
  const summary = runCommandFormalCheck(repositoryRoot);
  if (json) {
    process.stdout.write(`${JSON.stringify(summary, null, 2)}\n`);
  } else {
    process.stdout.write(summary.rawOutput);
    if (!summary.ok && summary.counterexamples.length > 0) {
      process.stderr.write(`\nFormal counterexample:\n${summary.counterexamples.join('\n')}\n`);
    }
  }
  if (!summary.ok) {
    process.exitCode = summary.exitCode || 1;
  }
};

if (process.argv[1] && pathToFileURL(process.argv[1]).href === import.meta.url) {
  main();
}
