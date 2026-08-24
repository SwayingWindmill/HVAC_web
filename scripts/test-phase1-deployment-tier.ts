import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";

import {
  parseRuntimeEnvironment,
  resolveDeploymentTier,
} from "./phase1-deployment-tier.ts";

const [contract, compose] = await Promise.all([
  readFile(
    new URL(
      "../deploy/platform/phase1/deployment-tiers.v1.json",
      import.meta.url,
    ),
    "utf8",
  ).then(JSON.parse),
  readFile(
    new URL("../deploy/platform/phase1/compose.yaml", import.meta.url),
    "utf8",
  ),
]);

test("demo tier renders an explicit core profile inside its certified host budget", () => {
  const resolved = resolveDeploymentTier({
    contract,
    compose,
    tierId: "demo",
    environment: "testing",
  });

  assert.deepEqual(resolved.profiles, ["observability-core"]);
  assert.equal(resolved.environment.PHASE1_DEPLOYMENT_TIER, "demo");
  assert.equal(
    resolved.environment.PHASE1_OBSERVABILITY_PROFILE,
    "observability-core",
  );
  assert.ok(resolved.resourceTotals.memoryGiB <= 8);
  assert.ok(
    resolved.resourceTotals.cpuLimitCores <=
      4 * resolved.maximumCpuLimitOvercommitRatio,
  );
});

test("single-lite is the production default and applies the reviewed resource overrides", () => {
  const runtime = parseRuntimeEnvironment(
    "HVAC_ENV=production\nPHASE1_DEPLOYMENT_TIER=single-lite\n",
  );
  const resolved = resolveDeploymentTier({
    contract,
    compose,
    tierId: runtime.PHASE1_DEPLOYMENT_TIER,
    environment: runtime.HVAC_ENV,
  });

  assert.deepEqual(resolved.profiles, ["observability-logs"]);
  assert.equal(resolved.environment.CLICKHOUSE_CPUS, "2");
  assert.equal(resolved.environment.CLICKHOUSE_MEMORY_LIMIT, "2048m");
  assert.equal(
    resolved.environment.CLICKHOUSE_SERVER_MEMORY_USAGE,
    "1610612736",
  );
  assert.ok(resolved.resourceTotals.memoryGiB <= 16);
});

test("a non-production tier cannot be selected for production", () => {
  assert.throws(
    () =>
      resolveDeploymentTier({
        contract,
        compose,
        tierId: "demo",
        environment: "production",
      }),
    /not allowed for production/,
  );
});

test("unknown tiers fail closed", () => {
  assert.throws(
    () =>
      resolveDeploymentTier({
        contract,
        compose,
        tierId: "missing",
        environment: "testing",
      }),
    /Unknown Phase 1 deployment tier/,
  );
});

test("effective runtime resource overrides are included in the certified budget", () => {
  assert.throws(
    () =>
      resolveDeploymentTier({
        contract,
        compose,
        tierId: "single-full",
        environment: "staging",
        runtimeEnvironment: { ENERGY_API_CPUS: "100" },
      }),
    /exceed certified overcommit budget/,
  );
});
