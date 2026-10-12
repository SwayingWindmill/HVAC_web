import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { contrastGrade, contrastRatio, TOKEN_GROUPS } from '../apps/hvac-web/src/features/system/design-system/design-tokens.ts';

test('contrast follows WCAG: black on white is 21:1, a colour on itself 1:1', () => {
  assert.equal(contrastRatio('#000000', '#ffffff'), 21);
  assert.equal(contrastRatio('#fff', '#ffffff'), 1);
  assert.ok(Math.abs(contrastRatio('#767676', '#ffffff') - 4.54) < 0.01);
});

test('text needs 4.5:1 and marks 3:1 to pass', () => {
  assert.equal(contrastGrade(4.6), 'AA');
  assert.equal(contrastGrade(4.4), '不足');
  assert.equal(contrastGrade(3.1, true), 'AA');
  assert.equal(contrastGrade(7.2), 'AAA');
});

// The page reads the theme the browser applies; this guards the light theme as shipped.
test('every light-theme pairing the design system lists meets its level', () => {
  const css = readFileSync(new URL('../apps/hvac-web/src/global.css', import.meta.url), 'utf8');
  const root = css.slice(css.indexOf(':root {'), css.indexOf('.dark {'));
  const value = (name) => root.match(new RegExp(`--${name}:\\s*(#[0-9a-f]{3,6});`, 'i'))?.[1];
  for (const group of TOKEN_GROUPS) {
    for (const token of group.tokens.filter((candidate) => !candidate.decorative)) {
      const ratio = contrastRatio(value(token.name), value(token.against));
      assert.notEqual(contrastGrade(ratio, token.large), '不足', `${token.name} on ${token.against} is ${ratio.toFixed(2)}:1`);
    }
  }
});
