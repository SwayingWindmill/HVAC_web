# Design system assets

The repository root `DESIGN.md` is the authoritative design guide. This directory contains visual previews and the migration baseline used by the automated design gate.

## Preview

Open either static file in a browser:

- `preview.html` — light theme
- `preview-dark.html` — dark theme

The previews demonstrate the intended product language: compact navigation, one-line hierarchy, low-shadow surfaces, semantic status colors, tabular operational data, independent Telemetry Point rows, and responsive degradation.

They are not production components and must not be imported by the application.

## Automated checks

```bash
npm run design:test
npm run design:check
```

`design:check` verifies:

- root `DESIGN.md` frontmatter and source-of-truth policy;
- the Linear reference remains reference-only;
- light and dark previews exist;
- TypeScript brand/status colors remain present;
- Ant Design global radii remain aligned to 8px controls and 16px cards;
- no new non-standard radius usage is added beyond the committed migration baseline.

The exception file records legacy drift so the codebase can converge incrementally. Existing exceptions may be removed without updating the baseline. Update it only after an intentional review:

```bash
node scripts/check-design-system.mjs --update-radius-baseline
```
