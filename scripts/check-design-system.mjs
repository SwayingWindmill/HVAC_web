import { mkdir, readFile, readdir, writeFile } from 'node:fs/promises';
import { dirname, extname, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const scriptPath = fileURLToPath(import.meta.url);
const defaultRoot = resolve(dirname(scriptPath), '..');
const baselinePath = 'docs/design-system/radius-exceptions.v1.json';
const sourcePath = 'apps/hvac-web/src';

const allowedRadiusValues = new Set(['0', '0px', '8px', '16px', '20px', '999px']);
const requiredFiles = [
  'DESIGN.md',
  'docs/design-references/README.md',
  'docs/design-references/linear/DESIGN.md',
  'docs/design-system/README.md',
  'docs/design-system/preview.css',
  'docs/design-system/preview.html',
  'docs/design-system/preview-dark.html',
  'apps/hvac-web/src/theme/tokens.ts',
  'apps/hvac-web/src/theme/AppTheme.tsx',
  baselinePath,
];

function normalizeNewlines(value) {
  return value.replace(/\r\n/g, '\n');
}

async function readRequired(root, path) {
  try {
    return normalizeNewlines(await readFile(resolve(root, path), 'utf8'));
  } catch (error) {
    throw new Error(`Missing required design asset: ${path} (${error.message})`);
  }
}

function frontmatterOf(markdown) {
  if (!markdown.startsWith('---\n')) return null;
  const end = markdown.indexOf('\n---\n', 4);
  return end === -1 ? null : markdown.slice(4, end);
}

function requireText(errors, source, label, expected) {
  if (!source.includes(expected)) errors.push(`${label} must contain ${JSON.stringify(expected)}`);
}

async function walkFiles(root, directory) {
  const absolute = resolve(root, directory);
  const entries = await readdir(absolute, { withFileTypes: true });
  const files = [];
  for (const entry of entries) {
    const path = `${directory}/${entry.name}`.replaceAll('\\', '/');
    if (entry.isDirectory()) files.push(...await walkFiles(root, path));
    else files.push(path);
  }
  return files;
}

function addRadiusCount(result, path, token) {
  const normalized = token === '0.0' ? '0' : token;
  if (allowedRadiusValues.has(normalized)) return;
  result[path] ??= {};
  result[path][normalized] = (result[path][normalized] ?? 0) + 1;
}

function collectCssRadii(result, path, source) {
  const declarations = source.matchAll(/border-radius\s*:\s*([^;}\n]+)/g);
  for (const declaration of declarations) {
    const values = declaration[1].match(/(?:\d+(?:\.\d+)?(?:px|rem|%)|\b0\b)/g) ?? [];
    for (const value of values) addRadiusCount(result, path, value);
  }
}

function collectTypeScriptRadii(result, path, source) {
  const declarations = source.matchAll(/borderRadius(?:LG|SM|XS)?\s*:\s*(?:['"]([^'"]+)['"]|(\d+(?:\.\d+)?))/g);
  for (const declaration of declarations) {
    const raw = declaration[1] ?? declaration[2];
    const values = raw.match(/(?:\d+(?:\.\d+)?(?:px|rem|%)|\b0\b)/g) ?? [];
    for (const value of values) addRadiusCount(result, path, declaration[2] ? `${value}px` : value);
  }
}

export async function collectRadiusDrift(root = defaultRoot) {
  const result = {};
  const files = await walkFiles(root, sourcePath);
  for (const path of files.sort()) {
    const extension = extname(path);
    if (!['.css', '.ts', '.tsx'].includes(extension)) continue;
    const source = normalizeNewlines(await readFile(resolve(root, path), 'utf8'));
    if (extension === '.css') collectCssRadii(result, path, source);
    else collectTypeScriptRadii(result, path, source);
  }
  return result;
}

export function compareRadiusBaseline(current, baseline) {
  const errors = [];
  for (const path of Object.keys(current).sort()) {
    for (const value of Object.keys(current[path]).sort()) {
      const count = current[path][value];
      const allowance = baseline[path]?.[value] ?? 0;
      if (count > allowance) {
        errors.push(`${path} uses ${value} ${count} times; baseline allows ${allowance}`);
      }
    }
  }
  return errors;
}

function parseArgs(argv) {
  const rootArg = argv.find((argument) => argument.startsWith('--root='));
  return {
    root: rootArg ? resolve(rootArg.slice('--root='.length)) : defaultRoot,
    updateRadiusBaseline: argv.includes('--update-radius-baseline'),
  };
}

async function validateDesignSystem(root) {
  const errors = [];
  const sources = Object.fromEntries(await Promise.all(requiredFiles.map(async (path) => [path, await readRequired(root, path)])));

  const design = sources['DESIGN.md'];
  const frontmatter = frontmatterOf(design);
  if (!frontmatter) errors.push('DESIGN.md must begin with valid YAML frontmatter');
  else {
    for (const expected of [
      'schema: design.md/v1',
      'sourceOfTruth: true',
      'designDirection: Industrial Calm',
      'components: Ant Design',
      'charts: ECharts',
      'icons: Ant Design Icons',
      'light: docs/design-system/preview.html',
      'dark: docs/design-system/preview-dark.html',
      'styles: docs/design-system/preview.css',
      'path: docs/design-references/linear/DESIGN.md',
      'policy: reference-only',
      'brandPrimaryRgb: [15, 181, 174]',
      'brandStrongRgb: [14, 156, 150]',
      'brandDeepRgb: [11, 74, 76]',
      'semanticSuccessRgb: [22, 163, 74]',
      'semanticWarningRgb: [245, 158, 11]',
      'semanticErrorRgb: [220, 38, 38]',
      'semanticInformationRgb: [37, 99, 235]',
      'feature: 20px',
      'card: 16px',
      'control: 8px',
      'pill: 999px',
      'check: npm run design:check',
    ]) requireText(errors, frontmatter, 'DESIGN.md frontmatter', expected);
  }

  requireText(errors, design, 'DESIGN.md', '# 泉来禾智慧能源平台设计系统');
  requireText(errors, design, 'DESIGN.md', 'Industrial Calm');
  requireText(errors, sources['docs/design-references/README.md'], 'design reference policy', 'reference-only');
  requireText(errors, sources['docs/design-references/linear/DESIGN.md'], 'Linear reference', 'name: Linear-design-analysis');
  requireText(errors, sources['docs/design-system/preview.css'], 'design preview styles', '--brand: #0fb5ae');
  requireText(errors, sources['docs/design-system/preview.css'], 'design preview styles', '--brand-deep: #0b4a4c');
  requireText(errors, sources['docs/design-system/preview.css'], 'design preview styles', 'border-radius: 16px');
  requireText(errors, sources['docs/design-system/preview.css'], 'design preview styles', 'border-radius: 8px');
  requireText(errors, sources['docs/design-system/preview.html'], 'light preview', '泉来禾智慧能源设计预览');
  requireText(errors, sources['docs/design-system/preview-dark.html'], 'dark preview', 'data-theme="dark"');

  const tokens = sources['apps/hvac-web/src/theme/tokens.ts'];
  for (const expected of [
    "teal: '#0FB5AE'",
    "tealStrong: '#0E9C96'",
    "deepTeal: '#0B4A4C'",
    "ok: '#16A34A'",
    "warn: '#F59E0B'",
    "err: '#DC2626'",
    "info: '#2563EB'",
  ]) requireText(errors, tokens, 'theme tokens', expected);

  const theme = sources['apps/hvac-web/src/theme/AppTheme.tsx'];
  for (const expected of [
    'colorPrimary: BRAND.teal',
    'borderRadius: 8',
    'itemBorderRadius: 8',
    'borderRadiusLG: 16',
  ]) requireText(errors, theme, 'Ant Design theme', expected);

  const baselineDocument = JSON.parse(sources[baselinePath]);
  if (baselineDocument.schemaVersion !== 1 || typeof baselineDocument.exceptions !== 'object') {
    errors.push(`${baselinePath} must use schemaVersion 1 and an exceptions object`);
  } else {
    const current = await collectRadiusDrift(root);
    errors.push(...compareRadiusBaseline(current, baselineDocument.exceptions));
  }

  return { errors, legacyRadiusFiles: Object.keys(baselineDocument.exceptions ?? {}).length };
}

async function main() {
  const { root, updateRadiusBaseline } = parseArgs(process.argv.slice(2));
  if (updateRadiusBaseline) {
    const exceptions = await collectRadiusDrift(root);
    const output = {
      schemaVersion: 1,
      policy: 'Existing non-standard radii may be reduced but not increased. New files and values fail design:check.',
      allowed: [...allowedRadiusValues],
      exceptions,
    };
    const outputPath = resolve(root, baselinePath);
    await mkdir(dirname(outputPath), { recursive: true });
    await writeFile(outputPath, `${JSON.stringify(output, null, 2)}\n`, 'utf8');
    console.log(`Updated design radius baseline: ${relative(root, outputPath).replaceAll('\\', '/')}`);
    return;
  }

  const { errors, legacyRadiusFiles } = await validateDesignSystem(root);
  if (errors.length > 0) {
    console.error('Design system check failed:');
    for (const error of errors) console.error(`- ${error}`);
    process.exitCode = 1;
    return;
  }
  console.log(`Design system check passed: authoritative guide, reference policy, previews, theme tokens and ${legacyRadiusFiles} legacy radius files verified.`);
}

if (resolve(process.argv[1] ?? '') === scriptPath) await main();
