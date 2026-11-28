import { existsSync } from 'node:fs';
import { readFile, readdir, stat } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, relative, resolve } from 'node:path';
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

function repoRelative(root: string, filePath: string): string {
  return relative(root, filePath).replaceAll('\\', '/');
}

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
    const relativePath = repoRelative(root, path);

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
    const relativePath = repoRelative(root, path);

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
    const relativePath = repoRelative(root, path);
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
    const relativePath = repoRelative(root, path);
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
  const headBlock = /<head\b[^>]*>([\s\S]*?)<\/head>/i.exec(indexHtml)?.[1] ?? '';
  if (!/localStorage\.getItem\(\s*['"]hvac-theme-mode['"]\s*\)/.test(headBlock) || !/classList\.toggle\(\s*['"]dark['"]/.test(headBlock)) {
    errors.push('apps/hvac-web/index.html <head> must include the pre-paint hvac-theme-mode bootstrap script toggling the dark class to prevent theme FOUC');
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
    const relativePath = repoRelative(root, path);
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

interface WebFileLinesBaseline {
  readonly schemaVersion: number;
  readonly policy: string;
  readonly maxLinesPerFile: number;
  readonly legacyFileCaps: Readonly<Record<string, number>>;
}

const NUQS_ALLOWED_FILES = new Set([
  'apps/hvac-web/src/routes/__root.tsx',
  'apps/hvac-web/src/components/data-table/data-table-filter-list.tsx',
  'apps/hvac-web/src/lib/parsers.ts',
]);

export function countSourceLines(source: string): number {
  const normalized = source.replaceAll('\r\n', '\n');
  if (normalized.length === 0) return 0;
  const trimmed = normalized.endsWith('\n') ? normalized.slice(0, -1) : normalized;
  return trimmed.length === 0 ? 0 : trimmed.split('\n').length;
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
  { label: '时均', pattern: /时均/u, replacement: '每小时平均 / 平均' },
  { label: '每时用电', pattern: /每时用电/u, replacement: '每小时用电' },
  { label: '平均每时段', pattern: /平均每时段/u, replacement: '每小时平均' },
  { label: '分日明细', pattern: /分日明细/u, replacement: '每日明细' },
  { label: '退役空间', pattern: /退役空间/u, replacement: '停用空间 / 退役此空间' },
  { label: '退役点位', pattern: /退役点位/u, replacement: '停用点位 / 退役此点位' },
  { label: '空间登记资源', pattern: /空间登记资源/u, replacement: '空间' },
  { label: '设备资产', pattern: /设备资产/u, replacement: '设备' },
  { label: '泳道', pattern: /泳道/u, replacement: '设备启停状态' },
  { label: '燃尽', pattern: /燃尽/u, replacement: '累计进度' },
  { label: '包络曲面', pattern: /包络曲面/u, replacement: '不同负荷下的能效' },
  { label: '没有草稿的工单', pattern: /没有草稿的工单/u, replacement: '还没有草稿工单' },
  { label: '本期间', pattern: /本期间/u, replacement: '所选时段 / 这段时间' },
  { label: '通信端点', pattern: /通信端点/u, replacement: '具体电表或传感器名称' },
  { label: '告警分诊', pattern: /告警分诊/u, replacement: '告警处理' },
  { label: '诊断发现', pattern: /诊断发现/u, replacement: '诊断结论' },
  { label: '没人确认', pattern: /没人确认/u, replacement: '无人确认' },
  { label: '90% 把握', pattern: /90% 把握/u, replacement: '按 90% 置信区间估算' },
  { label: '未找到该 Alarm', pattern: /未找到该 Alarm/u, replacement: '找不到这条告警' },
  { label: 'Work Order 服务', pattern: /Work Order 服务/u, replacement: '工单服务' },
  { label: 'Registry ', pattern: /\bRegistry\s/u, replacement: '资产登记 / 登记信息' },
  { label: '不会用示例数据代替', pattern: /不会用示例数据代替/u, replacement: '空状态只说明现状，不写自证声明' },
  { label: '也不会用本地数据代替', pattern: /也不会用本地数据代替/u, replacement: '空状态只说明现状，不写自证声明' },
  { label: '没有浏览器侧虚构模板目录', pattern: /没有浏览器侧虚构模板目录/u, replacement: '空状态只说明现状，不写自证声明' },
  { label: '浏览器不会生成本地模拟站点', pattern: /浏览器不会生成本地模拟站点/u, replacement: '空状态只说明现状，不写自证声明' },
  { label: '页面不显示推测的数值', pattern: /页面不显示推测的数值/u, replacement: '空状态只说明现状，不写自证声明' },
  { label: '没有显示任何替代记录', pattern: /没有显示任何替代记录/u, replacement: '空状态只说明现状，不写自证声明' },
];

const FORBIDDEN_TEMPLATE_CONCAT_RULES: ReadonlyArray<{
  label: string;
  before?: string;
  after: string;
  suggestion: string;
}> = [
  { label: '${...}均', after: '均', suggestion: '按粒度字典声明完整标签（如每小时平均、日均用电）' },
  { label: '每${...}用电', before: '每', after: '用电', suggestion: '按粒度字典声明完整标签（如每小时用电、每日用电）' },
  { label: '分${...}明细', before: '分', after: '明细', suggestion: '按粒度字典声明完整标签（如逐时明细、每日明细）' },
  { label: '单${...}明细', before: '单', after: '明细', suggestion: '按粒度字典声明完整提示文案（如不摊入逐时明细）' },
  { label: '没有${...}的工单', before: '没有', after: '的工单', suggestion: '按状态字典声明完整空状态文案' },
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

function expressionCanYieldSecondary(expr: ts.Expression): boolean {
  if (ts.isStringLiteralLike(expr)) return expr.text === 'secondary';
  if (ts.isParenthesizedExpression(expr) || ts.isAsExpression(expr) || ts.isSatisfiesExpression(expr)) {
    return expressionCanYieldSecondary(expr.expression);
  }
  if (ts.isConditionalExpression(expr)) {
    return expressionCanYieldSecondary(expr.whenTrue) || expressionCanYieldSecondary(expr.whenFalse);
  }
  if (ts.isBinaryExpression(expr)) {
    const op = expr.operatorToken.kind;
    if (op === ts.SyntaxKind.BarBarToken || op === ts.SyntaxKind.QuestionQuestionToken) {
      return expressionCanYieldSecondary(expr.left) || expressionCanYieldSecondary(expr.right);
    }
    if (op === ts.SyntaxKind.AmpersandAmpersandToken) {
      return expressionCanYieldSecondary(expr.right);
    }
  }
  return false;
}

function hasSecondaryButtonVariant(attributes: ts.JsxAttributes): boolean {
  for (const attribute of attributes.properties) {
    if (!ts.isJsxAttribute(attribute) || !ts.isIdentifier(attribute.name) || attribute.name.text !== 'variant') continue;
    const init = attribute.initializer;
    if (!init) continue;
    if (ts.isStringLiteralLike(init) && init.text === 'secondary') return true;
    if (ts.isJsxExpression(init) && init.expression && expressionCanYieldSecondary(init.expression)) return true;
  }
  return false;
}

function disallowedConsoleMethod(expression: ts.LeftHandSideExpression): 'log' | 'debug' | 'info' | null {
  if (!ts.isPropertyAccessExpression(expression)) return null;
  const method = expression.name.text;
  if (method !== 'log' && method !== 'debug' && method !== 'info') return null;
  const target = expression.expression;
  if (ts.isIdentifier(target) && target.text === 'console') return method;
  if (ts.isPropertyAccessExpression(target)
    && target.name.text === 'console'
    && ts.isIdentifier(target.expression)
    && (target.expression.text === 'window' || target.expression.text === 'globalThis')) {
    return method;
  }
  return null;
}

function isMethodCallOnIdentifier(call: ts.CallExpression, objectName: string, methodName: string): boolean {
  return ts.isPropertyAccessExpression(call.expression)
    && ts.isIdentifier(call.expression.expression)
    && call.expression.expression.text === objectName
    && call.expression.name.text === methodName;
}

function isJsonRoundTripClone(node: ts.CallExpression): boolean {
  if (!isMethodCallOnIdentifier(node, 'JSON', 'parse') || node.arguments.length === 0) return false;
  const firstArg = unwrapParentheses(node.arguments[0]!);
  return ts.isCallExpression(firstArg) && isMethodCallOnIdentifier(firstArg, 'JSON', 'stringify');
}

const INFINITE_ANIMATIONS: ReadonlySet<string> = new Set([
  'animate-ping',
  'animate-pulse',
  'animate-bounce',
  'animate-spin',
]);

function isInsideTestIdAttribute(node: ts.Node): boolean {
  let current: ts.Node | undefined = node.parent;
  while (current) {
    if (ts.isJsxAttribute(current) && ts.isIdentifier(current.name)) {
      return current.name.text === 'data-testid' || current.name.text === 'testId';
    }
    if (ts.isJsxOpeningLikeElement(current) || ts.isJsxElement(current)) return false;
    current = current.parent;
  }
  return false;
}

function hasReducedMotionGuard(sourceFile: ts.SourceFile, node: ts.Node, text: string): boolean {
  if (/\bmotion-reduce:animate-none\b/.test(text)) return true;
  let current: ts.Node | undefined = node.parent;
  while (current) {
    if (ts.isJsxAttribute(current)) {
      return /\bmotion-reduce:animate-none\b/.test(current.getText(sourceFile));
    }
    if (ts.isCallExpression(current) && ts.isIdentifier(current.expression) && current.expression.text === 'cn') {
      if (/\bmotion-reduce:animate-none\b/.test(current.getText(sourceFile))) return true;
    }
    if (ts.isBlock(current) || ts.isSourceFile(current) || ts.isFunctionLike(current)) break;
    current = current.parent;
  }
  return false;
}

function effectHookWithoutDeps(call: ts.CallExpression): string | null {
  if (call.arguments.length >= 2) return null;
  const expr = call.expression;
  if (ts.isIdentifier(expr) && (expr.text === 'useEffect' || expr.text === 'useLayoutEffect')) {
    return expr.text;
  }
  if (ts.isPropertyAccessExpression(expr)
    && ts.isIdentifier(expr.expression)
    && expr.expression.text === 'React'
    && (expr.name.text === 'useEffect' || expr.name.text === 'useLayoutEffect')) {
    return expr.name.text;
  }
  return null;
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
    const relativePath = repoRelative(root, path);
    const isFeatureOrRoute = relativePath.startsWith('apps/hvac-web/src/features/')
      || relativePath.startsWith('apps/hvac-web/src/routes/');
    const isUiPrimitive = relativePath.startsWith('apps/hvac-web/src/components/ui/');
    const sourceFile = ts.createSourceFile(
      path,
      source,
      ts.ScriptTarget.Latest,
      true,
      path.endsWith('.tsx') ? ts.ScriptKind.TSX : ts.ScriptKind.TS,
    );

    if (!isUiPrimitive) {
      for (const statement of sourceFile.statements) {
        if (ts.isExpressionStatement(statement)
          && ts.isStringLiteralLike(statement.expression)
          && statement.expression.text === 'use client') {
          errors.push(`${relativePath}:${lineOfNode(sourceFile, statement)} avoid Next.js "use client" directives in Vite SPA modules; remove it`);
        }
      }
    }

    if (!NUQS_ALLOWED_FILES.has(relativePath)) {
      for (const statement of sourceFile.statements) {
        if ((ts.isImportDeclaration(statement) || ts.isExportDeclaration(statement))
          && statement.moduleSpecifier
          && ts.isStringLiteralLike(statement.moduleSpecifier)) {
          const specifier = statement.moduleSpecifier.text;
          if (specifier === 'nuqs' || specifier.startsWith('nuqs/')) {
            errors.push(
              `${relativePath}:${lineOfNode(sourceFile, statement)} avoid \`nuqs\` outside DataTable filter internals; declare route search params in TanStack Router validateSearch instead`,
            );
          }
        }
      }
    }

    const seenCommentStarts = new Set<number>();
    const checkCommentsAt = (ranges: readonly ts.CommentRange[] | undefined): void => {
      if (!ranges) return;
      for (const range of ranges) {
        if (seenCommentStarts.has(range.pos)) continue;
        seenCommentStarts.add(range.pos);
        const commentText = source.slice(range.pos, range.end);
        if (/(?:@ts-ignore|@ts-nocheck|eslint-disable)\b/.test(commentText)) {
          const line = sourceFile.getLineAndCharacterOfPosition(range.pos).line + 1;
          errors.push(`${relativePath}:${line} avoid @ts-ignore, @ts-nocheck, and eslint-disable comments; fix the type or guardrail directly`);
        }
      }
    };

    const visit = (node: ts.Node): void => {
      checkCommentsAt(ts.getLeadingCommentRanges(source, node.getFullStart()));
      checkCommentsAt(ts.getTrailingCommentRanges(source, node.getEnd()));

      if (ts.isAsExpression(node) && !isConstAsExpression(node)) {
        nonConstAsCastCount += 1;
        const inner = unwrapParentheses(node.expression);
        if (ts.isAsExpression(inner) && inner.type.kind === ts.SyntaxKind.UnknownKeyword) {
          errors.push(`${relativePath}:${lineOfNode(sourceFile, node)} avoid \`as unknown as\`; narrow with a type guard or use typed DTOs instead`);
        }
      }

      if (ts.isCallExpression(node)) {
        const consoleMethod = disallowedConsoleMethod(node.expression);
        if (consoleMethod) {
          errors.push(`${relativePath}:${lineOfNode(sourceFile, node)} avoid console.${consoleMethod}() in web source; remove debug logging or surface errors explicitly`);
        }
        if (isJsonRoundTripClone(node)) {
          errors.push(`${relativePath}:${lineOfNode(sourceFile, node)} avoid JSON.parse(JSON.stringify(...)); use structuredClone(...) instead`);
        }
        const hookName = effectHookWithoutDeps(node);
        if (hookName) {
          errors.push(`${relativePath}:${lineOfNode(sourceFile, node)} ${hookName}() must specify an explicit dependency array (\`[]\` or \`[deps]\`) to avoid re-running subscriptions/effects on every render`);
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

      if (ts.isTemplateExpression(node)) {
        for (let i = 0; i < node.templateSpans.length; i += 1) {
          const prevText = i === 0 ? node.head.text : node.templateSpans[i - 1].literal.text;
          const nextText = node.templateSpans[i].literal.text;
          for (const rule of FORBIDDEN_TEMPLATE_CONCAT_RULES) {
            if ((!rule.before || prevText.endsWith(rule.before)) && nextText.startsWith(rule.after)) {
              errors.push(`${relativePath}:${lineOfNode(sourceFile, node)} UI copy uses template concatenation "${rule.label}"; ${rule.suggestion} (DESIGN.md §1)`);
            }
          }
        }
      }

      if (isCopyLiteralNode(node)) {
        const text = node.text;
        for (const rule of FORBIDDEN_COPY_RULES) {
          if (rule.pattern.test(text)) {
            errors.push(`${relativePath}:${lineOfNode(sourceFile, node)} UI copy uses deprecated term "${rule.label}"; use "${rule.replacement}" (DESIGN.md §1)`);
          }
        }
        if (text.trim() === '--' || /(?:^|\s)--(?:\s|$)/.test(text)) {
          errors.push(`${relativePath}:${lineOfNode(sourceFile, node)} UI placeholder uses ASCII \`--\`; use Chinese em dash \`—\` instead`);
        }
        if (!ts.isJsxText(node)) {
          if (/\breal-shell-[a-z0-9-]+\b/.test(text) && !isInsideTestIdAttribute(node)) {
            errors.push(`${relativePath}:${lineOfNode(sourceFile, node)} avoid dead legacy CSS class \`real-shell-*\`; use semantic Tailwind design tokens (data-testid / testId attributes remain allowed)`);
          }
          if (!isUiPrimitive && /(?:^|[\s:])transition-all(?=$|\s)/.test(text)) {
            errors.push(`${relativePath}:${lineOfNode(sourceFile, node)} avoid \`transition-all\`; transition only specific properties (e.g. \`transition-colors\`, \`transition-opacity\`, or \`transition-[border-color,box-shadow]\`)`);
          }
          const viewportHeightCalcMatch = /calc\(\s*100[sdl]?vh[\s_]*-[\s_]*\d+(?:\.\d+)?(?:px|rem)\s*\)/i.exec(text);
          if (viewportHeightCalcMatch) {
            errors.push(`${relativePath}:${lineOfNode(sourceFile, node)} avoid hardcoded viewport height subtraction \`${viewportHeightCalcMatch[0]}\`; use \`<Main fixed>\` flex sizing (\`flex-1 min-h-0\`) or \`var(--header-height)\` instead`);
          }
          if (!hasReducedMotionGuard(sourceFile, node, text)) {
            const flaggedAnimations = new Set<string>();
            for (const token of text.trim().split(/\s+/)) {
              const parts = token.split(':');
              const base = parts[parts.length - 1] ?? '';
              const variants = parts.slice(0, -1);
              if (base && INFINITE_ANIMATIONS.has(base) && !variants.includes('motion-safe') && !flaggedAnimations.has(base)) {
                flaggedAnimations.add(base);
                errors.push(`${relativePath}:${lineOfNode(sourceFile, node)} infinite animation \`${base}\` must respect prefers-reduced-motion; use \`motion-safe:${base}\` (or pair with \`motion-reduce:animate-none\`)`);
              }
            }
          }
        }
      }

      ts.forEachChild(node, visit);
    };

    checkCommentsAt(ts.getLeadingCommentRanges(source, sourceFile.endOfFileToken.getFullStart()));
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
  } else if (nonConstAsCastCount < baseline.maxNonConstAsCasts) {
    errors.push(`apps/hvac-web/src non-const \`as\` casts dropped from ${baseline.maxNonConstAsCasts} to ${nonConstAsCastCount}; tighten scripts/web-as-cast-baseline.json`);
  }
}

async function validateWebFileLinesRatchet(root: string, errors: string[]): Promise<void> {
  const srcRoot = resolve(root, 'apps/hvac-web/src');
  if (!existsSync(srcRoot)) return;

  const baselinePath = resolve(root, 'scripts/web-file-lines-baseline.json');
  if (!existsSync(baselinePath)) {
    errors.push('scripts/web-file-lines-baseline.json is missing; add the frontend file-line baseline file');
    return;
  }

  const baseline = JSON.parse(await readFile(baselinePath, 'utf8')) as WebFileLinesBaseline;
  const sourcePaths = (await collectFeatureSources(srcRoot))
    .filter((path) => !path.endsWith('.gen.ts') && !path.endsWith('.d.ts'))
    .sort();
  const lineCounts = new Map<string, number>();

  for (const path of sourcePaths) {
    const relativePath = repoRelative(root, path);
    if (relativePath.startsWith('apps/hvac-web/src/components/ui/')) continue;
    const source = await readFile(path, 'utf8');
    const lines = countSourceLines(source);
    lineCounts.set(relativePath, lines);

    const cap = baseline.legacyFileCaps[relativePath];
    if (cap === undefined) {
      if (lines > baseline.maxLinesPerFile) {
        errors.push(`${relativePath} has ${lines} lines (limit ${baseline.maxLinesPerFile}); split it into cohesive modules instead of growing oversized files`);
      }
    } else if (cap <= baseline.maxLinesPerFile) {
      errors.push(`scripts/web-file-lines-baseline.json: stale cap ${cap} for ${relativePath} (already <= ${baseline.maxLinesPerFile}); remove it`);
    } else if (lines > cap) {
      errors.push(`${relativePath} grew from ${cap} to ${lines} lines (scripts/web-file-lines-baseline.json); split it instead of expanding legacy oversized files`);
    } else if (lines <= baseline.maxLinesPerFile) {
      errors.push(`scripts/web-file-lines-baseline.json: stale entry for ${relativePath} (${lines} <= ${baseline.maxLinesPerFile}); remove it`);
    }
  }

  for (const legacyPath of Object.keys(baseline.legacyFileCaps)) {
    if (!lineCounts.has(legacyPath)) {
      errors.push(`scripts/web-file-lines-baseline.json: stale entry for missing/exempt file ${legacyPath}; remove it`);
    }
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

export async function validateDesignSystem(rawRoot = defaultRoot): Promise<{ errors: string[] }> {
  const root = resolve(rawRoot);
  const errors: string[] = [];
  await validateTableComponentUsage(root, errors);
  await validateContentGrammar(root, errors);
  await validateDataTableSurfaceGrammar(root, errors);
  await validateImeSafeEnterHandlers(root, errors);
  await validateFactTileProps(root, errors);
  await validateWebAstAndCopyGuardrails(root, errors);
  await validateWebFileLinesRatchet(root, errors);
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
