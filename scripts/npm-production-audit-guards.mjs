const scriptReferencePattern = /\bnpm\s+run\s+([A-Za-z0-9:_-]+)/g;

const forbiddenBuildMarkers = [
  '--ssr',
  'react-router build',
  'react-router dev',
  'remix build',
  '@react-router/dev',
  '@react-router/serve',
];

export const reactRouterServerMarkers = Object.freeze([
  'createStaticRouter',
  'ServerRouter',
  'HydratedRouter',
  'createRequestHandler',
  'unstable_RSC',
  '@react-router/node',
  '@react-router/serve',
  'react-router/dom-export',
  'react-router/server',
]);

function referencedScripts(command) {
  return [...command.matchAll(scriptReferencePattern)].map((match) => match[1]);
}

export function expandNpmScriptGraph(scripts, entryName) {
  const expanded = [];
  const visiting = new Set();

  function visit(name) {
    if (visiting.has(name)) throw new Error(`npm script cycle detected while evaluating ${entryName}: ${name}`);
    const command = scripts?.[name];
    if (typeof command !== 'string' || !command.trim()) return;

    visiting.add(name);
    expanded.push({ name, command });
    for (const referenced of referencedScripts(command)) visit(referenced);
    visiting.delete(name);
  }

  visit(entryName);
  return expanded;
}

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

function executesHvacWebViteBuild(command) {
  return /(?:^|&&|\|\||;)\s*(?:[^\s]+[\\/])?vite(?:\.cmd)?\s+build\s+apps[\\/]hvac-web(?:\s|$)/.test(command);
}

function assertClientOnlyViteBuild(scriptName, graph) {
  assert(graph.length > 0, `React Router exception requires npm script ${scriptName}`);
  assert(
    graph.some(({ command }) => executesHvacWebViteBuild(command)),
    `React Router exception requires ${scriptName} to execute the HVAC Web Vite build`,
  );
  for (const { name, command } of graph) {
    for (const marker of forbiddenBuildMarkers) {
      assert(!command.includes(marker), `React Router exception invalidated by server build marker in ${name}: ${marker}`);
    }
  }
}

export function verifyReactRouterClientOnlyViteSpa({
  scripts,
  compatibilityEntry,
  demoEntry,
  source,
}) {
  assertClientOnlyViteBuild('build', expandNpmScriptGraph(scripts, 'build'));
  assertClientOnlyViteBuild('build:demo', expandNpmScriptGraph(scripts, 'build:demo'));
  assertClientOnlyViteBuild('build:real', expandNpmScriptGraph(scripts, 'build:real'));

  assert(
    compatibilityEntry.includes("import './demo/main'"),
    'React Router exception requires the compatibility entry to remain browser-only Demo delegation',
  );
  assert(demoEntry.includes('BrowserRouter'), 'React Router exception requires BrowserRouter in the Demo browser entry');

  for (const marker of reactRouterServerMarkers) {
    assert(!source.includes(marker), `React Router exception invalidated by server/RSC marker: ${marker}`);
  }
}
