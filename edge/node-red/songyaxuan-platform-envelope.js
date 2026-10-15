// Node-RED function node「平台·形成信封」on the Songyaxuan Gateway (tab「Edge·采集与上报」).
// Input: a device function's message { <device>: [{ ts, values }] }.
// Output: one platform telemetry envelope (Connectivity MQTT contract, schemaVersion 2.0) per device
// message, or nothing for devices the platform has no source key for (the Gateway's own status). A meter
// message also yields the plant total, a temperature message the calculated cooling, and both the
// equipment readings (COPIES), below. Each goes on to「Outbox·形成事件」, which stores it in the
// durable outbox until the broker acknowledges it:
//   msg.outboxCommand = "enqueue";
//   msg.outboxRecord = { id: msg.payload.messageId, topic: msg.topic, payload: msg.payload };
// Registered devices and points: deploy/sites/songyaxuan.site.json.
const GATEWAY_ID = "01a14000-4000-7000-8000-000000000100";
const METERS = ["ammeter-unit1", "ammeter-unit2", "ammeter-unit3", "ammeter-unit4"];
const DEVICES = {
  temperature: { codes: null, unit: "Cel" },
  waterflow: { codes: { flowMeter: ["flow_meter", "m3/h"], flowVelocity: ["flow_velocity", "m/s"] } },
  et1010: { codes: null, unit: null },
};
for (const meter of METERS) {
  DEVICES[meter] = { codes: { powerTotal: ["power_total", "kW"], combinedActiveTotalElectricalEnergy: ["active_energy_total", "kWh"] } };
}
// Meter 4 was read from register 64, which is always 0; on 2026-10-14 its energy register was found at 60
// (76938 kWh then). The platform had counted it from 0, so it keeps counting from there: the reading
// less what the meter showed when the address was corrected, with no jump into one interval.
const ENERGY_BASELINE = { "ammeter-unit4": 76938 };
// Temperature channels and the header flow copied onto the equipment they measure, per the site's
// channel table (2026-10-15), as the points with `from` in deploy/sites/songyaxuan.site.json: the
// evaporator inlet (t2) and condenser inlet (t1) are shared by the four machines.
const COPIES = {
  temperature: {
    t2: METERS.map((meter) => [meter, "chw_entering_temperature"]),
    t1: METERS.map((meter) => [meter, "cw_entering_temperature"]),
    t3: [["ammeter-unit1", "chw_leaving_temperature"]],
    t4: [["ammeter-unit1", "cw_leaving_temperature"]],
    t5: [["ammeter-unit2", "chw_leaving_temperature"]],
    t6: [["ammeter-unit2", "cw_leaving_temperature"]],
    t7: [["ammeter-unit3", "chw_leaving_temperature"]],
    t8: [["ammeter-unit3", "cw_leaving_temperature"]],
    t9: [["ammeter-unit4", "chw_leaving_temperature"]],
    t10: [["ammeter-unit4", "cw_leaving_temperature"]],
    t12: [["room", "room_temperature"]],
    t13: [["outdoor", "ambient_temperature"]],
    t14: [["outdoor", "ambient_temperature_2"]],
    t15: [["outdoor", "ambient_wet_bulb_temperature"]],
  },
  waterflow: { flowMeter: [["chwp-main", "flow_rate"]] },
};
const COPY_UNITS = { temperature: "Cel", waterflow: "m3/h" };
// Spare inputs with no probe read 0 every minute; the platform has them INACTIVE and they are not sent.
const SPARE = { temperature: ["t11", "t20"] };
// A machine runs while its meter reads more than 5 kW (idle ones read under 1 kW); the chilled-water
// pumps run while the header flow exceeds 10 m³/h.
const runState = (running) => (running ? "RUNNING" : "STOPPED");

const source = msg.payload;
const device = Object.keys(source)[0];
const mapping = DEVICES[device];
if (!mapping) return null;
const sample = { ...source[device][0], values: { ...source[device][0].values } };
if (ENERGY_BASELINE[device] !== undefined && Number.isFinite(sample.values.combinedActiveTotalElectricalEnergy)) {
  sample.values.combinedActiveTotalElectricalEnergy -= ENERGY_BASELINE[device];
}

const points = [];
for (const [key, value] of Object.entries(sample.values)) {
  let code = key;
  let unit = mapping.unit;
  if (mapping.codes) {
    if (!mapping.codes[key]) continue;
    [code, unit] = mapping.codes[key];
  }
  if (typeof value !== "number" || !Number.isFinite(value)) continue;
  if ((SPARE[device] || []).includes(key)) continue;
  // An unconnected temperature input reads exactly 0; it is not a 0 °C measurement.
  const quality = device === "temperature" && value === 0 ? 1 : 0;
  const point = { code, value, quality };
  if (unit) point.unit = unit;
  points.push(point);
}
if (points.length === 0) return null;
if (METERS.includes(device) && Number.isFinite(sample.values.powerTotal)) {
  points.push({ code: "run_state", value: runState(sample.values.powerTotal > 5), quality: 0 });
}

// UUIDv7: 48-bit Unix milliseconds, version 7, variant 10, random rest.
function uuidv7(ms) {
  const hex = ms.toString(16).padStart(12, "0");
  const random = () => Math.floor(Math.random() * 0x10000).toString(16).padStart(4, "0");
  const variant = (0x8000 | (Math.floor(Math.random() * 0x4000))).toString(16);
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-7${random().slice(1)}-${variant}-${random()}${random()}${random()}`;
}

// The platform drops any message whose sequence does not exceed the last one it saw, so the
// sequence follows the clock and survives a restart: milliseconds × 1000 plus a counter.
function envelope(deviceId, deviceTimestamp, devicePoints) {
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
      payload: { devices: [{ deviceId, deviceTimestamp, points: devicePoints }] },
    },
  };
}

const out = [envelope(device, sample.ts, points)];

const copies = {};
for (const [field, targets] of Object.entries(COPIES[device] || {})) {
  const value = sample.values[field];
  if (typeof value !== "number" || !Number.isFinite(value) || (device === "temperature" && value === 0)) continue;
  for (const [target, code] of targets) {
    if (!copies[target]) copies[target] = [];
    copies[target].push({ code, value, quality: 0, unit: COPY_UNITS[device] });
  }
}
if (copies["chwp-main"]) copies["chwp-main"].push({ code: "run_state", value: runState(sample.values.flowMeter > 10), quality: 0 });
for (const [target, targetPoints] of Object.entries(copies)) out.push(envelope(target, sample.ts, targetPoints));

// The plant room has no total meter: the four machines' meters added up stand for it
// (「空调总电表（四台机组合计）」), once each has reported since the flow started.
if (METERS.includes(device)) {
  const latest = context.get("meterLatest") || {};
  latest[device] = { power: sample.values.powerTotal, energy: sample.values.combinedActiveTotalElectricalEnergy };
  context.set("meterLatest", latest);
  const readings = METERS.map((meter) => latest[meter]);
  if (readings.every((reading) => reading && Number.isFinite(reading.power) && Number.isFinite(reading.energy))) {
    const sum = (field) => Math.round(readings.reduce((total, reading) => total + reading[field], 0) * 1000) / 1000;
    out.push(envelope("ammeter-total", sample.ts, [
      { code: "power_total", value: sum("power"), quality: 0, unit: "kW" },
      { code: "active_energy_total", value: sum("energy"), quality: 0, unit: "kWh" },
    ]));
  }
}

// No BTU meter either: cooling is 1.163 × chilled-water flow (m³/h) × (return t16 − supply t18) in kW
// while the machines draw more than 20 kW, else zero (the pumps keep water moving while they are off,
// and that warming is not cooling), never below zero, with a flow reading from the last 3 minutes,
// integrated into kWh; a gap of more than 10 minutes is not integrated. Until all four meters have
// reported there is no power to judge by and nothing is sent. scripts/thingsboard-history-import.mjs
// derives the history the same way.
if (device === "waterflow" && Number.isFinite(sample.values.flowMeter)) {
  context.set("latestFlow", { ts: sample.ts, value: sample.values.flowMeter });
}
if (device === "temperature") {
  const flow = context.get("latestFlow");
  const meters = context.get("meterLatest") || {};
  const plantPower = METERS.every((meter) => meters[meter] && Number.isFinite(meters[meter].power))
    ? METERS.reduce((total, meter) => total + meters[meter].power, 0) : null;
  const supply = sample.values.t18;
  const returnTemperature = sample.values.t16;
  if (flow && sample.ts - flow.ts <= 180000 && plantPower !== null && supply > 0 && returnTemperature > 0) {
    const difference = returnTemperature - supply;
    const capacity = plantPower > 20 ? Math.max(0, 1.163 * flow.value * difference) : 0;
    const cooling = context.get("cooling") || { energy: 0, previous: null };
    if (cooling.previous && sample.ts - cooling.previous.ts <= 600000) {
      cooling.energy += (cooling.previous.capacity + capacity) / 2 * (sample.ts - cooling.previous.ts) / 3600000;
    }
    cooling.previous = { ts: sample.ts, capacity };
    context.set("cooling", cooling);
    out.push(envelope("btu-calc", sample.ts, [
      { code: "supply_water_temperature", value: supply, quality: 0, unit: "Cel" },
      { code: "return_water_temperature", value: returnTemperature, quality: 0, unit: "Cel" },
      { code: "temperature_difference", value: Math.round(difference * 100) / 100, quality: 0, unit: "K" },
      { code: "flow_rate", value: flow.value, quality: 0, unit: "m3/h" },
      { code: "instant_cooling_capacity", value: Math.round(capacity * 10) / 10, quality: 0, unit: "kW" },
      { code: "accumulated_cooling_energy", value: Math.round(cooling.energy * 1000) / 1000, quality: 0, unit: "kWh" },
    ]));
  }
}
return [out];
