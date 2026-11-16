# HTML Report Format

The architectural review is rendered as a single self-contained HTML file under `.scratch/architecture-review-<timestamp>.html` (never inside `apps/hvac-web/`). Tailwind and Mermaid both come from CDNs. Mermaid handles graph-shaped diagrams reliably; hand-built divs and inline SVG handle editorial visuals (mass diagrams, cross-sections). Mix the two rather than using Mermaid for everything.

## Scaffold

```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <title>Architecture review for {{repo name}}</title>
    <script src="https://cdn.tailwindcss.com"></script>
    <script type="module">
      import mermaid from "https://cdn.jsdelivr.net/npm/mermaid@11/dist/mermaid.esm.min.mjs";
      mermaid.initialize({ startOnLoad: true, theme: "neutral", securityLevel: "loose" });
    </script>
    <style>
      .seam { stroke-dasharray: 4 4; }
      .leak { stroke: #dc2626; }
      .deep { background: linear-gradient(135deg, #0f172a, #1e293b); }
    </style>
  </head>
  <body class="bg-stone-50 text-slate-900 font-sans">
    <main class="max-w-5xl mx-auto px-6 py-12 space-y-12">
      <header>...</header>
      <section id="candidates" class="space-y-10">...</section>
      <section id="top-recommendation">...</section>
    </main>
  </body>
</html>
```

## Header

Repo name, date, and a compact legend: solid box = module, dashed line = seam, red arrow = leakage, thick dark box = deep module. No introduction paragraph. Straight into the candidates.

## Candidate card

The diagrams carry the weight. Prose is sparse, plain, and uses `references/deep-module-lens.md` and `GLOSSARY.md` terms without ceremony.

Each candidate is one `<article>`:

- **Title**: short, names the deepening (e.g. "Collapse the telemetry query wrappers").
- **Badge row**: recommendation strength (`Strong` = emerald, `Worth exploring` = amber, `Speculative` = slate), plus dependency category (`in-process`, `local-substitutable`, `ports & adapters`, `mock`).
- **Files**: monospaced list, `font-mono text-sm`.
- **Before / After diagram**: the centrepiece, two columns side by side.
- **Problem**: one sentence on what hurts.
- **Solution**: one sentence on what changes.
- **Wins**: short bullets (under ~8 words) naming **locality**, **leverage**, or shrunk **interface** surface.
- **ADR callout** (if applicable): one line in an amber-tinted box.

If a diagram needs a paragraph to be understood, redraw the diagram.

## Diagram patterns

Mix patterns across cards so every diagram fits what it shows:

- **Mermaid graph**: `flowchart` or `sequenceDiagram` for call chains, round-trip reductions, and dependency leaks (`classDef leak stroke:#dc2626,stroke-width:2px;`).
- **Hand-built boxes-and-arrows**: `<div>` modules with SVG arrows when showing faded internals inside one thick-bordered deep module.
- **Cross-section**: stacked horizontal bands (`h-12 border-l-4`) contrasting many thin pass-through layers against one consolidated deep band.
- **Mass diagram**: two rectangles per module (interface surface area vs implementation mass) contrasting shallow (wide/tall interface) vs deep (short interface, tall implementation).
- **Call-graph collapse**: nested call boxes collapsed into one module with internal calls faded inside.

## Style guidance

- Lean editorial, generous whitespace, restrained accent palette (emerald/indigo + red leakage + amber warnings).
- Keep before/after diagrams ~320px tall so they fit side by side without excess scrolling.
- Static HTML only beyond Tailwind CDN and Mermaid ESM import.

## Top recommendation section

One larger card naming the candidate to tackle first, one sentence on why, and an anchor link to its card.
