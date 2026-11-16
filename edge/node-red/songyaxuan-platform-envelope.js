// Node-RED「平台·形成信封」for Songyaxuan: { <device>: [{ ts, values }] } -> telemetry envelope 2.0.
const GATEWAY_ID = "01a14000-4000-7000-8000-000000000100";
const AB = { startKw: 12.75, stopKw: 5 }, CD = { startKw: 57.75, stopKw: 45 };
const METER_RUN_KW = { "ammeter-unit1": CD, "ammeter-unit2": CD, "ammeter-unit3": AB, "ammeter-unit4": AB };
const METERS = Object.keys(METER_RUN_KW);
const METER_FRESH_MS = 900000;
const DEVICES = {
  temperature: { codes: null, unit: "Cel" },
  "temperature-voltage": { codes: null, unit: "mV" },
  "temperature-rssi": { codes: null, unit: "dBm" },
  "temperature-status": { codes: null, unit: null, allowString: true },
  waterflow: { codes: { flowMeter: ["flow_meter", "m3/h"], flowVelocity: ["flow_velocity", "m/s"] } },
  et1010: { codes: null, unit: null },
};
for (const meter of METERS) {
  DEVICES[meter] = { codes: { powerTotal: ["power_total", "kW"], combinedActiveTotalElectricalEnergy: ["active_energy_total", "kWh"] } };
}
const POINT_UNITS = { min_voltage_mv: "mV", min_rssi_dbm: "dBm" };
const ENERGY_BASELINE = { "ammeter-unit4": 76938 };

// 温度复制映射须与 deploy/sites/songyaxuan.site.json 的 from 一致
const COPIES = {
  temperature: {
    t1: [["ammeter-unit4", "chw_leaving_temperature"]],
    t2: [["ammeter-unit3", "chw_leaving_temperature"]],
    t3: [["ammeter-unit1", "chw_leaving_temperature"]],
    t4: [["ammeter-unit2", "chw_leaving_temperature"]],
    t6: [["ammeter-unit4", "cw_leaving_temperature"]],
    t7: [["ammeter-unit3", "cw_leaving_temperature"]],
    t8: [["ammeter-unit1", "cw_leaving_temperature"]],
    t9: [["ammeter-unit2", "cw_leaving_temperature"]],
    t10: [["ammeter-unit4", "chw_entering_temperature_branch"]],
    t16: METERS.map((meter) => [meter, "chw_entering_temperature"]),
    t5: METERS.map((meter) => [meter, "cw_entering_temperature"]),
    t12: [["cooling-tower", "cooling_tower_leaving_temperature"]],
    t13: [["cooling-tower", "cooling_tower_entering_temperature"]],
    t15: [["outdoor", "ambient_temperature"]],
    t53: [["room", "room_temperature"]],
    t54: [["room", "banquet_room_temperature"]],
  },
  waterflow: { flowMeter: [["chwp-main", "flow_rate"]] },
};
const COPY_UNITS = { temperature: "Cel", waterflow: "m3/h" };

// 没接探头或停用的通道不上报（如 20 备用、52 离线停用）。
const SPARE = {
  temperature: ["t20", "t52"],
  "temperature-voltage": ["v20", "v52"],
  "temperature-rssi": ["rssi20", "rssi52"],
  "temperature-status": ["status20", "status52"],
};

const runState = (running) => (running ? "RUNNING" : "STOPPED");
const statePoint = (running) => ({ code: "run_state", value: runState(running), quality: 0 });
const DO_CONTROLLERS = { u11: "ctrl-chwp-1", u12: "ctrl-cwp-1", u13: "ctrl-tower-1", u21: "ctrl-chwp-2", u22: "ctrl-cwp-2", u23: "ctrl-tower-2", u31: "ctrl-chwp-3", u32: "ctrl-cwp-3", u33: "ctrl-tower-3", u41: "ctrl-chwp-4", u42: "ctrl-cwp-4", u43: "ctrl-tower-4" };
const METER_CONTROLLERS = { "ammeter-unit4": "ctrl-chiller-a", "ammeter-unit3": "ctrl-chiller-b", "ammeter-unit1": "ctrl-chiller-c", "ammeter-unit2": "ctrl-chiller-d" };
const extract = (entryVal) => {
  if (entryVal !== null && typeof entryVal === "object") {
    return {
      value: entryVal.value,
      quality: Number.isInteger(entryVal.quality) ? entryVal.quality : null,
    };
  }
  return { value: entryVal, quality: null };
};
const isNum = Number.isFinite;
const numValue = (values, key) => {
  const value = extract(values[key]).value;
  return isNum(value) ? value : NaN;
};

// UUIDv7: 48-bit Unix milliseconds, version 7, variant 10, random rest.
function uuidv7(ms) {
  const hex = ms.toString(16).padStart(12, "0");
  const random = () => Math.floor(Math.random() * 0x10000).toString(16).padStart(4, "0");
  const variant = (0x8000 | (Math.floor(Math.random() * 0x4000))).toString(16);
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-7${random().slice(1)}-${variant}-${random()}${random()}${random()}`;
}

function envelope(devices) {
  const now = Date.now();
  const sequence = Math.max(now * 1000, (context.get("lastSequence") || 0) + 1);
  context.set("lastSequence", sequence);
  return {
    topic: `hvac/v1/${GATEWAY_ID}/up/telemetry`,
    qos: 1,
    retain: false,
    payload: {
      schemaVersion: "2.0",
      messageId: uuidv7(now),
      gatewayId: GATEWAY_ID,
      timestamp: now,
      sequence,
      replay: false,
      payload: { devices },
    },
  };
}

const source = msg.payload;
if (!source || typeof source !== "object") return null;

const entry = (deviceId, deviceTimestamp, devicePoints) => ({ deviceId, deviceTimestamp, points: devicePoints });
const entries = [];
const copies = {};
const statusValues = source["temperature-status"]?.[0]?.values || null;
const isOfflineChannel = (device, key) => {
  if (!statusValues || (device !== "temperature" && device !== "temperature-voltage" && device !== "temperature-rssi")) return false;
  const match = /^[a-z]+(\d+)$/.exec(key);
  if (!match) return false;
  return extract(statusValues[`status${match[1]}`]).value === "OFFLINE";
};

for (const [device, samples] of Object.entries(source)) {
  const mapping = DEVICES[device];
  if (!mapping || !Array.isArray(samples) || !samples[0] || !samples[0].values) continue;
  const sample = samples[0];
  const baseline = ENERGY_BASELINE[device];
  const spare = SPARE[device] || [];
  const points = [];

  for (const [key, entryVal] of Object.entries(sample.values)) {
    let code = key;
    let unit = POINT_UNITS[key] || mapping.unit;
    if (mapping.codes) {
      if (!mapping.codes[key]) continue;
      [code, unit] = mapping.codes[key];
    }
    if (spare.includes(key)) continue;

    let { value, quality } = extract(entryVal);
    if (baseline !== undefined && key === "combinedActiveTotalElectricalEnergy" && isNum(value)) {
      value -= baseline;
    }
    const isNumber = isNum(value);
    const isString = mapping.allowString && typeof value === "string" && value.length > 0;
    if (!isNumber && !isString) continue;

    if (quality === null) {
      // An unconnected temperature input reads 0, and offline probe voltage/RSSI registers are not live readings.
      if ((device === "temperature" && value === 0) || isOfflineChannel(device, key)) quality = 1;
      else quality = 0;
    }
    const point = { code, value, quality };
    if (unit) point.unit = unit;
    points.push(point);
  }
  const doVals = device === "et1010"
    ? Object.entries(DO_CONTROLLERS).flatMap(([ch, ctrl]) => {
        const v = numValue(sample.values, ch);
        return Number.isFinite(v) ? [[ctrl, v]] : [];
      })
    : null;
  if (doVals && doVals.length > 0) points.push(statePoint(doVals.some(([, v]) => v > 0)));
  if (points.length === 0) continue;

  const powerTotal = numValue(sample.values, "powerTotal");
  let meterRunning = null;
  if (METERS.includes(device) && Number.isFinite(powerTotal)) {
    const bounds = METER_RUN_KW[device];
    const meterRunState = { ...(context.get("meterRunState") || {}) };
    meterRunning = meterRunState[device] ? powerTotal > bounds.stopKw : powerTotal > bounds.startKw;
    meterRunState[device] = meterRunning;
    context.set("meterRunState", meterRunState);
    points.push(statePoint(meterRunning));
  }
  entries.push(entry(device, sample.ts, points));

  for (const [field, targets] of Object.entries(COPIES[device] || {})) {
    const parsed = extract(sample.values[field]);
    const copyQuality = parsed.quality ?? 0;
    if (!isNum(parsed.value)) continue;
    if (device === "temperature" && (parsed.value === 0 || copyQuality !== 0 || isOfflineChannel(device, field))) continue;
    for (const [target, code] of targets) {
      if (!copies[target]) copies[target] = { ts: sample.ts, points: [] };
      copies[target].points.push({ code, value: parsed.value, quality: 0, unit: COPY_UNITS[device] });
    }
  }
  if (device === "waterflow" && copies["chwp-main"]) {
    const flowMeter = numValue(sample.values, "flowMeter");
    if (Number.isFinite(flowMeter)) {
      copies["chwp-main"].points.push(statePoint(flowMeter > 10));
    }
  }

  const isFreshMeter = (reading, nowTs) =>
    Boolean(reading && Number.isFinite(reading.ts) && nowTs - reading.ts <= METER_FRESH_MS && Number.isFinite(reading.power) && Number.isFinite(reading.energy));

  // 4台冷冻机组电表在 15 分钟内都有新鲜读数时合计为总电表（用于对账）
  if (METERS.includes(device)) {
    const energy = numValue(sample.values, "combinedActiveTotalElectricalEnergy") - (baseline || 0);
    const latest = context.get("meterLatest") || {};
    latest[device] = { ts: sample.ts, power: powerTotal, energy };
    context.set("meterLatest", latest);
    const readings = METERS.map((meter) => latest[meter]);
    if (readings.every((reading) => isFreshMeter(reading, sample.ts))) {
      const sum = (field) => Math.round(readings.reduce((total, reading) => total + reading[field], 0) * 1000) / 1000;
      entries.push(entry("ammeter-total", sample.ts, [
        { code: "power_total", value: sum("power"), quality: 0, unit: "kW" },
        { code: "active_energy_total", value: sum("energy"), quality: 0, unit: "kWh" },
      ]));
    }
  }

  // 制冷量计算：1.163 × 冷水流量(m³/h) × (总管回水 t16 − 总管供水 t18)
  if (device === "waterflow") {
    const flowMeter = numValue(sample.values, "flowMeter");
    if (Number.isFinite(flowMeter)) context.set("latestFlow", { ts: sample.ts, value: flowMeter });
  }
  if (device === "temperature") {
    const flow = context.get("latestFlow");
    const meters = context.get("meterLatest") || {};
    const freshMeters = METERS.map((meter) => meters[meter]).filter(
      (reading) => reading && Number.isFinite(reading.ts) && sample.ts - reading.ts <= METER_FRESH_MS && Number.isFinite(reading.power),
    );
    const plantPower = freshMeters.length > 0
      ? freshMeters.reduce((total, reading) => total + reading.power, 0) : null;
    const t18 = extract(sample.values.t18);
    const t16 = extract(sample.values.t16);
    const supply = t18.value;
    const returnTemperature = t16.value;
    const goodTemps = typeof supply === "number" && typeof returnTemperature === "number"
      && supply > 0 && returnTemperature > 0
      && (t18.quality ?? 0) === 0 && (t16.quality ?? 0) === 0
      && !isOfflineChannel("temperature", "t18") && !isOfflineChannel("temperature", "t16");
    if (flow && sample.ts - flow.ts <= 180000 && plantPower !== null && goodTemps) {
      const difference = returnTemperature - supply;
      const capacity = plantPower > 20 ? Math.max(0, 1.163 * flow.value * difference) : 0;
      const cooling = context.get("cooling") || { energy: 0, previous: null };
      if (cooling.previous && sample.ts - cooling.previous.ts <= 600000) {
        cooling.energy += (cooling.previous.capacity + capacity) / 2 * (sample.ts - cooling.previous.ts) / 3600000;
      }
      cooling.previous = { ts: sample.ts, capacity };
      context.set("cooling", cooling);
      entries.push(entry("btu-calc", sample.ts, [
        { code: "supply_water_temperature", value: supply, quality: 0, unit: "Cel" },
        { code: "return_water_temperature", value: returnTemperature, quality: 0, unit: "Cel" },
        { code: "temperature_difference", value: Math.round(difference * 100) / 100, quality: 0, unit: "K" },
        { code: "flow_rate", value: flow.value, quality: 0, unit: "m3/h" },
        { code: "instant_cooling_capacity", value: Math.round(capacity * 10) / 10, quality: 0, unit: "kW" },
        { code: "accumulated_cooling_energy", value: Math.round(cooling.energy * 1000) / 1000, quality: 0, unit: "kWh" },
      ]));
    }
  }
  if (doVals) for (const [ctrl, val] of doVals) entries.push(entry(ctrl, sample.ts, [statePoint(val > 0)]));
  if (METER_CONTROLLERS[device] && meterRunning !== null) entries.push(entry(METER_CONTROLLERS[device], sample.ts, [statePoint(meterRunning)]));
}

for (const [target, copied] of Object.entries(copies)) {
  entries.push(entry(target, copied.ts, copied.points));
}
if (entries.length === 0) return null;
return envelope(entries);
