import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtemp, mkdir, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import test from 'node:test';

const root = resolve(import.meta.dirname, '..');

test('design system gate validates the shadcn application authority contract', () => {
  const result = spawnSync(process.execPath, ['scripts/check-design-system.mjs'], {
    cwd: root,
    encoding: 'utf8',
  });

  assert.equal(result.status, 0, result.stderr || result.stdout);
  assert.match(result.stdout, /Design system check passed/);
});

test('design system gate rejects legacy Ant design authority language', async () => {
  const { validateDesignSystem } = await import('./check-design-system.mjs');
  const tempRoot = await mkdtemp(join(tmpdir(), 'hvac-design-check-'));

  const files = [
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

  for (const path of files) {
    const source = await import('node:fs/promises').then(({ readFile }) => readFile(resolve(root, path), 'utf8'));
    const target = resolve(tempRoot, path);
    await mkdir(resolve(target, '..'), { recursive: true });
    await writeFile(target, source, 'utf8');
  }

  await mkdir(resolve(tempRoot, 'apps/hvac-web/src/features'), { recursive: true });

  const designPath = resolve(tempRoot, 'DESIGN.md');
  const design = await import('node:fs/promises').then(({ readFile }) => readFile(designPath, 'utf8'));
  await writeFile(designPath, `${design}\n统一使用 \`ProLayout\`\n`, 'utf8');

  const result = await validateDesignSystem(tempRoot);
  assert.ok(result.errors.some((error) => error.includes('legacy authority') && error.includes('ProLayout')));
});
