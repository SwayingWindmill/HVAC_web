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

    def to_dict(self) -> dict[str, Any]:
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
            "verificationPlan": {
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
            },
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
    )
