import { mkdir, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';

import {
  formatOperationsAgentBenchmarkSummary,
  runOperationsAgentBenchmark,
} from '../benchmarks/operations-agent/benchmark-runner.v1.mjs';

const options = {
  scenarioDirectory: resolve('benchmarks/operations-agent/scenarios'),
  reportPath: null,
  jsonOnly: false,
};

let usageError = null;
for (const argument of process.argv.slice(2)) {
  if (argument.startsWith('--scenarios=')) {
    options.scenarioDirectory = resolve(argument.slice('--scenarios='.length));
    continue;
  }
  if (argument.startsWith('--report=')) {
    options.reportPath = resolve(argument.slice('--report='.length));
    continue;
  }
  if (argument === '--json') {
    options.jsonOnly = true;
    continue;
  }
  usageError = `Unknown argument: ${argument}`;
  break;
}

if (usageError) {
  console.error(usageError);
  console.error('Usage: npm run operations-agent:benchmark -- [--scenarios=<dir>] [--report=<file>] [--json]');
  process.exitCode = 2;
} else {
  const report = await runOperationsAgentBenchmark({
    scenarioDirectory: options.scenarioDirectory,
  });
  const serialized = `${JSON.stringify(report, null, 2)}\n`;

  if (options.reportPath) {
    await mkdir(dirname(options.reportPath), { recursive: true });
    await writeFile(options.reportPath, serialized, 'utf8');
  }

  console.log(options.jsonOnly ? serialized.trimEnd() : formatOperationsAgentBenchmarkSummary(report));
  process.exitCode = report.status === 'PASSED' ? 0 : 1;
}
