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
let deploymentTier = resolveDeploymentTier({
  contract,
  compose,
  tierId:
    process.env.PHASE1_DEPLOYMENT_TIER || runtimeValues.PHASE1_DEPLOYMENT_TIER,
  environment: process.env.HVAC_ENV || runtimeValues.HVAC_ENV,
  runtimeEnvironment: { ...runtimeValues, ...process.env },
});

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
const ownerSplitIndex = args.indexOf("--owner-split");
const ownerSplit = ownerSplitIndex >= 0;
if (ownerSplit) {
  args.splice(ownerSplitIndex, 1);
  if (deploymentTier.tier.id !== "single-full") {
    throw new Error(
      "The owner-split topology requires PHASE1_DEPLOYMENT_TIER=single-full",
    );
  }
  deploymentTier = resolveDeploymentTier({
    contract,
    compose,
    tierId: deploymentTier.tier.id,
    environment: process.env.HVAC_ENV || runtimeValues.HVAC_ENV,
    runtimeEnvironment: { ...runtimeValues, ...process.env },
    additionalComposeDocuments: [
      readFileSync(path.join(phase1Dir, "owner-split.compose.yaml"), "utf8"),
    ],
    additionalProfiles: ["owner-split"],
  });
}

const composeFiles = [path.join(phase1Dir, "compose.yaml")];
if (ownerSplit)
  composeFiles.push(path.join(phase1Dir, "owner-split.compose.yaml"));

const result = spawnSync(
  "docker",
  [
    "compose",
    ...[
      ...deploymentTier.profiles,
      ...(ownerSplit ? ["owner-split"] : []),
    ].flatMap((profile) => ["--profile", profile]),
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
      PHASE1_OBSERVABILITY_CONFIG: deploymentTier.profiles[0].replace(/^observability-/, ""),
      PHASE1_ENV_FILE: runtimeEnvPath,
    },
    stdio: "inherit",
  },
);

if (result.error) throw result.error;
process.exit(result.status ?? 1);
