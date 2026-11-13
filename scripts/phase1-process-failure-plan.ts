const expectedTargets = [
  "postgres",
  "redis",
  "mqtt-broker",
  "clickhouse",
  "energy-api",
  "scheduler",
  "telemetry-worker",
  "metric-worker",
  "connectivity",
] as const;

type RecoveryTarget = (typeof expectedTargets)[number];

const expectedRecoveryProbes: Record<RecoveryTarget, string[]> = {
  postgres: ["postgres", "energy-api", "scheduler"],
  redis: ["redis", "energy-api"],
  "mqtt-broker": ["connectivity"],
  clickhouse: ["clickhouse", "telemetry-worker"],
  "energy-api": ["energy-api"],
  scheduler: ["scheduler"],
  "telemetry-worker": ["telemetry-worker"],
  "metric-worker": ["metric-worker"],
  connectivity: ["connectivity"],
};

const isRecoveryTarget = (target: string): target is RecoveryTarget =>
  Object.prototype.hasOwnProperty.call(expectedRecoveryProbes, target);

export interface ProcessFailureScenario {
  id: string;
  target: string;
  maximumRecoverySeconds: number;
  recoveryProbeServices: string[];
  [key: string]: unknown;
}

export interface ProcessFailureManifest {
  schemaVersion: number;
  environment: string;
  productionClaimAllowed: boolean;
  containerOnlyEvidenceSufficient: boolean;
  killSignal: string;
  scenarios?: ProcessFailureScenario[];
}

export function buildProcessFailurePlan(
  manifest: ProcessFailureManifest,
  { environment }: { environment: string | undefined },
) {
  if (environment !== "staging" || manifest.environment !== "staging") {
    throw new Error("Phase 1 process failure drill is staging only");
  }
  if (
    manifest.schemaVersion !== 1 ||
    manifest.productionClaimAllowed !== false ||
    manifest.containerOnlyEvidenceSufficient !== false
  ) {
    throw new Error(
      "Phase 1 process failure manifest must remain non-production evidence",
    );
  }
  const scenarios = manifest.scenarios ?? [];
  if (
    JSON.stringify(scenarios.map((scenario) => scenario.target)) !==
    JSON.stringify(expectedTargets)
  ) {
    throw new Error(
      "Phase 1 process failure targets or recovery order drifted",
    );
  }
  if (
    new Set(scenarios.map((scenario) => scenario.id)).size !== scenarios.length
  ) {
    throw new Error("Phase 1 process failure scenario IDs must be unique");
  }
  return scenarios.map((scenario) => {
    if (
      !Number.isInteger(scenario.maximumRecoverySeconds) ||
      scenario.maximumRecoverySeconds <= 0
    ) {
      throw new Error(
        `Phase 1 process failure scenario ${scenario.id} has no recovery bound`,
      );
    }
    if (
      !isRecoveryTarget(scenario.target) ||
      JSON.stringify(scenario.recoveryProbeServices) !==
        JSON.stringify(expectedRecoveryProbes[scenario.target])
    ) {
      throw new Error(
        `Phase 1 process failure scenario ${scenario.id} has no governed live recovery probes`,
      );
    }
    return { ...scenario, killSignal: manifest.killSignal };
  });
}
