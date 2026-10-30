import { readFile, readdir } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

// Checks the Web code against the design system's composition rules (DESIGN.md): tables, page
// landmarks and the shadcn setup. Documents are not checked here.
const scriptPath = fileURLToPath(import.meta.url);
const defaultRoot = resolve(dirname(scriptPath), '..');

async function collectFeatureSources(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  const files = [];
  for (const entry of entries) {
    const path = resolve(directory, entry.name);
    if (entry.isDirectory()) {
      files.push(...await collectFeatureSources(path));
    } else if (entry.isFile() && /\.(?:ts|tsx)$/.test(entry.name)) {
      files.push(path);
    }
  }
  return files;
}

async function validateTableComponentUsage(root, errors) {
  const featureRoot = resolve(root, 'apps/hvac-web/src/features');
  for (const path of await collectFeatureSources(featureRoot)) {
    const source = await readFile(path, 'utf8');
    const relativePath = path.slice(root.length + 1).replaceAll('\\', '/');

    if (/<DataTable(?=[\s>])[^>]*\bsurface=/.test(source)) {
      errors.push(`${relativePath} must not use DataTable surface variants; DataTable is standalone-only and compact read-only tables use @/components/ui/table`);
    }
  }
}

function componentRanges(source, componentName) {
  const token = new RegExp(`<${componentName}(?=[\\s>])[^>]*>|<\\/${componentName}>`, 'g');
  const stack = [];
  const ranges = [];
  for (const match of source.matchAll(token)) {
    if (match[0].startsWith('</')) {
      const open = stack.pop();
      if (open) ranges.push({ ...open, end: match.index + match[0].length });
      continue;
    }
    stack.push({ start: match.index, openTag: match[0] });
  }
  return ranges;
}

function cardRanges(source) {
  return componentRanges(source, 'Card');
}

async function validateContentGrammar(root, errors) {
  const featureRoot = resolve(root, 'apps/hvac-web/src/features');
  for (const path of await collectFeatureSources(featureRoot)) {
    const source = await readFile(path, 'utf8');
    const relativePath = path.slice(root.length + 1).replaceAll('\\', '/');

    for (const match of source.matchAll(/<DataTableBlock(?=[\s>])[^>]*\btitle=["']([^"']+)["']/g)) {
      const title = match[1];
      if (!/(?:台账|列表|明细|工作台)/.test(title)) continue;
      const line = source.slice(0, match.index).split('\n').length;
      errors.push(`${relativePath}:${line} DataTableBlock title must name the business object, not the UI container: ${JSON.stringify(title)}`);
    }
    // The shell's SidebarInset is the page landmark; pages use Main and PageHeader.
    for (const match of source.matchAll(/<main[\s>]/g)) {
      const line = source.slice(0, match.index).split('\n').length;
      errors.push(`${relativePath}:${line} pages must not render <main>; use Main from @/components/layout/Main`);
    }
  }
}

async function validateDataTableSurfaceGrammar(root, errors) {
  const featureRoot = resolve(root, 'apps/hvac-web/src/features');
  for (const path of await collectFeatureSources(featureRoot)) {
    const source = await readFile(path, 'utf8');
    const relativePath = path.slice(root.length + 1).replaceAll('\\', '/');
    const surfaceCardTableException = /@surface-card-table-exception\s+07\b/.test(source);

    if (source.includes('DataTableViewPills')) {
      errors.push(`${relativePath} must not reintroduce DataTableViewPills; status/type/tier belongs in Filter and true content-model switching belongs in Tabs`);
    }

    if (!surfaceCardTableException && /getRowProps\s*=\s*\{[^\n]*className:\s*['"][^'"]*(?:text-xs|text-\[)/.test(source)) {
      errors.push(`${relativePath} must not shrink complete DataTable rows below the shared text-sm density`);
    }

    const tableBlockRanges = componentRanges(source, 'DataTableBlock');
    const isInDataTableBlock = (index) => tableBlockRanges.some((range) => index >= range.start && index < range.end);

    for (const match of source.matchAll(/<DataTable(?=[\s>])/g)) {
      if (!isInDataTableBlock(match.index) && !surfaceCardTableException) {
        const line = source.slice(0, match.index).split('\n').length;
        errors.push(`${relativePath}:${line} DataTable must be composed inside @/blocks/data-table DataTableBlock`);
      }
    }

    for (const componentName of ['DataTableAdvancedToolbar', 'DataTablePagination']) {
      const pattern = new RegExp(`<${componentName}(?=[\\s>])`, 'g');
      for (const match of source.matchAll(pattern)) {
        if (isInDataTableBlock(match.index) || surfaceCardTableException) continue;
        const line = source.slice(0, match.index).split('\n').length;
        errors.push(`${relativePath}:${line} ${componentName} belongs to the standardized DataTableBlock composition`);
      }
    }

    for (const range of cardRanges(source)) {
      const block = source.slice(range.start, range.end);
      const line = source.slice(0, range.start).split('\n').length;

      if (!surfaceCardTableException && (/<DataTable(?=[\s>])/.test(block) || block.includes('<DataTableBlock'))) {
        errors.push(`${relativePath}:${line} Card must not wrap DataTable/DataTableBlock; page-level data workspaces own their own surface`);
      }

      if (/<div[^>]*className=["'](?=[^"']*rounded)(?=[^"']*\bborder\b)[^"']*["'][^>]*>\s*<Table(?=[\s>])/s.test(block)) {
        errors.push(`${relativePath}:${line} Card must not add a second rounded bordered wrapper around a compact shadcn Table`);
      }
    }
  }
}

function parseArgs(argv) {
  const rootArg = argv.find((argument) => argument.startsWith('--root='));
  return { root: rootArg ? resolve(rootArg.slice('--root='.length)) : defaultRoot };
}

export async function validateDesignSystem(root = defaultRoot) {
  const errors = [];
  await validateTableComponentUsage(root, errors);
  await validateContentGrammar(root, errors);
  await validateDataTableSurfaceGrammar(root, errors);

  const componentsJson = await readFile(resolve(root, 'apps/hvac-web/components.json'), 'utf8');
  for (const expected of ['"style": "radix-nova"', '"iconLibrary": "lucide"']) {
    if (!componentsJson.includes(expected)) errors.push(`apps/hvac-web/components.json must contain ${expected}`);
  }

  // shadcn's base styles are a copy of the pinned CLI version's dist/tailwind.css, not an
  // npm dependency; the copy and the CLI pin must move together.
  const cliSource = await readFile(resolve(root, 'scripts/shadcn-cli.mjs'), 'utf8');
  const cliVersion = /SHADCN_CLI_VERSION = '([^']+)'/.exec(cliSource)?.[1];
  const shadcnStyles = await readFile(resolve(root, 'apps/hvac-web/src/styles/shadcn-tailwind.css'), 'utf8');
  if (!cliVersion) {
    errors.push('scripts/shadcn-cli.mjs must pin SHADCN_CLI_VERSION');
  } else if (!shadcnStyles.includes(`shadcn@${cliVersion} dist/tailwind.css`)) {
    errors.push(`apps/hvac-web/src/styles/shadcn-tailwind.css must be the dist/tailwind.css of shadcn@${cliVersion}, the version scripts/shadcn-cli.mjs runs`);
  }
  const globalCss = await readFile(resolve(root, 'apps/hvac-web/src/global.css'), 'utf8');
  if (globalCss.includes('"shadcn/tailwind.css"')) {
    errors.push('apps/hvac-web/src/global.css must import ./styles/shadcn-tailwind.css; the shadcn package is not installed');
  }

  return { errors };
}

async function main() {
  const { root } = parseArgs(process.argv.slice(2));
  const result = await validateDesignSystem(root);
  if (result.errors.length > 0) {
    for (const error of result.errors) console.error(`- ${error}`);
    process.exitCode = 1;
    return;
  }

  console.log('Design system check passed: tables, page landmarks and the shadcn setup follow DESIGN.md.');
}

if (process.argv[1] && resolve(process.argv[1]) === scriptPath) {
  main().catch((error) => {
    console.error(error);
    process.exitCode = 1;
  });
}
