import { readFile } from "node:fs/promises";
import { spawnSync } from "node:child_process";
import { resolve } from "node:path";
import process from "node:process";

import {
  validateRecoveryAttainment,
  validateRecoveryEvidenceBinding,
} from "./phase1-recovery-attainment.ts";

const fileArg = process.argv.find((arg) => arg.startsWith("--file="));
const asOfArg = process.argv.find((arg) => arg.startsWith("--as-of="));
const drillRecordArg = process.argv.find((arg) =>
  arg.startsWith("--drill-record="),
);
const file = resolve(
  process.cwd(),
  fileArg?.slice("--file=".length) ||
    "deploy/platform/phase1/recovery/attainment.v1.json",
);
const attainment = JSON.parse(await readFile(file, "utf8"));
const governed = JSON.parse(
  await readFile(
    resolve(
      process.cwd(),
      "deploy/platform/phase1/recovery/attainment.v1.json",
    ),
    "utf8",
  ),
);
const now = asOfArg ? new Date(asOfArg.slice("--as-of=".length)) : new Date();

validateRecoveryAttainment(attainment, { now });
if (
  attainment.state !== "DRILL_PASSED" ||
  attainment.productionClaimAllowed !== true
) {
  throw new Error(
    "Phase 1 production recovery claim is unavailable until a valid DRILL_PASSED attainment is installed",
  );
}
if (!drillRecordArg) {
  throw new Error(
    "Pass --drill-record=/path/to/drill-record.json for a production claim",
  );
}
const drillRecord = resolve(
  process.cwd(),
  drillRecordArg.slice("--drill-record=".length),
);
const recordBytes = await readFile(drillRecord);
validateRecoveryEvidenceBinding(attainment, {
  recordBytes,
  governedCadenceDays: governed.minimumDrillCadenceDays,
});
const verification = spawnSync(
  process.execPath,
  [
    "--experimental-strip-types",
    resolve(process.cwd(), "scripts/verify-phase1-recovery-drill.mjs"),
    `--file=${drillRecord}`,
  ],
  { cwd: process.cwd(), stdio: "inherit" },
);
if (verification.error) throw verification.error;
if (verification.status !== 0)
  throw new Error("Recovery drill semantic verification failed");

console.log(
  `Phase 1 production recovery claim is valid until ${attainment.expiresAt}`,
);
