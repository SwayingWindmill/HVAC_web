import { readFileSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import { createPackageScriptLongChainBaseline } from './check-repository-governance.ts';

const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const packageJson = JSON.parse(readFileSync(join(repositoryRoot, 'package.json'), 'utf8'));
const baseline = createPackageScriptLongChainBaseline(packageJson.scripts ?? {});
const outputPath = join(repositoryRoot, 'scripts/package-script-long-chain-baseline.json');

writeFileSync(outputPath, `${JSON.stringify(baseline, null, 2)}\n`, 'utf8');
console.log(`Updated ${Object.keys(baseline.scripts).length} long-chain script exemptions.`);
