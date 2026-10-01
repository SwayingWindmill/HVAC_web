import { writeFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";

function activeCondition(active) {
  return {
    spec: { type: "SIMPLE" },
    condition: [
      {
        key: { key: "active", type: "ATTRIBUTE" },
        value: null,
        predicate: {
          type: "BOOLEAN",
          operation: "EQUAL",
          value: {
            userValue: null,
            defaultValue: active,
            dynamicValue: null,
          },
        },
        valueType: "BOOLEAN",
      },
    ],
  };
}

export function buildActiveAlarmRule({ id, alarmType, severity, details }) {
  return {
    id,
    alarmType,
    clearRule: {
      schedule: null,
      condition: activeCondition(true),
      dashboardId: null,
      alarmDetails: details,
    },
    createRules: {
      [severity]: {
        schedule: null,
        condition: activeCondition(false),
        dashboardId: null,
        alarmDetails: details,
      },
    },
    propagate: false,
    propagateToOwner: false,
    propagateToTenant: false,
    propagateRelationTypes: null,
  };
}

export const edgeHealthProfiles = Object.freeze([
  {
    name: "HVAC Edge Gateway",
    description: "EG8200 northbound gateway connectivity health",
    devices: ["EdgeGateway"],
    inactivityTimeoutMs: 180_000,
    alarm: buildActiveAlarmRule({
      id: "hvacEdgeGatewayOffline",
      alarmType: "网关离线",
      severity: "CRITICAL",
      details: "EG8200 网关超过 3 分钟没有活动。",
    }),
  },
  {
    name: "HVAC Edge Fast Telemetry",
    description: "One-minute HVAC telemetry freshness health",
    devices: ["temperature", "waterflow", "et1010"],
    inactivityTimeoutMs: 600_000,
    alarm: buildActiveAlarmRule({
      id: "hvacEdgeFastTelemetryStale",
      alarmType: "遥测数据陈旧",
      severity: "MAJOR",
      details: "一分钟采集设备超过 10 分钟没有新遥测。",
    }),
  },
  {
    name: "HVAC Edge Meter Telemetry",
    description: "Five-minute HVAC meter telemetry freshness health",
    devices: [
      "ammeter-unit1",
      "ammeter-unit2",
      "ammeter-unit3",
      "ammeter-unit4",
    ],
    inactivityTimeoutMs: 1_200_000,
    alarm: buildActiveAlarmRule({
      id: "hvacEdgeMeterTelemetryStale",
      alarmType: "遥测数据陈旧",
      severity: "MAJOR",
      details: "五分钟电表采集设备超过 20 分钟没有新遥测。",
    }),
  },
]);

export function validateEdgeHealthProfiles(profiles) {
  const names = new Set();
  const devices = new Set();
  for (const profile of profiles) {
    if (names.has(profile.name)) {
      throw new Error(`duplicate profile name: ${profile.name}`);
    }
    names.add(profile.name);
    if (!Number.isInteger(profile.inactivityTimeoutMs)) {
      throw new Error(`invalid inactivity timeout: ${profile.name}`);
    }
    if (profile.inactivityTimeoutMs < 180_000) {
      throw new Error(`inactivity timeout is too short: ${profile.name}`);
    }
    for (const device of profile.devices) {
      if (devices.has(device)) {
        throw new Error(`device assigned to multiple profiles: ${device}`);
      }
      devices.add(device);
    }
  }
}

export async function parseThingsBoardResponse(response, method, path) {
  const responseText = await response.text();
  if (!response.ok) {
    throw new Error(
      `ThingsBoard ${method} ${path} failed: HTTP ${response.status} ${responseText}`,
    );
  }
  return responseText ? JSON.parse(responseText) : null;
}

class ThingsBoardApi {
  constructor(baseUrl) {
    this.baseUrl = baseUrl.replace(/\/$/u, "");
    this.token = "";
  }

  async request(path, { method = "GET", body } = {}) {
    const response = await fetch(`${this.baseUrl}${path}`, {
      method,
      headers: {
        ...(this.token ? { "X-Authorization": `Bearer ${this.token}` } : {}),
        ...(body ? { "Content-Type": "application/json" } : {}),
      },
      ...(body ? { body: JSON.stringify(body) } : {}),
    });
    return parseThingsBoardResponse(response, method, path);
  }

  async login(username, password) {
    const result = await this.request("/api/auth/login", {
      method: "POST",
      body: { username, password },
    });
    this.token = result.token;
  }

  async profileByName(name) {
    const page = await this.request(
      `/api/deviceProfileInfos?pageSize=100&page=0&textSearch=${encodeURIComponent(name)}`,
    );
    const info = page.data.find((profile) => profile.name === name);
    if (!info) {
      return null;
    }
    return this.request(`/api/deviceProfile/${info.id.id}`);
  }

  async deviceByName(name) {
    return this.request(
      `/api/tenant/devices?deviceName=${encodeURIComponent(name)}`,
    );
  }

  async serverAttributes(deviceId) {
    return this.request(
      `/api/plugins/telemetry/DEVICE/${deviceId}/values/attributes/SERVER_SCOPE`,
    );
  }

  async saveProfile(profile) {
    return this.request("/api/deviceProfile", {
      method: "POST",
      body: profile,
    });
  }

  async saveDevice(device) {
    return this.request("/api/device", { method: "POST", body: device });
  }

  async saveServerAttributes(deviceId, attributes) {
    await this.request(
      `/api/plugins/telemetry/DEVICE/${deviceId}/SERVER_SCOPE`,
      { method: "POST", body: attributes },
    );
  }

  async deleteServerAttribute(deviceId, key) {
    await this.request(
      `/api/plugins/telemetry/DEVICE/${deviceId}/SERVER_SCOPE/${encodeURIComponent(key)}`,
      { method: "DELETE" },
    );
  }

  async deleteProfile(profileId) {
    await this.request(`/api/deviceProfile/${profileId}`, { method: "DELETE" });
  }
}

export function buildDeviceProfile(existing, definition) {
  return {
    ...(existing ?? {}),
    name: definition.name,
    description: definition.description,
    type: "DEFAULT",
    transportType: "DEFAULT",
    default: false,
    defaultRuleChainId: existing?.defaultRuleChainId ?? null,
    defaultDashboardId: existing?.defaultDashboardId ?? null,
    defaultQueueName: existing?.defaultQueueName ?? null,
    profileData: {
      configuration: { type: "DEFAULT" },
      transportConfiguration: { type: "DEFAULT" },
      provisionConfiguration: {
        type: "DISABLED",
        provisionDeviceSecret: null,
      },
      alarms: [definition.alarm],
    },
  };
}

function attributeValue(attributes, key) {
  return attributes.find((attribute) => attribute.key === key)?.value ?? null;
}

export async function restoreEdgeHealthBackup(api, backup, mutations) {
  for (const deviceBackup of backup.devices.filter((device) =>
    mutations.devices.has(device.name),
  )) {
    await api.saveDevice(deviceBackup.value);
    if (deviceBackup.inactivityTimeout === null) {
      await api.deleteServerAttribute(
        deviceBackup.value.id.id,
        "inactivityTimeout",
      );
    } else {
      await api.saveServerAttributes(deviceBackup.value.id.id, {
        inactivityTimeout: deviceBackup.inactivityTimeout,
      });
    }
  }
  for (const profileBackup of backup.profiles.filter((profile) =>
    mutations.profiles.has(profile.name),
  )) {
    const mutation = mutations.profiles.get(profileBackup.name);
    if (profileBackup.value) {
      await api.saveProfile(profileBackup.value);
    } else if (mutation.createdProfileId) {
      await api.deleteProfile(mutation.createdProfileId);
    }
  }
}

async function main() {
  validateEdgeHealthProfiles(edgeHealthProfiles);

  const apply = process.argv.includes("--apply");
  const backupArgument = process.argv.find((argument) =>
    argument.startsWith("--backup="),
  );
  const backupPath = backupArgument?.slice("--backup=".length);
  if (apply && !backupPath) {
    throw new Error("--backup=<path> is required with --apply");
  }

  const username = process.env.TB_TENANT_ADMIN_EMAIL;
  const password = process.env.TB_TENANT_ADMIN_PASSWORD;
  if (!username || !password) {
    throw new Error(
      "TB_TENANT_ADMIN_EMAIL and TB_TENANT_ADMIN_PASSWORD are required",
    );
  }

  const api = new ThingsBoardApi(
    process.env.TB_BASE_URL ?? "http://127.0.0.1:8080",
  );
  await api.login(username, password);

  const backup = {
    createdAt: new Date().toISOString(),
    profiles: [],
    devices: [],
  };
  const plan = [];
  for (const definition of edgeHealthProfiles) {
    const existingProfile = await api.profileByName(definition.name);
    backup.profiles.push({ name: definition.name, value: existingProfile });
    for (const deviceName of definition.devices) {
      const device = await api.deviceByName(deviceName);
      const attributes = await api.serverAttributes(device.id.id);
      backup.devices.push({
        name: deviceName,
        value: device,
        inactivityTimeout: attributeValue(attributes, "inactivityTimeout"),
      });
    }
    plan.push({
      profile: definition.name,
      operation: existingProfile ? "update" : "create",
      alarmType: definition.alarm.alarmType,
      inactivityTimeoutMs: definition.inactivityTimeoutMs,
      devices: definition.devices,
    });
  }

  if (!apply) {
    console.log(JSON.stringify({ dryRun: true, plan }, null, 2));
    return;
  }

  await writeFile(backupPath, `${JSON.stringify(backup, null, 2)}\n`, {
    encoding: "utf8",
    mode: 0o600,
    flag: "wx",
  });

  const applied = [];
  const mutations = { devices: new Set(), profiles: new Map() };
  try {
    for (const definition of edgeHealthProfiles) {
      const existingProfile = backup.profiles.find(
        (profile) => profile.name === definition.name,
      ).value;
      if (existingProfile) {
        mutations.profiles.set(definition.name, { createdProfileId: null });
      }
      const savedProfile = await api.saveProfile(
        buildDeviceProfile(existingProfile, definition),
      );
      if (!existingProfile) {
        mutations.profiles.set(definition.name, {
          createdProfileId: savedProfile.id.id,
        });
      }
      for (const deviceName of definition.devices) {
        const device = backup.devices.find(
          (candidate) => candidate.name === deviceName,
        ).value;
        mutations.devices.add(deviceName);
        await api.saveDevice({
          ...device,
          deviceProfileId: savedProfile.id,
        });
        await api.saveServerAttributes(device.id.id, {
          inactivityTimeout: definition.inactivityTimeoutMs,
        });
      }
      applied.push({
        profile: definition.name,
        profileId: savedProfile.id.id,
        devices: definition.devices,
        inactivityTimeoutMs: definition.inactivityTimeoutMs,
      });
    }
  } catch (applyError) {
    try {
      await restoreEdgeHealthBackup(api, backup, mutations);
    } catch (rollbackError) {
      throw new AggregateError(
        [applyError, rollbackError],
        `ThingsBoard edge health apply and rollback both failed; restore from ${backupPath}`,
      );
    }
    throw new Error(
      `ThingsBoard edge health apply failed; confirmed mutations were rolled back: ${applyError.message}`,
      { cause: applyError },
    );
  }

  console.log(JSON.stringify({ dryRun: false, backupPath, applied }, null, 2));
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(process.argv[1]).href
) {
  await main();
}
