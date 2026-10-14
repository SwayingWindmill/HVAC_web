import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { contrastGrade, contrastRatio, surfaceOf, tinted, TOKEN_GROUPS } from '../apps/hvac-web/src/features/system/design-system/design-tokens.ts';

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
