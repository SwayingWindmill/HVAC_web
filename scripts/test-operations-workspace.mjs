import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { build } from 'esbuild';

async function loadBundledModule(entryPoint) {
  const result = await build({
    entryPoints: [entryPoint],
    bundle: true,
    platform: 'node',
    format: 'esm',
    write: false,
    logLevel: 'silent',
  });
  const source = result.outputFiles[0].text;
  return import(`data:text/javascript;base64,${Buffer.from(source).toString('base64')}`);
}

const loadContract = () => loadBundledModule('apps/hvac-web/src/api/operations-contract.ts');

const investigation = {
  schemaVersion: 1,
  id: 'investigation-001',
  scope: {
    organizationId: 'organization-001',
    siteId: 'site-001',
    equipmentId: null,
    deviceId: null,
  },
  status: 'COMPLETED',
  revision: 9,
  createdAt: 1,
  activeRun: null,
  outcome: 'SUPPORTED_SITE_FINDING',
  evidence: [],
  analysisReferences: [],
  findings: [],
};
const plan = {
  schemaVersion: 1,
  id: 'site-night-energy-investigation',
  label: 'Site night-energy investigation',
  completedSteps: 4,
  totalSteps: 4,
  progressPercent: 100,
  steps: [
    { id: 'READ_SITE_CONTEXT', label: 'Read authoritative Site context', status: 'COMPLETED' },
    { id: 'READ_ENERGY_SERIES', label: 'Read authoritative night-energy periods', status: 'COMPLETED' },
    { id: 'ANALYZE', label: 'Run deterministic night-energy analysis', status: 'COMPLETED' },
    { id: 'COMMIT_RESULT', label: 'Commit Evidence, Analysis and Finding', status: 'COMPLETED' },
  ],
};

function event(id, type, payload) {
  return `id: ${id}\nevent: ${type}\ndata: ${JSON.stringify({ type, ...payload })}\n\n`;
}

const validStream = [
  event('9:0', 'RUN_STARTED', { threadId: investigation.id, runId: 'run-001' }),
  event('9:1', 'STATE_SNAPSHOT', {
    snapshot: {
      schemaVersion: 'operations-investigation-ui/v1',
      investigation,
      plan,
      toolActivities: [],
    },
  }),
  event('9:2', 'RUN_FINISHED', {
    threadId: investigation.id,
    runId: 'run-001',
    outcome: { type: 'success' },
  }),
].join('');

test('Operations Workspace parser accepts the bounded committed event lifecycle', async () => {
  const { parseOperationsAgUiEventStream } = await loadContract();
  const parsed = parseOperationsAgUiEventStream(validStream);
  assert.deepEqual(parsed.map((item) => item.event.type), [
    'RUN_STARTED', 'STATE_SNAPSHOT', 'RUN_FINISHED',
  ]);
  assert.equal(parsed[1].event.snapshot.plan.progressPercent, 100);
  assert.equal(parsed[1].event.snapshot.investigation.revision, 9);
});

test('Operations Workspace parser rejects internal state and arbitrary Tool payloads', async () => {
  const { parseOperationsAgUiEventStream } = await loadContract();
  const unsafeState = validStream.replace(
    '"findings":[]',
    '"findings":[],"checkpoint":{"opaqueState":"secret"}',
  );
  assert.throws(() => parseOperationsAgUiEventStream(unsafeState), /forbidden field checkpoint/u);

  const unsafeTool = validStream.replace(
    event('9:2', 'RUN_FINISHED', {
      threadId: investigation.id,
      runId: 'run-001',
      outcome: { type: 'success' },
    }),
    event('9:2', 'TOOL_CALL_ARGS', {
      toolCallId: 'receipt-001',
      delta: JSON.stringify({
        recordId: 'receipt-001',
        logicalTool: 'registry.getSite',
        owner: 'registry',
        resultCategory: 'SUCCEEDED',
        startedAt: 1,
        completedAt: 2,
        metadata: { raw: true },
      }),
    }) + event('9:3', 'RUN_FINISHED', {
      threadId: investigation.id,
      runId: 'run-001',
      outcome: { type: 'success' },
    }),
  );
  assert.throws(() => parseOperationsAgUiEventStream(unsafeTool), /forbidden field metadata/u);

  const outOfOrder = validStream.replace('id: 9:1', 'id: 9:2');
  assert.throws(() => parseOperationsAgUiEventStream(outOfOrder), /identity is invalid/u);
  const crossRevision = validStream.replace('id: 9:2', 'id: 10:2');
  assert.throws(() => parseOperationsAgUiEventStream(crossRevision), /identity is invalid/u);
});

test('scoped Operations API accepts the authorized stream and rejects a mismatched Organization', async () => {
  const { streamSiteNightEnergyInvestigationEvents } = await loadBundledModule('apps/hvac-web/src/api/operations.ts');
  const requests = [];
  const fetchImplementation = async (input, init) => {
    requests.push({ input: String(input), init });
    return new Response(validStream, {
      status: 200,
      headers: { 'Content-Type': 'text/event-stream; charset=utf-8' },
    });
  };
  const events = await streamSiteNightEnergyInvestigationEvents(investigation.id, {
    trustedOrganizationId: investigation.scope.organizationId,
    trustedSiteId: investigation.scope.siteId,
    fetchImplementation,
  });
  assert.equal(events.length, 3);
  assert.equal(requests[0].input, '/api/v1/sites/site-001/operations/investigations/investigation-001/events');
  assert.equal(requests[0].init.credentials, 'same-origin');
  assert.equal(requests[0].init.headers.Accept, 'text/event-stream, application/problem+json');

  await assert.rejects(
    streamSiteNightEnergyInvestigationEvents(investigation.id, {
      trustedOrganizationId: 'organization-other',
      trustedSiteId: investigation.scope.siteId,
      fetchImplementation,
    }),
    /超出当前已验证 Site Scope/u,
  );
});

test('Real Site shell resolves a URL Operations route backed by CopilotKit Headless', async () => {
  const routingModule = await loadBundledModule('apps/hvac-web/src/real/site-routing.ts');
  const site = {
    id: '0198f5c0-7c00-7000-8000-000000000002',
    owningOrganizationId: '0198f5c0-7c00-7000-8000-000000000001',
    code: 'SITE-001',
    displayName: 'Authorized Site',
    timezone: 'Asia/Shanghai',
    status: 'ACTIVE',
    revision: 1,
    createdAt: '2026-07-31T00:00:00.000',
    updatedAt: '2026-07-31T00:00:00.000',
  };
  const path = routingModule.siteRoute(site, 'operations');
  assert.equal(path, `/sites/${site.id}/operations`);
  assert.deepEqual(
    routingModule.resolveSiteRouting(path, [site], ['site.read']),
    {
      state: 'READY',
      route: 'operations',
      context: { site, actingOrganizationId: site.owningOrganizationId },
    },
  );
  assert.deepEqual(routingModule.resolveSiteRouting(path, [site], []), { state: 'FORBIDDEN' });

  const [shell, workspace, agent] = await Promise.all([
    readFile('apps/hvac-web/src/real/SiteScopedShell.tsx', 'utf8'),
    readFile('apps/hvac-web/src/real/OperationsInvestigation.tsx', 'utf8'),
    readFile('apps/hvac-web/src/real/operations/OperationsInvestigationAgent.ts', 'utf8'),
  ]);
  assert.match(shell, /siteRoute\(site, 'operations'\)/u);
  assert.match(shell, /<OperationsInvestigation/u);
  assert.match(workspace, /<CopilotKit/u);
  assert.match(workspace, /registerProtectedResource/u);
  assert.match(agent, /streamSiteNightEnergyInvestigationEvents/u);
  assert.doesNotMatch(workspace, /HvacMockAgent|readAiSnapshot|mock telemetry/iu);
});
