import { readFile, readdir } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const scriptPath = fileURLToPath(import.meta.url);
const defaultRoot = resolve(dirname(scriptPath), '..');

const requiredFiles = [
  'DESIGN.md',
  'PRODUCT.md',
  'docs/design-system/shadcn-redesign-2026-09-13.md',
  'docs/design-system/hvac-application-ui-specification.md',
  'docs/design-system/shadcn-component-contract.md',
  'docs/design-system/legacy-ui-quarantine.md',
  'docs/architecture/shadcn-tablecn-reui-source-review-2026-09-20.md',
  'docs/architecture/kibo-diceui-advanced-components-source-review-2026-09-20.md',
  'apps/hvac-web/components.json',
  'apps/hvac-web/src/blocks/README.md',
  'apps/hvac-web/src/blocks/data-table/data-table-block.tsx',
  'docs/architecture/smart-energy-react-spa-frontend-architecture.md',
  'docs/architecture/smart-energy-react-spa-frontend-skeleton-review.md',
  'docs/architecture/shadcn-ui-shadcn-admin-source-review.md',
  'docs/reference/shadcn-admin与shadcn-ui在智慧能源React-SPA中的应用方案.md',
  'docs/product/README.md',
  'docs/product/content-design.md',
  'docs/product/smart-energy-system-page-architecture-v3-research-backed.md',
  'docs/product/global-navigation-context-interaction-contract-v2.md',
  'apps/hvac-web/src/app/workspace-catalog.ts',
  'docs/product/surface-catalog-final-review-2026-09-15.md',
  'docs/product/surface-specifications/01-enterprise-overview.md',
  'docs/product/surface-specifications/02-portfolio-benchmarking.md',
  'docs/product/surface-specifications/03-site-overview.md',
  'docs/product/surface-specifications/04-system-operations.md',
  'docs/product/surface-specifications/05-trend-analysis.md',
  'docs/product/surface-specifications/06-device-center.md',
  'docs/product/surface-specifications/07-device-detail.md',
  'docs/product/surface-specifications/08-comfort-ieq.md',
  'docs/product/surface-specifications/09-alarm-center.md',
  'docs/product/surface-specifications/10-diagnosis-center.md',
  'docs/product/surface-specifications/11-work-order-center.md',
  'docs/product/surface-specifications/12-work-order-detail.md',
  'docs/product/surface-specifications/13-functional-verification.md',
  'docs/product/surface-specifications/14-energy-analysis.md',
  'docs/product/surface-specifications/15-demand-load-flexibility.md',
  'docs/product/surface-specifications/16-efficiency-analysis.md',
  'docs/product/surface-specifications/17-energy-review.md',
  'docs/product/surface-specifications/18-billing-cost-tariff.md',
  'docs/product/surface-specifications/19-carbon-emissions.md',
  'docs/product/surface-specifications/20-distributed-energy-flexibility.md',
  'docs/product/surface-specifications/21-energy-opportunities.md',
  'docs/product/surface-specifications/22-optimization-plan.md',
  'docs/product/surface-specifications/23-objectives-action-plans.md',
  'docs/product/surface-specifications/24-measurement-verification.md',
  'docs/product/surface-specifications/25-control-center.md',
  'docs/product/surface-specifications/26-strategy-center.md',
  'docs/product/surface-specifications/27-strategy-detail.md',
  'docs/product/surface-specifications/28-execution-record.md',
  'docs/product/surface-specifications/29-report-center.md',
  'docs/product/surface-specifications/30-management-review.md',
  'docs/product/surface-specifications/31-data-quality.md',
  'docs/product/surface-specifications/32-metering-semantic-model.md',
  'docs/product/surface-specifications/33-rules-notifications.md',
  'docs/product/surface-specifications/34-integration-management.md',
  'docs/product/surface-specifications/35-site-system-configuration.md',
  'docs/product/surface-specifications/36-users-access-audit.md',
  'docs/product/smart-energy-system-page-architecture-audit-v1.md',
  'docs/product/smart-energy-system-page-architecture-v1.md',
  'docs/product/product-information-architecture-v2.md',
  '.agents/skills/impeccable/SKILL.md',
  '.agents/skills/frontend-design/SKILL.md',
  '.agents/skills/web-design-guidelines/SKILL.md',
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

function frontmatterOf(markdown) {
  if (!markdown.startsWith('---\n')) return null;
  const end = markdown.indexOf('\n---\n', 4);
  return end === -1 ? null : markdown.slice(4, end);
}

function requireText(errors, source, label, expected) {
  if (!source.includes(expected)) errors.push(`${label} must contain ${JSON.stringify(expected)}`);
}

function rejectText(errors, source, label, rejected) {
  if (source.includes(rejected)) errors.push(`${label} must not contain legacy authority ${JSON.stringify(rejected)}`);
}

function parseArgs(argv) {
  const rootArg = argv.find((argument) => argument.startsWith('--root='));
  return { root: rootArg ? resolve(rootArg.slice('--root='.length)) : defaultRoot };
}

export async function validateDesignSystem(root = defaultRoot) {
  const errors = [];
  const sources = Object.fromEntries(
    await Promise.all(requiredFiles.map(async (path) => [path, await readRequired(root, path)])),
  );
  await validateTableComponentUsage(root, errors);
  await validateContentGrammar(root, errors);
  await validateDataTableSurfaceGrammar(root, errors);

  const design = sources['DESIGN.md'];
  const frontmatter = frontmatterOf(design);
  if (!frontmatter) {
    errors.push('DESIGN.md must begin with valid YAML frontmatter');
  } else {
    for (const expected of [
      'version: shadcn-app-v1',
      'name: 泉来禾智慧能源平台',
      'designStatus: selected',
      'selectedDirection: shadcn-application',
      'scope: apps/hvac-web',
      'directionDecision: docs/design-system/shadcn-redesign-2026-09-13.md',
      'architecture: docs/architecture/smart-energy-react-spa-frontend-architecture.md',
    ]) {
      requireText(errors, frontmatter, 'DESIGN.md frontmatter', expected);
    }
  }

  for (const expected of [
    '# 泉来禾智慧能源平台 DESIGN.md',
    'Shadcn Application System',
    'shadcn-admin 是当前主要应用布局与后台产品参考',
    'Application Shell',
    'docs/product/smart-energy-system-page-architecture-v3-research-backed.md',
    'docs/product/global-navigation-context-interaction-contract-v2.md',
    '当前实现和历史参考图不在视觉权威链中',
    'docs/design-system/legacy-ui-quarantine.md',
    'Tailwind CSS v4',
    'shadcn/ui',
    'TanStack Router',
    'TanStack Query',
    'TanStack Table',
    'React Hook Form',
    'Recharts through shadcn Chart for ordinary application charts',
    'Apache ECharts',
    'sadmann7/tablecn',
    'ReUI',
    'Kibo UI',
    'Dice UI',
    'AntV X6',
    'AntV G6',
    '面向中文用户的业务界面默认 **中文优先**',
    '中文负责可理解性，标准缩写负责专业精度',
    '用户可理解性 / Content Design',
    '事实 + 原因 + 下一步',
    '状态使用文字 + 图标/形状/标签表达',
    'WCAG 2.2 AA',
    '用户友好 = 更快理解正确事实并知道下一步',
    '旧参考资料的地位',
    '不建立 Ant Design 兼容层',
    '不建立旧 Control Desk 视觉兼容层',
  ]) {
    requireText(errors, design, 'DESIGN.md', expected);
  }

  for (const rejected of [
    'version: control-desk-v1',
    'selectedDirection: control-desk',
    'Control Desk 全局语法',
    'Visual System v1',
    '统一使用 `ProLayout`',
    '图表统一使用 `@ant-design/charts`',
    '直接使用官方 ProComponents → 基于官方组件做薄封装',
    'ProComponents 官方文档是组件选型与实现的第一参考',
  ]) {
    rejectText(errors, design, 'DESIGN.md', rejected);
  }

  const decision = sources['docs/design-system/shadcn-redesign-2026-09-13.md'];
  for (const expected of [
    'SELECTED / SHADCN APPLICATION SYSTEM',
    'satnaing/shadcn-admin',
    'shadcn/ui current component language',
    'direct redesign',
    'Data Table First',
    'WSL/Windows Chrome',
    'Superseded authority',
  ]) {
    requireText(errors, decision, 'shadcn redesign decision', expected);
  }

  const applicationUiSpec = sources['docs/design-system/hvac-application-ui-specification.md'];
  for (const expected of [
    'SELECTED / ACTIVE',
    'Breadcrumb',
    'Page Header',
    'Surface archetypes',
    'tablecn',
    'ReUI',
    'Kibo UI',
    'Dice UI',
    'Advanced application components',
    'Industrial data truthfulness',
    'Completed ≠ Verified',
    'shadcn Chart + Recharts',
    'Apache ECharts',
  ]) {
    requireText(errors, applicationUiSpec, 'HVAC application UI specification', expected);
  }

  const componentContract = sources['docs/design-system/shadcn-component-contract.md'];
  for (const expected of [
    'ACTIVE / UI COMPONENT AUTHORITY',
    '`radix-nova`',
    'TanStack Table v9',
    'tablecn interaction/composition grammar',
    'ReUI Data Grid',
    'Kibo UI',
    'Dice UI',
    'Advanced application component layer',
    'Field and forms',
    'Input Group and search',
    'Dialog, Alert Dialog, Sheet and contextual inspector',
    'do not mix Base UI `render` composition into Radix components',
    'unified `radix-ui` package',
  ]) {
    requireText(errors, componentContract, 'shadcn component contract', expected);
  }

  const legacyUiQuarantine = sources['docs/design-system/legacy-ui-quarantine.md'];
  for (const expected of [
    'ACTIVE GUARDRAIL',
    'Historical implementations, screenshots, archived source reviews and migration-only runtime code are **not** design authority.',
    'do not use repository-wide search hits from quarantined paths as visual evidence',
    'never introduce an Ant↔shadcn compatibility wrapper',
  ]) {
    requireText(errors, legacyUiQuarantine, 'legacy UI quarantine', expected);
  }

  const componentsJson = sources['apps/hvac-web/components.json'];
  requireText(errors, componentsJson, 'shadcn components.json', '"style": "radix-nova"');
  requireText(errors, componentsJson, 'shadcn components.json', '"iconLibrary": "lucide"');

  const product = sources['PRODUCT.md'];
  for (const expected of [
    '`PRODUCT.md` 只记录稳定的产品事实',
    '不是营销网站',
    '未知 / 不可用 / 未接入 / 缺失',
    'Offline 不自动等于 Fault',
    '控制安全',
    '与设计文档的边界',
  ]) {
    requireText(errors, product, 'PRODUCT.md', expected);
  }

  const productBlueprint = sources['docs/product/smart-energy-system-page-architecture-v3-research-backed.md'];
  for (const expected of [
    'SELECTED / RESEARCH-BACKED WORKSPACE BLUEPRINT',
    '成熟 BMS / EMIS / FDD / FM',
    'Siemens Building X Operations Manager',
    'Johnson Controls Metasys UI',
    'Clockworks Analytics',
    'Desktop split inspector',
    '最终一级导航建议',
    '共 10 个一级入口',
    'Advanced Trend Studio',
    'REMOVE_PRIMARY_ROUTE',
    'SETTINGS_CHILD',
  ]) {
    requireText(errors, productBlueprint, 'Smart Energy product blueprint v3', expected);
  }

  const interactionContract = sources['docs/product/global-navigation-context-interaction-contract-v2.md'];
  for (const expected of [
    'SELECTED / PRODUCT INTERACTION AUTHORITY',
    'Capability',
    'Workspace View',
    'Primary Workspaces only',
    'Operational Ledger contract',
    'Desktop List → Detail contract',
    'Narrow List → Detail contract',
    'Dialog contract',
    'Durable Detail Route contract',
    'Contextual Control contract',
    'ResizablePanelGroup',
    'DataTableBlock',
    'tablecn',
    'ReUI',
    'Kibo UI',
    'Dice UI',
    'Shadcnblocks',
    'Sheet is therefore the **responsive representation of the Context Inspector**',
    'Do not create compatibility navigation',
    'real browser review validates desktop and narrow layouts',
  ]) {
    requireText(errors, interactionContract, 'Global navigation/context interaction contract v2', expected);
  }

  const finalSurfaceReview = sources['docs/product/surface-catalog-final-review-2026-09-15.md'];
  for (const expected of [
    'SUPERSEDED / HISTORICAL 36-SURFACE REVIEW',
    'smart-energy-system-page-architecture-v3-research-backed.md',
    'global-navigation-context-interaction-contract-v2.md',
    '36 个 Surface 能力目录',
  ]) {
    requireText(errors, finalSurfaceReview, 'Historical Surface Catalog review', expected);
  }

  const enterpriseOverviewSpec = sources['docs/product/surface-specifications/01-enterprise-overview.md'];
  for (const expected of [
    'SELECTED / READY FOR WIREFRAME',
    'Primary Job',
    'portfolio attention router',
    'Primary Questions',
    'Benchmark Signal ≠ Root Cause',
    'Expected Savings ≠ Verified Savings',
    'User-friendly Content Contract',
    '10-second comprehension',
    'Browser Acceptance Criteria',
    'No Defensive Programming / No Compatibility Design',
  ]) {
    requireText(errors, enterpriseOverviewSpec, 'Enterprise Overview Surface Specification', expected);
  }

  const portfolioBenchmarkingSpec = sources['docs/product/surface-specifications/02-portfolio-benchmarking.md'];
  for (const expected of [
    'SELECTED / READY FOR WIREFRAME',
    'Primary Job',
    'Peer Group Contract',
    'Ranking Contract',
    'Ranking ≠ Root Cause',
    'Peer Median ≠ Target',
    'Comparable State',
    'User-friendly Content Contract',
    '10-second comprehension',
    'Browser Acceptance Criteria',
    'No Defensive Programming / No Compatibility Design',
  ]) {
    requireText(errors, portfolioBenchmarkingSpec, 'Portfolio Benchmarking Surface Specification', expected);
  }

  const siteOverviewSpec = sources['docs/product/surface-specifications/03-site-overview.md'];
  for (const expected of [
    'SELECTED / READY FOR WIREFRAME',
    'Route intent',
    'Primary Job',
    'Primary Questions',
    'Entry Points',
    'Exit Paths',
    'Route / URL State Ownership',
    'Priority Attention',
    'Data Authority Contract',
    'No Defensive Programming / No Compatibility Design',
    'Component Mapping',
    'Accessibility',
    'Browser Acceptance Criteria',
    'Wireframe Gate',
    'U.S. DOE FEMP',
    'ISA-101',
    'ASHRAE Guideline 36',
  ]) {
    requireText(errors, siteOverviewSpec, 'Site Overview Surface Specification', expected);
  }

  const systemOperationsSpec = sources['docs/product/surface-specifications/04-system-operations.md'];
  for (const expected of [
    'SELECTED / READY FOR WIREFRAME',
    'Primary Job',
    'Primary Questions',
    'Screen Hierarchy',
    'Primary Operating Workspace',
    'Topology Visual Contract',
    'Context Inspector',
    'Sequence / Stage / Setpoint Contract',
    'Control Boundary',
    'Data Authority Contract',
    'State Semantics',
    'No Defensive Programming / No Compatibility Design',
    'Realtime Contract',
    'Accessibility',
    'Browser Acceptance Criteria',
    'Wireframe Gate',
    'ASHRAE Guideline 36',
    'ISA-101',
    'ISA-18',
    'OpenBuildingControl',
  ]) {
    requireText(errors, systemOperationsSpec, 'System Operations Surface Specification', expected);
  }

  const trendAnalysisSpec = sources['docs/product/surface-specifications/05-trend-analysis.md'];
  for (const expected of [
    'SELECTED / READY FOR WIREFRAME',
    'Primary Job',
    'Primary Questions',
    'Route 与 URL State',
    'Series Selection',
    'Dual Y-Axis Policy',
    'Signal Type Rendering',
    'Sampling / Aggregation / Downsampling',
    'Missing / Stale / Bad Quality',
    'Event / Evidence Lanes',
    'No Defensive Programming / No Compatibility Architecture',
    'Realtime Contract',
    'Accessibility',
    'Browser Acceptance Criteria',
    'READY FOR WIREFRAME Decision',
    'DOE / FEMP EMIS',
    'ASHRAE BACnet / Trend Log',
    'UK ONS / Government Analysis Function',
  ]) {
    requireText(errors, trendAnalysisSpec, 'Trend Analysis Surface Specification', expected);
  }

  const deviceCenterSpec = sources['docs/product/surface-specifications/06-device-center.md'];
  for (const expected of [
    'SELECTED / READY FOR WIREFRAME',
    'Primary Job',
    'Primary Questions',
    'Route / URL State Ownership',
    'Default View: Ledger / Table First',
    'Independent State Semantics',
    'Context Inspector',
    'Device Detail Boundary',
    'Data Authority Contract',
    'Query / Read Model Contract',
    'Snapshot / Stream Realtime Contract',
    'No Defensive Programming / No Compatibility Design',
    'Accessibility',
    'Browser Acceptance Criteria',
    'READY FOR WIREFRAME Decision',
    'ISO 55000 / ISO 55001',
    'NIST SP 800-82 Rev. 3',
    'ASHRAE Standard 223P',
    'DOE Semantic Modeling and Interoperability',
    'ASHRAE BACnet',
    'W3C',
  ]) {
    requireText(errors, deviceCenterSpec, 'Device Center Surface Specification', expected);
  }

  const deviceDetailSpec = sources['docs/product/surface-specifications/07-device-detail.md'];
  for (const expected of [
    'SELECTED / READY FOR WIREFRAME',
    'Primary Job',
    'Primary Questions',
    'Page Information Architecture',
    'Independent State Strip',
    'Current Operation',
    'Control Boundary',
    'Current Attention',
    'Recent Evidence',
    'Relationships',
    'Engineering Points',
    'Writable Point != Authorized Action',
    'Data Authority Contract',
    'Query / Read Model Contract',
    'Snapshot / Stream Realtime Contract',
    'Empty / Not Found / Unauthorized / Unavailable',
    'No Defensive Programming / No Compatibility Design',
    'Accessibility',
    'Browser Acceptance Criteria',
    'READY FOR WIREFRAME Decision',
    'ISO 55001:2024',
    'NIST SP 800-82 Rev. 3',
    'DOE Semantic Modeling / ASHRAE 223',
    'BACnet',
    'DOE OpenBuildingControl',
    'W3C APG',
  ]) {
    requireText(errors, deviceDetailSpec, 'Device Detail Surface Specification', expected);
  }

  const comfortIeqSpec = sources['docs/product/surface-specifications/08-comfort-ieq.md'];
  for (const expected of [
    'SELECTED / READY FOR WIREFRAME',
    'Primary Job',
    'Primary Questions',
    'Capability Gating',
    'Target Semantics',
    'Thermal Comfort Contract',
    'IAQ Contract',
    'CO₂ Contract',
    'Occupancy Contract',
    'Violation Semantics',
    'Zone Ledger',
    'Spatial / Floor View Policy',
    'Selected Zone Inspector',
    'Evidence Workspace',
    'HVAC Contribution / Serving-System Contract',
    'Data Authority Contract',
    'Query / Read Model Contract',
    'Snapshot / Stream Realtime Contract',
    'No Defensive Programming / No Compatibility Design',
    'Accessibility',
    'Browser Acceptance Criteria',
    'READY FOR WIREFRAME Decision',
    'ASHRAE Standard 55-2023',
    'ASHRAE Standard 62.1-2025',
    'ASHRAE Indoor CO₂ Position Document 2025',
    'U.S. EPA',
    'DOE Building Controls',
  ]) {
    requireText(errors, comfortIeqSpec, 'Comfort / IEQ Surface Specification', expected);
  }

  const alarmCenterSpec = sources['docs/product/surface-specifications/09-alarm-center.md'];
  for (const expected of [
    'SELECTED / READY FOR WIREFRAME',
    'Primary Job',
    'Alarm Domain Vocabulary',
    'Mandatory Semantics',
    'Primary Questions',
    'active-first operator triage workspace',
    'Shelving Contract',
    'Suppressed by Design Contract',
    'Out of Service Contract',
    'Selected Alarm Inspector',
    'Alarm Flood Contract',
    'Alarm Grouping / Correlation',
    'Alarm → Diagnosis Contract',
    'Alarm → Work Contract',
    'Performance View',
    'Rule Administration Boundary',
    'Data Authority Contract',
    'Query / Read Model Contract',
    'Snapshot / Stream Realtime Contract',
    'Mutation Contract',
    'No Defensive Programming / No Compatibility Design',
    'Accessibility',
    'Browser Acceptance Criteria',
    'READY FOR WIREFRAME Decision',
    'ANSI/ISA-18.2',
    'IEC 62682:2022',
    'ISA-101',
    'EEMUA 191 Edition 4',
    'ISA-TR18.2.5',
  ]) {
    requireText(errors, alarmCenterSpec, 'Alarm Center Surface Specification', expected);
  }

  const diagnosisCenterSpec = sources['docs/product/surface-specifications/10-diagnosis-center.md'];
  for (const expected of [
    'SELECTED / READY FOR WIREFRAME',
    'Primary Job',
    'Diagnostic Vocabulary',
    'Mandatory Semantic Separation',
    'Primary Questions',
    'Investigation Queue',
    'Verified Facts',
    'Published Finding',
    'Hypothesis Contract',
    'Supporting / Contradicting Evidence',
    'Confidence Semantics',
    'Applicability / Operating Context',
    'Evidence Window Contract',
    'Sequence / Control Evidence',
    'Next Verification',
    'Root Cause Confirmation Contract',
    'Diagnosis → Work Contract',
    'Diagnosis → Functional Verification',
    'Rule / Model Metadata Boundary',
    'Data Authority Contract',
    'Query / Read Model Contract',
    'AI / Copilot Contract',
    'No Defensive Programming / No Compatibility Design',
    'Accessibility',
    'Browser Acceptance Criteria',
    'READY FOR WIREFRAME Decision',
    'DOE/FEMP',
    'NIST FDD',
    'ASHRAE Guideline 36-2024',
    'NIST AI RMF',
  ]) {
    requireText(errors, diagnosisCenterSpec, 'Diagnosis Center Surface Specification', expected);
  }

  const workOrderCenterSpec = sources['docs/product/surface-specifications/11-work-order-center.md'];
  for (const expected of [
    'SELECTED / READY FOR WIREFRAME',
    'Primary Job',
    'Work Domain Vocabulary',
    'Mandatory Semantic Separation',
    'Primary Questions',
    'Work Type Contract',
    'Work Priority Contract',
    'Lifecycle State Contract',
    'Planning / Readiness Contract',
    'Owner / Assignee Contract',
    'SLA / Due Contract',
    'Work Ledger',
    'Selected Work Inspector',
    'Blocked Contract',
    'Verification Contract',
    'Work Completion Contract',
    'Data Authority Contract',
    'Query / Read Model Contract',
    'No Defensive Programming / No Compatibility Design',
    'Accessibility',
    'Browser Acceptance Criteria',
    'READY FOR WIREFRAME Decision',
    'DOE/FEMP',
    'ISO 55001:2024',
    'SMRP',
    'IBM Maximo',
    'ASHRAE Commissioning',
  ]) {
    requireText(errors, workOrderCenterSpec, 'Work Order Center Surface Specification', expected);
  }

  const workOrderDetailSpec = sources['docs/product/surface-specifications/12-work-order-detail.md'];
  for (const expected of [
    'SELECTED / READY FOR WIREFRAME',
    'Primary Job',
    'Work Detail Vocabulary',
    'Mandatory Semantic Separation',
    'Primary Questions',
    'Work Scope Contract',
    'Job Plan Template vs Work Plan',
    'Task / Checklist Contract',
    'Safety & Preconditions Contract',
    'Permit Contract',
    'LOTO / Isolation Contract',
    'Field Observations / Notes',
    'Attachments Contract',
    'Planned vs Actual Contract',
    'Execution Evidence Contract',
    'Completion Criteria Contract',
    'Complete Work Mutation Contract',
    'Verification Handoff Contract',
    'Completion Evidence vs Verification Evidence',
    'Follow-up Work Contract',
    'Data Authority Contract',
    'Query / Read Model Contract',
    'Mutation Concurrency Contract',
    'No Defensive Programming / No Compatibility Design',
    'Accessibility',
    'Browser Acceptance Criteria',
    'READY FOR WIREFRAME Decision',
    'DOE/FEMP',
    'ISO 55001:2024',
    'SMRP',
    'IBM Maximo',
    'OSHA 29 CFR 1910.147',
    'ASHRAE Commissioning',
  ]) {
    requireText(errors, workOrderDetailSpec, 'Work Order Detail Surface Specification', expected);
  }

  const functionalVerificationSpec = sources['docs/product/surface-specifications/13-functional-verification.md'];
  for (const expected of [
    'SELECTED / READY FOR WIREFRAME',
    'Primary Job',
    'Verification Domain Vocabulary',
    'Mandatory Semantic Separation',
    'Primary Questions',
    'Verification Queue',
    'Requirement Contract',
    'Test Definition Contract',
    'Preconditions Contract',
    'Test Conditions Contract',
    'Expected Behavior Contract',
    'Observed Behavior Contract',
    'Evidence Workspace',
    'Pass / Fail / Inconclusive Contract',
    'Result Authority Contract',
    'Automated Test Boundary',
    'Corrective Action Contract',
    'Retest Contract',
    'Persistence Monitoring Contract',
    'Functional Verification vs M&V',
    'Data Quality Contract',
    'Data Authority Contract',
    'Query / Read Model Contract',
    'Test Run Lifecycle / Mutation Contract',
    'No Defensive Programming / No Compatibility Design',
    'Accessibility',
    'Browser Acceptance Criteria',
    'READY FOR WIREFRAME Decision',
    'ASHRAE Guideline 36-2024',
    'ASHRAE Commissioning',
    'DOE Monitoring-Based Commissioning',
    'DOE OpenBuildingControl',
    'LBNL BOPTEST',
  ]) {
    requireText(errors, functionalVerificationSpec, 'Functional Verification Surface Specification', expected);
  }

  const energyAnalysisSpec = sources['docs/product/surface-specifications/14-energy-analysis.md'];
  for (const expected of [
    'SELECTED / READY FOR WIREFRAME',
    'Primary Job',
    'Energy Analysis Vocabulary',
    'Mandatory Semantic Separation',
    'Primary Questions',
    'Period / Calendar Contract',
    'Timezone / Interval Contract',
    'Energy Type / Carrier Contract',
    'Actual Consumption Contract',
    'Power-to-Energy Derivation Boundary',
    'Primary Load Profile',
    'Named Comparison Contract',
    'Baseline Contract',
    'Normalization Contract',
    'Ranked Contributors Contract',
    'Contributor Boundary / Double-counting Contract',
    'Meter Lineage Contract',
    'Allocation Contract',
    'Variance Window Contract',
    'Demand Boundary',
    'Efficiency Boundary',
    'M&V Boundary',
    'Data Quality Contract',
    'Aggregation / Resolution Contract',
    'Query / Read Model Contract',
    'No Defensive Programming / No Compatibility Design',
    'Accessibility',
    'Browser Acceptance Criteria',
    'READY FOR WIREFRAME Decision',
    'DOE/FEMP EMIS',
    'DOE Metering Best Practices',
    'ENERGY STAR Portfolio Manager',
    'ISO 50006:2023',
    'IPMVP Core Concepts',
  ]) {
    requireText(errors, energyAnalysisSpec, 'Energy Analysis Surface Specification', expected);
  }

  const demandLoadFlexibilitySpec = sources['docs/product/surface-specifications/15-demand-load-flexibility.md'];
  for (const expected of [
    'SELECTED / READY FOR WIREFRAME',
    'Primary Job',
    'Domain Vocabulary',
    'Mandatory Semantic Separation',
    'Primary Questions',
    'Demand Definition Contract',
    'Peak Demand Contract',
    'Site Peak vs Billing Demand',
    'Coincident / System Peak Contract',
    'Load Profile Contract',
    'Heatmap Contract',
    'Load Duration Curve Contract',
    'Peak Contributor Contract',
    'Contributor Coverage / Remainder',
    'Scheduled vs Unexpected Peak',
    'Demand Charge Context',
    'Flexibility Capability Model',
    'Available / Dispatchable / Enrolled / Committed / Delivered',
    'Shed Contract',
    'Shift Contract',
    'Storage Flexibility Contract',
    'HVAC Flexibility Guardrails',
    'Rebound / Recovery Contract',
    'DR Event Lifecycle',
    'Strategy / Control Boundary',
    'Delivered Response / Performance',
    'Data Quality Contract',
    'Query / Read Model Contract',
    'No Defensive Programming / No Compatibility Design',
    'Accessibility',
    'Browser Acceptance Criteria',
    'READY FOR WIREFRAME Decision',
    'DOE/FEMP Grid-Interactive Efficient Buildings',
    'FERC',
    'NREL GEB',
    'OpenADR',
  ]) {
    requireText(errors, demandLoadFlexibilitySpec, 'Demand Load Flexibility Surface Specification', expected);
  }

  const efficiencyAnalysisSpec = sources['docs/product/surface-specifications/16-efficiency-analysis.md'];
  for (const expected of [
    'SELECTED / READY FOR WIREFRAME',
    'Primary Job',
    'Efficiency Domain Vocabulary',
    'Mandatory Semantic Separation',
    'Primary Questions',
    'Metric Definition Contract',
    'Thermal Load Contract',
    'COP Contract',
    'kW/RT Contract',
    'Rated Performance vs Field Performance',
    'Part-load Context',
    'Lift / Temperature Context',
    'Expected Performance Contract',
    'Load vs Efficiency Scatter',
    'Subsystem Decomposition Contract',
    'Equipment Comparison Contract',
    'Outlier Contract',
    'ΔT Contract',
    'Tower Approach Contract',
    'Pump / Hydronic Transport Efficiency',
    'Air-side Transport Efficiency',
    'Validity Contract',
    'Low-load / Near-zero Denominator Contract',
    'Sensor / Meter Alignment Contract',
    'Data Quality Contract',
    'Efficiency Period Aggregation Contract',
    'Query / Read Model Contract',
    'No Defensive Programming / No Compatibility Design',
    'Accessibility',
    'Browser Acceptance Criteria',
    'READY FOR WIREFRAME Decision',
    'AHRI 550/590',
    'DOE/FEMP',
    'DOE/LBNL Chilled-Water Plant Guidance',
    'DOE Better Plants',
    'DOE Pump/Fan System Assessment',
  ]) {
    requireText(errors, efficiencyAnalysisSpec, 'Efficiency Analysis Surface Specification', expected);
  }

  const energyReviewSpec = sources['docs/product/surface-specifications/17-energy-review.md'];
  for (const expected of [
    'SELECTED / READY FOR WIREFRAME',
    'Primary Job',
    '产品语言契约',
    'Domain Vocabulary',
    'Mandatory Semantic Separation',
    'Primary Questions',
    'Energy Review Lifecycle',
    'Review Cycle Contract',
    'Scope / Boundary Contract',
    'SEU Selection Contract',
    'SEU Register',
    'Relevant Variable Contract',
    'Static Factor Contract',
    'EnPI Definition Contract',
    'EnPI Validity Contract',
    'EnB Definition Contract',
    'Baseline ≠ Comparison',
    'Baseline Version / Effective Period',
    'Baseline Health Contract',
    'Baseline Adjustment / Re-establishment Boundary',
    'Normalization Contract',
    'Model Applicability Contract',
    'Opportunity Handoff',
    'Objectives / Targets Boundary',
    'M&V Boundary',
    'Data Quality Contract',
    'Data Authority Contract',
    'Query / Read Model Contract',
    'Mutation / Governance Contract',
    'Concurrency / Revision Contract',
    'No Defensive Programming / No Compatibility Design',
    'Accessibility',
    'Browser Acceptance Criteria',
    'READY FOR WIREFRAME Decision',
    'ISO 50001:2018',
    'ISO 50006:2023',
    'DOE 50001 Ready',
    'DOE SEP 50001 M&V Protocol',
    'ENERGY STAR Portfolio Manager',
  ]) {
    requireText(errors, energyReviewSpec, 'Energy Review Surface Specification', expected);
  }

  const billingCostTariffSpec = sources['docs/product/surface-specifications/18-billing-cost-tariff.md'];
  for (const expected of [
    'SELECTED / READY FOR WIREFRAME',
    'Primary Job',
    '产品语言契约',
    'Domain Vocabulary',
    'Mandatory Semantic Separation',
    'Primary Questions',
    'Capability Gating',
    'Bill Identity Contract',
    'Bundled / Unbundled Contract',
    'Billing Period Contract',
    'Actual / Estimated / Mixed Contract',
    'Bill State Dimensions',
    'Bill Ledger Contract',
    'Bill Line Item Contract',
    'Cost Composition Contract',
    'Tariff / Rate Version Contract',
    'TOU Contract',
    'Billing Demand Contract',
    'Demand Charge Contract',
    'Demand Ratchet / Look-back Contract',
    'Coincident / System Peak Billing Contract',
    'Bill vs Meter Reconciliation Contract',
    'Reconciliation Method / Tolerance',
    'Missing Bill Contract',
    'Duplicate / Overlap / Gap Contract',
    'Corrected Bill / Rebill Contract',
    'Bill Correction Workflow',
    'Projected / Billed / Settled Cost Contract',
    'Cost Allocation Contract',
    'Allocation Coverage / Remainder',
    'Data Authority Contract',
    'Query / Read Model Contract',
    'Mutation / Reconciliation Contract',
    'No Defensive Programming / No Compatibility Design',
    'Accessibility',
    'Browser Acceptance Criteria',
    'READY FOR WIREFRAME Decision',
    'DOE/FEMP',
    'ENERGY STAR Portfolio Manager',
    'NARUC',
    'OpenEI Utility Rate Database',
  ]) {
    requireText(errors, billingCostTariffSpec, 'Billing Cost Tariff Surface Specification', expected);
  }

  const carbonEmissionsSpec = sources['docs/product/surface-specifications/19-carbon-emissions.md'];
  for (const expected of [
    'SELECTED / READY FOR WIREFRAME',
    'Primary Job',
    '产品语言契约',
    'Domain Vocabulary',
    'Mandatory Semantic Separation',
    'Primary Questions',
    'Capability Gating',
    'Inventory Identity / Revision Contract',
    'Inventory Boundary Contract',
    'Scope 1 Contract',
    'Stationary Combustion Contract',
    'Refrigerant / Fugitive Emissions Contract',
    'Scope 2 Activity Contract',
    'Location-based Contract',
    'Market-based Contract',
    'Dual Reporting Contract',
    'Renewable Instrument Contract',
    'Renewable Instrument ≠ Physical Power Contract',
    'Residual Mix / Supplier Factor Contract',
    'Emission Factor Contract',
    'GWP Contract',
    'Activity Data Quality Contract',
    'Factor Quality / Applicability Contract',
    'Double-counting Contract',
    'Carbon Intensity Contract',
    'Absolute vs Intensity Contract',
    'Change Attribution Contract',
    'Emission Factor Change Contract',
    'Target Contract',
    'Base Year Contract',
    'Project Contribution Contract',
    'Avoided Emissions Boundary',
    'On-site Generation Contract',
    'Data Authority Contract',
    'Query / Read Model Contract',
    'No Defensive Programming / No Compatibility Design',
    'Accessibility',
    'Browser Acceptance Criteria',
    'READY FOR WIREFRAME Decision',
    'GHG Protocol Corporate Standard',
    'GHG Protocol Scope 2 Guidance',
    'EPA eGRID',
    'EPA GHG Emission Factors Hub',
    'ISO 14064-1:2018',
  ]) {
    requireText(errors, carbonEmissionsSpec, 'Carbon Emissions Surface Specification', expected);
  }

  const distributedEnergyFlexibilitySpec = sources['docs/product/surface-specifications/20-distributed-energy-flexibility.md'];
  for (const expected of [
    'SELECTED / READY FOR WIREFRAME',
    'Primary Job',
    '产品语言契约',
    'Domain Vocabulary',
    'Mandatory Semantic Separation',
    'Primary Questions',
    'Capability Gating',
    'Energy Flow Contract',
    'Power Flow Diagram Contract',
    'Grid Connection / Interconnection Contract',
    'Grid Mode Contract',
    'Resource State Dimensions',
    'Resource Ledger Contract',
    'BESS Contract',
    'BESS Power / Energy Capability Contract',
    'BESS Reserve Contract',
    'PV Contract',
    'PV Curtailment Contract',
    'EVSE / EV Fleet Contract',
    'Managed EV Charging Contract',
    'V2G Contract',
    'Generator / CHP Contract',
    'Thermal Storage Contract',
    'Forecast Contract',
    'Resource Flexibility Contract',
    'Flexibility State Separation',
    '15 Demand Flexibility vs 20 DER Boundary',
    'DR Event Contract',
    'Dispatch Lifecycle Contract',
    'Control Boundary',
    'Realtime Contract',
    'Resilience / Critical Load Contract',
    'Survival / Endurance Estimate Contract',
    'Island Transition Contract',
    'Safety / Interlock Contract',
    'Data Quality Contract',
    'Data Authority Contract',
    'Query / Read Model Contract',
    'No Defensive Programming / No Compatibility Design',
    'Accessibility',
    'Browser Acceptance Criteria',
    'READY FOR WIREFRAME Decision',
    'DOE Grid-Interactive Efficient Buildings',
    'NREL REopt',
    'IEEE 1547',
    'OpenADR',
    'DOE/FEMP Managed EV Charging',
  ]) {
    requireText(errors, distributedEnergyFlexibilitySpec, 'Distributed Energy Flexibility Surface Specification', expected);
  }

  const energyOpportunitiesSpec = sources['docs/product/surface-specifications/21-energy-opportunities.md'];
  for (const expected of [
    'SELECTED / READY FOR WIREFRAME',
    'Primary Job',
    '产品语言契约',
    'Opportunity Domain Vocabulary',
    'Mandatory Semantic Separation',
    'Primary Questions',
    'Opportunity Lifecycle Contract',
    'Source / Evidence Contract',
    'Evidence Sufficiency Contract',
    'Supporting / Limiting / Contradicting Evidence',
    'Expected Benefit Dimensions',
    'Calculation Basis Contract',
    'Estimate Revision Contract',
    'Confidence Contract',
    'Confidence ≠ Verification',
    'Applicability Contract',
    'Constraint Contract',
    'Risk Contract',
    'Economic Evaluation Contract',
    'Payback Boundary',
    'Priority Policy Contract',
    'Explainable Priority Contract',
    'Opportunity Ledger Contract',
    'Opportunity Inspector Contract',
    'Opportunity Detail Contract',
    'Duplicate / Related Opportunity Contract',
    'Dependency Contract',
    'Next Action Contract',
    'Convert to Optimization Plan Contract',
    'Opportunity → Optimization Semantic Boundary',
    'Objectives / Action Plan Boundary',
    'M&V Boundary',
    'Data Quality Contract',
    'Data Authority Contract',
    'Query / Read Model Contract',
    'AI Assistance Boundary',
    'No Defensive Programming / No Compatibility Design',
    'Accessibility',
    'Browser Acceptance Criteria',
    'READY FOR WIREFRAME Decision',
    'DOE / 50001',
    'DOE/FEMP EMIS',
    'DOE Better Plants Energy Treasure Hunt',
    'ISO 50001:2018',
    'DOE/FEMP Life-Cycle Cost',
    'DOE/FEMP M&V / IPMVP',
  ]) {
    requireText(errors, energyOpportunitiesSpec, 'Energy Opportunities Surface Specification', expected);
  }

  const optimizationPlanSpec = sources['docs/product/surface-specifications/22-optimization-plan.md'];
  for (const expected of [
    'SELECTED / READY FOR WIREFRAME',
    'Primary Job',
    '产品语言契约',
    'Optimization Plan Domain Vocabulary',
    'Mandatory Semantic Separation',
    'Primary Questions',
    'Optimization Plan Lifecycle Contract',
    'Plan Identity / Revision Contract',
    'Revision Significance Contract',
    'Current State Contract',
    'Proposed Change Contract',
    'Current vs Proposed Diff Contract',
    'Affected Scope Contract',
    'Blast Radius Contract',
    'Dependency Contract',
    'Preconditions Contract',
    'Preconditions ≠ Guardrails',
    'Guardrail Contract',
    'Interlock Contract',
    'Comfort / IAQ Impact Contract',
    'Reliability / Equipment Impact Contract',
    'Safety / OT Security Impact Contract',
    'Simulation / What-if Contract',
    'Simulation Validity Contract',
    'Simulation ≠ Verification',
    'Expected Effect Contract',
    'Risk Contract',
    'Change / Risk Classification Contract',
    'Test Plan Contract',
    'Test Plan Must Exist Before Execution',
    'Verification Requirement Contract',
    'M&V Planning Boundary',
    'Rollback Plan Contract',
    'Rollback Target Contract',
    'Rollback Trigger Contract',
    'Rollback Plan ≠ Automatic Rollback',
    'Rollback Execution Contract',
    'Approval Contract',
    'Approval Matrix Contract',
    'Approval Revision Binding',
    'Conditional Approval Contract',
    'Approval ≠ Authorization Bypass',
    'Execution Window Contract',
    'Implementation Route Contract',
    'Control / Strategy Handoff Contract',
    'Work Order Handoff Contract',
    'Alarm / Rule Change Boundary',
    'Simulation / Execution Drift Contract',
    'Execution Record Boundary',
    'Plan Success Boundary',
    'Data Authority Contract',
    'Query / Read Model Contract',
    'AI Assistance Boundary',
    'Wireframe Intent',
    'No Defensive Programming / No Compatibility Design',
    'Accessibility',
    'Browser Acceptance Criteria',
    'READY FOR WIREFRAME Decision',
    'DOE/FEMP Commissioning Process',
    'ASHRAE Commissioning',
    'DOE OpenBuildingControl',
    'NIST SP 800-82 Rev. 3',
    'ISA Alarm Lifecycle / Management of Change',
    'DOE/FEMP Life-Cycle Cost',
  ]) {
    requireText(errors, optimizationPlanSpec, 'Optimization Plan Surface Specification', expected);
  }

  const objectivesActionPlansSpec = sources['docs/product/surface-specifications/23-objectives-action-plans.md'];
  for (const expected of [
    'SELECTED / READY FOR WIREFRAME',
    'Primary Job',
    '产品语言契约',
    'Objective / Target / Action Plan Domain Vocabulary',
    'Mandatory Semantic Separation',
    'Primary Questions',
    'Information Architecture',
    'Route / URL State Contract',
    'Objective Contract',
    'Target Definition Contract',
    'Target Direction / Threshold Contract',
    'Target Revision Contract',
    'Baseline / EnPI Reference Contract',
    'Baseline Change Impact Contract',
    'Action Plan Contract',
    'Action Contract',
    'Milestone Contract',
    'Resource Contract',
    'Dependency Contract',
    'Blocker Contract',
    'Delivery Progress Contract',
    'Energy Performance Progress Contract',
    'Delivery Progress ≠ Performance Progress',
    'Expected / Implemented / Verified Contribution Contract',
    'Contribution Double-counting Contract',
    'Forecast Contract',
    'Forecast ≠ Achievement',
    'Target Status Contract',
    'Target Achievement Contract',
    'Pending Verification Contract',
    'Objective Achievement Contract',
    'Opportunity / Optimization Plan Relationship',
    'Work / Strategy / Control Relationship',
    'Verification Method Contract',
    'M&V Boundary',
    'Energy Review Boundary',
    'Management Review Handoff Contract',
    'Management Decision Contract',
    'Review Cadence Contract',
    'Target Ledger Contract',
    'Target Inspector Contract',
    'Action Plan Detail Contract',
    'Progress Calculation Boundary',
    'Overdue Contract',
    'Forecast / Scenario Boundary',
    'Data Authority Contract',
    'Query / Read Model Contract',
    'Revision / Audit Contract',
    'Historical Integrity Contract',
    'AI Assistance Boundary',
    'No Defensive Programming / No Compatibility Design',
    'Accessibility',
    'Browser Acceptance Criteria',
    'READY FOR WIREFRAME Decision',
    'ISO 50001',
    'DOE 50001 Ready',
    'ISO 50006:2023',
    'DOE EMIS + EnMS',
  ]) {
    requireText(errors, objectivesActionPlansSpec, 'Objectives and Action Plans Surface Specification', expected);
  }

  const measurementVerificationSpec = sources['docs/product/surface-specifications/24-measurement-verification.md'];
  for (const expected of [
    'SELECTED / READY FOR WIREFRAME',
    'Primary Job',
    '产品语言契约',
    'M&V Domain Vocabulary',
    'Mandatory Semantic Separation',
    'Primary Questions',
    'Information Architecture',
    'Route / URL State Contract',
    'Entry / Source Contract',
    'M&V Project Identity / Revision Contract',
    'M&V Plan Contract',
    'M&V Option Contract',
    'Measurement Boundary Contract',
    'Measurement Boundary Change Contract',
    'Baseline Period Contract',
    'Reporting Period Contract',
    'Baseline Model Contract',
    'Independent Variables Contract',
    'Fixed / Stipulated Parameters Contract',
    'Routine Adjustment Contract',
    'Non-routine Adjustment Contract',
    'Routine ≠ Non-routine Adjustment',
    'Savings Calculation Contract',
    'Raw Baseline ≠ Adjusted Baseline',
    'Actual Reduction ≠ Savings',
    'Operational Verification Contract',
    'Operational Verification ≠ M&V',
    'Data Source / Meter Lineage Contract',
    'Data Coverage / Quality Contract',
    'Missing Data / Exclusion Contract',
    'Model Quality Contract',
    'Model Validity Contract',
    'Uncertainty Contract',
    'Interactive Effects / Multiple Carriers Contract',
    'Cost / Carbon Boundary',
    'Result State Contract',
    'Result Revision / Historical Integrity Contract',
    'Persistence Monitoring / Snapback Contract',
    'Cross-surface Handoff Contract',
    'Aggregate Savings Contract',
    'M&V Ledger / Inspector / Detail Contract',
    'Primary Analytical Visualization',
    'Data Authority Contract',
    'Query / Read Model Contract',
    'Realtime / Freshness Contract',
    'Permission / Audit Contract',
    'AI Assistance Boundary',
    'No Defensive Programming / No Compatibility Design',
    'Accessibility / Responsive Contract',
    'Browser Acceptance Criteria',
    'READY FOR WIREFRAME Decision',
    'DOE/FEMP M&V Guidelines 5.0',
    'FEMP / IPMVP Options A/B/C/D',
    'ASHRAE Guideline 14-2023',
    'ISO 50015:2014',
  ]) {
    requireText(errors, measurementVerificationSpec, 'Measurement and Verification Surface Specification', expected);
  }

  const controlCenterSpec = sources['docs/product/surface-specifications/25-control-center.md'];
  for (const expected of [
    'SELECTED / READY FOR WIREFRAME',
    'Primary Job',
    '产品语言契约',
    'Control Domain Vocabulary',
    'Mandatory Semantic Separation',
    'Primary Questions',
    'Information Architecture',
    'Route / URL State Contract',
    'Entry / Source Contract',
    'Control Capability Contract',
    'Command Availability Contract',
    'Permission Contract',
    'Control Authority Contract',
    'BACnet Priority / Source Contract',
    'Active Command Conflict Contract',
    'Current State Contract',
    'Proposed State Contract',
    'Preconditions Contract',
    'Interlock Contract',
    'Guardrail Contract',
    'Impact Scope Contract',
    'Control Reason Contract',
    'Confirmation Contract',
    'Confirmation Freshness Contract',
    'Command Lifecycle Contract',
    'Requested / Attempted Contract',
    'ACK Contract',
    'Readback Contract',
    'Verified Behavior Contract',
    'Execution State Unknown Contract',
    'No Automatic Retry Contract',
    'Idempotency / Duplicate Submission Contract',
    'Optimistic UI Prohibition',
    'Override Contract',
    'Override Expiry Contract',
    'Release / Relinquish Contract',
    'Active Override Review Contract',
    'Schedule Contract',
    'Mode Contract',
    'Start / Stop Contract',
    'Setpoint Contract',
    'Bulk Control Contract',
    'Emergency / Life Safety Boundary',
    'Approval Contract',
    'Execution Window Contract',
    'Rollback / Recovery Boundary',
    'Superseded / Preempted Command Contract',
    'Control vs Strategy Boundary',
    'Control vs Functional Verification Boundary',
    'Execution Record Boundary',
    'Snapshot + Stream Contract',
    'Stream Disconnect Contract',
    'Reconciliation Contract',
    'Audit Contract',
    'Mutation / Command API Contract',
    'Security Boundary Contract',
    'AI Assistance Boundary',
    'No Defensive Programming / No Compatibility Design',
    'Browser Acceptance Criteria',
    'READY FOR WIREFRAME Decision',
    'NIST SP 800-82 Rev. 3',
    'DOE OpenBuildingControl',
    'ISA-101',
    'BACnet Command Prioritization',
    'Requested ≠ Attempted',
    'ACK ≠ Readback',
    'Readback ≠ Verified Behavior',
    'Release Override ≠ Write Normal Value',
    'Approval ≠ Interlock Bypass',
  ]) {
    requireText(errors, controlCenterSpec, 'Control Center Surface Specification', expected);
  }

  const strategyCenterSpec = sources['docs/product/surface-specifications/26-strategy-center.md'];
  for (const expected of [
    'SELECTED / READY FOR WIREFRAME',
    'Primary Job',
    '产品语言契约',
    'Strategy Domain Vocabulary',
    'Mandatory Semantic Separation',
    'Primary Questions',
    'Information Architecture',
    'Route / URL State Contract',
    'Strategy Lifecycle Contract',
    'Strategy Version Contract',
    'Publish Contract',
    'Deployment Contract',
    'Deployment Drift Contract',
    'Enable Contract',
    'Eligibility Contract',
    'Active Contract',
    'Schedule Contract',
    'Trigger Contract',
    'Trigger State Contract',
    'Priority / Arbitration Contract',
    'Conflict Contract',
    'Conflict State Contract',
    'Control Authority Contract',
    'Preconditions Contract',
    'Guardrail Contract',
    'Interlock Boundary',
    'Fail-safe Contract',
    'Fail-safe ≠ Rollback',
    'Execution Contract',
    'Execution State Contract',
    'Observability / Strategy Health Contract',
    'Verification Contract',
    'Publish / Deploy / Verify Chain',
    'Rollback Contract',
    'Rollback Verification Contract',
    'Disable Strategy Contract',
    'Strategy Ledger Contract',
    'Strategy Inspector Contract',
    'Strategy vs Control Center Boundary',
    'Strategy vs Execution Record Boundary',
    'Strategy vs Functional Verification Boundary',
    'Snapshot + Stream Contract',
    'No Automatic Retry Contract',
    'Security Boundary Contract',
    'Audit Contract',
    'Query / Read Model Contract',
    'No Universal Rule Engine UI',
    'AI Assistance Boundary',
    'No Defensive Programming / No Compatibility Design',
    'Browser Acceptance Criteria',
    'READY FOR WIREFRAME Decision',
    'DOE OpenBuildingControl',
    'ASHRAE Guideline 36-2024',
    'NIST SP 800-82 Rev.3',
    'ISA-101',
    'Published ≠ Deployed',
    'Deployed ≠ Enabled',
    'Enabled ≠ Eligible',
    'Eligible ≠ Active',
    'Higher Priority ≠ Better Strategy',
    'Conflict Detected ≠ Conflict Resolved',
    'Strategy Disabled ≠ Existing Override Released',
    'Rollback Requested ≠ Previous Version Restored',
  ]) {
    requireText(errors, strategyCenterSpec, 'Strategy Center Surface Specification', expected);
  }

  const strategyDetailSpec = sources['docs/product/surface-specifications/27-strategy-detail.md'];
  for (const expected of [
    'SELECTED / READY FOR WIREFRAME',
    'Primary Job',
    '产品语言契约',
    'Strategy Engineering Vocabulary',
    'Mandatory Semantic Separation',
    'Primary Questions',
    'Information Architecture',
    'Route / URL State Contract',
    'Entry / Source Contract',
    'Strategy Version Contract',
    'Version Diff Contract',
    'Version Diff Risk Contract',
    'Objective Contract',
    'Scope Contract',
    'Inputs Contract',
    'Missing / Bad Input Contract',
    'Outputs Contract',
    'Trigger Logic Contract',
    'Schedule Contract',
    'Trigger vs Precondition Contract',
    'Preconditions Contract',
    'Guardrail Contract',
    'Interlock Boundary',
    'Priority / Arbitration Contract',
    'Conflict Policy Contract',
    'Fail-safe Contract',
    'Fail-safe vs Degraded Mode Contract',
    'Simulation Contract',
    'Simulation Scenario Contract',
    'Simulation KPI Contract',
    'Simulation Result Contract',
    'Simulation Applicability Contract',
    'Simulation vs Field Contract',
    'Historical Replay Contract',
    'Historical Replay Limitations Contract',
    'Replay Data Quality Contract',
    'Expected Impact Contract',
    'Expected Savings Boundary',
    'Risk / Constraint Contract',
    'Contradicting Evidence Contract',
    'Functional Test Definition Contract',
    'Functional Test Version Binding',
    'Approval Readiness Contract',
    'Approval State Contract',
    'Approval Version Binding Contract',
    'Approval Independence Contract',
    'Publish Contract',
    'Publish State Unknown Contract',
    'Deployment Handoff Contract',
    'Rollout Plan Contract',
    'Rollback Target Contract',
    'Rollback Boundary',
    'Version History Contract',
    'Audit Contract',
    'Concurrent Editing Contract',
    'No Automatic Retry Contract',
    'Security Boundary Contract',
    'AI Assistance Boundary',
    'No Defensive Programming / No Compatibility Design',
    'Accessibility Contract',
    'Browser Acceptance Criteria',
    'READY FOR WIREFRAME Decision',
    'DOE OpenBuildingControl',
    'ASHRAE Guideline 36-2024',
    'DOE BOPTEST',
    'NIST SP 800-82 Rev.3',
    'ISA-101',
    'Draft ≠ Approved',
    'Approved ≠ Published',
    'Published ≠ Deployed',
    'Simulation Passed ≠ Field Verified',
    'Historical Replay ≠ Counterfactual Truth',
    'Expected Savings ≠ Verified Savings',
    'Approval ≠ Interlock Bypass',
    'Rollback Target Selected ≠ Rollback Completed',
  ]) {
    requireText(errors, strategyDetailSpec, 'Strategy Detail Surface Specification', expected);
  }

  const executionRecordSpec = sources['docs/product/surface-specifications/28-execution-record.md'];
  for (const expected of [
    'SELECTED / READY FOR WIREFRAME',
    'Primary Job',
    '产品语言契约',
    'Execution Domain Vocabulary',
    'Mandatory Semantic Separation',
    'Primary Questions',
    'Information Architecture',
    'Route / URL State Contract',
    'Entry / Source Contract',
    'Execution Identity Contract',
    'Execution Source Contract',
    'Requester / Actor Contract',
    'Reason Contract',
    'Intended Command Contract',
    'Request State Contract',
    'Authorization Contract',
    'Authorization Result Contract',
    'Attempt Contract',
    'Retry Contract',
    'Retry Reason Contract',
    'Idempotency Contract',
    'Gateway Delivery Contract',
    'Target Response Contract',
    'ACK Contract',
    'BACnet Priority Evidence Contract',
    'Value Source Contract',
    'Readback Contract',
    'Readback Matching Contract',
    'Verified Result Contract',
    'Execution State Model',
    'Final Outcome Contract',
    'Partial Execution Contract',
    'Multi-target Result Matrix Contract',
    'Atomic vs Non-atomic Contract',
    'Execution Unknown Contract',
    'Reconciliation Contract',
    'Correlation Contract',
    'Correlation ≠ Causation',
    'Strategy Arbitration Evidence Contract',
    'Superseded / Preempted Contract',
    'Override Contract',
    'Relinquish Contract',
    'Rollback Contract',
    'Fail-safe Execution Contract',
    'Timeline Contract',
    'Immutable Timeline Contract',
    'Timestamp Contract',
    'Out-of-order Event Contract',
    'Audit Contract',
    'Audit Source / Target Contract',
    'Audit Record ≠ Business Execution',
    'Unauthorized / Rejected Attempt Contract',
    'Functional Verification Boundary',
    'M&V Boundary',
    'Ledger Contract',
    'Default Prioritization Contract',
    'Execution Inspector Contract',
    'Execution Detail Contract',
    'Advanced Protocol Evidence Boundary',
    'No Replay Command From Log',
    'Snapshot + Event Stream Contract',
    'Stream Disconnect Contract',
    'Reconnect Contract',
    'Event Ordering Contract',
    'Query / Read Model Contract',
    'Retention / Archive Contract',
    'Security Boundary Contract',
    'Privacy / Sensitive Audit Contract',
    'AI Assistance Boundary',
    'No Defensive Programming / No Compatibility Design',
    'Accessibility Contract',
    'Browser Acceptance Criteria',
    'READY FOR WIREFRAME Decision',
    'NIST SP 800-82 Rev.3',
    'BACnet Command Prioritization',
    'BACnet Value Source',
    'BACnet Audit Reporting',
    'DOE OpenBuildingControl',
    'ASHRAE Guideline 36-2024',
    'ISA-101',
    'Request ≠ Authorization',
    'ACK ≠ Readback',
    'Readback ≠ Verified Result',
    'Unknown ≠ Failed',
    'Partial Execution ≠ Success',
    'Rollback ACK ≠ Restored State',
  ]) {
    requireText(errors, executionRecordSpec, 'Execution Record Surface Specification', expected);
  }

  const reportCenterSpec = sources['docs/product/surface-specifications/29-report-center.md'];
  for (const expected of [
    'SELECTED / READY FOR WIREFRAME',
    'Primary Job',
    '产品语言契约',
    'Report Domain Vocabulary',
    'Mandatory Semantic Separation',
    'Primary Questions',
    'Information Architecture',
    'Route / URL State Contract',
    'Capability Gating',
    'Report Definition Contract',
    'Definition Version Contract',
    'Report Instance Contract',
    'Reporting Period Contract',
    'Data Cutoff Contract',
    'Snapshot vs Live Contract',
    'Source Snapshot Contract',
    'Source Lineage Contract',
    'Completeness Contract',
    'Completeness vs Data Quality',
    'Estimated / Actual Contract',
    'Missing Data Contract',
    'Generation Request Contract',
    'Generation Lifecycle Contract',
    'No Automatic High-level Retry',
    'Report Revision Contract',
    'Approval Contract',
    'Approval ≠ Publish',
    'Artifact Contract',
    'Artifact Integrity Contract',
    'Report Ledger Contract',
    'Schedule Contract',
    'Scheduled ≠ Generated',
    'Recipient Contract',
    'Distribution Contract',
    'Distribution State Contract',
    'Download ≠ Distribution',
    'Correction Trigger Contract',
    'Source Correction Impact Contract',
    'Materiality Contract',
    'Correction Contract',
    'Reissue Contract',
    'Reissue Diff Contract',
    'Withdrawal Contract',
    'Management Review Pack Contract',
    'Management Review Snapshot Integrity',
    'Live Preview Contract',
    'Preview ≠ Generated Report',
    'AI Assistance Boundary',
    'Retention Contract',
    'Security Boundary Contract',
    'Audit Contract',
    'No Frontend Recalculation Contract',
    'Report Revision vs Definition Version',
    'Browser Acceptance Criteria',
    'No Defensive Programming / No Compatibility Design',
    'Accessibility Contract',
    'READY FOR WIREFRAME Decision',
    'ISO 50001:2018',
    'ISO 50006:2023',
    'NIST SP 800-92',
    'Reporting Period ≠ Data Cutoff',
    'Generated ≠ Approved',
    'Approved ≠ Published',
    'Published ≠ Distributed',
    'Correction ≠ Overwrite',
    'Reissue ≠ Delete Old Revision',
    'Missing ≠ Zero',
  ]) {
    requireText(errors, reportCenterSpec, 'Report Center Surface Specification', expected);
  }

  const managementReviewSpec = sources['docs/product/surface-specifications/30-management-review.md'];
  for (const expected of [
    'SELECTED / READY FOR WIREFRAME',
    'Primary Job',
    '产品语言契约',
    'Management Review Domain Vocabulary',
    'Mandatory Semantic Separation',
    'Primary Questions',
    'Information Architecture',
    'Route / URL State Contract',
    'Capability Gating',
    'Review Definition Contract',
    'Review Type Contract',
    'Review Cadence Contract',
    'Review Instance Identity Contract',
    'Review Lifecycle Contract',
    'Review Pack Contract',
    'Review Pack Immutability Contract',
    'Review Input Coverage Contract',
    'Review Readiness Contract',
    'Participant Contract',
    'Quorum Contract',
    'Previous Review Actions Contract',
    'Energy Performance Input Contract',
    'EnPI / EnB Review Contract',
    'SEU Change Contract',
    'Target Progress Contract',
    'Verified Savings Contract',
    'Major Deviation Contract',
    'Operational Risk Contract',
    'Data Risk Contract',
    'Internal Audit Input Contract',
    'Corrective Action Contract',
    'Improvement Opportunities Contract',
    'Resource Needs Contract',
    'Management Decision Contract',
    'Recommendation vs Decision Contract',
    'Decision State Contract',
    'Decision Revision Contract',
    'Decision ≠ Direct Execution',
    'Review Action Contract',
    'Action State Contract',
    'Action Blocker Contract',
    'Action Completion Contract',
    'Effectiveness Review Contract',
    'Action Handoff Contract',
    'Management Review Record Contract',
    'Review Record ≠ Meeting Notes',
    'Review Conclusion Contract',
    'Continual Improvement Contract',
    'Current Evidence vs Historical Evidence',
    'Source Correction Impact Contract',
    'Extraordinary Review Contract',
    'Review Frequency / Full Coverage Contract',
    'Policy Review Contract',
    'Objective / Target Change Contract',
    'EnPI / EnB Change Contract',
    'Resource Decision Contract',
    'Resource Decision ≠ Spend Executed',
    'Review Agenda Contract',
    'Decision Needed Queue Contract',
    'Decision Audit Contract',
    'Review Audit Contract',
    'Review Record Correction Contract',
    'Live Action Status vs Frozen Review Record',
    'Effectiveness Handoff Contract',
    'Security Boundary Contract',
    'AI Assistance Boundary',
    'Accessibility Contract',
    'Browser Acceptance Criteria',
    'No Defensive Programming / No Compatibility Design',
    'READY FOR WIREFRAME Decision',
    'ISO 50001:2018',
    'ISO 50004:2020',
    'DOE 50001 Ready / eGuide',
    'ISO 50006:2023',
    'Review Pack ≠ Management Review',
    'Presented Information ≠ Management Decision',
    'Decision ≠ Review Action',
    'Action Completed ≠ Effectiveness Confirmed',
    'Target At Risk ≠ Target Failed',
    'Current Evidence ≠ Historical Review Evidence',
    'Partial Review ≠ Full Review',
  ]) {
    requireText(errors, managementReviewSpec, 'Management Review Surface Specification', expected);
  }

  const dataQualitySpec = sources['docs/product/surface-specifications/31-data-quality.md'];
  for (const expected of [
    'SELECTED / READY FOR WIREFRAME',
    'Primary Job',
    '产品语言契约',
    'Data Quality Domain Vocabulary',
    'Mandatory Semantic Separation',
    'Information Architecture',
    'Route / URL State Contract',
    'Capability Gating',
    'Quality Dimension Contract',
    'Coverage Contract',
    'Freshness / Stale Contract',
    'Completeness Contract',
    'Validity / Plausibility Contract',
    'Synchronization Contract',
    'Event Time vs Ingest Time Contract',
    'Source Health Contract',
    'BACnet Quality Mapping Contract',
    'Estimated / Backfilled Contract',
    'Missing Data Contract',
    'Unit / Scale Contract',
    'Semantic Integrity Contract',
    'Lineage Contract',
    'Business Impact Contract',
    'Downstream Impact Graph Contract',
    'Issue Contract',
    'Issue Lifecycle Contract',
    'Data Quality Finding vs Root Cause',
    'Owner Contract',
    'Correction Contract',
    'Correction ≠ History Erasure',
    'Recomputation Contract',
    'Recomputation ≠ Historical Overwrite',
    'Report / Review Impact Contract',
    'M&V Impact Contract',
    'Control Safety Boundary',
    'Alarm Boundary',
    'Integration / Semantic Boundaries',
    'Issue Ledger Contract',
    'Default Prioritization Contract',
    'Issue Inspector Contract',
    'Evidence Contract',
    'Time-series Rendering Contract',
    'Quality Rule Version Contract',
    'Historical Integrity Contract',
    'Query / Read Model Contract',
    'Snapshot + Event Stream Contract',
    'Late / Out-of-order Data Contract',
    'Security Boundary Contract',
    'Audit Contract',
    'AI Assistance Boundary',
    'Browser Acceptance Criteria',
    'No Defensive Programming / No Compatibility Design',
    'READY FOR WIREFRAME Decision',
    'DOE / FEMP EMIS Technical Resources',
    'BACnet Status / Reliability',
    'NIST Time Synchronization',
    'ASHRAE 223P',
    'Missing ≠ 0',
    'Stale ≠ Offline',
    'Bad Quality ≠ Alarm',
    'Source Down ≠ Device Down',
    'Fresh ≠ Valid',
    'Complete ≠ Accurate',
    'Source Health ≠ Data Quality',
    'Source Corrected ≠ Recomputed',
    'Recomputed ≠ Historical Result Overwritten',
  ]) {
    requireText(errors, dataQualitySpec, 'Data Quality Surface Specification', expected);
  }

  const meteringSemanticSpec = sources['docs/product/surface-specifications/32-metering-semantic-model.md'];
  for (const expected of [
    'SELECTED / READY FOR WIREFRAME',
    'Primary Job',
    'Mandatory Semantic Separation',
    'Identity Contract',
    'Point Contract',
    'Relationship Contract',
    'Topology Contract',
    'Physical Meter Contract',
    'Meter Hierarchy Contract',
    'Measurement Boundary Contract',
    'Virtual Meter Contract',
    'Calculated Point Contract',
    'Formula Contract',
    'Source Binding Contract',
    'Effective-dated Model Contract',
    'Model Revision Contract',
    'Validation Contract',
    '31 Data Quality Handoff',
    'Downstream Recomputation Contract',
    'Historical Integrity Contract',
    'No Defensive Programming / No Compatibility Design',
    'Browser Acceptance Criteria',
    'READY FOR WIREFRAME Decision',
    'Name ≠ Identity',
    'Naming Convention ≠ Semantic Model',
    'Tag ≠ Relationship',
    'Point ≠ Device',
    'hasPoint ≠ hosts ≠ controls ≠ feeds ≠ hasPart ≠ hasLocation',
    'Topology ≠ Geometry',
    'Physical Meter ≠ Virtual Meter',
    'Calculated Point ≠ Measured Point',
    'Meter Hierarchy ≠ Allocation Hierarchy',
    'Measurement Boundary ≠ Meter Tree',
    'Current Mapping ≠ Historical Mapping',
    'Model Published ≠ Downstream Recomputed',
    'relationship missing',
    'virtual meter missing input',
    'model revision',
    'published model',
  ]) {
    requireText(errors, meteringSemanticSpec, 'Metering and Semantic Model Surface Specification', expected);
  }

  const rulesNotificationsSpec = sources['docs/product/surface-specifications/33-rules-notifications.md'];
  for (const expected of [
    'SELECTED / READY FOR WIREFRAME',
    'Primary Job',
    'Mandatory Semantic Separation',
    'Rule Identity Contract',
    'Rule Revision Contract',
    'Rule Lifecycle Contract',
    'Alarm Rule Boundary',
    'Input Quality Gate',
    'Threshold Contract',
    'Condition Contract',
    'Evaluation Window Contract',
    'Delay / Persistence Contract',
    'Hysteresis / Deadband Contract',
    'Recovery Contract',
    'Domain Output Contract',
    'Event Identity Contract',
    'Notification Policy Identity Contract',
    'Notification Attempt Contract',
    'Delivery State Contract',
    'Grouping Contract',
    'Notification Deduplication Contract',
    'Suppression Contract',
    'Inhibition Contract',
    'Alarm Shelving Boundary',
    'Escalation Contract',
    'Retry Contract',
    'Idempotency Contract',
    'Dead-letter / Failed Delivery Contract',
    'Rule Test Contract',
    'Historical Replay Contract',
    'Notification Dry-run Contract',
    'Publish ≠ Runtime Activation',
    'Version / Effective Date Contract',
    'Management of Change Contract',
    'Alarm Center Handoff',
    'Control / Safety Boundary',
    'No Defensive Programming / No Compatibility Design',
    'Browser Acceptance Criteria',
    'READY FOR WIREFRAME Decision',
    'Rule Definition ≠ Rule Evaluation',
    'Published ≠ Enabled',
    'Threshold ≠ Condition',
    'Evaluation Window ≠ Delay',
    'Delay ≠ Hysteresis',
    'Trigger Condition ≠ Recovery Condition',
    'Rule Match ≠ Domain Event automatically',
    'Domain Event ≠ Alarm automatically',
    'Alarm Occurrence ≠ Notification Attempt',
    'Notification Attempt ≠ Provider Accepted',
    'Provider Accepted ≠ Delivered to Recipient',
    'Delivered ≠ Read',
    'Read ≠ Alarm Acknowledged',
    'Notification Suppressed ≠ Event Suppressed',
    'Notification Suppressed ≠ Alarm Shelved',
    'Grouping ≠ Event Merge',
    'Deduplication ≠ Event Deletion',
    'Rule Severity ≠ Alarm Priority automatically',
    'Test Passed ≠ Approved',
  ]) {
    requireText(errors, rulesNotificationsSpec, 'Rules and Notifications Surface Specification', expected);
  }

  const integrationManagementSpec = sources['docs/product/surface-specifications/34-integration-management.md'];
  for (const expected of [
    'SELECTED / READY FOR WIREFRAME',
    'Primary Job',
    'Mandatory Semantic Separation',
    'Integration Identity Contract',
    'Integration Lifecycle Contract',
    'Connector Definition Contract',
    'Endpoint Contract',
    'Authentication Contract',
    'Authentication ≠ Authorization',
    'Authorization Scope Contract',
    'Capability Contract',
    'Read Capability ≠ Write / Control Capability',
    'Control-capable Integration Boundary',
    'Security Profile Contract',
    'Certificate / Trust Contract',
    'Credential Rotation Contract',
    'Source Object Contract',
    'Mapping Contract',
    'Mapping Validation Contract',
    'Schema Drift Contract',
    'Event Time / Ingest Time Contract',
    'Live Sync Contract',
    'Historical Sync Contract',
    'Sync Lag ≠ Data Freshness Automatically',
    'Duplicate / Idempotency Contract',
    'Protocol Delivery ≠ Business Exactly-once',
    'Retry Contract',
    'Control Retry Boundary',
    'Backpressure Contract',
    'Backfill Contract',
    'Replay Contract',
    'Connector Health Contract',
    'Connector Health ≠ Source Health',
    'Source Health ≠ Device Health',
    'Data Flow Health Contract',
    'Test Connection Contract',
    'Connection Test Passed ≠ Data Flow Healthy',
    'Mapping Revision / Effective Date Contract',
    'Current Mapping ≠ Historical Mapping',
    'Mapping Changed ≠ Historical Data Rewritten',
    'Integration Revision Contract',
    'Cutover Contract',
    'Rollback Requested ≠ Restored',
    'Security Boundary Contract',
    'Secret Handling Contract',
    'No Defensive Programming / No Compatibility Design',
    'Browser Acceptance Criteria',
    'READY FOR WIREFRAME Decision',
    'Authenticated ≠ Authorized',
    'Read Capability ≠ Write Capability',
    'Write Capability ≠ Control Authority',
    'Connected ≠ Synchronized',
    'Sync Running ≠ Sync Caught Up',
    'Credential Reference ≠ Secret',
    'Source ID ≠ Canonical ID',
    'Mapping Changed ≠ Historical Mapping Rewritten',
    'Configuration Published ≠ Runtime Activated',
    'Rollback Requested ≠ Previous Version Restored',
  ]) {
    requireText(errors, integrationManagementSpec, 'Integration Management Surface Specification', expected);
  }

  const siteSystemConfigurationSpec = sources['docs/product/surface-specifications/35-site-system-configuration.md'];
  for (const expected of [
    'SELECTED / READY FOR WIREFRAME',
    'Primary Job',
    'Mandatory Semantic Separation',
    'Canonical Identity Contract',
    '35 / 32 Authority Boundary',
    'Site Contract',
    'Site Boundary Contract',
    'EnMS Scope / Boundary Contract',
    'Building Contract',
    'Space Contract',
    'Space ≠ Thermal Zone',
    'Operational HVAC System Contract',
    'System Boundary Contract',
    'System Boundary ≠ Meter Boundary',
    'Site Timezone Contract',
    'Timezone ≠ UTC Offset',
    'Timezone Change Contract',
    'Business Calendar Contract',
    'Business Calendar ≠ Control Schedule',
    'Holiday / Exception Calendar Contract',
    'Calendar Effective Period Contract',
    'Weather Source Binding Contract',
    'Weather Source ≠ Weather Data',
    'Weather Station Selection Contract',
    'Weather Binding Change Contract',
    'Site Lifecycle Contract',
    'Site Active ≠ Occupied',
    'Commissioning State Contract',
    'Commissioning State ≠ Runtime State',
    'Commissioned ≠ Healthy',
    'Functional Verification Boundary',
    'Site Capability Contract',
    'Capability Enablement Contract',
    'Capability Enabled ≠ Integration Healthy',
    'Capability Enabled ≠ Data Available',
    'Capability Enabled ≠ Permission',
    'Capability Enabled ≠ Safe to Control',
    'Configuration Revision Contract',
    'Current vs Future Configuration Contract',
    'Effective Date Contract',
    'Historical Configuration Contract',
    'Site Split / Merge Contract',
    'Retirement Contract',
    'No Defensive Programming / No Compatibility Design',
    'Information Architecture',
    'Browser Acceptance Criteria',
    'READY FOR WIREFRAME Decision',
    'Organization ≠ Portfolio',
    'Site ≠ Building',
    'Space ≠ Thermal Zone automatically',
    'Weather Source Changed ≠ Historical Weather Rewritten',
    'Configuration Published ≠ Effective Now',
    'Site Retired ≠ Historical Data Deleted',
    'site timezone missing',
    'weather source unavailable',
    'business calendar missing',
    'functional verification PASS',
    'control capability enabled',
  ]) {
    requireText(errors, siteSystemConfigurationSpec, 'Site and System Configuration Surface Specification', expected);
  }

  const usersAccessAuditSpec = sources['docs/product/surface-specifications/36-users-access-audit.md'];
  for (const expected of [
    'SELECTED / READY FOR WIREFRAME',
    'Primary Job',
    'Mandatory Semantic Separation',
    'Identity Contract',
    'Identity ≠ Principal',
    'Principal Contract',
    'Human Principal ≠ Service Principal',
    'Role Contract',
    'Role ≠ Permission',
    'Group ≠ Role',
    'Role Revision Contract',
    'Role Hierarchy Contract',
    'Permission Contract',
    'Permission Granularity Contract',
    'Read ≠ Edit ≠ Approve ≠ Execute',
    'Resource Scope Contract',
    'Site Scope Contract',
    'Site Scope ≠ Action Permission',
    'Resource Scope ≠ UI Filter',
    'Authentication Contract',
    'Authentication ≠ Authorization',
    'Session Contract',
    'Session Active ≠ Privileged Session',
    'Step-up Authentication Contract',
    'Control Permission Boundary',
    'Interlock Bypass Boundary',
    'Sensitive Action Contract',
    'Separation of Duties Contract',
    'Static Separation of Duties',
    'Dynamic Separation of Duties',
    'Approval ≠ Permission',
    'Role Assignment Contract',
    'Temporary Access Contract',
    'Temporary Access ≠ Delegation',
    'Delegation Contract',
    'Emergency / Break-glass Access Contract',
    'Emergency Access ≠ Safety Bypass',
    'Service Provider Access Contract',
    'Access Review Contract',
    'Access Review Decision Contract',
    'Authorization Decision Contract',
    'Policy Unavailable Contract',
    'Default Deny Contract',
    'Audit Event Contract',
    'Audit Event ≠ Business Execution',
    'Audit Record ≠ Physical Outcome',
    'Correlation ≠ Causation',
    'Audit Immutability Contract',
    'Privileged Function Audit Contract',
    'Access Change Workflow Contract',
    'Approval Bound to Change Contract',
    'Access Enforcement Contract',
    'Session Revocation ≠ Credential Revocation',
    'Historical Access Contract',
    'Current Access ≠ Historical Access',
    'Security Boundary',
    'No Defensive Programming / No Compatibility Design',
    'Browser Acceptance Criteria',
    'READY FOR WIREFRAME Decision',
    'Control Permission ≠ Control Authority',
    'Control Permission ≠ Interlock Bypass',
    'Control Permission ≠ Safe to Control',
    'Audit Logged',
    'Policy service unavailable',
    'site scope missing',
    'legacy admin flag fallback',
    'frontend-only permission enforcement',
  ]) {
    requireText(errors, usersAccessAuditSpec, 'Users, Access and Audit Surface Specification', expected);
  }

  const benchmarkAudit = sources['docs/product/smart-energy-system-page-architecture-audit-v1.md'];
  for (const expected of ['COMPLETED / EXTERNAL BENCHMARK AUDIT', 'U.S. DOE / FEMP', 'ENERGY STAR Portfolio Manager', 'ASHRAE Guideline 36', 'Siemens Building X', 'Schneider EcoStruxure', 'Honeywell Forge', '易用性与专业性：必须同时成立']) {
    requireText(errors, benchmarkAudit, 'Smart Energy external benchmark audit', expected);
  }

  const historicalBlueprint = sources['docs/product/smart-energy-system-page-architecture-v1.md'];
  requireText(errors, historicalBlueprint, 'historical Smart Energy blueprint v1', 'SUPERSEDED / HISTORICAL');
  requireText(errors, historicalBlueprint, 'historical Smart Energy blueprint v1', 'docs/product/smart-energy-system-page-architecture-v2.md');

  const historicalProductIa = sources['docs/product/product-information-architecture-v2.md'];
  requireText(errors, historicalProductIa, 'historical Product IA v2', 'SUPERSEDED / HISTORICAL');
  requireText(errors, historicalProductIa, 'historical Product IA v2', 'docs/product/smart-energy-system-page-architecture-v2.md');

  const architecture = sources['docs/architecture/smart-energy-react-spa-frontend-architecture.md'];
  for (const expected of ['Tailwind CSS', 'shadcn/ui', 'TanStack Router', 'TanStack Query', 'TanStack Table v9', 'tablecn interaction/composition grammar', 'shadcn Chart + Recharts', 'Apache ECharts']) {
    requireText(errors, architecture, 'frontend architecture baseline', expected);
  }

  const skeleton = sources['docs/architecture/smart-energy-react-spa-frontend-skeleton-review.md'];
  for (const expected of ['复杂度随业务增长', 'Zustand（确有需要时）', 'Route 文件保持薄', 'Snapshot + Stream', 'BaseRepository', 'Global Event Bus']) {
    requireText(errors, skeleton, 'frontend skeleton review', expected);
  }

  const sourceReview = sources['docs/architecture/shadcn-ui-shadcn-admin-source-review.md'];
  for (const expected of ['ADOPT', 'ADAPT', 'REJECT', 'shadcn/ui', 'shadcn-admin', 'shadcn Chart + Recharts', 'Apache ECharts']) {
    requireText(errors, sourceReview, 'shadcn source review', expected);
  }

  const tablecnReuiReview = sources['docs/architecture/shadcn-tablecn-reui-source-review-2026-09-20.md'];
  for (const expected of ['SELECTED / ACTIVE SOURCE REVIEW', 'sadmann7/tablecn', 'ReUI', 'TanStack Table v9', 'Source pin policy', 'ReUI Data Grid is **not the default table path**']) {
    requireText(errors, tablecnReuiReview, 'tablecn/ReUI source review', expected);
  }

  const kiboDiceReview = sources['docs/architecture/kibo-diceui-advanced-components-source-review-2026-09-20.md'];
  for (const expected of ['SELECTED / ACTIVE SOURCE REVIEW', 'Kibo UI', 'Dice UI', 'Advanced application layer', 'Radix', 'Source pin policy', 'select one implementation']) {
    requireText(errors, kiboDiceReview, 'Kibo/Dice advanced component source review', expected);
  }

  const suppliedPlan = sources['docs/reference/shadcn-admin与shadcn-ui在智慧能源React-SPA中的应用方案.md'];
  for (const expected of [
    'shadcn/ui 是我们的基础组件代码来源',
    'satnaing/shadcn-admin 是高质量参考实现，而不是业务框架',
    'Pattern over Dependency',
  ]) {
    requireText(errors, suppliedPlan, 'user-supplied shadcn application plan', expected);
  }

  for (const skillPath of [
    '.agents/skills/impeccable/SKILL.md',
    '.agents/skills/frontend-design/SKILL.md',
    '.agents/skills/web-design-guidelines/SKILL.md',
  ]) {
    const skill = sources[skillPath];
    requireText(errors, skill, skillPath, 'name:');
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

  console.log('Design system check passed: shadcn application authority and product/architecture boundaries are aligned.');
}

if (process.argv[1] && resolve(process.argv[1]) === scriptPath) {
  main().catch((error) => {
    console.error(error);
    process.exitCode = 1;
  });
}
