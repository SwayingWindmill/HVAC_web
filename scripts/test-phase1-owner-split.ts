import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import { test } from "node:test";

import { resolveDeploymentTier } from "./phase1-deployment-tier.ts";
import {
  ownerSplitImageVariables,
  validateOwnerSplitRelease,
} from "./phase1-owner-split-release.ts";

const [
  overlay,
  baseCompose,
  tiers,
  gatewaySource,
  launcher,
  genericLauncher,
  drillRunner,
  stagingEnvironment,
  productionEnvironment,
  availability,
  liveContract,
] = await Promise.all([
  readFile(
    new URL(
      "../deploy/platform/phase1/owner-split.compose.yaml",
      import.meta.url,
    ),
    "utf8",
  ),
  readFile(
    new URL("../deploy/platform/phase1/compose.yaml", import.meta.url),
    "utf8",
  ),
  readFile(
    new URL(
      "../deploy/platform/phase1/deployment-tiers.v1.json",
      import.meta.url,
    ),
    "utf8",
  ).then(JSON.parse),
  readFile(
    new URL(
      "../services/platform-gateway/cmd/platform-gateway/embedded_energy.go",
      import.meta.url,
    ),
    "utf8",
  ),
  readFile(new URL("./phase1-wsl-compose.mjs", import.meta.url), "utf8"),
  readFile(new URL("./phase1-compose.ts", import.meta.url), "utf8"),
  readFile(
    new URL("./run-phase1-owner-split-drill.ts", import.meta.url),
    "utf8",
  ),
  readFile(
    new URL(
      "../deploy/platform/phase1/environments/staging.runtime.env.example",
      import.meta.url,
    ),
    "utf8",
  ),
  readFile(
    new URL(
      "../deploy/platform/phase1/environments/production.runtime.env.example",
      import.meta.url,
    ),
    "utf8",
  ),
  readFile(
    new URL(
      "../deploy/platform/phase1/availability-tier.v1.json",
      import.meta.url,
    ),
    "utf8",
  ).then(JSON.parse),
  readFile(
    new URL(
      "../deploy/platform/phase1/owner-split-live-contract.v1.json",
      import.meta.url,
    ),
    "utf8",
  ).then(JSON.parse),
]);

const owners = [
  ["iam-owner", "./services/iam-service/cmd/iam-service"],
  ["audit-owner", "./services/audit-ledger-service/cmd/audit-ledger-service"],
  ["core-owner", "./services/platform-core-service/cmd/platform-core-service"],
  [
    "telemetry-query-owner",
    "./services/telemetry-query-service/cmd/telemetry-query-service",
  ],
  ["command-owner", "./services/command-service/cmd/command-service"],
  ["alarm-owner", "./services/alarm-service/cmd/alarm-service"],
  ["work-order-owner", "./services/work-order-service/cmd/work-order-service"],
];

test("owner-split overlay runs the existing same-version owner artifacts and keeps only notification embedded", () => {
  assert.match(overlay, /ENERGY_API_EMBEDDED_OWNERS: notification/);
  for (const [service, servicePackage] of owners) {
    assert.match(overlay, new RegExp(`^  ${service}:`, "m"));
    assert.ok(overlay.includes(`SERVICE_PACKAGE: ${servicePackage}`));
  }
  assert.equal(overlay.match(/_OWNER_CPUS:-0\.20/g)?.length, owners.length);
  const full = resolveDeploymentTier({
    contract: tiers,
    compose: baseCompose,
    tierId: "single-full",
    environment: "staging",
  });
  const ownerSplitTier = resolveDeploymentTier({
    contract: tiers,
    compose: baseCompose,
    tierId: "single-full",
    environment: "staging",
    additionalComposeDocuments: [overlay],
    additionalProfiles: ["owner-split"],
  });
  assert.ok(
    ownerSplitTier.resourceTotals.cpuLimitCores <=
      full.tier.resourceBudget.cpuCores * full.maximumCpuLimitOvercommitRatio,
  );
  assert.throws(
    () =>
      resolveDeploymentTier({
        contract: tiers,
        compose: baseCompose,
        tierId: "single-full",
        environment: "staging",
        runtimeEnvironment: { IAM_OWNER_CPUS: "100" },
        additionalComposeDocuments: [overlay],
        additionalProfiles: ["owner-split"],
      }),
    /exceed certified overcommit budget/,
  );
  assert.match(gatewaySource, /ENERGY_API_EMBEDDED_OWNERS/);
  assert.match(launcher, /--owner-split/);
  assert.match(launcher, /deploymentTier\.tier\.id !== 'single-full'/);
  assert.match(genericLauncher, /--owner-split/);
  assert.match(
    genericLauncher,
    /deploymentTier\.tier\.id !== ["']single-full["']/,
  );
  assert.doesNotMatch(overlay, /kafka|zookeeper|redpanda/i);
});

test("owner-split production artifacts are immutable and the staging drill cannot make a production claim", () => {
  for (const variable of [
    "IAM_SERVICE_IMAGE",
    "AUDIT_SERVICE_IMAGE",
    "CORE_SERVICE_IMAGE",
    "TELEMETRY_QUERY_SERVICE_IMAGE",
    "COMMAND_SERVICE_IMAGE",
    "ALARM_SERVICE_IMAGE",
    "WORK_ORDER_SERVICE_IMAGE",
  ]) {
    assert.match(
      stagingEnvironment,
      new RegExp(`^${variable}=.+@sha256:`, "m"),
    );
    assert.match(
      productionEnvironment,
      new RegExp(`^${variable}=.+@sha256:`, "m"),
    );
  }
  assert.match(drillRunner, /--confirm-staging-owner-split/);
  assert.match(drillRunner, /productionClaimAllowed: false/);
  assert.match(drillRunner, /same-version-live-contract-drill/);
  assert.match(drillRunner, /await fetch/);
  assert.match(drillRunner, /liveContractSha256/);
  assert.match(stagingEnvironment, /^PHASE1_OWNER_SPLIT_LIVE_JOURNEYS_FILE=/m);
  assert.deepEqual(Object.keys(liveContract.owners).sort(), owners.map(([owner]) => owner).sort());
  assert.equal(
    new Set(Object.values(liveContract.owners).map((route) => `${route.method} ${route.pathPattern}`)).size,
    owners.length,
  );
});

test("same-version evidence rejects a digest set outside the approved release manifest", () => {
  const runtime = Object.fromEntries(
    ownerSplitImageVariables.map((variable) => [
      variable,
      `registry.example.com/${variable.toLowerCase()}@sha256:${"a".repeat(64)}`,
    ]),
  );
  const manifest = {
    schemaVersion: 1,
    kind: "PHASE1_OWNER_SPLIT_RELEASE",
    productVersion: "0.1.0",
    productReleaseRevision: 1,
    sourceRevision: "b".repeat(40),
    images: {
      ...runtime,
      IAM_SERVICE_IMAGE: `registry.example.com/iam@sha256:${"c".repeat(64)}`,
    },
  };
  const bytes = Buffer.from(JSON.stringify(manifest));
  assert.throws(
    () =>
      validateOwnerSplitRelease({
        manifestBytes: bytes,
        expectedSha256: createHash("sha256").update(bytes).digest("hex"),
        runtime,
        productRelease: { productVersion: "0.1.0", releaseRevision: 1 },
      }),
    /IAM_SERVICE_IMAGE is not bound/,
  );
});

test("Stage 1 contract requires configuration and a live same-version drill before certification", () => {
  const stage = availability.monolithToMultiInstancePath.stages.find(
    (candidate) => candidate.id === "stage-1",
  );
  assert.equal(stage.state, "implemented-runtime-drill-required");
  assert.ok(stage.exitEvidence.includes("owner-split-compose-config-gate"));
  assert.ok(stage.exitEvidence.includes("same-version-live-contract-drill"));
});
