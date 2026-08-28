# EG8200 Central Plant Simulator

A deterministic virtual edge gateway for validating the HVAC platform before a physical EG8200 is connected. The simulator models a small commercial central plant, publishes device telemetry to ThingsBoard through the Device HTTP API, polls server-side RPC, applies commands to the physical model, and reports subsequent state through normal telemetry.

## MVP topology

- `CHILLER-01`: water-cooled chiller
- `CHWP-01`: chilled-water pump
- `CWP-01`: cooling-water pump
- `CT-01`: cooling tower
- `METER-HVAC-TOTAL`: aggregate HVAC power meter
- `BTU-METER-01`: chilled-water thermal energy meter

The model includes chiller COP, cooling capacity, auxiliary power, accumulated electrical energy, accumulated cooling energy, pump affinity laws, cooling-tower approach temperature, and basic plant interlocks. Chilled-water telemetry is kept on one water-side energy balance (`Q ≈ 1.163 × flow(m³/h) × ΔT(°C)`). Pump frequency, cooling-tower fan speed, chiller capacity, and water temperatures evolve over elapsed plant time; a successful command applies a target, while actual device readback changes on later plant ticks.

## Supported commands

| Device | RPC method | Parameters | Validation |
| --- | --- | --- | --- |
| Chiller | `start`, `stop`, `resetFault` | none | — |
| Chiller | `setChilledWaterTemperatureSetpoint` | `setpointC` | 5–12 °C |
| Chiller | `setLoadLimit` | `loadLimitPct` | 20–100% |
| Pumps | `start`, `stop`, `resetFault` | none | — |
| Pumps | `setFrequency` | `frequencyHz` | 20–50 Hz |
| Cooling tower | `start`, `stop`, `resetFault` | none | — |
| Cooling tower | `setFanSpeed` | `fanSpeedPct` | 20–100% |

Each simulator command result contains `success`, a stable result `code`, and the applied value when relevant. Invalid commands do not mutate reported equipment state. Telemetry Business Revision is owned by the platform telemetry runtime, not by the simulator.

## ThingsBoard preparation

Create six ThingsBoard devices matching the IDs in `configs/central-plant.local.json`, using access-token credentials. Export the tokens through environment variables; do not put credentials in the config file:

```bash
export TB_TOKEN_CHILLER_01='...'
export TB_TOKEN_CHWP_01='...'
export TB_TOKEN_CWP_01='...'
export TB_TOKEN_CT_01='...'
export TB_TOKEN_HVAC_METER='...'
export TB_TOKEN_BTU_METER_01='...'
```

The default config targets `http://localhost:8080`. Plain HTTP is accepted only for `localhost`, `127.0.0.1`, and `host.docker.internal`; non-local ThingsBoard origins must use HTTPS. When the simulator runs in Docker and ThingsBoard runs on the host, use `http://host.docker.internal:8080`.

## Run

From the repository root:

```bash
go run ./tools/eg8200-simulator/cmd/eg8200-simulator \
  -config ./tools/eg8200-simulator/configs/central-plant.local.json
```

Or set:

```bash
export EG8200_SIMULATOR_CONFIG="$PWD/tools/eg8200-simulator/configs/central-plant.local.json"
go run ./tools/eg8200-simulator/cmd/eg8200-simulator
```

Diagnostics default to port `19092`:

```text
GET /health/live
GET /health/ready
```

Readiness becomes successful only after all six device telemetry publications complete in one simulation cycle.

## Container image

Build the non-root runtime image from the repository root:

```bash
npm run build:eg8200-simulator-image
```

Mount the config read-only and pass tokens through environment variables when running the image. The image does not contain credentials or a default production configuration.

## Telemetry behavior

The simulator publishes one timestamped ThingsBoard telemetry payload per device at the configured interval. Important relationships include:

- pump flow proportional to speed;
- pump and cooling-tower fan power proportional to the cube of speed;
- chiller power derived from cooling capacity and COP;
- COP affected by chilled-water setpoint, condenser-water temperature, and low-load operation;
- chiller cooling disabled when condenser-water flow, chilled-water flow, or cooling-tower availability is insufficient;
- total HVAC power includes chiller, both pumps, and cooling-tower fan power;
- BTU-meter cooling capacity uses the same plant cooling balance as the chiller.

## Tests

```bash
go test ./tools/eg8200-simulator/...
```

The current unit tests cover strict config and Scenario parsing, stepwise Scenario progression, energy balance, pump affinity behavior, command validation, plant interlocks, telemetry payloads, and simulator authority boundaries.

## Scope boundary

This MVP intentionally uses one ThingsBoard device token per simulated field device because the existing repository command path already uses the ThingsBoard Device HTTP API. A later ticket can add the ThingsBoard Gateway MQTT API when the project needs one logical EG8200 connection representing hundreds of downstream Modbus/BACnet devices. The HVAC physical model and command interface are kept independent of the transport so that migration does not require rewriting device behavior.
