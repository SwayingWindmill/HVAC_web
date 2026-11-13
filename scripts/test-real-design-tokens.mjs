import assert from 'node:assert/strict';
import { cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { contrastGrade, contrastRatio, surfaceOf, tinted, TOKEN_GROUPS } from '../apps/hvac-web/src/features/system/design-system/design-tokens.ts';
import { validateDesignSystem } from './check-design-system.mjs';

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

// The page reads the theme the browser applies; this guards the light theme as shipped.
test('every light-theme pairing the design system lists meets its level', () => {
  const css = readFileSync(new URL('../apps/hvac-web/src/global.css', import.meta.url), 'utf8');
  const root = css.slice(css.indexOf(':root {'), css.indexOf('.dark {'));
  const value = (name) => root.match(new RegExp(`--${name}:\\s*(#[0-9a-f]{3,6});`, 'i'))?.[1];
  for (const group of TOKEN_GROUPS) {
    for (const token of group.tokens) {
      const ratio = contrastRatio(value(token.name), surfaceOf(token, value(token.name), value(token.against)));
      assert.notEqual(contrastGrade(ratio, token.use), '不足', `${token.name} on ${token.against} is ${ratio.toFixed(2)}:1`);
    }
  }
});

test('design system check passes on the repository and rejects unguarded Enter submit or missing color-scheme', async () => {
  const repoRoot = fileURLToPath(new URL('..', import.meta.url));
  assert.deepEqual((await validateDesignSystem(repoRoot)).errors, []);

  const sandbox = mkdtempSync(join(tmpdir(), 'hvac-ds-check-'));
  try {
    for (const rel of ['apps/hvac-web/components.json', 'apps/hvac-web/src/styles/shadcn-tailwind.css', 'scripts/shadcn-cli.mjs']) {
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
    const { errors } = await validateDesignSystem(sandbox);
    assert.ok(errors.some((entry) => entry.includes('BadInput.tsx:2 text input Enter onKeyDown must guard IME composition')));
    assert.ok(errors.some((entry) => entry.includes('BadFactTile.tsx:2 FactTile does not accept className; pass valueClassName')));
    assert.ok(errors.some((entry) => entry.includes('apps/hvac-web/index.html must declare <meta name="color-scheme"')));
    assert.ok(errors.some((entry) => entry.includes(':root must declare color-scheme: light dark;')));
    assert.ok(errors.some((entry) => entry.includes(':root[data-theme="light"] must declare color-scheme: light;')));
    assert.ok(errors.some((entry) => entry.includes('.dark must declare color-scheme: dark;')));
  } finally {
    rmSync(sandbox, { recursive: true, force: true });
  }
});
