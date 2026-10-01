#!/usr/bin/env node

import { readFileSync, readdirSync, statSync } from 'node:fs';
import path from 'node:path';

const ROOT = process.cwd();
const args = process.argv.slice(2);
const json = args.includes('--json');
const targets = args.filter((arg) => !arg.startsWith('--'));
const EXTENSIONS = new Set(['.css', '.scss', '.sass', '.less', '.tsx', '.jsx', '.ts', '.js', '.html']);
const findings = [];
const advisory = [];

function relative(file) {
  return path.relative(ROOT, file).replaceAll('\\', '/');
}

function lineOf(text, index) {
  return text.slice(0, index).split('\n').length;
}

function add(bucket, file, text, match, ruleId, message) {
  bucket.push({
    ruleId,
    file: relative(file),
    line: lineOf(text, match.index ?? 0),
    message,
  });
}

function scanCss(file, text) {
  const ruleRe = /([^{}]+)\{([^{}]*)\}/gms;
  for (const match of text.matchAll(ruleRe)) {
    const selector = match[1].trim();
    const body = match[2];

    if (/\.ant-card-body\b/.test(selector)) {
      const beforeBody = selector.split(/\.ant-card-body\b/)[0];
      const broadDescendant = /\s$/.test(beforeBody) || /\s[^>]*$/.test(beforeBody);
      const parentLooksLikeCard = /card\b/i.test(beforeBody);
      if (broadDescendant && parentLooksLikeCard && !/>\s*$/.test(beforeBody)) {
        add(
          findings,
          file,
          text,
          match,
          'nested-ant-card-body-descendant',
          `Broad descendant selector "${selector.replace(/\s+/g, ' ')}" can leak structural Card-body styles into nested Ant Cards. Prefer a direct-child or more specific structural scope.`,
        );
      }
    }

    if (/transition\s*:\s*all\b/i.test(body)) {
      add(findings, file, text, match, 'transition-all', 'Avoid transition: all; transition only the properties that are intentional UI feedback.');
    }

    if (/(?:linear|radial|conic)-gradient\s*\(/i.test(body)) {
      add(advisory, file, text, match, 'decorative-gradient', 'Gradient detected. HVAC Operate surfaces should use gradients only when the approved reference/product meaning requires them.');
    }

    if (/backdrop-filter\s*:/i.test(body)) {
      add(advisory, file, text, match, 'glassmorphism', 'backdrop-filter detected. Avoid glassmorphism on operator surfaces unless a specific overlay interaction justifies it.');
    }

    if (/outline\s*:\s*(?:none|0)\b/i.test(body) && !/:focus-visible|:focus-within|:focus\b/i.test(selector)) {
      add(findings, file, text, match, 'focus-outline-suppressed', 'Focus outline is suppressed outside an explicit focus-state rule. Preserve a visible keyboard focus treatment.');
    }

    const tiny = /font-size\s*:\s*(9|10)px\b/i.exec(body);
    if (tiny) {
      add(advisory, file, text, match, 'tiny-operational-text', `${tiny[1]}px text detected. Verify it is truly tertiary metadata and remains readable at the rendered target viewport.`);
    }

    const shadow = /box-shadow\s*:\s*([^;]+)/i.exec(body);
    if (shadow && !/^\s*(?:none|inset\b)/i.test(shadow[1])) {
      const px = [...shadow[1].matchAll(/(-?\d+(?:\.\d+)?)px/g)].map((item) => Math.abs(Number(item[1])));
      if (px.some((value) => value >= 16)) {
        add(advisory, file, text, match, 'high-elevation-shadow', 'Large shadow detected. Level-2/3 elevation should be reserved for overlays/confirmation layers, not ordinary operator cards.');
      }
    }
  }
}

function scanMarkup(file, text) {
  for (const match of text.matchAll(/<img\b(?![^>]*\balt=)[^>]*>/gms)) {
    add(findings, file, text, match, 'image-missing-alt', 'Image has no alt attribute. Use meaningful alt text or alt="" for decorative images.');
  }

  for (const match of text.matchAll(/<(?:div|span)\b[^>]*\bonClick=\{[^}]+\}[^>]*>/gms)) {
    const tag = match[0];
    if (!/\brole=["']button["']/.test(tag) && !/\btabIndex=/.test(tag)) {
      add(advisory, file, text, match, 'nonsemantic-click-target', 'Non-semantic element has onClick without an obvious keyboard/button semantic in the same opening tag. Verify keyboard access or use a native/Ant button control.');
    }
  }
}

function scanFile(file) {
  if (!EXTENSIONS.has(path.extname(file).toLowerCase())) return;
  let text;
  try {
    text = readFileSync(file, 'utf8');
  } catch {
    return;
  }
  if (/\.(?:css|scss|sass|less)$/i.test(file)) scanCss(file, text);
  else scanMarkup(file, text);
}

function walk(target) {
  const absolute = path.resolve(ROOT, target);
  let stat;
  try {
    stat = statSync(absolute);
  } catch {
    return;
  }
  if (stat.isFile()) {
    scanFile(absolute);
    return;
  }
  if (!stat.isDirectory()) return;
  for (const entry of readdirSync(absolute, { withFileTypes: true })) {
    if (['node_modules', 'dist', 'out', '.git', '.worktrees'].includes(entry.name)) continue;
    const child = path.join(absolute, entry.name);
    if (entry.isDirectory()) walk(child);
    else scanFile(child);
  }
}

for (const target of targets.length > 0 ? targets : ['apps/hvac-web/src']) walk(target);

const payload = {
  engine: 'hvac-impeccable-adapter',
  upstream: 'pbakaus/impeccable',
  findings,
  advisory,
  counts: { findings: findings.length, advisory: advisory.length },
};

if (json) {
  process.stdout.write(`${JSON.stringify(payload, null, 2)}\n`);
} else {
  for (const item of findings) console.log(`ERROR ${item.ruleId} ${item.file}:${item.line} ${item.message}`);
  for (const item of advisory) console.log(`ADVISORY ${item.ruleId} ${item.file}:${item.line} ${item.message}`);
  console.log(`${findings.length} blocking-quality finding(s), ${advisory.length} advisory finding(s).`);
}

process.exit(findings.length > 0 ? 2 : 0);
