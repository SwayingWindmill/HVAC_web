import assert from "node:assert/strict";
import { test } from "node:test";

import {
  buildDrillPassedAttainment,
  validateRecoveryAttainment,
  validateRecoveryEvidenceBinding,
} from "./phase1-recovery-attainment.ts";
import { createHash } from "node:crypto";

const targetDefined = {
  schemaVersion: 1,
  name: "Phase 1 recovery attainment state",
  targetsContract: "deploy/platform/phase1/recovery/recovery-targets.v1.json",
  state: "TARGET_DEFINED",
  productionClaimAllowed: false,
  minimumDrillCadenceDays: 90,
  lastTimestampedRestoreDrill: null,
  expiresAt: null,
  evidence: null,
  containerOnlyEvidenceSufficient: false,
  rules: ["Targets are recovery objectives, not attained guarantees."],
};

const productionDrill = {
  drillId: "drill-2026-08-24-a",
  environment: "production",
  scenario: "WHOLE_SERVER_REPLACEMENT",
  businessValidationCompletedAt: "2026-08-24T04:00:00Z",
};

test("target-defined state is valid but cannot make a production claim", () => {
  assert.doesNotThrow(() =>
    validateRecoveryAttainment(targetDefined, {
      now: new Date("2026-08-24T05:00:00Z"),
    }),
  );
});

test("a real production whole-server drill builds a bounded production claim", () => {
  const attainment = buildDrillPassedAttainment({
    current: targetDefined,
    record: productionDrill,
    recordSha256: "a".repeat(64),
  });

  assert.equal(attainment.state, "DRILL_PASSED");
  assert.equal(attainment.productionClaimAllowed, true);
  assert.equal(
    attainment.lastTimestampedRestoreDrill,
    "2026-08-24T04:00:00.000Z",
  );
  assert.equal(attainment.expiresAt, "2026-11-22T04:00:00.000Z");
  assert.doesNotThrow(() =>
    validateRecoveryAttainment(attainment, {
      now: new Date("2026-09-01T00:00:00Z"),
    }),
  );
});

test("expired drill evidence fails the production claim gate", () => {
  const attainment = buildDrillPassedAttainment({
    current: targetDefined,
    record: productionDrill,
    recordSha256: "b".repeat(64),
  });
  assert.throws(
    () =>
      validateRecoveryAttainment(attainment, {
        now: new Date("2026-11-23T00:00:00Z"),
      }),
    /expired/,
  );
});

test("future-dated drill evidence cannot mint a current production claim", () => {
  const attainment = buildDrillPassedAttainment({
    current: targetDefined,
    record: productionDrill,
    recordSha256: "d".repeat(64),
  });
  assert.throws(
    () =>
      validateRecoveryAttainment(attainment, {
        now: new Date("2026-08-24T03:59:59Z"),
      }),
    /cannot be in the future/,
  );
});

test("staging or container-only drills cannot mint production attainment", () => {
  assert.throws(
    () =>
      buildDrillPassedAttainment({
        current: targetDefined,
        record: {
          ...productionDrill,
          environment: "staging",
          scenario: "PROCESS_FAILURE",
        },
        recordSha256: "c".repeat(64),
      }),
    /production WHOLE_SERVER_REPLACEMENT/,
  );
});

test("production claim is cryptographically bound to the governed drill record", () => {
  const recordBytes = Buffer.from(JSON.stringify(productionDrill));
  const attainment = buildDrillPassedAttainment({
    current: targetDefined,
    record: productionDrill,
    recordSha256: createHash("sha256").update(recordBytes).digest("hex"),
  });
  assert.doesNotThrow(() =>
    validateRecoveryEvidenceBinding(attainment, {
      recordBytes,
      governedCadenceDays: 90,
    }),
  );
  assert.throws(
    () =>
      validateRecoveryEvidenceBinding(attainment, {
        recordBytes: Buffer.from(`${recordBytes.toString("utf8")} `),
        governedCadenceDays: 90,
      }),
    /does not match/,
  );
});
