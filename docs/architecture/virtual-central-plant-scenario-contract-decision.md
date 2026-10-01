# Virtual Central Plant — Minimal Scenario Contract Decision

Status: WAYFINDER DECISION
Wayfinder map: `wayfinder: virtual central plant`
Decision ticket: `Decide minimal Virtual Plant Scenario contract`
Decided: 2026-08-28

## Decision

The Virtual Central Plant v1 Scenario contract is a strict, typed, simulator-local configuration contract below `DeviceAdapter`. It owns only exogenous physical inputs and descriptive scenario metadata. It does not own Registry, Telemetry, Alarm, FDD, Forecast, Optimization, Notification, Work Order, frontend or IAM facts.

## Canonical document shape

`STATIC` and `SCENARIO` are explicit discriminated shapes. There is no implicit default mode and no compatibility guessing between them.

### STATIC

```json
{
  "schemaVersion": 1,
  "mode": "STATIC",
  "name": "summer-design",
  "description": "Optional descriptive text",
  "inputs": {
    "ambientDryBulbC": 35,
    "ambientWetBulbC": 28,
    "coolingLoadKw": 850
  }
}
```

### SCENARIO

```json
{
  "schemaVersion": 1,
  "mode": "SCENARIO",
  "name": "summer-day",
  "description": "Optional descriptive text",
  "steps": [
    {
      "offset": "0s",
      "inputs": {
        "ambientDryBulbC": 30,
        "ambientWetBulbC": 24,
        "coolingLoadKw": 420
      }
    },
    {
      "offset": "1h",
      "inputs": {
        "ambientDryBulbC": 33,
        "ambientWetBulbC": 25,
        "coolingLoadKw": 560
      }
    }
  ]
}
```

`name` and `description` are optional descriptive metadata only. They are not identity and must not participate in Registry, Telemetry or business contracts.

## v1 exogenous inputs

The exact v1 input set is:

- `ambientDryBulbC`
- `ambientWetBulbC`
- `coolingLoadKw`

`coolingLoadKw` is the total cooling demand at the central-plant boundary.

The prototype `loadFraction` field is rejected from the canonical contract and must not survive as an alias. It incorrectly couples building cooling demand to the rated capacity of one chiller and becomes ambiguous for multi-chiller plants.

Occupancy is not a v1 central-plant input. A future building/load model may use occupancy to derive `coolingLoadKw`, but the plant physics consumes the resulting cooling demand rather than taking responsibility for a building-demand model.

## Validation semantics

Required semantic validation is intentionally small:

- all input numbers are finite;
- `ambientWetBulbC <= ambientDryBulbC`;
- `coolingLoadKw >= 0`;
- SCENARIO steps are ordered by strictly increasing offset and the first step starts at `0s`;
- required fields for the selected mode are present and fields from the other mode are rejected.

No arbitrary defensive caps such as `loadFraction <= 1.2`, maximum seven-day duration or other speculative limits are part of the contract.

## Scenario progression

SCENARIO is stepwise in v1:

- no interpolation;
- each step remains active until the next step;
- after the final step, the last input set is held;
- scenarios do not loop automatically;
- live simulation remains 1x wall-clock; accelerated historical progression belongs to the separate Historical Replay runner.

## File surfaces

JSON is the single complete canonical Scenario document format.

CSV is supported only as a fixed SCENARIO step-series input surface and is parsed into the same typed domain model.

Canonical CSV columns are:

```csv
offset,ambientDryBulbC,ambientWetBulbC,coolingLoadKw
0s,30,24,420
1h,33,25,560
2h,36,28,850
```

CSV does not support:

- STATIC mode;
- alternate/alias column names;
- dynamic columns;
- arbitrary metadata columns;
- plugin-defined fields.

The loader is a parser, not a second domain contract.

## Complexity boundary

v1 does not introduce:

- Datasource plugins/factories;
- scripting or expression engines;
- arbitrary source adapters;
- schema compatibility aliases;
- silent defaults for required physical inputs;
- simulator-specific business APIs or frontend models.

A new abstraction is justified only when a second concrete use case requires it.

## Implementation consequence

The existing Scenario prototype is evidence only. During implementation it should be changed to this canonical v1 contract in one authoritative migration rather than supporting both old and new field shapes.
