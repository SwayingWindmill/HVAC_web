"""Deterministic HVAC physics optimization solver."""

from __future__ import annotations

from dataclasses import dataclass
import hashlib
import math

from ai_solver.model import (
    AuxiliaryTargetRecommendation,
    HARD_CHILLED_WATER_MIN_C,
    HARD_FAN_SPEED_MAX_PCT,
    HARD_FAN_SPEED_MIN_PCT,
    HARD_PUMP_FREQ_MAX_HZ,
    HARD_PUMP_FREQ_MIN_HZ,
    SolverInfeasibleError,
    SolverRequest,
    SolverResponse,
)

SOLVER_VERSION = "python-hvac-v1"
GRID_STEP_C = 0.1


def deterministic_uuidv7(seed: str) -> str:
    digest = bytearray(hashlib.sha256(seed.encode("utf-8")).digest()[:16])
    digest[6] = (digest[6] & 0x0F) | 0x70
    digest[8] = (digest[8] & 0x3F) | 0x80
    h = bytes(digest).hex()
    return f"{h[0:8]}-{h[8:12]}-{h[12:16]}-{h[16:20]}-{h[20:32]}"


def clamp(value: float, lo: float, hi: float) -> float:
    return max(lo, min(hi, value))


@dataclass(frozen=True)
class _CandidateEval:
    supply_temp_c: float
    projected_zone_temp_c: float
    daily_energy_kwh: float
    daily_cost: float
    comfort_margin_c: float
    delta_c: float


class HVACPhysicsSolver:
    """Physics-constrained fine-grid HVAC setpoint and VFD coordination solver."""

    def solve(self, req: SolverRequest) -> SolverResponse:
        base = req.baseline
        cons = req.constraints
        rm = req.response_model

        hard_min_c = max(HARD_CHILLED_WATER_MIN_C, cons.supply_temp_min_c)
        hard_max_c = cons.supply_temp_max_c
        if hard_min_c > hard_max_c:
            raise SolverInfeasibleError(
                f"effective supply bounds [{hard_min_c:.2f}, {hard_max_c:.2f}] are empty"
            )

        cost_per_kwh = base.daily_cost / base.daily_energy_kwh
        step_limit = cons.max_supply_temp_step_c
        # Generate deterministic fine grid within [-step_limit, +step_limit] plus exact 0 and bounds.
        count = max(1, int(math.floor(step_limit / GRID_STEP_C + 1e-9)))
        deltas = {0.0, -step_limit, step_limit}
        for idx in range(-count, count + 1):
            deltas.add(round(idx * GRID_STEP_C, 4))

        best: _CandidateEval | None = None
        for delta in sorted(deltas):
            if abs(delta) > step_limit + 1e-9:
                continue
            supply_c = base.supply_temp_c + delta
            if supply_c < hard_min_c - 1e-9 or supply_c > hard_max_c + 1e-9:
                continue
            supply_c = clamp(supply_c, hard_min_c, hard_max_c)
            effective_delta = supply_c - base.supply_temp_c

            proj_zone = base.zone_temp_c + rm.zone_temp_delta_per_supply_c * effective_delta
            zone_low = proj_zone - rm.zone_temp_uncertainty_p90_c
            zone_high = proj_zone + rm.zone_temp_uncertainty_p90_c
            if zone_low < cons.zone_temp_min_c - 1e-9 or zone_high > cons.zone_temp_max_c + 1e-9:
                continue

            margin = min(zone_low - cons.zone_temp_min_c, cons.zone_temp_max_c - zone_high)
            energy_kwh = max(0.0, base.daily_energy_kwh + rm.daily_energy_delta_per_supply_c * effective_delta)
            cost = energy_kwh * cost_per_kwh

            cand = _CandidateEval(
                supply_temp_c=supply_c,
                projected_zone_temp_c=proj_zone,
                daily_energy_kwh=energy_kwh,
                daily_cost=cost,
                comfort_margin_c=max(0.0, margin),
                delta_c=effective_delta,
            )
            if best is None or self._is_better(cand, best):
                best = cand

        if best is None:
            raise SolverInfeasibleError(
                "no HVAC setpoint candidate satisfies freeze floor, step limit, and comfort P90 bounds"
            )

        risk_level = "LOW"
        if best.comfort_margin_c <= max(0.5, 2.0 * rm.zone_temp_uncertainty_p90_c):
            risk_level = "MEDIUM"

        aux_targets = self._compute_auxiliary_targets(best.delta_c, best.comfort_margin_c)
        rec_id = deterministic_uuidv7(f"{req.optimization_run_id}:recommendation:1")

        return SolverResponse(
            id=rec_id,
            supply_temp_c=best.supply_temp_c,
            projected_zone_temp_c=best.projected_zone_temp_c,
            daily_energy_kwh=best.daily_energy_kwh,
            daily_cost=best.daily_cost,
            expected_saving_kwh_per_day=base.daily_energy_kwh - best.daily_energy_kwh,
            expected_cost_saving_per_day=base.daily_cost - best.daily_cost,
            energy_uncertainty_p90_kwh=rm.energy_uncertainty_p90_kwh,
            zone_temp_uncertainty_p90_c=rm.zone_temp_uncertainty_p90_c,
            risk_level=risk_level,
            comfort_margin_c=best.comfort_margin_c,
            restore_supply_temp_c=base.supply_temp_c,
            solver_version=SOLVER_VERSION,
            auxiliary_targets=aux_targets,
        )

    @staticmethod
    def _is_better(cand: _CandidateEval, current: _CandidateEval) -> bool:
        if cand.daily_cost < current.daily_cost - 1e-6:
            return True
        if abs(cand.daily_cost - current.daily_cost) <= 1e-6:
            if cand.comfort_margin_c > current.comfort_margin_c + 1e-6:
                return True
            if abs(cand.comfort_margin_c - current.comfort_margin_c) <= 1e-6:
                return abs(cand.delta_c) < abs(current.delta_c)
        return False

    @staticmethod
    def _compute_auxiliary_targets(
        supply_delta_c: float,
        comfort_margin_c: float,
    ) -> tuple[AuxiliaryTargetRecommendation, ...]:
        # Higher CHW supply temperature slightly lowers lift/flow demand, bounded within safe VFD curves.
        comfort_bias_hz = 1.5 if comfort_margin_c < 0.8 else 0.0
        chw_pump_hz = clamp(40.0 - 1.5 * supply_delta_c + comfort_bias_hz, HARD_PUMP_FREQ_MIN_HZ, HARD_PUMP_FREQ_MAX_HZ)
        cw_pump_hz = clamp(42.0 - 1.0 * supply_delta_c + comfort_bias_hz, HARD_PUMP_FREQ_MIN_HZ, HARD_PUMP_FREQ_MAX_HZ)
        tower_fan_pct = clamp(70.0 - 3.0 * supply_delta_c, HARD_FAN_SPEED_MIN_PCT, HARD_FAN_SPEED_MAX_PCT)

        return (
            AuxiliaryTargetRecommendation(
                loop_key="chilled_water_pump_freq",
                capability="SET_FREQUENCY",
                target_value=round(chw_pump_hz, 2),
                unit="Hz",
                rationale="CHW pump frequency coordinated with supply reset and comfort margin",
            ),
            AuxiliaryTargetRecommendation(
                loop_key="cooling_water_pump_freq",
                capability="SET_FREQUENCY",
                target_value=round(cw_pump_hz, 2),
                unit="Hz",
                rationale="CW pump frequency coordinated for condenser heat rejection",
            ),
            AuxiliaryTargetRecommendation(
                loop_key="cooling_tower_fan_speed",
                capability="SET_FAN_SPEED",
                target_value=round(tower_fan_pct, 1),
                unit="%",
                rationale="Cooling tower fan speed balanced against wet-bulb heat rejection",
            ),
        )
