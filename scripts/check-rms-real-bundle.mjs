import fs from 'node:fs';
import path from 'node:path';

const workspaceRoot = process.cwd();
const realBundleRoot = path.join(workspaceRoot, 'apps', 'hvac-web', 'dist', 'real');
const demoBundleRoot = path.join(workspaceRoot, 'apps', 'hvac-web', 'dist', 'demo');
const outputPath = path.join(workspaceRoot, 'out', 'rms-01', 'build-artifact-audit.json');
const configuredBuildId = process.env.HVAC_WEB_BUILD_ID?.trim();
const realBuildId = configuredBuildId || 'real-local';
const demoBuildId = configuredBuildId || 'demo-local';

const realRequiredMarkers = [
  'HVAC_WEB_REAL_GRAPH_V1',
  'REAL MODE · AUTHORITATIVE SHELL',
  realBuildId,
];

const realForbiddenMarkers = [
  'DEMO MODE · 非权威演示数据',
  'HvacMockAgent',
  'mockAlarms',
  'mockSuggestions',
  'A-2093',
  'OPT-201',
  '总部大楼',
  '研发中心',
  'VITE_API_MODE',
];

const demoRequiredMarkers = [
  'DEMO MODE · 非权威演示数据',
  demoBuildId,
];

function walk(directory) {
  const entries = fs.readdirSync(directory, { withFileTypes: true });
  return entries.flatMap((entry) => {
    const fullPath = path.join(directory, entry.name);
    return entry.isDirectory() ? walk(fullPath) : [fullPath];
  });
}

function auditBundle(bundleRoot, requiredMarkers, forbiddenMarkers = []) {
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
    foundIn: contents.filter((item) => item.text.includes(marker)).map((item) => path.relative(workspaceRoot, item.filename).replace(/\\/g, '/')),
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

const real = auditBundle(realBundleRoot, realRequiredMarkers, realForbiddenMarkers);
const demo = auditBundle(demoBundleRoot, demoRequiredMarkers);
const report = {
  schemaVersion: 1,
  real,
  demo,
  passed: real.passed && demo.passed,
};

fs.mkdirSync(path.dirname(outputPath), { recursive: true });
fs.writeFileSync(outputPath, `${JSON.stringify(report, null, 2)}\n`);

if (!report.passed) {
  for (const artifact of [real, demo]) {
    if (artifact.error) console.error(`${artifact.bundleRoot}: ${artifact.error}`);
    if (artifact.missingRequired.length > 0) console.error(`${artifact.bundleRoot} is missing marker(s): ${artifact.missingRequired.join(', ')}`);
    for (const violation of artifact.forbidden) console.error(`Forbidden marker ${violation.marker} found in ${violation.file}`);
  }
  process.exit(1);
}

console.log(`RMS build artifact audit passed: Real ${real.files.length} file(s), Demo ${demo.files.length} file(s).`);
