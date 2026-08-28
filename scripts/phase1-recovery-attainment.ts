const dayMilliseconds = 24 * 60 * 60 * 1000;

function timestamp(name, value) {
  const parsed = Date.parse(value);
  if (!Number.isFinite(parsed))
    throw new Error(`${name} must be an RFC3339 timestamp`);
  return parsed;
}

export function buildDrillPassedAttainment({ current, record, recordSha256 }) {
  if (
    record.environment !== "production" ||
    record.scenario !== "WHOLE_SERVER_REPLACEMENT"
  ) {
    throw new Error(
      "Production attainment requires a production WHOLE_SERVER_REPLACEMENT drill",
    );
  }
  if (!/^[a-f0-9]{64}$/.test(recordSha256))
    throw new Error("Recovery drill record SHA-256 is invalid");
  const completedAt = timestamp(
    "businessValidationCompletedAt",
    record.businessValidationCompletedAt,
  );
  const cadenceDays = current.minimumDrillCadenceDays;
  if (!Number.isInteger(cadenceDays) || cadenceDays <= 0)
    throw new Error("minimumDrillCadenceDays must be positive");

  return {
    ...current,
    state: "DRILL_PASSED",
    productionClaimAllowed: true,
    lastTimestampedRestoreDrill: new Date(completedAt).toISOString(),
    expiresAt: new Date(
      completedAt + cadenceDays * dayMilliseconds,
    ).toISOString(),
    evidence: {
      drillId: record.drillId,
      environment: record.environment,
      scenario: record.scenario,
      recordSha256,
    },
  };
}

export function validateRecoveryAttainment(
  attainment,
  { now = new Date() } = {},
) {
  if (attainment.schemaVersion !== 1)
    throw new Error("Recovery attainment schemaVersion must be 1");
  if (
    !Number.isInteger(attainment.minimumDrillCadenceDays) ||
    attainment.minimumDrillCadenceDays <= 0
  ) {
    throw new Error(
      "Recovery attainment minimumDrillCadenceDays must be positive",
    );
  }
  if (attainment.containerOnlyEvidenceSufficient !== false) {
    throw new Error(
      "Container-only evidence cannot establish recovery attainment",
    );
  }
  if (attainment.state === "TARGET_DEFINED") {
    if (
      attainment.productionClaimAllowed !== false ||
      attainment.lastTimestampedRestoreDrill !== null ||
      attainment.expiresAt !== null ||
      attainment.evidence !== null
    ) {
      throw new Error(
        "TARGET_DEFINED cannot carry production recovery evidence or claims",
      );
    }
    return attainment;
  }
  if (attainment.state !== "DRILL_PASSED")
    throw new Error(`Unknown recovery attainment state: ${attainment.state}`);
  if (attainment.productionClaimAllowed !== true)
    throw new Error("DRILL_PASSED must allow the bounded production claim");
  const completedAt = timestamp(
    "lastTimestampedRestoreDrill",
    attainment.lastTimestampedRestoreDrill,
  );
  if (completedAt > now.getTime()) {
    throw new Error("Recovery attainment drill completion cannot be in the future");
  }
  const expiresAt = timestamp("expiresAt", attainment.expiresAt);
  const expectedExpiry =
    completedAt + attainment.minimumDrillCadenceDays * dayMilliseconds;
  if (expiresAt !== expectedExpiry)
    throw new Error(
      "Recovery attainment expiry does not match the drill cadence",
    );
  if (now.getTime() > expiresAt)
    throw new Error("Recovery attainment evidence has expired");
  if (
    attainment.evidence?.environment !== "production" ||
    attainment.evidence?.scenario !== "WHOLE_SERVER_REPLACEMENT"
  ) {
    throw new Error(
      "DRILL_PASSED evidence must reference a production whole-server drill",
    );
  }
  if (!/^[a-f0-9]{64}$/.test(attainment.evidence?.recordSha256 ?? "")) {
    throw new Error("DRILL_PASSED evidence record SHA-256 is invalid");
  }
  return attainment;
}

export function validateRecoveryEvidenceBinding(
  attainment,
  { recordBytes, governedCadenceDays },
) {
  if (attainment.state !== "DRILL_PASSED") {
    throw new Error(
      "Recovery evidence binding requires DRILL_PASSED attainment",
    );
  }
  if (attainment.minimumDrillCadenceDays !== governedCadenceDays) {
    throw new Error(
      "Recovery attainment cadence differs from the governed contract",
    );
  }
  const sha256 = createHash("sha256").update(recordBytes).digest("hex");
  if (attainment.evidence?.recordSha256 !== sha256) {
    throw new Error(
      "Recovery attainment does not match the supplied drill record",
    );
  }
  const record = JSON.parse(recordBytes.toString("utf8"));
  if (
    record.drillId !== attainment.evidence.drillId ||
    record.environment !== attainment.evidence.environment ||
    record.scenario !== attainment.evidence.scenario ||
    new Date(record.businessValidationCompletedAt).toISOString() !==
      attainment.lastTimestampedRestoreDrill
  ) {
    throw new Error(
      "Recovery attainment metadata does not match the drill record",
    );
  }
  return record;
}
import { createHash } from "node:crypto";
