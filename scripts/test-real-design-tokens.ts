import assert from 'node:assert/strict';
import { cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { contrastGrade, contrastRatio, surfaceOf, tinted, TOKEN_GROUPS } from '../apps/hvac-web/src/features/system/design-system/design-tokens.ts';
import { isOneOf, isPlainRecord } from '../apps/hvac-web/src/lib/typed.ts';
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

test('typed narrowing helpers narrow plain records and literal tuples without casts', () => {
  assert.equal(isPlainRecord({ ok: true }), true);
  assert.equal(isPlainRecord(null), false);
  assert.equal(isPlainRecord(['a']), false);
  assert.equal(isPlainRecord('str'), false);

  const views = ['trend', 'tou', 'detail'] as const;
  assert.equal(isOneOf(views, 'tou'), true);
  assert.equal(isOneOf(views, 'other'), false);
  assert.equal(isOneOf(views, 123), false);
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
      'scripts/web-file-lines-baseline.json',
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
    mkdirSync(join(sandbox, 'apps/hvac-web/src/components/ui'), { recursive: true });
    writeFileSync(
      join(sandbox, 'apps/hvac-web/src/components/ui/AllowedClient.tsx'),
      `"use client";\nexport const AllowedClient = () => <div />;\n`,
    );
    writeFileSync(
      join(sandbox, 'apps/hvac-web/src/components/ui/AllowedTransition.tsx'),
      `export const AllowedTransition = () => <div className="transition-all motion-safe:animate-pulse" />;\n`,
    );
    writeFileSync(
      join(sandbox, 'apps/hvac-web/src/components/ui/BadUiMotion.tsx'),
      `export const BadUiMotion = () => <div className="animate-pulse rounded-md" />;\n`,
    );
    writeFileSync(
      join(sandbox, 'apps/hvac-web/src/features/demo/BadClient.tsx'),
      `"use client";\nexport const BadClient = ({ active }: { active: boolean }) => (\n  <Button variant={active ? 'secondary' : 'outline'}>确定</Button>\n);\n`,
    );
    writeFileSync(
      join(sandbox, 'apps/hvac-web/src/features/demo/BadWave2Guardrails.tsx'),
      [
        'import { useEffect } from "react";',
        'export function BadWave2Guardrails() {',
        '  useEffect(() => { window.addEventListener("keydown", () => {}); });',
        '  return (',
        '    <div className="real-shell-error">',
        '      <button type="button" className="transition-all">切换</button>',
        '      <span className="animate-ping" />',
        '      <span className="animate-spin" />',
        '      <div className="h-[calc(100svh-262px)]" />',
        '      <span>--</span>',
        '      <span>{"上月 --"}</span>',
        '    </div>',
        '  );',
        '}',
      ].join('\n') + '\n',
    );
    writeFileSync(
      join(sandbox, 'apps/hvac-web/src/features/demo/GoodWave2Patterns.tsx'),
      [
        'import { useEffect } from "react";',
        'export function GoodWave2Patterns({ flag }: { flag: boolean }) {',
        '  useEffect(() => {}, []);',
        '  return (',
        '    <div data-testid="real-shell-site">',
        '      <div testId="real-shell-bootstrapping" />',
        '      <button type="button" className={flag ? "transition-colors motion-safe:animate-ping" : "animate-pulse motion-reduce:animate-none"}>',
        '        确定',
        '      </button>',
        '      <div className="h-[calc(100svh-var(--header-height)-32px)] text-[var(--chart-1)]" />',
        '      <span>—</span>',
        '      <span>{"---"}</span>',
        '      <span>{"--chart-1"}</span>',
        '    </div>',
        '  );',
        '}',
      ].join('\n') + '\n',
    );
    writeFileSync(
      join(sandbox, 'apps/hvac-web/src/features/demo/BadPatterns.tsx'),
      [
        'export function badPatterns(raw: string, obj: Record<string, unknown>) {',
        '  // eslint-disable-next-line no-console',
        '  console.log(raw);',
        '  // @ts-ignore',
        '  const copy = JSON.parse(JSON.stringify(obj));',
        '  console.error(copy);',
        '  const x = raw as unknown as number;',
        '  void fetch("/api/v1/health");',
        '  const ws = new WebSocket("wss://example.com");',
        '  return (',
        '    <div>',
        '      <Button variant="secondary">认领</Button>',
        '      <span>分表计收</span>',
        '      <span>平台账号 整栋总表 返回执行结果</span>',
        '      <span>{`${raw}均`} {`没有${raw}的工单`} 本期间 Registry 服务</span>',
        '      <Button variant={raw === "secondary" ? "outline" : "ghost"}>切换</Button>',
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
    writeFileSync(
      join(sandbox, 'scripts/web-file-lines-baseline.json'),
      JSON.stringify({
        schemaVersion: 1,
        policy: 'web-file-lines-ratchet',
        maxLinesPerFile: 20,
        legacyFileCaps: {
          'apps/hvac-web/src/features/demo/TooLongLegacy.tsx': 22,
          'apps/hvac-web/src/features/demo/ShrunkLegacy.tsx': 25,
          'apps/hvac-web/src/features/demo/MissingLegacy.tsx': 25,
        },
      }),
    );
    writeFileSync(
      join(sandbox, 'apps/hvac-web/src/features/demo/TooLongNew.tsx'),
      Array.from({ length: 22 }, (_, i) => `export const n${i} = ${i};`).join('\n') + '\n',
    );
    writeFileSync(
      join(sandbox, 'apps/hvac-web/src/features/demo/TooLongLegacy.tsx'),
      Array.from({ length: 24 }, (_, i) => `export const l${i} = ${i};`).join('\n') + '\n',
    );
    writeFileSync(
      join(sandbox, 'apps/hvac-web/src/features/demo/ShrunkLegacy.tsx'),
      'export const shrunk = 1;\n',
    );
    writeFileSync(
      join(sandbox, 'apps/hvac-web/src/features/demo/BadNuqs.tsx'),
      `import { parseAsString } from 'nuqs';\nexport const badQuery = parseAsString;\n`,
    );
    mkdirSync(join(sandbox, 'apps/hvac-web/src/lib'), { recursive: true });
    writeFileSync(
      join(sandbox, 'apps/hvac-web/src/lib/parsers.ts'),
      `import { createParser } from 'nuqs/server';\nexport const allowedParser = createParser;\n`,
    );
    mkdirSync(join(sandbox, 'apps/hvac-web/src/routes'), { recursive: true });
    writeFileSync(join(sandbox, 'apps/hvac-web/src/routes/__root.tsx'), 'import { createRootRoute } from "@tanstack/react-router";\nexport const Route = createRootRoute({});\n');
    writeFileSync(join(sandbox, 'apps/hvac-web/src/routes/index.tsx'), 'import { createFileRoute } from "@tanstack/react-router";\nexport const Route = createFileRoute("/")({});\n');
    writeFileSync(join(sandbox, 'apps/hvac-web/src/routeTree.gen.ts'), '// stale route tree\n');

    const { errors } = await validateDesignSystem(sandbox);
    assert.equal(errors.some((entry: string) => entry.includes('AllowedClient.tsx')), false);
    assert.equal(errors.some((entry: string) => entry.includes('AllowedTransition.tsx')), false);
    assert.equal(errors.some((entry: string) => entry.includes('GoodWave2Patterns.tsx')), false);
    assert.equal(errors.some((entry: string) => entry.includes('apps/hvac-web/src/lib/parsers.ts')), false);
    assert.ok(errors.some((entry: string) => entry.includes('BadNuqs.tsx:1 avoid `nuqs` outside DataTable filter internals')));
    assert.ok(errors.some((entry: string) => entry.includes('TooLongNew.tsx has 22 lines (limit 20)')));
    assert.ok(errors.some((entry: string) => entry.includes('TooLongLegacy.tsx grew from 22 to 24 lines')));
    assert.ok(errors.some((entry: string) => entry.includes('stale entry for apps/hvac-web/src/features/demo/ShrunkLegacy.tsx (1 <= 20)')));
    assert.ok(errors.some((entry: string) => entry.includes('stale entry for missing/exempt file apps/hvac-web/src/features/demo/MissingLegacy.tsx')));
    assert.ok(errors.some((entry: string) => entry.includes('BadUiMotion.tsx:1 infinite animation `animate-pulse` must respect prefers-reduced-motion')));
    assert.ok(errors.some((entry: string) => entry.includes('BadWave2Guardrails.tsx:3 useEffect() must specify an explicit dependency array')));
    assert.ok(errors.some((entry: string) => entry.includes('BadWave2Guardrails.tsx:5 avoid dead legacy CSS class `real-shell-*`')));
    assert.ok(errors.some((entry: string) => entry.includes('BadWave2Guardrails.tsx:6 avoid `transition-all`')));
    assert.ok(errors.some((entry: string) => entry.includes('BadWave2Guardrails.tsx:7 infinite animation `animate-ping` must respect prefers-reduced-motion')));
    assert.ok(errors.some((entry: string) => entry.includes('BadWave2Guardrails.tsx:8 infinite animation `animate-spin` must respect prefers-reduced-motion')));
    assert.ok(errors.some((entry: string) => entry.includes('BadWave2Guardrails.tsx:9 avoid hardcoded viewport height subtraction `calc(100svh-262px)`')));
    assert.ok(errors.some((entry: string) => entry.includes('BadWave2Guardrails.tsx:10 UI placeholder uses ASCII `--`')));
    assert.ok(errors.some((entry: string) => entry.includes('BadWave2Guardrails.tsx:11 UI placeholder uses ASCII `--`')));
    assert.ok(errors.some((entry: string) => entry.includes('BadClient.tsx:1 avoid Next.js "use client" directives')));
    assert.ok(errors.some((entry: string) => entry.includes('BadClient.tsx:3 avoid <Button variant="secondary">')));
    assert.ok(errors.some((entry: string) => entry.includes('BadInput.tsx:2 text input Enter onKeyDown must guard IME composition')));
    assert.ok(errors.some((entry: string) => entry.includes('BadFactTile.tsx:2 FactTile does not accept className; pass valueClassName')));
    assert.ok(errors.some((entry: string) => entry.includes('apps/hvac-web/index.html must declare <meta name="color-scheme"')));
    assert.ok(errors.some((entry: string) => entry.includes('apps/hvac-web/index.html <head> must include the pre-paint hvac-theme-mode bootstrap script toggling the dark class to prevent theme FOUC')));
    assert.ok(errors.some((entry: string) => entry.includes(':root must declare color-scheme: light dark;')));
    assert.ok(errors.some((entry: string) => entry.includes(':root[data-theme="light"] must declare color-scheme: light;')));
    assert.ok(errors.some((entry: string) => entry.includes('.dark must declare color-scheme: dark;')));
    assert.ok(errors.some((entry: string) => entry.includes('BadPatterns.tsx:2 avoid @ts-ignore, @ts-nocheck, and eslint-disable comments')));
    assert.ok(errors.some((entry: string) => entry.includes('BadPatterns.tsx:3 avoid console.log()')));
    assert.ok(errors.some((entry: string) => entry.includes('BadPatterns.tsx:4 avoid @ts-ignore, @ts-nocheck, and eslint-disable comments')));
    assert.ok(errors.some((entry: string) => entry.includes('BadPatterns.tsx:5 avoid JSON.parse(JSON.stringify(...))')));
    assert.equal(errors.some((entry: string) => entry.includes('BadPatterns.tsx:6')), false);
    assert.ok(errors.some((entry: string) => entry.includes('BadPatterns.tsx:7 avoid `as unknown as`')));
    assert.ok(errors.some((entry: string) => entry.includes('BadPatterns.tsx:8 feature/route modules must use gatewayRequest instead of raw fetch()')));
    assert.ok(errors.some((entry: string) => entry.includes('BadPatterns.tsx:9 feature/route modules must use src/platform/telemetry-live/ instead of new WebSocket()')));
    assert.ok(errors.some((entry: string) => entry.includes('BadPatterns.tsx:12 avoid <Button variant="secondary">')));
    assert.ok(errors.some((entry: string) => entry.includes('BadPatterns.tsx:12 UI copy uses deprecated term "认领"')));
    assert.ok(errors.some((entry: string) => entry.includes('BadPatterns.tsx:13 UI copy uses deprecated term "分表"')));
    assert.ok(errors.some((entry: string) => entry.includes('BadPatterns.tsx:13 UI copy uses deprecated term "计收"')));
    assert.ok(errors.some((entry: string) => entry.includes('BadPatterns.tsx:15 UI copy uses template concatenation "${...}均"')));
    assert.ok(errors.some((entry: string) => entry.includes('BadPatterns.tsx:15 UI copy uses template concatenation "没有${...}的工单"')));
    assert.ok(errors.some((entry: string) => entry.includes('BadPatterns.tsx:15 UI copy uses deprecated term "本期间"')));
    assert.ok(errors.some((entry: string) => entry.includes('BadPatterns.tsx:15 UI copy uses deprecated term "Registry "')));
    assert.equal(errors.some((entry: string) => entry.includes('BadPatterns.tsx:14')), false);
    assert.equal(errors.some((entry: string) => entry.includes('BadPatterns.tsx:16')), false);
    assert.ok(errors.some((entry: string) => entry.includes('apps/hvac-web/src non-const `as` casts grew from 0 to 2')));
    assert.ok(errors.some((entry: string) => entry.includes('apps/hvac-web/src/routeTree.gen.ts is stale')));

    writeFileSync(
      join(sandbox, 'scripts/web-as-cast-baseline.json'),
      JSON.stringify({ schemaVersion: 1, policy: 'web-non-const-as-cast-ratchet', maxNonConstAsCasts: 5 }),
    );
    const { errors: slackErrors } = await validateDesignSystem(sandbox);
    assert.ok(slackErrors.some((entry: string) => entry.includes('non-const `as` casts dropped from 5 to 2; tighten scripts/web-as-cast-baseline.json')));
  } finally {
    rmSync(sandbox, { recursive: true, force: true });
  }
});
