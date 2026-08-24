import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";

import { buildProcessFailurePlan } from "./phase1-process-failure-plan.ts";

const [manifest, runner] = await Promise.all([
  readFile(
    new URL(
      "../deploy/platform/phase1/recovery/process-failure-scenarios.v1.json",
      import.meta.url,
    ),
    "utf8",
  ).then(JSON.parse),
  readFile(new URL("./run-phase1-process-failure-drill.ts", import.meta.url), "utf8"),
]);

test("staging process failure plan covers every Phase 1 single point and owner process in a safe order", () => {
  const plan = buildProcessFailurePlan(manifest, { environment: "staging" });
  assert.deepEqual(
    plan.map((step) => step.target),
    [
      "postgres",
      "redis",
      "mqtt-broker",
      "clickhouse",
      "energy-api",
      "scheduler",
      "telemetry-worker",
      "metric-worker",
      "iot-service",
    ],
  );
  assert.match(runner, /"iot-service": "http:\/\/127\.0\.0\.1:19094\/health\/ready"/);
  assert.match(runner, /activeReadinessPassed\(target\)/);
  assert.match(runner, /waitForReadinessFailure/);
  assert.ok(plan.every((step) => step.killSignal === "SIGKILL"));
  assert.ok(plan.every((step) => step.maximumRecoverySeconds > 0));
  assert.ok(plan.every((step) => step.recoveryProbeServices.length > 0));
  assert.deepEqual(
    plan.find((step) => step.target === "mqtt-broker").recoveryProbeServices,
    ["iot-service"],
  );
});

test("process failure plan refuses production execution", () => {
  assert.throws(
    () => buildProcessFailurePlan(manifest, { environment: "production" }),
    /staging only/,
  );
});
