import { createHash } from "node:crypto";

export const ownerSplitImageVariables = [
  "ENERGY_API_IMAGE",
  "IAM_SERVICE_IMAGE",
  "CORE_SERVICE_IMAGE",
  "TELEMETRY_QUERY_SERVICE_IMAGE",
  "COMMAND_SERVICE_IMAGE",
  "ALARM_SERVICE_IMAGE",
  "WORK_ORDER_SERVICE_IMAGE",
];

export interface ProductReleaseInfo {
  productVersion?: string;
  releaseRevision?: string;
}

export interface ValidateOwnerSplitReleaseInput {
  manifestBytes: Buffer | Uint8Array;
  expectedSha256?: string;
  runtime: Record<string, string | undefined>;
  productRelease: ProductReleaseInfo;
}

interface OwnerSplitReleaseManifest {
  schemaVersion?: number;
  kind?: string;
  productVersion?: string;
  productReleaseRevision?: string;
  sourceRevision?: string;
  images?: Record<string, string | undefined>;
}

export function validateOwnerSplitRelease({
  manifestBytes,
  expectedSha256,
  runtime,
  productRelease,
}: ValidateOwnerSplitReleaseInput) {
  if (!/^[a-f0-9]{64}$/.test(expectedSha256 ?? "")) {
    throw new Error(
      "PHASE1_OWNER_SPLIT_RELEASE_MANIFEST_SHA256 must be an approved SHA-256",
    );
  }
  const actualSha256 = createHash("sha256").update(manifestBytes).digest("hex");
  if (actualSha256 !== expectedSha256) {
    throw new Error(
      "Owner-split release manifest SHA-256 does not match the approved value",
    );
  }
  const manifest = JSON.parse(Buffer.from(manifestBytes).toString("utf8")) as OwnerSplitReleaseManifest;
  if (
    manifest.schemaVersion !== 1 ||
    manifest.kind !== "PHASE1_OWNER_SPLIT_RELEASE"
  ) {
    throw new Error("Owner-split release manifest identity is invalid");
  }
  if (
    manifest.productVersion !== productRelease.productVersion ||
    manifest.productReleaseRevision !== productRelease.releaseRevision
  ) {
    throw new Error(
      "Owner-split images are not bound to the active product release",
    );
  }
  if (!/^[a-f0-9]{40}$/.test(manifest.sourceRevision ?? "")) {
    throw new Error("Owner-split release sourceRevision must be a Git commit");
  }
  for (const variable of ownerSplitImageVariables) {
    const image = runtime[variable];
    if (!image?.includes("@sha256:") || manifest.images?.[variable] !== image) {
      throw new Error(
        `${variable} is not bound to the approved owner-split release manifest`,
      );
    }
  }
  return {
    manifestSha256: actualSha256,
    productVersion: manifest.productVersion,
    productReleaseRevision: manifest.productReleaseRevision,
    sourceRevision: manifest.sourceRevision,
    images: Object.fromEntries(
      ownerSplitImageVariables.map((variable) => [variable, runtime[variable]]),
    ),
  };
}
