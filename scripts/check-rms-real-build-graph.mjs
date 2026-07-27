import fs from 'node:fs';
import path from 'node:path';
import {
  collectRealDependencyGraph,
  evaluateRealDependencyGraph,
  relativeGraphReport,
} from './rms-real-build-audit-lib.mjs';

const workspaceRoot = process.cwd();
const outputPath = path.join(workspaceRoot, 'out', 'rms-01', 'real-dependency-graph.json');
const graph = collectRealDependencyGraph({
  entry: path.join(workspaceRoot, 'apps', 'hvac-web', 'src', 'real', 'main.tsx'),
  tsconfig: path.join(workspaceRoot, 'apps', 'hvac-web', 'tsconfig.json'),
  sourceRoot: path.join(workspaceRoot, 'apps', 'hvac-web', 'src'),
});
const violations = evaluateRealDependencyGraph(graph);
const report = relativeGraphReport(graph, workspaceRoot, violations);

fs.mkdirSync(path.dirname(outputPath), { recursive: true });
fs.writeFileSync(outputPath, `${JSON.stringify(report, null, 2)}\n`);

if (violations.length > 0) {
  console.error(`Real dependency graph audit failed with ${violations.length} violation(s).`);
  for (const violation of violations) console.error(`- ${violation.rule}: ${violation.file}${violation.specifier ? ` -> ${violation.specifier}` : ''}`);
  process.exit(1);
}

console.log(`Real dependency graph audit passed: ${report.files.length} reachable local modules.`);
