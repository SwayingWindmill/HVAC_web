import assert from "node:assert/strict";
import test from "node:test";

import {
  buildActiveAlarmRule,
  buildDeviceProfile,
  edgeHealthProfiles,
  parseThingsBoardResponse,
  restoreEdgeHealthBackup,
  validateEdgeHealthProfiles,
} from "./configure-thingsboard-edge-health.mjs";

test("builds an active-state alarm that creates on false and clears on true", () => {
  const rule = buildActiveAlarmRule({
    id: "edgeGatewayOffline",
    alarmType: "网关离线",
    severity: "CRITICAL",
    details: "网关已停止活动",
  });

  assert.equal(rule.id, "edgeGatewayOffline");
  assert.equal(rule.alarmType, "网关离线");
  assert.deepEqual(Object.keys(rule.createRules), ["CRITICAL"]);
  assert.equal(
    rule.createRules.CRITICAL.condition.condition[0].predicate.value
      .defaultValue,
    false,
  );
  assert.equal(
    rule.clearRule.condition.condition[0].predicate.value.defaultValue,
    true,
  );
  assert.equal(rule.propagate, false);
});

test("defines distinct inactivity windows for gateway, fast telemetry and meters", () => {
  validateEdgeHealthProfiles(edgeHealthProfiles);

  assert.deepEqual(
    edgeHealthProfiles.map((profile) => ({
      name: profile.name,
      devices: profile.devices,
      inactivityTimeoutMs: profile.inactivityTimeoutMs,
      alarmType: profile.alarm.alarmType,
      severity: Object.keys(profile.alarm.createRules)[0],
    })),
    [
      {
        name: "HVAC Edge Gateway",
        devices: ["EdgeGateway"],
        inactivityTimeoutMs: 180_000,
        alarmType: "网关离线",
        severity: "CRITICAL",
      },
      {
        name: "HVAC Edge Fast Telemetry",
        devices: ["temperature", "waterflow", "et1010"],
        inactivityTimeoutMs: 600_000,
        alarmType: "遥测数据陈旧",
        severity: "MAJOR",
      },
      {
        name: "HVAC Edge Meter Telemetry",
        devices: [
          "ammeter-unit1",
          "ammeter-unit2",
          "ammeter-unit3",
          "ammeter-unit4",
        ],
        inactivityTimeoutMs: 1_200_000,
        alarmType: "遥测数据陈旧",
        severity: "MAJOR",
      },
    ],
  );
});

test("rejects a device assigned to more than one edge health profile", () => {
  const duplicated = structuredClone(edgeHealthProfiles);
  duplicated[1].devices.push("EdgeGateway");

  assert.throws(
    () => validateEdgeHealthProfiles(duplicated),
    /assigned to multiple profiles: EdgeGateway/,
  );
});

test("creates a ThingsBoard default-transport device profile", () => {
  const profile = buildDeviceProfile(null, edgeHealthProfiles[0]);

  assert.equal(profile.type, "DEFAULT");
  assert.equal(profile.transportType, "DEFAULT");
  assert.equal(profile.profileData.transportConfiguration.type, "DEFAULT");
});

test("accepts an empty successful ThingsBoard attribute response", async () => {
  const response = new Response(null, { status: 200 });

  assert.equal(
    await parseThingsBoardResponse(response, "POST", "/api/attributes"),
    null,
  );
});

test("restores devices, timeouts and newly created profiles after a partial apply", async () => {
  const calls = [];
  const api = {
    saveDevice: async (device) => calls.push(["saveDevice", device.id.id]),
    deleteServerAttribute: async (deviceId, key) =>
      calls.push(["deleteServerAttribute", deviceId, key]),
    saveServerAttributes: async (deviceId, attributes) =>
      calls.push(["saveServerAttributes", deviceId, attributes]),
    saveProfile: async (profile) => calls.push(["saveProfile", profile.name]),
    deleteProfile: async (profileId) =>
      calls.push(["deleteProfile", profileId]),
  };
  const backup = {
    devices: [
      {
        name: "temperature",
        value: { id: { id: "temperature-id" } },
        inactivityTimeout: null,
      },
      {
        name: "meter",
        value: { id: { id: "meter-id" } },
        inactivityTimeout: 900_000,
      },
    ],
    profiles: [
      { name: "created-profile", value: null },
      { name: "existing-profile", value: { name: "existing-profile" } },
    ],
  };

  await restoreEdgeHealthBackup(api, backup, {
    devices: new Set(["temperature"]),
    profiles: new Map([
      ["created-profile", { createdProfileId: "created-profile-id" }],
    ]),
  });

  assert.deepEqual(calls, [
    ["saveDevice", "temperature-id"],
    ["deleteServerAttribute", "temperature-id", "inactivityTimeout"],
    ["deleteProfile", "created-profile-id"],
  ]);
});
