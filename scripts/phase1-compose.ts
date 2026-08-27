import { readFileSync } from "node:fs";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import path from "node:path";

import {
  parseRuntimeEnvironment,
  resolveDeploymentTier,
} from "./phase1-deployment-tier.ts";

const repoRoot = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "..",
);
const phase1Dir = path.join(repoRoot, "deploy", "platform", "phase1");
const runtimeEnv = process.env.PHASE1_ENV_FILE;
if (!runtimeEnv) throw new Error("PHASE1_ENV_FILE is required");

const runtimeEnvPath = path.resolve(runtimeEnv);
const runtimeValues = parseRuntimeEnvironment(
  readFileSync(runtimeEnvPath, "utf8"),
);
const contract = JSON.parse(
  readFileSync(path.join(phase1Dir, "deployment-tiers.v1.json"), "utf8"),
);
const compose = readFileSync(path.join(phase1Dir, "compose.yaml"), "utf8");
const args = process.argv.slice(2);
if (
  args.some(
    (arg, index) =>
      (arg === "--profile" && args[index + 1] === "intelligence") ||
      arg === "--profile=intelligence",
  )
) {
  throw new Error(
    "The intelligence profile requires a separately certified capacity tier",
  );
}

const takeFlag = (flag: string) => {
  const index = args.indexOf(flag);
  if (index < 0) return false;
  args.splice(index, 1);
  return true;
};

const ownerSplit = takeFlag("--owner-split");
const integration = takeFlag("--integration");
const postgresMode =
  process.env.PHASE1_POSTGRES_MODE || runtimeValues.PHASE1_POSTGRES_MODE || "local";
if (postgresMode !== "local" && postgresMode !== "external") {
  throw new Error(`Unknown PHASE1_POSTGRES_MODE: ${postgresMode}`);
}
const clickhouseMode =
  process.env.PHASE1_CLICKHOUSE_MODE || runtimeValues.PHASE1_CLICKHOUSE_MODE || "local";
if (clickhouseMode !== "local" && clickhouseMode !== "external") {
  throw new Error(`Unknown PHASE1_CLICKHOUSE_MODE: ${clickhouseMode}`);
}
const redisMode =
  process.env.PHASE1_REDIS_MODE || runtimeValues.PHASE1_REDIS_MODE || "local";
if (redisMode !== "local" && redisMode !== "external") {
  throw new Error(`Unknown PHASE1_REDIS_MODE: ${redisMode}`);
}
const localPostgres = postgresMode === "local";
const localClickHouse = clickhouseMode === "local";
const localRedis = redisMode === "local";
const externalState = !localPostgres || !localClickHouse || !localRedis;
const tierId =
  process.env.PHASE1_DEPLOYMENT_TIER || runtimeValues.PHASE1_DEPLOYMENT_TIER;
if (ownerSplit && tierId !== "single-full") {
  throw new Error(
    "The owner-split runtime mode requires PHASE1_DEPLOYMENT_TIER=single-full",
  );
}

const ownerSplitCompose = ownerSplit
  ? readFileSync(path.join(phase1Dir, "owner-split.compose.yaml"), "utf8")
  : null;
const runtimeProfiles = [
  ...(localPostgres ? ["local-postgres"] : []),
  ...(localClickHouse ? ["local-clickhouse"] : []),
  ...(localRedis ? ["local-redis"] : []),
  ...(integration ? ["integration"] : []),
  ...(ownerSplit ? ["owner-split"] : []),
];
const deploymentTier = resolveDeploymentTier({
  contract,
  compose,
  tierId,
  environment: process.env.HVAC_ENV || runtimeValues.HVAC_ENV,
  runtimeEnvironment: { ...runtimeValues, ...process.env },
  additionalComposeDocuments: ownerSplitCompose ? [ownerSplitCompose] : [],
  additionalProfiles: runtimeProfiles,
});

const composeFiles = [path.join(phase1Dir, "compose.yaml")];
if (ownerSplit)
  composeFiles.push(path.join(phase1Dir, "owner-split.compose.yaml"));

const result = spawnSync(
  "docker",
  [
    "compose",
    ...[...deploymentTier.profiles, ...runtimeProfiles].flatMap((profile) => [
      "--profile",
      profile,
    ]),
    "--env-file",
    runtimeEnvPath,
    ...composeFiles.flatMap((composeFile) => ["-f", composeFile]),
    ...args,
  ],
  {
    cwd: repoRoot,
    env: {
      ...process.env,
      COMPOSE_PROFILES: "",
      ...deploymentTier.environment,
      PHASE1_DATA_NETWORK_INTERNAL: externalState ? "false" : "true",
      PHASE1_OBSERVABILITY_CONFIG: deploymentTier.profiles[0].replace(/^observability-/, ""),
      PHASE1_ENV_FILE: runtimeEnvPath,
    },
    stdio: "inherit",
  },
);

if (result.error) throw result.error;
process.exit(result.status ?? 1);
