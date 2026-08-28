import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { readFile, writeFile } from "node:fs/promises";
import { resolve } from "node:path";

import { parseRuntimeEnvironment } from "./phase1-deployment-tier.ts";
import { validateOwnerSplitRelease } from "./phase1-owner-split-release.ts";

const root = resolve(import.meta.dirname, "..");
const phase1Dir = resolve(root, "deploy/platform/phase1");
const runtimeEnv = resolve(
  process.env.PHASE1_ENV_FILE ||
    resolve(phase1Dir, "environments/staging.runtime.env"),
);
const outputArg = process.argv.find((arg) => arg.startsWith("--output="));
const confirmed = process.argv.includes("--confirm-staging-owner-split");
const ownerServices = [
  "iam-owner",
  "audit-owner",
  "core-owner",
  "telemetry-query-owner",
  "command-owner",
  "alarm-owner",
  "work-order-owner",
];
const [runtimeText, productRelease, liveContractBytes] = await Promise.all([
  readFile(runtimeEnv, "utf8"),
  readFile(resolve(phase1Dir, "product-release.v1.json"), "utf8").then(
    JSON.parse,
  ),
  readFile(resolve(phase1Dir, "owner-split-live-contract.v1.json")),
]);
const runtime = parseRuntimeEnvironment(runtimeText);
if (runtime.HVAC_ENV !== "staging")
  throw new Error("The owner-split live contract drill runs only in staging");
const releaseManifestPath = resolve(
  runtime.PHASE1_OWNER_SPLIT_RELEASE_MANIFEST ?? "",
);
const liveJourneysPath = resolve(
  runtime.PHASE1_OWNER_SPLIT_LIVE_JOURNEYS_FILE ?? "",
);
const release = validateOwnerSplitRelease({
  manifestBytes: await readFile(releaseManifestPath),
  expectedSha256: runtime.PHASE1_OWNER_SPLIT_RELEASE_MANIFEST_SHA256,
  runtime,
  productRelease,
});
const liveJourneyBytes = await readFile(liveJourneysPath);
const liveJourneys = JSON.parse(liveJourneyBytes.toString("utf8"));
const liveContract = JSON.parse(liveContractBytes.toString("utf8"));
if (
  liveJourneys.schemaVersion !== 1 ||
  liveJourneys.environment !== "staging"
) {
  throw new Error("Owner-split live journeys must be a staging v1 contract");
}
for (const owner of ownerServices) {
  const route = liveContract.owners?.[owner];
  const matches = liveJourneys.steps?.filter(
    (step) =>
      step.owner === owner &&
      step.method === route?.method &&
      new RegExp(route?.pathPattern ?? "a^").test(step.path),
  );
  if (matches?.length !== 1) {
    throw new Error(`Owner-split live journeys do not cover ${owner}`);
  }
}
if (!confirmed)
  throw new Error(
    "Pass --confirm-staging-owner-split to execute the staging topology drill",
  );
if (!outputArg)
  throw new Error("Pass --output=/path/to/owner-split-record.json");

const env = {
  ...process.env,
  PHASE1_ENV_FILE: runtimeEnv,
  PHASE1_DEPLOYMENT_TIER: "single-full",
};

function run(command, args, { capture = false } = {}) {
  const result = spawnSync(command, args, {
    cwd: root,
    env,
    encoding: "utf8",
    stdio: capture ? ["ignore", "pipe", "pipe"] : "inherit",
  });
  if (result.error) throw result.error;
  if (result.status !== 0) {
    throw new Error(
      `${command} ${args.join(" ")} failed${capture ? `: ${result.stderr.trim()}` : ""}`,
    );
  }
  return result.stdout ?? "";
}

run("node", [
  "--experimental-strip-types",
  resolve(root, "scripts/phase1-compose.ts"),
  "--owner-split",
  "config",
  "--quiet",
]);
run("node", [
  "--experimental-strip-types",
  resolve(root, "scripts/phase1-compose.ts"),
  "--owner-split",
  "up",
  "-d",
  "--no-build",
]);

const psText = run(
  "node",
  [
    "--experimental-strip-types",
    resolve(root, "scripts/phase1-compose.ts"),
    "--owner-split",
    "ps",
    "--format",
    "json",
    ...ownerServices,
  ],
  { capture: true },
).trim();
const statuses = psText.startsWith("[")
  ? JSON.parse(psText)
  : psText
      .split(/\r?\n/)
      .filter(Boolean)
      .map((line) => JSON.parse(line));
for (const service of ownerServices) {
  const status = statuses.find((candidate) => candidate.Service === service);
  if (!status || status.State !== "running")
    throw new Error(`${service} is not running`);
  if (status.Health && status.Health !== "healthy")
    throw new Error(`${service} is not healthy`);
}
const publicOrigin = new URL(runtime.PUBLIC_ORIGIN);
const journeyResults = [];
for (const step of liveJourneys.steps) {
  if (!ownerServices.includes(step.owner))
    throw new Error(`Unknown owner-split journey owner: ${step.owner}`);
  if (
    !["GET", "POST"].includes(step.method) ||
    typeof step.path !== "string" ||
    !step.path.startsWith("/api/v1/") ||
    !Array.isArray(step.expectedStatuses) ||
    step.expectedStatuses.some((status) => status < 200 || status >= 300)
  ) {
    throw new Error(`${step.owner} live journey contract is invalid`);
  }
  const response = await fetch(new URL(step.path, publicOrigin), {
    method: step.method,
    headers: step.headers,
    body: step.body,
    signal: AbortSignal.timeout(30_000),
  });
  if (!step.expectedStatuses?.includes(response.status)) {
    throw new Error(
      `${step.owner} live journey ${step.method} ${step.path} returned ${response.status}`,
    );
  }
  journeyResults.push({
    owner: step.owner,
    method: step.method,
    path: step.path,
    status: response.status,
  });
}

const record = {
  schemaVersion: 1,
  kind: "same-version-live-contract-drill",
  environment: "staging",
  deploymentTier: "single-full",
  topology: "owner-split",
  productionClaimAllowed: false,
  release,
  owners: statuses.map(({ Service, State, Health }) => ({
    service: Service,
    state: State,
    health: Health || null,
  })),
  liveJourneys: journeyResults,
  liveContractSha256: createHash("sha256")
    .update(liveContractBytes)
    .digest("hex"),
  liveJourneysSha256: createHash("sha256")
    .update(liveJourneyBytes)
    .digest("hex"),
  completedAt: new Date().toISOString(),
};
const output = resolve(process.cwd(), outputArg.slice("--output=".length));
await writeFile(output, `${JSON.stringify(record, null, 2)}\n`, { flag: "wx" });
console.log(`Phase 1 owner-split live contract drill passed: ${output}`);
