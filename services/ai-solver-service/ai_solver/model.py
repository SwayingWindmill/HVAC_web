"""Typed request/response parsing and validation for /v1/solve."""

from __future__ import annotations

from dataclasses import dataclass, field
import math
import re
from typing import Any

HARD_CHILLED_WATER_MIN_C = 5.0
HARD_PUMP_FREQ_MIN_HZ = 28.0
HARD_PUMP_FREQ_MAX_HZ = 50.0
HARD_FAN_SPEED_MIN_PCT = 25.0
HARD_FAN_SPEED_MAX_PCT = 100.0

UUID_RE = re.compile(r"^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$")


class SolverInputError(ValueError):
    """Raised when the solve payload violates schema or physical preconditions."""


class SolverInfeasibleError(ValueError):
    """Raised when no candidate satisfies frozen comfort and safety constraints."""


def require_finite(raw: Any, field_name: str) -> float:
    if isinstance(raw, bool) or not isinstance(raw, (int, float)):
        raise SolverInputError(f"{field_name} must be a number")
    val = float(raw)
    if not math.isfinite(val):
        raise SolverInputError(f"{field_name} must be finite")
    return val


def require_positive(raw: Any, field_name: str) -> float:
    val = require_finite(raw, field_name)
    if val <= 0.0:
        raise SolverInputError(f"{field_name} must be > 0")
    return val


def require_non_negative(raw: Any, field_name: str) -> float:
    val = require_finite(raw, field_name)
    if val < 0.0:
        raise SolverInputError(f"{field_name} must be >= 0")
    return val


def require_non_empty_str(raw: Any, field_name: str) -> str:
    if not isinstance(raw, str) or not raw.strip():
        raise SolverInputError(f"{field_name} must be a non-empty string")
    return raw.strip()


def require_dict(raw: Any, field_name: str) -> dict[str, Any]:
    if not isinstance(raw, dict):
        raise SolverInputError(f"{field_name} must be an object")
    return raw


@dataclass(frozen=True)
class HVACBaseline:
    daily_energy_kwh: float
    daily_cost: float
    supply_temp_c: float
    zone_temp_c: float


@dataclass(frozen=True)
class HVACConstraints:
    supply_temp_min_c: float
    supply_temp_max_c: float
    zone_temp_min_c: float
    zone_temp_max_c: float
    max_supply_temp_step_c: float


@dataclass(frozen=True)
class HVACResponseModel:
    daily_energy_delta_per_supply_c: float
    zone_temp_delta_per_supply_c: float
    energy_uncertainty_p90_kwh: float
    zone_temp_uncertainty_p90_c: float
    source: str = ""
    status: str = ""
    calibrated_at: str = ""


@dataclass(frozen=True)
class CoupledChillerSpec:
    id: str
    rated_cooling_kw: float = 600.0
    base_cop: float = 5.7


@dataclass(frozen=True)
class CoupledPlantSpec:
    chillers: tuple[CoupledChillerSpec, ...] = (
        CoupledChillerSpec(id="CHILLER-01", rated_cooling_kw=600.0, base_cop=5.8),
        CoupledChillerSpec(id="CHILLER-02", rated_cooling_kw=600.0, base_cop=5.6),
    )
    chwp_rated_kw: float = 45.0
    chwp_rated_flow_m3h: float = 220.0
    chwp_static_head_frac: float = 0.10
    chwp_min_hz: float = HARD_PUMP_FREQ_MIN_HZ
    chwp_max_hz: float = HARD_PUMP_FREQ_MAX_HZ
    cwp_rated_kw: float = 37.0
    cwp_rated_flow_m3h: float = 260.0
    cwp_static_head_frac: float = 0.12
    cwp_min_hz: float = HARD_PUMP_FREQ_MIN_HZ
    cwp_max_hz: float = HARD_PUMP_FREQ_MAX_HZ
    tower_rated_kw: float = 22.0
    tower_design_heat_rej_kw: float = 1420.0
    tower_min_approach_c: float = 1.6
    tower_load_approach_coeff: float = 1.8
    tower_nonlinear_exp: float = 1.25
    tower_low_fan_penalty_c: float = 1.4
    tower_min_fan_pct: float = HARD_FAN_SPEED_MIN_PCT
    tower_max_fan_pct: float = HARD_FAN_SPEED_MAX_PCT
    zone_thermal_mass_kwh_per_c: float = 300.0
    zone_setpoint_c: float = 24.0
    design_supply_temp_c: float = 7.0
    coil_approach_min_c: float = 4.5
    coil_flow_exp: float = 0.72


@dataclass(frozen=True)
class CoupledHorizonSlot:
    duration_hours: float
    cooling_load_kw: float
    ambient_dry_bulb_c: float
    ambient_wet_bulb_c: float | None = None
    rel_humidity_pct: float | None = None
    tariff_rate_cny: float = 0.75


@dataclass(frozen=True)
class CoupledContext:
    precooling_enabled: bool = False
    cop_scale: float = 1.0
    current_load_kw: float | None = None
    ambient_dry_bulb_c: float = 30.0
    ambient_wet_bulb_c: float | None = None
    rel_humidity_pct: float | None = None
    tariff_rate_cny: float | None = None
    plant: CoupledPlantSpec = field(default_factory=CoupledPlantSpec)
    horizon: tuple[CoupledHorizonSlot, ...] = field(default_factory=tuple)


@dataclass(frozen=True)
class SolverRequest:
    tenant_id: str
    site_id: str
    optimization_run_id: str
    input_snapshot_id: str
    deployment_revision_id: str
    objective: str
    horizon: str
    horizon_minutes: int
    granularity: str
    valid_from: str
    baseline: HVACBaseline
    constraints: HVACConstraints
    response_model: HVACResponseModel
    subject_type: str = "SITE"
    subject_id: str = ""
    coupled_context: CoupledContext | None = None


@dataclass(frozen=True)
class AuxiliaryTargetRecommendation:
    loop_key: str
    capability: str
    target_value: float
    unit: str
    rationale: str = ""

    def to_dict(self) -> dict[str, Any]:
        payload: dict[str, Any] = {
            "loopKey": self.loop_key,
            "capability": self.capability,
            "targetValue": self.target_value,
            "unit": self.unit,
        }
        if self.rationale:
            payload["rationale"] = self.rationale
        return payload


@dataclass(frozen=True)
class SolverResponse:
    id: str
    supply_temp_c: float
    projected_zone_temp_c: float
    daily_energy_kwh: float
    daily_cost: float
    expected_saving_kwh_per_day: float
    expected_cost_saving_per_day: float
    energy_uncertainty_p90_kwh: float
    zone_temp_uncertainty_p90_c: float
    risk_level: str
    comfort_margin_c: float
    restore_supply_temp_c: float
    solver_version: str
    auxiliary_targets: tuple[AuxiliaryTargetRecommendation, ...] = field(default_factory=tuple)
    coupled_plan: dict[str, Any] | None = None

    def to_dict(self) -> dict[str, Any]:
        verification_plan: dict[str, Any] = {
            "windowMinutes": 30,
            "requiredSignals": [
                "supply_temperature",
                "zone_temperature",
                "site_load",
            ],
            "successCriteria": [
                "zone_temperature_within_frozen_constraint",
                "site_load_not_worse_than_baseline",
            ],
        }
        if self.coupled_plan:
            verification_plan.update(self.coupled_plan)

        return {
            "id": self.id,
            "candidate": {
                "supplyTempC": round(self.supply_temp_c, 4),
                "projectedZoneTempC": round(self.projected_zone_temp_c, 4),
                "dailyEnergyKWh": round(self.daily_energy_kwh, 4),
                "dailyCost": round(self.daily_cost, 4),
            },
            "expectedImpact": {
                "energySavingKWhPerDay": round(self.expected_saving_kwh_per_day, 4),
                "costSavingPerDay": round(self.expected_cost_saving_per_day, 4),
            },
            "uncertainty": {
                "energyP90KWh": round(self.energy_uncertainty_p90_kwh, 4),
                "zoneTempP90C": round(self.zone_temp_uncertainty_p90_c, 4),
            },
            "risk": {
                "level": self.risk_level,
                "comfortMarginC": round(self.comfort_margin_c, 4),
            },
            "rollbackPlan": {
                "restoreSupplyTempC": round(self.restore_supply_temp_c, 4),
                "conditions": [
                    "zone_comfort_violation",
                    "energy_saving_not_observed",
                    "operator_cancelled",
                ],
            },
            "verificationPlan": verification_plan,
            "solverVersion": self.solver_version,
            "auxiliaryTargets": [item.to_dict() for item in self.auxiliary_targets],
        }


def parse_solver_request(payload: Any) -> SolverRequest:
    root = require_dict(payload, "request")

    tenant_id = require_non_empty_str(root.get("tenantId"), "tenantId")
    site_id = require_non_empty_str(root.get("siteId"), "siteId")
    opt_run_id = require_non_empty_str(root.get("optimizationRunId"), "optimizationRunId")
    for name, val in (("tenantId", tenant_id), ("siteId", site_id), ("optimizationRunId", opt_run_id)):
        if not UUID_RE.match(val):
            raise SolverInputError(f"{name} must be a UUID")

    input_snapshot_id = require_non_empty_str(root.get("inputSnapshotId"), "inputSnapshotId")
    deployment_rev_id = require_non_empty_str(root.get("deploymentRevisionId"), "deploymentRevisionId")
    objective = require_non_empty_str(root.get("objective"), "objective")
    horizon = require_non_empty_str(root.get("horizon"), "horizon")
    raw_horizon_minutes = root.get("horizonMinutes")
    if isinstance(raw_horizon_minutes, bool) or not isinstance(raw_horizon_minutes, int) or raw_horizon_minutes <= 0:
        raise SolverInputError("horizonMinutes must be a positive integer")
    granularity = require_non_empty_str(root.get("granularity"), "granularity")
    valid_from = require_non_empty_str(root.get("validFrom"), "validFrom")

    raw_base = require_dict(root.get("baseline"), "baseline")
    baseline = HVACBaseline(
        daily_energy_kwh=require_positive(raw_base.get("dailyEnergyKWh"), "baseline.dailyEnergyKWh"),
        daily_cost=require_non_negative(raw_base.get("dailyCost"), "baseline.dailyCost"),
        supply_temp_c=require_finite(raw_base.get("supplyTempC"), "baseline.supplyTempC"),
        zone_temp_c=require_finite(raw_base.get("zoneTempC"), "baseline.zoneTempC"),
    )

    raw_cons = require_dict(root.get("constraints"), "constraints")
    supply_min = require_finite(raw_cons.get("supplyTempMinC"), "constraints.supplyTempMinC")
    supply_max = require_finite(raw_cons.get("supplyTempMaxC"), "constraints.supplyTempMaxC")
    zone_min = require_finite(raw_cons.get("zoneTempMinC"), "constraints.zoneTempMinC")
    zone_max = require_finite(raw_cons.get("zoneTempMaxC"), "constraints.zoneTempMaxC")
    max_step = require_positive(raw_cons.get("maxSupplyTempStepC"), "constraints.maxSupplyTempStepC")
    if supply_min > supply_max:
        raise SolverInputError("constraints.supplyTempMinC must be <= supplyTempMaxC")
    if zone_min > zone_max:
        raise SolverInputError("constraints.zoneTempMinC must be <= zoneTempMaxC")

    constraints = HVACConstraints(
        supply_temp_min_c=supply_min,
        supply_temp_max_c=supply_max,
        zone_temp_min_c=zone_min,
        zone_temp_max_c=zone_max,
        max_supply_temp_step_c=max_step,
    )

    raw_model = require_dict(root.get("responseModel"), "responseModel")
    response_model = HVACResponseModel(
        daily_energy_delta_per_supply_c=require_finite(
            raw_model.get("dailyEnergyDeltaKWhPerSupplyTempC"),
            "responseModel.dailyEnergyDeltaKWhPerSupplyTempC",
        ),
        zone_temp_delta_per_supply_c=require_finite(
            raw_model.get("zoneTempDeltaCPerSupplyTempC"),
            "responseModel.zoneTempDeltaCPerSupplyTempC",
        ),
        energy_uncertainty_p90_kwh=require_non_negative(
            raw_model.get("energyUncertaintyP90KWh"),
            "responseModel.energyUncertaintyP90KWh",
        ),
        zone_temp_uncertainty_p90_c=require_non_negative(
            raw_model.get("zoneTempUncertaintyP90C"),
            "responseModel.zoneTempUncertaintyP90C",
        ),
        source=str(raw_model.get("source") or "").strip(),
        status=str(raw_model.get("status") or "").strip(),
        calibrated_at=str(raw_model.get("calibratedAt") or "").strip(),
    )

    coupled_context: CoupledContext | None = None
    raw_coupled = root.get("coupledContext")
    if raw_coupled is not None:
        coupled_context = _parse_coupled_context(raw_coupled)

    return SolverRequest(
        tenant_id=tenant_id,
        site_id=site_id,
        optimization_run_id=opt_run_id,
        input_snapshot_id=input_snapshot_id,
        deployment_revision_id=deployment_rev_id,
        objective=objective,
        horizon=horizon,
        horizon_minutes=raw_horizon_minutes,
        granularity=granularity,
        valid_from=valid_from,
        baseline=baseline,
        constraints=constraints,
        response_model=response_model,
        subject_type=str(root.get("subjectType") or "SITE").strip() or "SITE",
        subject_id=str(root.get("subjectId") or site_id).strip() or site_id,
        coupled_context=coupled_context,
    )


def _optional_finite(raw: Any, field_name: str, default: float | None = None) -> float | None:
    if raw is None:
        return default
    return require_finite(raw, field_name)


def _parse_coupled_plant_spec(raw: Any) -> CoupledPlantSpec:
    if raw is None:
        return CoupledPlantSpec()
    obj = require_dict(raw, "coupledContext.plant")
    defaults = CoupledPlantSpec()
    chillers = defaults.chillers
    raw_chillers = obj.get("chillers")
    if raw_chillers is not None:
        if not isinstance(raw_chillers, list) or not raw_chillers:
            raise SolverInputError("coupledContext.plant.chillers must be a non-empty list")
        parsed_chillers: list[CoupledChillerSpec] = []
        for idx, item in enumerate(raw_chillers):
            ch_obj = require_dict(item, f"coupledContext.plant.chillers[{idx}]")
            parsed_chillers.append(
                CoupledChillerSpec(
                    id=str(ch_obj.get("id") or f"CHILLER-{idx + 1:02d}").strip(),
                    rated_cooling_kw=require_positive(
                        ch_obj.get("ratedCoolingKw", 600.0),
                        f"coupledContext.plant.chillers[{idx}].ratedCoolingKw",
                    ),
                    base_cop=require_positive(
                        ch_obj.get("baseCop", 5.7),
                        f"coupledContext.plant.chillers[{idx}].baseCop",
                    ),
                )
            )
        chillers = tuple(parsed_chillers)

    def pos(key: str, default_val: float) -> float:
        if key not in obj or obj[key] is None:
            return default_val
        return require_positive(obj[key], f"coupledContext.plant.{key}")

    def non_neg(key: str, default_val: float) -> float:
        if key not in obj or obj[key] is None:
            return default_val
        return require_non_negative(obj[key], f"coupledContext.plant.{key}")

    total_cap = sum(ch.rated_cooling_kw for ch in chillers)
    weighted_cop = sum(ch.rated_cooling_kw * ch.base_cop for ch in chillers) / max(1.0, total_cap)
    return CoupledPlantSpec(
        chillers=chillers,
        chwp_rated_kw=pos("chwpRatedKw", defaults.chwp_rated_kw),
        chwp_rated_flow_m3h=pos("chwpRatedFlowM3h", defaults.chwp_rated_flow_m3h),
        chwp_static_head_frac=non_neg("chwpStaticHeadFrac", defaults.chwp_static_head_frac),
        chwp_min_hz=pos("chwpMinHz", defaults.chwp_min_hz),
        chwp_max_hz=pos("chwpMaxHz", defaults.chwp_max_hz),
        cwp_rated_kw=pos("cwpRatedKw", defaults.cwp_rated_kw),
        cwp_rated_flow_m3h=pos("cwpRatedFlowM3h", defaults.cwp_rated_flow_m3h),
        cwp_static_head_frac=non_neg("cwpStaticHeadFrac", defaults.cwp_static_head_frac),
        cwp_min_hz=pos("cwpMinHz", defaults.cwp_min_hz),
        cwp_max_hz=pos("cwpMaxHz", defaults.cwp_max_hz),
        tower_rated_kw=pos("towerRatedKw", defaults.tower_rated_kw),
        tower_design_heat_rej_kw=pos("towerDesignHeatRejKw", total_cap * (1.0 + 1.0 / weighted_cop)),
        tower_min_approach_c=pos("towerMinApproachC", defaults.tower_min_approach_c),
        tower_load_approach_coeff=non_neg("towerLoadApproachCoeff", defaults.tower_load_approach_coeff),
        tower_nonlinear_exp=pos("towerNonlinearExp", defaults.tower_nonlinear_exp),
        tower_low_fan_penalty_c=non_neg("towerLowFanPenaltyC", defaults.tower_low_fan_penalty_c),
        tower_min_fan_pct=pos("towerMinFanPct", defaults.tower_min_fan_pct),
        tower_max_fan_pct=pos("towerMaxFanPct", defaults.tower_max_fan_pct),
        zone_thermal_mass_kwh_per_c=pos("zoneThermalMassKwhPerC", total_cap * 0.25),
        zone_setpoint_c=require_finite(obj.get("zoneSetpointC", defaults.zone_setpoint_c), "coupledContext.plant.zoneSetpointC"),
        design_supply_temp_c=require_finite(obj.get("designSupplyTempC", defaults.design_supply_temp_c), "coupledContext.plant.designSupplyTempC"),
        coil_approach_min_c=pos("coilApproachMinC", defaults.coil_approach_min_c),
        coil_flow_exp=pos("coilFlowExp", defaults.coil_flow_exp),
    )


def _parse_coupled_context(raw: Any) -> CoupledContext:
    obj = require_dict(raw, "coupledContext")
    precool = bool(obj.get("precoolingEnabled", False))
    cop_scale = require_positive(obj.get("copScale", 1.0), "coupledContext.copScale")
    plant = _parse_coupled_plant_spec(obj.get("plant"))

    horizon_slots: list[CoupledHorizonSlot] = []
    raw_horizon = obj.get("horizon")
    if raw_horizon is not None:
        if not isinstance(raw_horizon, list):
            raise SolverInputError("coupledContext.horizon must be a list")
        for idx, raw_slot in enumerate(raw_horizon):
            sl = require_dict(raw_slot, f"coupledContext.horizon[{idx}]")
            horizon_slots.append(
                CoupledHorizonSlot(
                    duration_hours=require_positive(sl.get("durationHours", 1.0), f"coupledContext.horizon[{idx}].durationHours"),
                    cooling_load_kw=require_positive(sl.get("coolingLoadKw"), f"coupledContext.horizon[{idx}].coolingLoadKw"),
                    ambient_dry_bulb_c=require_finite(sl.get("ambientDryBulbC", 30.0), f"coupledContext.horizon[{idx}].ambientDryBulbC"),
                    ambient_wet_bulb_c=_optional_finite(sl.get("ambientWetBulbC"), f"coupledContext.horizon[{idx}].ambientWetBulbC"),
                    rel_humidity_pct=_optional_finite(sl.get("relHumidityPct"), f"coupledContext.horizon[{idx}].relHumidityPct"),
                    tariff_rate_cny=require_non_negative(sl.get("tariffRateCny", 0.75), f"coupledContext.horizon[{idx}].tariffRateCny"),
                )
            )

    return CoupledContext(
        precooling_enabled=precool,
        cop_scale=max(0.55, min(1.35, cop_scale)),
        current_load_kw=_optional_finite(obj.get("currentLoadKw"), "coupledContext.currentLoadKw"),
        ambient_dry_bulb_c=float(_optional_finite(obj.get("ambientDryBulbC"), "coupledContext.ambientDryBulbC", 30.0) or 30.0),
        ambient_wet_bulb_c=_optional_finite(obj.get("ambientWetBulbC"), "coupledContext.ambientWetBulbC"),
        rel_humidity_pct=_optional_finite(obj.get("relHumidityPct"), "coupledContext.relHumidityPct"),
        tariff_rate_cny=_optional_finite(obj.get("tariffRateCny"), "coupledContext.tariffRateCny"),
        plant=plant,
        horizon=tuple(horizon_slots),
    )
