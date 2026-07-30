import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

import { validateOperationsAgentScenario } from '../benchmarks/operations-agent/scenario-contract.v1.mjs';

const paths = process.argv.slice(2);
if (paths.length === 0) {
  console.error('Usage: npm run operations-agent:benchmark:validate -- <scenario.json> [...]');
  process.exitCode = 2;
} else {
  for (const path of paths) {
    const absolutePath = resolve(path);
    let value;

    try {
      value = JSON.parse(await readFile(absolutePath, 'utf8'));
    } catch (cause) {
      console.error(`INVALID ${path}`);
      console.error(`- [JSON_INVALID] $: ${cause instanceof Error ? cause.message : String(cause)}`);
      process.exitCode = 1;
      continue;
    }

    const result = validateOperationsAgentScenario(value);
    if (result.valid) {
      console.log(`VALID ${result.scenario.scenarioId}@${result.scenario.scenarioVersion} ${path}`);
      continue;
    }

    console.error(`INVALID ${path}`);
    for (const issue of result.errors) {
      console.error(`- [${issue.code}] ${issue.path}: ${issue.message}`);
    }
    process.exitCode = 1;
  }
}
