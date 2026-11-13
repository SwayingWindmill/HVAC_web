import { existsSync } from 'node:fs';
import { readFile, readdir, stat } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import ts from 'typescript';
import { Generator, getConfig } from '@tanstack/router-generator';

// Checks the Web code against the design system's composition rules (DESIGN.md) and pinned
// web-platform guardrails (docs/architecture/modern-web-guidance-source-review-2026-11-11.md).
// Documents are not checked here.
interface ComponentRange {
  start: number;
  end: number;
  openTag: string;
}

const scriptPath = fileURLToPath(import.meta.url);
const defaultRoot = resolve(dirname(scriptPath), '..');

async function collectFeatureSources(directory: string): Promise<string[]> {
  const entries = await readdir(directory, { withFileTypes: true });
  const files: string[] = [];
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

async function validateTableComponentUsage(root: string, errors: string[]): Promise<void> {
  const featureRoot = resolve(root, 'apps/hvac-web/src/features');
  for (const path of await collectFeatureSources(featureRoot)) {
    const source = await readFile(path, 'utf8');
    const relativePath = path.slice(root.length + 1).replaceAll('\\', '/');

    if (/<DataTable(?=[\s>])[^>]*\bsurface=/.test(source)) {
      errors.push(`${relativePath} must not use DataTable surface variants; DataTable is standalone-only and compact read-only tables use @/components/ui/table`);
    }
  }
}

function componentRanges(source: string, componentName: string): ComponentRange[] {
  const token = new RegExp(`<${componentName}(?=[\\s>])[^>]*>|<\\/${componentName}>`, 'g');
  const stack: Array<{ start: number; openTag: string }> = [];
  const ranges: ComponentRange[] = [];
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

function cardRanges(source: string): ComponentRange[] {
  return componentRanges(source, 'Card');
}

async function validateContentGrammar(root: string, errors: string[]): Promise<void> {
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

async function validateDataTableSurfaceGrammar(root: string, errors: string[]): Promise<void> {
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
    const isInDataTableBlock = (index: number): boolean => tableBlockRanges.some((range) => index >= range.start && index < range.end);

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

function balancedBraces(source: string, openBraceIndex: number): string {
  let depth = 0;
  for (let index = openBraceIndex; index < source.length; index += 1) {
    const ch = source[index];
    if (ch === '{') depth += 1;
    else if (ch === '}') {
      depth -= 1;
      if (depth === 0) return source.slice(openBraceIndex, index + 1);
    }
  }
  return source.slice(openBraceIndex);
}

async function validateImeSafeEnterHandlers(root: string, errors: string[]): Promise<void> {
  const srcRoot = resolve(root, 'apps/hvac-web/src');
  const tagPattern = /<(?:input|textarea|Input|Textarea|InputGroupInput|CommandInput)\b/g;
  for (const path of await collectFeatureSources(srcRoot)) {
    const source = await readFile(path, 'utf8');
    const relativePath = path.slice(root.length + 1).replaceAll('\\', '/');
    for (const match of source.matchAll(tagPattern)) {
      const rest = source.slice(match.index);
      const keyDownMatch = /\bonKeyDown\s*=\s*\{/.exec(rest);
      if (!keyDownMatch) continue;
      // Ensure this onKeyDown belongs to the matched opening tag rather than a later sibling tag.
      const beforeAttr = rest.slice(match[0].length, keyDownMatch.index);
      if (/(?:^|[^=])>/.test(beforeAttr.replace(/\{[^{}]*\}/g, ''))) continue;

      const braceStart = match.index + keyDownMatch.index + keyDownMatch[0].length - 1;
      let handlerSource = balancedBraces(source, braceStart);
      const identifier = /^\{\s*([A-Za-z_$][\w$]*)\s*\}$/.exec(handlerSource)?.[1];
      if (identifier) {
        const decl = new RegExp(`(?:function\\s+${identifier}\\b[^\\{]*|const\\s+${identifier}\\s*=[^\\{]*)\\{`).exec(source);
        if (decl) handlerSource = balancedBraces(source, decl.index + decl[0].length - 1);
      }

      if (!/\.(?:key|code)\s*(?:===|!==)\s*['"]Enter['"]/.test(handlerSource)) continue;
      if (/\.(?:ctrlKey|metaKey)\b/.test(handlerSource)) continue;
      const hasComposing = /\bisComposing\b/.test(handlerSource);
      const hasKeyCode229 = /\.keyCode\s*===\s*229\b/.test(handlerSource);
      if (!hasComposing || !hasKeyCode229) {
        const line = source.slice(0, braceStart).split('\n').length;
        errors.push(`${relativePath}:${line} text input Enter onKeyDown must guard IME composition with both isComposing and keyCode === 229`);
      }
    }
  }
}

function validateColorSchemeSetup(indexHtml: string, globalCss: string, errors: string[]): void {
  if (!/<meta\s+name=["']color-scheme["']\s+content=["']light dark["']\s*\/?>/.test(indexHtml)) {
    errors.push('apps/hvac-web/index.html must declare <meta name="color-scheme" content="light dark" />');
  }
  const rootBlock = /:root\s*\{([^}]*)\}/.exec(globalCss)?.[1] ?? '';
  if (!/\bcolor-scheme\s*:\s*light\s+dark\s*;/.test(rootBlock)) {
    errors.push('apps/hvac-web/src/global.css :root must declare color-scheme: light dark;');
  }
  const lightThemeBlock = /:root\[data-theme=["']light["']\]\s*\{([^}]*)\}/.exec(globalCss)?.[1] ?? '';
  if (!/\bcolor-scheme\s*:\s*light\s*;/.test(lightThemeBlock)) {
    errors.push('apps/hvac-web/src/global.css :root[data-theme="light"] must declare color-scheme: light;');
  }
  const darkBlock = /(?:^|\n)\.dark\s*\{([^}]*)\}/.exec(globalCss)?.[1] ?? '';
  if (!/\bcolor-scheme\s*:\s*dark\s*;/.test(darkBlock)) {
    errors.push('apps/hvac-web/src/global.css .dark must declare color-scheme: dark;');
  }
}

function jsxOpeningTag(source: string, tagStartIndex: number): string {
  let braceDepth = 0;
  let quote: string | null = null;
  for (let index = tagStartIndex; index < source.length; index += 1) {
    const ch = source[index];
    if (quote) {
      if (ch === '\\') {
        index += 1;
        continue;
      }
      if (ch === quote) quote = null;
      continue;
    }
    if (ch === '"' || ch === "'" || ch === '`') {
      quote = ch;
      continue;
    }
    if (ch === '{') braceDepth += 1;
    else if (ch === '}' && braceDepth > 0) braceDepth -= 1;
    else if (ch === '>' && braceDepth === 0) return source.slice(tagStartIndex, index + 1);
  }
  return source.slice(tagStartIndex);
}

async function validateFactTileProps(root: string, errors: string[]): Promise<void> {
  const srcRoot = resolve(root, 'apps/hvac-web/src');
  for (const path of await collectFeatureSources(srcRoot)) {
    const source = await readFile(path, 'utf8');
    const relativePath = path.slice(root.length + 1).replaceAll('\\', '/');
    for (const match of source.matchAll(/<FactTile(?=\s|\/?>)/g)) {
      const openingTag = jsxOpeningTag(source, match.index);
      if (/\sclassName\s*=/.test(openingTag)) {
        const line = source.slice(0, match.index).split('\n').length;
        errors.push(`${relativePath}:${line} FactTile does not accept className; pass valueClassName for the <dd> value or wrap FactTile in <div className="col-span-..."> for grid layout`);
      }
    }
  }
}

interface WebAsCastBaseline {
  schemaVersion: number;
  policy: string;
  maxNonConstAsCasts: number;
}

const FORBIDDEN_COPY_RULES: ReadonlyArray<{ label: string; pattern: RegExp; replacement: string }> = [
  { label: '台账', pattern: /(?<!平)台账/u, replacement: '设备清单 / 安装清单' },
  { label: '核算', pattern: /核算/u, replacement: '统计 / 核对 / 计算' },
  { label: '测量边界', pattern: /测量边界/u, replacement: '冷站进线电表 / 独立电表' },
  { label: '分表', pattern: /分表/u, replacement: '设备电表' },
  { label: '总表', pattern: /(?<!整栋)总表/u, replacement: '整栋总表 / 总电表' },
  { label: '未分项', pattern: /未分项/u, replacement: '其他设备 / 其余设备' },
  { label: '基线检验', pattern: /基线检验/u, replacement: '对比结果' },
  { label: '回执', pattern: /(?<!返)回执/u, replacement: '执行结果' },
  { label: '待核对', pattern: /待核对/u, replacement: '待确认 / 还没收到' },
  { label: '工况', pattern: /工况/u, replacement: '运行条件 / 天气条件' },
  { label: '计收', pattern: /计收/u, replacement: '计费' },
  { label: '认领', pattern: /认领/u, replacement: '分配 / 由我负责' },
  { label: '区间未计入', pattern: /区间未计入/u, replacement: '当前区间不算在内' },
];

function lineOfNode(sourceFile: ts.SourceFile, node: ts.Node): number {
  return sourceFile.getLineAndCharacterOfPosition(node.getStart(sourceFile)).line + 1;
}

function isConstAsExpression(node: ts.AsExpression): boolean {
  return ts.isTypeReferenceNode(node.type)
    && ts.isIdentifier(node.type.typeName)
    && node.type.typeName.text === 'const';
}

function unwrapParentheses(expression: ts.Expression): ts.Expression {
  let current = expression;
  while (ts.isParenthesizedExpression(current)) {
    current = current.expression;
  }
  return current;
}

function isDirectFetchCall(expression: ts.LeftHandSideExpression): boolean {
  if (ts.isIdentifier(expression) && expression.text === 'fetch') return true;
  return ts.isPropertyAccessExpression(expression)
    && expression.name.text === 'fetch'
    && ts.isIdentifier(expression.expression)
    && (expression.expression.text === 'window' || expression.expression.text === 'globalThis');
}

function isCopyLiteralNode(node: ts.Node): node is ts.StringLiteral | ts.NoSubstitutionTemplateLiteral | ts.TemplateHead | ts.TemplateMiddle | ts.TemplateTail | ts.JsxText {
  if (!(ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node) || ts.isTemplateHead(node) || ts.isTemplateMiddle(node) || ts.isTemplateTail(node) || ts.isJsxText(node))) {
    return false;
  }
  const parent = node.parent;
  if (!parent) return true;
  if ((ts.isImportDeclaration(parent) || ts.isExportDeclaration(parent)) && parent.moduleSpecifier === node) return false;
  if (ts.isExternalModuleReference(parent) && parent.expression === node) return false;
  if (ts.isLiteralTypeNode(parent)) return false;
  return true;
}

function hasSecondaryButtonVariant(attributes: ts.JsxAttributes): boolean {
  for (const attribute of attributes.properties) {
    if (!ts.isJsxAttribute(attribute) || !ts.isIdentifier(attribute.name) || attribute.name.text !== 'variant') continue;
    const initializer = attribute.initializer;
    if (!initializer) continue;
    if (ts.isStringLiteral(initializer) && initializer.text === 'secondary') return true;
    if (ts.isJsxExpression(initializer) && initializer.expression && ts.isStringLiteral(initializer.expression) && initializer.expression.text === 'secondary') {
      return true;
    }
  }
  return false;
}

async function validateWebAstAndCopyGuardrails(root: string, errors: string[]): Promise<void> {
  const srcRoot = resolve(root, 'apps/hvac-web/src');
  if (!existsSync(srcRoot)) return;

  const sourcePaths = (await collectFeatureSources(srcRoot))
    .filter((path) => !path.endsWith('.gen.ts') && !path.endsWith('.d.ts'))
    .sort();
  let nonConstAsCastCount = 0;

  for (const path of sourcePaths) {
    const source = await readFile(path, 'utf8');
    const relativePath = path.slice(root.length + 1).replaceAll('\\', '/');
    const isFeatureOrRoute = relativePath.startsWith('apps/hvac-web/src/features/')
      || relativePath.startsWith('apps/hvac-web/src/routes/');
    const sourceFile = ts.createSourceFile(
      path,
      source,
      ts.ScriptTarget.Latest,
      true,
      path.endsWith('.tsx') ? ts.ScriptKind.TSX : ts.ScriptKind.TS,
    );

    const visit = (node: ts.Node): void => {
      if (ts.isAsExpression(node) && !isConstAsExpression(node)) {
        nonConstAsCastCount += 1;
        const inner = unwrapParentheses(node.expression);
        if (ts.isAsExpression(inner) && inner.type.kind === ts.SyntaxKind.UnknownKeyword) {
          errors.push(`${relativePath}:${lineOfNode(sourceFile, node)} avoid \`as unknown as\`; narrow with a type guard or use typed DTOs instead`);
        }
      }

      if (isFeatureOrRoute) {
        if (ts.isCallExpression(node) && isDirectFetchCall(node.expression)) {
          errors.push(`${relativePath}:${lineOfNode(sourceFile, node)} feature/route modules must use gatewayRequest instead of raw fetch()`);
        } else if (ts.isNewExpression(node) && ts.isIdentifier(node.expression) && node.expression.text === 'WebSocket') {
          errors.push(`${relativePath}:${lineOfNode(sourceFile, node)} feature/route modules must use src/platform/telemetry-live/ instead of new WebSocket()`);
        }
      }

      if ((ts.isJsxOpeningElement(node) || ts.isJsxSelfClosingElement(node))
        && ts.isIdentifier(node.tagName)
        && node.tagName.text === 'Button'
        && hasSecondaryButtonVariant(node.attributes)) {
        errors.push(`${relativePath}:${lineOfNode(sourceFile, node)} avoid <Button variant="secondary">; DESIGN.md uses default/outline/ghost/destructive variants`);
      }

      if (isCopyLiteralNode(node)) {
        const text = node.text;
        for (const rule of FORBIDDEN_COPY_RULES) {
          if (rule.pattern.test(text)) {
            errors.push(`${relativePath}:${lineOfNode(sourceFile, node)} UI copy uses deprecated term "${rule.label}"; use "${rule.replacement}" (DESIGN.md §1)`);
          }
        }
      }

      ts.forEachChild(node, visit);
    };

    visit(sourceFile);
  }

  const baselinePath = resolve(root, 'scripts/web-as-cast-baseline.json');
  if (!existsSync(baselinePath)) {
    errors.push('scripts/web-as-cast-baseline.json is missing; add the non-const `as` cast baseline file');
    return;
  }
  const baseline = JSON.parse(await readFile(baselinePath, 'utf8')) as WebAsCastBaseline;
  if (nonConstAsCastCount > baseline.maxNonConstAsCasts) {
    errors.push(`apps/hvac-web/src non-const \`as\` casts grew from ${baseline.maxNonConstAsCasts} to ${nonConstAsCastCount} (scripts/web-as-cast-baseline.json); narrow types or parse payloads instead of adding casts`);
  }
}

async function validateRouteTreeFreshness(root: string, errors: string[]): Promise<void> {
  const webRoot = resolve(root, 'apps/hvac-web');
  const routesDir = resolve(webRoot, 'src/routes');
  if (!existsSync(routesDir)) return;

  const targetRouteTree = resolve(webRoot, 'src/routeTree.gen.ts');
  if (!existsSync(targetRouteTree)) {
    errors.push('apps/hvac-web/src/routeTree.gen.ts is missing; run npm -w @hvac/web run build to regenerate it');
    return;
  }

  const config = getConfig({
    target: 'react',
    autoCodeSplitting: true,
    tmpDir: resolve(tmpdir(), 'hvac-tanstack-router-check'),
    disableLogging: true,
  }, webRoot);
  const virtualWrites = new Map<string, string>();
  let wroteRouteTree = false;
  let generatedContent: string | undefined;

  const generator = new Generator({
    config,
    root: webRoot,
    fs: {
      stat: async (filePath) => {
        if (virtualWrites.has(filePath)) {
          return { mtimeMs: 1n, mode: 0o100644, uid: 0, gid: 0 };
        }
        const entryStat = await stat(filePath, { bigint: true });
        return {
          mtimeMs: entryStat.mtimeMs,
          mode: Number(entryStat.mode),
          uid: Number(entryStat.uid),
          gid: Number(entryStat.gid),
        };
      },
      readFile: async (filePath) => {
        const cached = virtualWrites.get(filePath);
        if (cached !== undefined) {
          return { stat: { mtimeMs: 1n }, fileContent: cached };
        }
        if (!existsSync(filePath)) return 'file-not-existing';
        const entryStat = await stat(filePath, { bigint: true });
        const fileContent = (await readFile(filePath, 'utf8')).replaceAll('\r\n', '\n');
        return { stat: { mtimeMs: entryStat.mtimeMs }, fileContent };
      },
      writeFile: async (filePath, content) => {
        virtualWrites.set(filePath, content.replaceAll('\r\n', '\n'));
      },
      rename: async (oldPath, newPath) => {
        const content = virtualWrites.get(oldPath);
        if (content !== undefined) {
          virtualWrites.delete(oldPath);
          virtualWrites.set(newPath, content);
        }
        if (newPath === targetRouteTree) {
          wroteRouteTree = true;
          generatedContent = content;
        }
      },
      chmod: async () => {},
      chown: async () => {},
    },
  });

  await generator.run();
  const actualContent = (await readFile(targetRouteTree, 'utf8')).replaceAll('\r\n', '\n');
  if (wroteRouteTree && generatedContent !== actualContent) {
    errors.push('apps/hvac-web/src/routeTree.gen.ts is stale; run npm -w @hvac/web run build to regenerate it');
  }
}

function parseArgs(argv: readonly string[]): { root: string } {
  const rootArg = argv.find((argument) => argument.startsWith('--root='));
  return { root: rootArg ? resolve(rootArg.slice('--root='.length)) : defaultRoot };
}

export async function validateDesignSystem(root = defaultRoot): Promise<{ errors: string[] }> {
  const errors: string[] = [];
  await validateTableComponentUsage(root, errors);
  await validateContentGrammar(root, errors);
  await validateDataTableSurfaceGrammar(root, errors);
  await validateImeSafeEnterHandlers(root, errors);
  await validateFactTileProps(root, errors);
  await validateWebAstAndCopyGuardrails(root, errors);
  await validateRouteTreeFreshness(root, errors);

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
  const indexHtml = await readFile(resolve(root, 'apps/hvac-web/index.html'), 'utf8');
  const globalCss = await readFile(resolve(root, 'apps/hvac-web/src/global.css'), 'utf8');
  if (globalCss.includes('"shadcn/tailwind.css"')) {
    errors.push('apps/hvac-web/src/global.css must import ./styles/shadcn-tailwind.css; the shadcn package is not installed');
  }
  validateColorSchemeSetup(indexHtml, globalCss, errors);

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
