# ADR 0011 — Spatial asset, sensor and telemetry point model

Status: accepted

Date: 2026-08-03

## Context

ADR 0001 separates Equipment from Device, but the platform now needs to represent commercial HVAC installations where:

- Equipment is installed in a recursive spatial hierarchy below a Site;
- one Equipment may be observed or controlled through several Device Endpoints;
- one Device Endpoint may serve several Equipment objects or an Area;
- a Sensor may be embedded, externally wired, or an independently communicating Device;
- one Sensor may emit several Telemetry Points;
- every Telemetry Point is sampled, timestamped, quality-classified and published independently;
- measured, calculated, command and feedback points have different authority and lifecycle rules.

Treating every sensor as a Device inflates inventory and destroys the Equipment view. Treating every sensor as an anonymous telemetry key loses calibration, provenance, replacement history and independent reporting semantics.

## Decision

### Canonical hierarchy

The canonical inventory hierarchy is:

```text
Organization
└─ Site
   ├─ Area
   │  ├─ Child Area
   │  ├─ Equipment
   │  ├─ Device Endpoint
   │  ├─ Independent Sensor Device
   │  └─ Calculated Point
   ├─ Equipment
   │  ├─ Device Endpoint Binding
   │  ├─ Sensor Binding
   │  └─ Calculated Point
   ├─ Site Gateway Endpoint
   └─ Site Calculated Point
```

`Area` is recursive and represents spatial containment. It does not replace Equipment or Device identity.

### Identity and bindings

- `Equipment` remains the maintainable physical business asset.
- `Device Endpoint` is the existing Registry `Device`: an addressable communication endpoint.
- `Sensor` is a first-class measurement identity with calibration and replacement lifecycle.
- `Telemetry Point` is a typed data channel. A Sensor may own zero or more points; a Device may also expose controller-internal points without a separately managed Sensor.
- Equipment, Device, Sensor and Area relationships are versioned bindings with `validFrom` and optional `validTo`; current placement must never overwrite history.
- Mounting location and measured subject are independent. A Sensor may be mounted in one Area while measuring a Site, Area or Equipment elsewhere.
- A Device may be bound to an Equipment, an Area, or both with explicit roles such as `CONTROLLER`, `METER`, `SENSOR`, `GATEWAY` or `SUPERVISORY_CONTROLLER`.

### Point authority

Telemetry Points are classified as:

- `MEASURED`: direct sensor or controller observation;
- `CALCULATED`: derived from one or more input points and a versioned formula;
- `STATE`: reported equipment or controller state;
- `COMMAND`: desired value submitted to a writable endpoint;
- `FEEDBACK`: authoritative acknowledgement or physical feedback for a command.

Command and feedback points are distinct. A calculated point records its ordered input references and formula revision.

### Independent observations

Every accepted observation preserves at least:

- reporting Device identity;
- optional Sensor identity;
- point key;
- sampled/observed time;
- received time;
- sequence or source position;
- value and engineering unit;
- quality and quality reason;
- source protocol and source address when available.

Sample interval, publish interval and aggregation interval are independent. A Device containing many Sensors may publish each point independently with its own timestamp. Independent reporting does not require each Sensor to be a ThingsBoard Device.

### ThingsBoard mapping

- EG8200 is represented as one Gateway Device when Gateway MQTT transport is enabled.
- Physical controllers, meters, VAV controllers and independently communicating sensors may be downstream ThingsBoard Devices.
- Embedded or externally wired Sensors normally publish as separate telemetry keys on their reporting Device.
- An independently communicating Sensor may be both a Registry Sensor and a Registry/ThingsBoard Device.
- ThingsBoard identifiers remain external bindings and never become platform identity.

### Spatial and count semantics

The product must not expose a single ambiguous “device count”. Inventory summaries distinguish:

- Area count;
- Equipment count;
- Device Endpoint count;
- Sensor count;
- Telemetry Point count;
- current usable, suspect and unavailable point counts.

### Security and tenancy

All new Registry entities carry Organization and Site scope, use UUIDv7 identities, forced PostgreSQL RLS and the existing Site authorization context. Point-level telemetry authorization continues to use exact Device and telemetry-key scope; Sensor identity never broadens an authorized key set.

## Consequences

- Existing Equipment and Device identities remain valid.
- The Registry gains Area, Sensor, Telemetry Point and versioned binding tables.
- The EG8200 simulator configuration moves to schema version 2 and validates the complete identity graph before publishing.
- Simulator publication changes from one synchronized Device snapshot to independently scheduled point observations.
- Existing clients may continue using Equipment and Device collections; richer spatial and sensor views can be introduced without redefining those identities.
- Expanding the commercial HVAC fixture becomes data-driven instead of adding more singleton fields to `PlantConfig`.

## Invariants

1. Area parent and child belong to the same Organization and Site, and Area cycles are rejected.
2. A current primary Equipment placement has at most one active `INSTALLED_IN` Area binding.
3. A Sensor reports through at most one active Device Endpoint at a time.
4. A point key is unique within its reporting Device.
5. A `CALCULATED` point has at least one input reference; non-calculated points cannot own calculated inputs.
6. A point observation is authoritative only for its exact Device and point key.
7. Replacing a Sensor closes the previous binding; historical observations retain their original Sensor identity.
8. Site, Area and Equipment calculated points never erase their input provenance or formula revision.
