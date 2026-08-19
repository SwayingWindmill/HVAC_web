import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { readFileSync, writeFileSync } from 'node:fs';
import test from 'node:test';

import {
  OPERATIONS_AGENT_RUNTIME_READ_TOOLS,
  OPERATIONS_AGENT_TOOL_AUTHORIZATION_TOOLS,
  OPERATIONS_AGENT_TOOL_CATALOG,
  OPERATIONS_AGENT_TOOL_CATALOG_VERSION,
} from '../benchmarks/operations-agent/generated/tool-catalog.v1.mjs';

const catalog = JSON.parse(readFileSync('contracts/operations-agent/tool-catalog.v1.json', 'utf8'));
const schema = JSON.parse(readFileSync('contracts/operations-agent/trusted-runtime-context.v1.schema.json', 'utf8'));

test('generated Operations Agent control contracts have no drift', () => {
  const result = spawnSync(process.execPath, ['scripts/generate-operations-agent-control-contracts.mjs', '--check'], {
    cwd: process.cwd(), encoding: 'utf8', windowsHide: true,
  });
  assert.equal(result.status, 0, result.stderr || result.stdout);
});

test('generated contract drift checks accept Windows checkout line endings', () => {
  const outputPath = 'apps/hvac-web/src/api/generated/operations-tool-contract.ts';
  const original = readFileSync(outputPath, 'utf8');
  try {
    writeFileSync(outputPath, original.replace(/\r\n?|\n/gu, '\r\n'), 'utf8');
    const result = spawnSync(process.execPath, ['scripts/generate-operations-agent-control-contracts.mjs', '--check'], {
      cwd: process.cwd(), encoding: 'utf8', windowsHide: true,
    });
    assert.equal(result.status, 0, result.stderr || result.stdout);
  } finally {
    writeFileSync(outputPath, original, 'utf8');
  }
});

test('runtime and authorization tools match the versioned catalog', () => {
  assert.equal(OPERATIONS_AGENT_TOOL_CATALOG_VERSION, 'operations-agent-tool-catalog/v1');
  assert.deepEqual(OPERATIONS_AGENT_RUNTIME_READ_TOOLS, [
    'registry.getSite', 'registry.listSiteAssets', 'telemetry.getCurrentSnapshot',
    'analytics.getEnergySeries', 'commands.getCapabilities',
  ]);
  assert.deepEqual(OPERATIONS_AGENT_TOOL_AUTHORIZATION_TOOLS, [
    'registry.getSite', 'registry.listSiteAssets', 'analytics.getEnergySeries',
  ]);
  assert.deepEqual(
    catalog.tools.filter((tool) => tool.runtimeReadAllowed).map((tool) => tool.logicalTool),
    OPERATIONS_AGENT_RUNTIME_READ_TOOLS,
  );
  assert.deepEqual(schema.properties.allowedReadTools.items.enum, OPERATIONS_AGENT_RUNTIME_READ_TOOLS);
});

test('benchmark catalog retains authoritative tool owners', () => {
  for (const tool of OPERATIONS_AGENT_RUNTIME_READ_TOOLS) assert.ok(Object.hasOwn(OPERATIONS_AGENT_TOOL_CATALOG, tool));
  assert.equal(OPERATIONS_AGENT_TOOL_CATALOG['registry.getSite'], 'platform-core-service');
  assert.equal(OPERATIONS_AGENT_TOOL_CATALOG['telemetry.getCurrentSnapshot'], 'telemetry-runtime-service');
  assert.equal(OPERATIONS_AGENT_TOOL_CATALOG['analytics.getEnergySeries'], 'telemetry-query-service');
  assert.equal(OPERATIONS_AGENT_TOOL_CATALOG['commands.getCapabilities'], 'command-service');
});

test('trusted runtime schema is exact and excludes untrusted content', () => {
  assert.equal(schema.additionalProperties, false);
  assert.equal(schema.properties.scope.additionalProperties, false);
  assert.equal(schema.properties.source.const, 'APPLICATION_POLICY');
  assert.equal(schema.properties.trust.const, 'TRUSTED_CONTROL');
  assert.equal(schema.properties.effectPolicy.const, 'READ_ONLY');
  assert.equal(schema.properties.scopePolicy.const, 'EXACT_INVESTIGATION_SCOPE');
  assert.equal(schema.properties.untrustedContentPolicy.const, 'EXCLUDED');
  assert.equal(schema.properties.allowedReadTools.uniqueItems, true);
});
