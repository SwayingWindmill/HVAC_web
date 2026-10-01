import fs from 'node:fs';
import path from 'node:path';

const workspaceRoot = process.cwd();
const bundleRoot = path.join(workspaceRoot, 'apps', 'hvac-web', 'dist');
const outputPath = path.join(workspaceRoot, 'out', 'rms-web-build', 'build-artifact-audit.json');
const configuredBuildId = process.env.HVAC_WEB_BUILD_ID?.trim();
const buildId = configuredBuildId || 'local';

const requiredMarkers = [
  'HVAC_WEB_AUTHORITATIVE_GRAPH_V1',
  'AUTHORITATIVE WEB SHELL',
  buildId,
];

const forbiddenMarkers = [
  'DEMO MODE · 非权威演示数据',
  'HvacMockAgent',
  'mockAlarms',
  'mockSuggestions',
  'A-2093',
  'OPT-201',
  'VITE_API_MODE',
];

function walk(directory) {
  const entries = fs.readdirSync(directory, { withFileTypes: true });
  return entries.flatMap((entry) => {
    const fullPath = path.join(directory, entry.name);
    return entry.isDirectory() ? walk(fullPath) : [fullPath];
  });
}

function auditBundle() {
  if (!fs.existsSync(bundleRoot)) {
    return {
      bundleRoot: path.relative(workspaceRoot, bundleRoot).replace(/\\/g, '/'),
      files: [],
      required: [],
      missingRequired: requiredMarkers,
      forbidden: [],
      error: 'bundle-not-found',
      passed: false,
    };
  }

  const files = walk(bundleRoot).filter((filename) => /\.(?:html|js|css|json)$/.test(filename));
  const contents = files.map((filename) => ({ filename, text: fs.readFileSync(filename, 'utf8') }));
  const required = requiredMarkers.map((marker) => ({
    marker,
    foundIn: contents
      .filter((item) => item.text.includes(marker))
      .map((item) => path.relative(workspaceRoot, item.filename).replace(/\\/g, '/')),
  }));
  const forbidden = forbiddenMarkers.flatMap((marker) => contents
    .filter((item) => item.text.includes(marker))
    .map((item) => ({ marker, file: path.relative(workspaceRoot, item.filename).replace(/\\/g, '/') })));
  const missingRequired = required.filter((item) => item.foundIn.length === 0).map((item) => item.marker);

  return {
    bundleRoot: path.relative(workspaceRoot, bundleRoot).replace(/\\/g, '/'),
    files: files.map((filename) => path.relative(workspaceRoot, filename).replace(/\\/g, '/')).sort(),
    required,
    missingRequired,
    forbidden,
    passed: missingRequired.length === 0 && forbidden.length === 0,
  };
}

const web = auditBundle();
const report = {
  schemaVersion: 2,
  artifact: 'AUTHORITATIVE_WEB',
  web,
  passed: web.passed,
};

fs.mkdirSync(path.dirname(outputPath), { recursive: true });
fs.writeFileSync(outputPath, `${JSON.stringify(report, null, 2)}\n`);

if (!report.passed) {
  if (web.error) console.error(`${web.bundleRoot}: ${web.error}`);
  if (web.missingRequired.length > 0) {
    console.error(`${web.bundleRoot} is missing marker(s): ${web.missingRequired.join(', ')}`);
  }
  for (const violation of web.forbidden) {
    console.error(`Forbidden marker ${violation.marker} found in ${violation.file}`);
  }
  process.exit(1);
}

console.log(`RMS build artifact audit passed: ${web.files.length} authoritative Web file(s).`);
