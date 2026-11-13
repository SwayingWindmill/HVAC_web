import assert from 'node:assert/strict';
import { cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { contrastGrade, contrastRatio, surfaceOf, tinted, TOKEN_GROUPS } from '../apps/hvac-web/src/features/system/design-system/design-tokens.ts';
import { validateDesignSystem } from './check-design-system.ts';

test('contrast follows WCAG: black on white is 21:1, a colour on itself 1:1', () => {
  assert.equal(contrastRatio('#000000', '#ffffff'), 21);
  assert.equal(contrastRatio('#fff', '#ffffff'), 1);
  assert.ok(Math.abs(contrastRatio('#767676', '#ffffff') - 4.54) < 0.01);
});

test('text needs 4.5:1 (7:1 for AAA); a mark has one level, 3:1; a divider none', () => {
  assert.equal(contrastGrade(4.6, 'text'), 'AA');
  assert.equal(contrastGrade(4.4, 'text'), '不足');
  assert.equal(contrastGrade(7.2, 'text'), 'AAA');
  assert.equal(contrastGrade(3.1, 'graphic'), '达标');
  assert.equal(contrastGrade(12, 'graphic'), '达标');
  assert.equal(contrastGrade(1.2, 'decorative'), '不要求');
});

test('a status label is read on a tint of its own colour', () => {
  assert.equal(tinted('#000000', '#ffffff', 0.1), '#e6e6e6');
  const plain = contrastRatio('#a15c0c', '#ffffff');
  const onTint = contrastRatio('#a15c0c', surfaceOf({ tint: 0.1 }, '#a15c0c', '#ffffff'));
  assert.ok(onTint < plain);
});

// The page reads the theme the browser applies; this guards both light and dark themes as shipped.
test('every light- and dark-theme pairing the design system lists meets its level', () => {
  const css = readFileSync(new URL('../apps/hvac-web/src/global.css', import.meta.url), 'utf8');
  const lightBlock = css.slice(css.indexOf(':root {'), css.indexOf(':root[data-theme="light"] {'));
  const darkBlock = css.slice(css.indexOf('.dark {'));
  for (const [themeName, block] of [['light', lightBlock], ['dark', darkBlock]] as const) {
    const value = (name: string): string | undefined => block.match(new RegExp(`--${name}:\\s*(#[0-9a-f]{3,6});`, 'i'))?.[1];
    for (const group of TOKEN_GROUPS) {
      for (const token of group.tokens) {
        const fg = value(token.name);
        const bg = value(token.against);
        assert.ok(fg && bg, `${themeName}: missing color token for ${token.name} on ${token.against}`);
        const ratio = contrastRatio(fg, surfaceOf(token, fg, bg));
        assert.notEqual(contrastGrade(ratio, token.use), '不足', `${themeName}: ${token.name} on ${token.against} is ${ratio.toFixed(2)}:1`);
      }
    }
  }
  assert.equal(contrastGrade(contrastRatio('#ffffff', '#e5484d'), 'text'), '不足');
  assert.equal(contrastGrade(contrastRatio('#ffffff', '#d9383a'), 'text'), 'AA');
});

test('design system check passes on the repository and rejects violations across AST, copy, and routeTree', async () => {
  const repoRoot = fileURLToPath(new URL('..', import.meta.url));
  assert.deepEqual((await validateDesignSystem(repoRoot)).errors, []);

  const sandbox = mkdtempSync(join(tmpdir(), 'hvac-ds-check-'));
  try {
    for (const rel of [
      'apps/hvac-web/components.json',
      'apps/hvac-web/src/styles/shadcn-tailwind.css',
      'scripts/shadcn-cli.mjs',
      'scripts/web-as-cast-baseline.json',
    ]) {
      cpSync(join(repoRoot, rel), join(sandbox, rel));
    }
    writeFileSync(join(sandbox, 'apps/hvac-web/index.html'), '<!doctype html><html lang="zh-CN"><head></head><body></body></html>\n');
    writeFileSync(join(sandbox, 'apps/hvac-web/src/global.css'), '@import "./styles/shadcn-tailwind.css";\n:root { --background: #fff; }\n.dark { --background: #000; }\n');
    mkdirSync(join(sandbox, 'apps/hvac-web/src/features/demo'), { recursive: true });
    writeFileSync(
      join(sandbox, 'apps/hvac-web/src/features/demo/BadInput.tsx'),
      `export const BadInput = ({ submit }: { submit: () => void }) => (\n  <Input onKeyDown={(event) => { if (event.key === 'Enter') submit(); }} />\n);\n`,
    );
    writeFileSync(
      join(sandbox, 'apps/hvac-web/src/features/demo/BadFactTile.tsx'),
      `export const BadFactTile = () => (\n  <FactTile label="累计节电" className="col-span-2">12 kWh</FactTile>\n);\n`,
    );
    writeFileSync(
      join(sandbox, 'apps/hvac-web/src/features/demo/BadPatterns.tsx'),
      [
        'export function badPatterns(raw: string) {',
        '  const x = raw as unknown as number;',
        '  void fetch("/api/v1/health");',
        '  const ws = new WebSocket("wss://example.com");',
        '  return (',
        '    <div>',
        '      <Button variant="secondary">认领</Button>',
        '      <span>分表计收</span>',
        '      <span>平台账号 整栋总表 返回执行结果</span>',
        '      {String(x) + String(ws)}',
        '    </div>',
        '  );',
        '}',
      ].join('\n') + '\n',
    );
    writeFileSync(
      join(sandbox, 'scripts/web-as-cast-baseline.json'),
      JSON.stringify({ schemaVersion: 1, policy: 'web-non-const-as-cast-ratchet', maxNonConstAsCasts: 0 }),
    );
    mkdirSync(join(sandbox, 'apps/hvac-web/src/routes'), { recursive: true });
    writeFileSync(join(sandbox, 'apps/hvac-web/src/routes/__root.tsx'), 'import { createRootRoute } from "@tanstack/react-router";\nexport const Route = createRootRoute({});\n');
    writeFileSync(join(sandbox, 'apps/hvac-web/src/routes/index.tsx'), 'import { createFileRoute } from "@tanstack/react-router";\nexport const Route = createFileRoute("/")({});\n');
    writeFileSync(join(sandbox, 'apps/hvac-web/src/routeTree.gen.ts'), '// stale route tree\n');

    const { errors } = await validateDesignSystem(sandbox);
    assert.ok(errors.some((entry: string) => entry.includes('BadInput.tsx:2 text input Enter onKeyDown must guard IME composition')));
    assert.ok(errors.some((entry: string) => entry.includes('BadFactTile.tsx:2 FactTile does not accept className; pass valueClassName')));
    assert.ok(errors.some((entry: string) => entry.includes('apps/hvac-web/index.html must declare <meta name="color-scheme"')));
    assert.ok(errors.some((entry: string) => entry.includes(':root must declare color-scheme: light dark;')));
    assert.ok(errors.some((entry: string) => entry.includes(':root[data-theme="light"] must declare color-scheme: light;')));
    assert.ok(errors.some((entry: string) => entry.includes('.dark must declare color-scheme: dark;')));
    assert.ok(errors.some((entry: string) => entry.includes('BadPatterns.tsx:2 avoid `as unknown as`')));
    assert.ok(errors.some((entry: string) => entry.includes('BadPatterns.tsx:3 feature/route modules must use gatewayRequest instead of raw fetch()')));
    assert.ok(errors.some((entry: string) => entry.includes('BadPatterns.tsx:4 feature/route modules must use src/platform/telemetry-live/ instead of new WebSocket()')));
    assert.ok(errors.some((entry: string) => entry.includes('BadPatterns.tsx:7 avoid <Button variant="secondary">')));
    assert.ok(errors.some((entry: string) => entry.includes('BadPatterns.tsx:7 UI copy uses deprecated term "认领"')));
    assert.ok(errors.some((entry: string) => entry.includes('BadPatterns.tsx:8 UI copy uses deprecated term "分表"')));
    assert.ok(errors.some((entry: string) => entry.includes('BadPatterns.tsx:8 UI copy uses deprecated term "计收"')));
    assert.equal(errors.some((entry: string) => entry.includes('BadPatterns.tsx:9')), false);
    assert.ok(errors.some((entry: string) => entry.includes('apps/hvac-web/src non-const `as` casts grew from 0 to 2')));
    assert.ok(errors.some((entry: string) => entry.includes('apps/hvac-web/src/routeTree.gen.ts is stale')));
  } finally {
    rmSync(sandbox, { recursive: true, force: true });
  }
});
