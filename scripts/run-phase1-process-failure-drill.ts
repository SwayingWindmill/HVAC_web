import { spawnSync } from "node:child_process";
import { readFile, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
import process from "node:process";

import {
  parseRuntimeEnvironment,
  resolveDeploymentTier,
} from "./phase1-deployment-tier.ts";
import { buildProcessFailurePlan } from "./phase1-process-failure-plan.ts";

const root = resolve(import.meta.dirname, "..");
const phase1Dir = resolve(root, "deploy/platform/phase1");
const runtimeEnv = resolve(
  process.env.PHASE1_ENV_FILE ||
    resolve(phase1Dir, "environments/staging.runtime.env"),
);
const outputArg = process.argv.find((arg) => arg.startsWith("--output="));
const planOnly = process.argv.includes("--plan");
const confirmed = process.argv.includes("--confirm-staging-process-failure");

const [runtimeText, contract, compose, manifest, productRelease] =
  await Promise.all([
    readFile(runtimeEnv, "utf8"),
    readFile(resolve(phase1Dir, "deployment-tiers.v1.json"), "utf8").then(
      JSON.parse,
    ),
    readFile(resolve(phase1Dir, "compose.yaml"), "utf8"),
    readFile(
      resolve(phase1Dir, "recovery/process-failure-scenarios.v1.json"),
      "utf8",
    ).then(JSON.parse),
    readFile(resolve(phase1Dir, "product-release.v1.json"), "utf8").then(
      JSON.parse,
    ),
  ]);
const runtime = parseRuntimeEnvironment(runtimeText);
const tier = resolveDeploymentTier({
  contract,
  compose,
  tierId: process.env.PHASE1_DEPLOYMENT_TIER || runtime.PHASE1_DEPLOYMENT_TIER,
  environment: runtime.HVAC_ENV,
  runtimeEnvironment: { ...runtime, ...process.env },
});
const plan = buildProcessFailurePlan(manifest, {
  environment: runtime.HVAC_ENV,
});

if (planOnly) {
  console.log(
    JSON.stringify(
      { environment: runtime.HVAC_ENV, tier: tier.tier.id, plan },
      null,
      2,
    ),
  );
  process.exit(0);
}
if (!confirmed)
  throw new Error(
    "Pass --confirm-staging-process-failure to execute the destructive staging drill",
  );
if (!outputArg)
  throw new Error("Pass --output=/path/to/process-failure-record.json");

const env = {
  ...process.env,
  COMPOSE_PROFILES: "",
  ...tier.environment,
  PHASE1_ENV_FILE: runtimeEnv,
};
const composePrefix = [
  "compose",
  ...tier.profiles.flatMap((profile) => ["--profile", profile]),
  "--env-file",
  runtimeEnv,
  "-f",
  resolve(phase1Dir, "compose.yaml"),
];

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

function composeCommand(args, options) {
  return run("docker", [...composePrefix, ...args], options);
}

function serviceStatus(target) {
  const output = composeCommand(["ps", "--format", "json", target], {
    capture: true,
  }).trim();
  if (!output) return null;
  const parsed = output.startsWith("[")
    ? JSON.parse(output)
    : output
        .split(/\r?\n/)
        .filter(Boolean)
        .map((line) => JSON.parse(line));
  return parsed[0] ?? null;
}

const activeReadinessUrls = {
  "energy-api": "http://127.0.0.1:19080/health/ready",
  scheduler: "http://127.0.0.1:19092/health/ready",
  "telemetry-worker": "http://127.0.0.1:19086/health/ready",
  "metric-worker": "http://127.0.0.1:19090/health/ready",
  "iot-service": "http://127.0.0.1:19094/health/ready",
};

function activeReadinessPassed(target) {
  const url = activeReadinessUrls[target];
  if (!url) return true;
  const result = spawnSync(
    "docker",
    [...composePrefix, "exec", "-T", target, "/healthcheck", url],
    { cwd: root, env, stdio: "ignore" },
  );
  if (result.error) throw result.error;
  return result.status === 0;
}

async function waitForRecovery(target, deadline) {
  while (Date.now() <= deadline) {
    const status = serviceStatus(target);
    const containerReady =
      status?.Health === "healthy" ||
      (!status?.Health && status?.State === "running");
    if (containerReady && activeReadinessPassed(target)) return status;
    await new Promise((resolveWait) => setTimeout(resolveWait, 2000));
  }
  throw new Error(`${target} did not recover before the governed deadline`);
}

async function waitForReadinessFailure(target, deadline) {
  while (Date.now() <= deadline) {
    if (!activeReadinessPassed(target)) return;
    await new Promise((resolveWait) => setTimeout(resolveWait, 1000));
  }
  throw new Error(`${target} did not observe the governed dependency failure`);
}

const startedAt = new Date();
const results = [];
for (const step of plan) {
  const incidentConfirmedAt = new Date();
  const deadline =
    incidentConfirmedAt.getTime() + step.maximumRecoverySeconds * 1000;
  composeCommand(["kill", "--signal", step.killSignal, step.target]);
  if (step.target === "mqtt-broker") {
    await waitForReadinessFailure(
      "iot-service",
      Math.min(deadline, Date.now() + 60_000),
    );
  }
  composeCommand(["up", "-d", "--no-deps", step.target]);
  await waitForRecovery(step.target, deadline);
  const probeStates = [];
  for (const probeService of step.recoveryProbeServices) {
    const status = await waitForRecovery(probeService, deadline);
    probeStates.push({
      service: probeService,
      state: status.State,
      health: status.Health || null,
    });
  }
  const recoveredAt = new Date();
  results.push({
    ...step,
    incidentConfirmedAt: incidentConfirmedAt.toISOString(),
    recoveredAt: recoveredAt.toISOString(),
    actualRecoverySeconds: Math.ceil(
      (recoveredAt - incidentConfirmedAt) / 1000,
    ),
    recoveryProbes: probeStates,
  });
}

for (const script of productRelease.criticalSmoke ?? [])
  run("npm", ["run", script]);

const record = {
  schemaVersion: 1,
  kind: "PHASE1_STAGING_PROCESS_FAILURE",
  environment: "staging",
  deploymentTier: tier.tier.id,
  productionClaimAllowed: false,
  containerOnlyEvidenceSufficient: false,
  startedAt: startedAt.toISOString(),
  completedAt: new Date().toISOString(),
  scenarios: results,
  criticalSmoke: productRelease.criticalSmoke,
  criticalSmokePassed: true,
};
const output = resolve(process.cwd(), outputArg.slice("--output=".length));
await writeFile(output, `${JSON.stringify(record, null, 2)}\n`, { flag: "wx" });
console.log(`Phase 1 staging process failure drill passed: ${output}`);
