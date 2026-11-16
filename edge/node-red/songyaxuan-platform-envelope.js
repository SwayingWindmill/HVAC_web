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
const METER_CONTROLLERS = { "ammeter-unit4": "ctrl-chiller-a", "ammeter-unit3": "ctrl-chiller-b", "ammeter-unit1": "ctrl-chiller-c", "ammeter-unit2": "ctrl-chiller-d" };

// 温度复制映射须与 deploy/sites/songyaxuan.site.json 的 from 一致
const COPIES = {
  temperature: {
    ...Object.fromEntries([["t1", "t6", "ammeter-unit4"], ["t2", "t7", "ammeter-unit3"], ["t3", "t8", "ammeter-unit1"], ["t4", "t9", "ammeter-unit2"]].flatMap(([chw, cw, m]) => [[chw, [[m, "chw_leaving_temperature"]]], [cw, [[m, "cw_leaving_temperature"]]]])),
    t10: [["ammeter-unit4", "chw_entering_temperature_branch"]],
    t16: METERS.map((m) => [m, "chw_entering_temperature"]),
    t5: METERS.map((m) => [m, "cw_entering_temperature"]),
    t55: [["cooling-tower", "cooling_tower_leaving_temperature"]],
    t12: [["cooling-tower", "cooling_tower_leaving_temperature"]],
    t13: [["cooling-tower", "cooling_tower_entering_temperature"]],
    t15: [["outdoor", "ambient_temperature"]],
    t53: [["room", "room_temperature"]],
    t54: [["room", "banquet_room_temperature"]],
  },
  waterflow: { flowMeter: [["chwp-main", "flow_rate"]] },
};
const COPY_UNITS = { temperature: "Cel", waterflow: "m3/h" };

// 没接探头或停用的通道不上报（如 20/51 备用或空槽、52 离线停用）。
const SPARE_PREFIX = { temperature: "t", "temperature-voltage": "v", "temperature-rssi": "rssi", "temperature-status": "status" };

const statePoint = (r) => ({ code: "run_state", value: r ? "RUNNING" : "STOPPED", quality: 0 });
const DO_CONTROLLERS = Object.fromEntries([1, 2, 3, 4].flatMap((i) => [[`u${i}1`, `ctrl-chwp-${i}`], [`u${i}2`, `ctrl-cwp-${i}`], [`u${i}3`, `ctrl-tower-${i}`]]));
const ET_KEYS = [1, 2, 3, 4, 5].flatMap((g) => [1, 2, 3, 4].map((c) => `u${g}${c}`));
const SHARED_AUX_KW = { "ammeter-unit1": { u31: 15, u32: 15, u33: 4 }, "ammeter-unit2": { u41: 15, u42: 15, u43: 4 } };
const extract = (v) => (v !== null && typeof v === "object"
  ? { value: v.value, quality: Number.isInteger(v.quality) ? v.quality : null }
  : { value: v, quality: null });
const isNum = Number.isFinite;
const numValue = (values, key) => { const v = extract(values[key]).value; return isNum(v) ? v : NaN; };

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
const et1010Coils = { ...(context.get("et1010Coils") || {}) };
const rawEt = source.et1010?.[0]?.values;
const etValues = rawEt && "u0" in rawEt ? Object.fromEntries(ET_KEYS.flatMap((k, i) => (`u${i}` in rawEt ? [[k, rawEt[`u${i}`]]] : []))) : rawEt;
if (etValues) {
  for (const ch of ["u31", "u32", "u33", "u41", "u42", "u43"]) {
    const v = numValue(etValues, ch);
    if (isNum(v)) et1010Coils[ch] = v > 0 ? 1 : 0;
  }
  context.set("et1010Coils", et1010Coils);
}
const isOfflineChannel = (device, key) => {
  if (!statusValues || (device !== "temperature" && device !== "temperature-voltage" && device !== "temperature-rssi")) return false;
  const match = /^[a-z]+(\d+)$/.exec(key);
  if (!match) return false;
  return extract(statusValues[`status${match[1]}`]).value === "OFFLINE";
};

const isFreshMeter = (reading, nowTs, needEnergy) =>
  Boolean(reading && isNum(reading.ts) && nowTs - reading.ts <= METER_FRESH_MS && isNum(reading.power) && (!needEnergy || isNum(reading.energy)));
const roundTo = (value, scale) => Math.round(value * scale) / scale;

for (const [device, samples] of Object.entries(source)) {
  const mapping = DEVICES[device];
  if (!mapping || !Array.isArray(samples) || !samples[0] || !samples[0].values) continue;
  const sample = samples[0];
  const vals = device === "et1010" ? etValues : sample.values;
  const baseline = ENERGY_BASELINE[device];
  const sparePrefix = SPARE_PREFIX[device];
  const isMeter = METERS.includes(device);
  const points = [];

  for (const [key, entryVal] of Object.entries(vals)) {
    let code = key;
    let unit = POINT_UNITS[key] || mapping.unit;
    if (mapping.codes) {
      if (!mapping.codes[key]) continue;
      [code, unit] = mapping.codes[key];
    }
    if (sparePrefix && /^(?:20|51|52)$/.test(key.slice(sparePrefix.length))) continue;

    let { value, quality } = extract(entryVal);
    if (baseline !== undefined && key === "combinedActiveTotalElectricalEnergy" && isNum(value)) value -= baseline;
    if (!isNum(value) && !(mapping.allowString && typeof value === "string" && value)) continue;
    if (quality === null) quality = (device === "temperature" && value === 0) || isOfflineChannel(device, key) ? 1 : 0;
    points.push(unit ? { code, value, quality, unit } : { code, value, quality });
  }
  const doVals = device === "et1010"
    ? Object.entries(DO_CONTROLLERS).flatMap(([ch, ctrl]) => {
        const v = numValue(vals, ch);
        return isNum(v) ? [[ctrl, v]] : [];
      })
    : null;
  if (doVals && doVals.length > 0) points.push(statePoint(doVals.some(([, v]) => v > 0)));
  if (points.length === 0) continue;

  const powerTotal = numValue(vals, "powerTotal");
  let meterRunning = null;
  if (isMeter && isNum(powerTotal)) {
    const meterRunState = { ...(context.get("meterRunState") || {}) };
    const shared = SHARED_AUX_KW[device];
    const auxKw = shared ? Object.entries(shared).reduce((sum, [ch, kw]) => sum + ((et1010Coils[ch] || 0) > 0 ? kw : 0), 0) : 0;
    const bounds = !shared || auxKw > 0 ? AB : METER_RUN_KW[device];
    const chillerKw = Math.max(0, powerTotal - auxKw);
    meterRunning = meterRunState[device] ? chillerKw > bounds.stopKw : chillerKw > bounds.startKw;
    meterRunState[device] = meterRunning;
    context.set("meterRunState", meterRunState);
    points.push(statePoint(meterRunning));
  }
  entries.push(entry(device, sample.ts, points));

  for (const [field, targets] of Object.entries(COPIES[device] || {})) {
    const parsed = extract(vals[field]);
    const copyQuality = parsed.quality ?? 0;
    if (!isNum(parsed.value)) continue;
    if (device === "temperature" && (parsed.value === 0 || copyQuality !== 0 || isOfflineChannel(device, field))) continue;
    for (const [target, code] of targets) {
      if (!copies[target]) copies[target] = { ts: sample.ts, points: [] };
      if (!copies[target].points.some((p) => p.code === code)) copies[target].points.push({ code, value: parsed.value, quality: 0, unit: COPY_UNITS[device] });
    }
  }

  // 4台冷冻机组电表在 15 分钟内都有新鲜读数时合计为总电表（用于对账）
  if (isMeter) {
    const energy = numValue(vals, "combinedActiveTotalElectricalEnergy") - (baseline || 0);
    const latest = context.get("meterLatest") || {};
    latest[device] = { ts: sample.ts, power: powerTotal, energy };
    context.set("meterLatest", latest);
    const readings = METERS.map((meter) => latest[meter]);
    if (readings.every((reading) => isFreshMeter(reading, sample.ts, true))) {
      const sum = (field) => roundTo(readings.reduce((total, reading) => total + reading[field], 0), 1000);
      entries.push(entry("ammeter-total", sample.ts, [
        { code: "power_total", value: sum("power"), quality: 0, unit: "kW" },
        { code: "active_energy_total", value: sum("energy"), quality: 0, unit: "kWh" },
      ]));
    }
  }

  // 制冷量计算：1.163 × 冷水流量(m³/h) × (总管回水 t16 − 总管供水 t18)
  if (device === "waterflow") {
    const flowMeter = numValue(vals, "flowMeter");
    if (isNum(flowMeter)) {
      context.set("latestFlow", { ts: sample.ts, value: flowMeter });
      if (copies["chwp-main"]) copies["chwp-main"].points.push(statePoint(flowMeter > 10));
    }
  }
  if (device === "temperature") {
    const flow = context.get("latestFlow");
    const meters = context.get("meterLatest") || {};
    const freshMeters = METERS.map((meter) => meters[meter]).filter((reading) => isFreshMeter(reading, sample.ts, false));
    const plantPower = freshMeters.length > 0 ? freshMeters.reduce((total, reading) => total + reading.power, 0) : null;
    const t18 = extract(vals.t18);
    const t16 = extract(vals.t16);
    const goodTemp = (t, key) => typeof t.value === "number" && t.value > 0 && (t.quality ?? 0) === 0 && !isOfflineChannel("temperature", key);
    if (flow && sample.ts - flow.ts <= 180000 && plantPower !== null && goodTemp(t18, "t18") && goodTemp(t16, "t16")) {
      const difference = t16.value - t18.value;
      const capacity = plantPower > 20 ? Math.max(0, 1.163 * flow.value * difference) : 0;
      const cooling = context.get("cooling") || { energy: 0, previous: null };
      if (cooling.previous && sample.ts - cooling.previous.ts <= 600000) {
        cooling.energy += (cooling.previous.capacity + capacity) / 2 * (sample.ts - cooling.previous.ts) / 3600000;
      }
      cooling.previous = { ts: sample.ts, capacity };
      context.set("cooling", cooling);
      entries.push(entry("btu-calc", sample.ts, [
        { code: "supply_water_temperature", value: t18.value, quality: 0, unit: "Cel" },
        { code: "return_water_temperature", value: t16.value, quality: 0, unit: "Cel" },
        { code: "temperature_difference", value: roundTo(difference, 100), quality: 0, unit: "K" },
        { code: "flow_rate", value: flow.value, quality: 0, unit: "m3/h" },
        { code: "instant_cooling_capacity", value: roundTo(capacity, 10), quality: 0, unit: "kW" },
        { code: "accumulated_cooling_energy", value: roundTo(cooling.energy, 1000), quality: 0, unit: "kWh" },
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
