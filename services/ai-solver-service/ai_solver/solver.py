"""Deterministic HVAC physics optimization solver."""

from __future__ import annotations

from dataclasses import dataclass
import hashlib
import math

from ai_solver.calibration import CalibrationProfile, SiteCalibration
from ai_solver.model import (
    AuxiliaryTargetRecommendation,
    CoupledContext,
    CoupledHorizonSlot,
    CoupledPlantSpec,
    HARD_CHILLED_WATER_MIN_C,
    HARD_FAN_SPEED_MAX_PCT,
    HARD_FAN_SPEED_MIN_PCT,
    HARD_PUMP_FREQ_MAX_HZ,
    HARD_PUMP_FREQ_MIN_HZ,
    SolverInfeasibleError,
    SolverRequest,
    SolverResponse,
)

SOLVER_VERSION = "python-coupled-mpc-v1"
GRID_STEP_C = 0.1
_WATER_CAP_KW_PER_M3H_C = 1.163
_FALLBACK_RH_PCT = 65.0
_INF_COST = 1e18


def estimate_wet_bulb_c(dry_bulb_c: float, relative_humidity_pct: float | None = None) -> float:
    """Estimate wet-bulb temperature (C) via Stull (2011), falling back to 65% RH when absent."""
    if not math.isfinite(dry_bulb_c):
        return 24.0
    rh = relative_humidity_pct
    if rh is None or not math.isfinite(rh) or rh <= 0.0:
        rh = _FALLBACK_RH_PCT
    rh = clamp(rh, 5.0, 100.0)
    tw = (
        dry_bulb_c * math.atan(0.151977 * math.sqrt(rh + 8.313659))
        + math.atan(dry_bulb_c + rh)
        - math.atan(rh - 1.676331)
        + 0.00391838 * (rh ** 1.5) * math.atan(0.023101 * rh)
        - 4.686035
    )
    return clamp(min(tw, dry_bulb_c), -25.0, 45.0)


@dataclass(frozen=True)
class MonotonicChillerModel:
    base_cop: float = 5.7
    ref_tchw_c: float = 7.0
    ref_tcw_c: float = 30.0
    tchw_slope_cop_per_c: float = 0.17
    tcw_slope_cop_per_c: float = 0.145
    condenser_quad_loss_per_c2: float = 0.0025
    condenser_mean_weight: float = 0.45
    plr_const_e0: float = 0.09
    plr_linear_e1: float = 0.57
    plr_quad_e2: float = 0.34
    cop_scale: float = 1.0

    def project_monotonic(self) -> MonotonicChillerModel:
        e0 = max(0.02, self.plr_const_e0)
        e1 = max(0.25, self.plr_linear_e1)
        e2 = max(0.02, self.plr_quad_e2)
        total = e0 + e1 + e2
        return MonotonicChillerModel(
            base_cop=clamp(self.base_cop if self.base_cop > 0 else 5.7, 2.2, 9.5),
            ref_tchw_c=self.ref_tchw_c,
            ref_tcw_c=self.ref_tcw_c,
            tchw_slope_cop_per_c=max(0.02, self.tchw_slope_cop_per_c),
            tcw_slope_cop_per_c=max(0.02, self.tcw_slope_cop_per_c),
            condenser_quad_loss_per_c2=max(0.0, self.condenser_quad_loss_per_c2),
            condenser_mean_weight=clamp(self.condenser_mean_weight, 0.0, 1.0),
            plr_const_e0=e0 / total,
            plr_linear_e1=e1 / total,
            plr_quad_e2=e2 / total,
            cop_scale=clamp(self.cop_scale if self.cop_scale > 0 else 1.0, 0.55, 1.35),
        )

    def lift_cop(self, t_chw_c: float, cw_in_c: float, cw_out_c: float) -> float:
        cw_eff = (1.0 - self.condenser_mean_weight) * cw_in_c + self.condenser_mean_weight * cw_out_c
        cw_over = max(0.0, cw_eff - self.ref_tcw_c)
        raw_cop = (
            self.base_cop
            + self.tchw_slope_cop_per_c * (t_chw_c - self.ref_tchw_c)
            - self.tcw_slope_cop_per_c * (cw_eff - self.ref_tcw_c)
            - self.condenser_quad_loss_per_c2 * cw_over * cw_over
        )
        return clamp(self.cop_scale * max(1.8, raw_cop), 1.5, 11.0)

    def power_kw(
        self, load_kw: float, rated_cap_kw: float, t_chw_c: float, cw_in_c: float, cw_out_c: float
    ) -> tuple[float, float]:
        if load_kw <= 0.0 or rated_cap_kw <= 0.0:
            return 0.0, 0.0
        lift_cop = self.lift_cop(t_chw_c, cw_in_c, cw_out_c)
        plr = load_kw / rated_cap_kw
        eir_plr = self.plr_const_e0 + self.plr_linear_e1 * plr + self.plr_quad_e2 * plr * plr
        p_kw = rated_cap_kw * eir_plr / lift_cop
        if p_kw <= 0.0:
            return 0.0, 0.0
        return p_kw, load_kw / p_kw


def variable_speed_pump_power_kw(rated_kw: float, static_head_frac: float, freq_hz: float, max_hz: float) -> float:
    if rated_kw <= 0.0 or max_hz <= 0.0 or freq_hz <= 0.0:
        return 0.0
    flow_frac = clamp(freq_hz / max_hz, 0.0, 1.0)
    stat = clamp(static_head_frac, 0.0, 0.5)
    return rated_kw * (stat + (1.0 - stat) * flow_frac * flow_frac * flow_frac)


def tower_approach_c(spec: CoupledPlantSpec, heat_rej_kw: float, fan_pct: float) -> float:
    design_rej = spec.tower_design_heat_rej_kw if spec.tower_design_heat_rej_kw > 0.0 else 1420.0
    heat_ratio = clamp(heat_rej_kw / design_rej, 0.05, 1.5)
    speed_frac = clamp(fan_pct / 100.0, 0.05, 1.0)
    low_fan_penalty = (
        spec.tower_low_fan_penalty_c * (0.55 - speed_frac) / 0.55 if speed_frac < 0.55 else 0.0
    )
    return (
        spec.tower_min_approach_c
        + spec.tower_load_approach_coeff * (heat_ratio ** spec.tower_nonlinear_exp) / (speed_frac ** 0.85)
        + low_fan_penalty
    )


def coil_capacity_limit_kw(spec: CoupledPlantSpec, zone_temp_c: float, t_chw_c: float, chwp_hz: float) -> float:
    total_rated = sum(ch.rated_cooling_kw for ch in spec.chillers)
    denom = max(1.0, spec.zone_setpoint_c - spec.design_supply_temp_c)
    coil_kw_per_c = total_rated / denom
    flow_frac = clamp(chwp_hz / spec.chwp_max_hz, 0.05, 1.0)
    delta_t = max(0.0, zone_temp_c - t_chw_c)
    limit = coil_kw_per_c * delta_t * (flow_frac ** spec.coil_flow_exp)
    if zone_temp_c - t_chw_c < spec.coil_approach_min_c:
        ratio = max(0.0, (zone_temp_c - t_chw_c) / max(1.0, spec.coil_approach_min_c))
        limit *= ratio
    return limit


@dataclass(frozen=True)
class _CoupledOpPoint:
    feasible: bool
    target_chillers: int
    supply_temp_c: float
    chwp_freq_hz: float
    cwp_freq_hz: float
    tower_fan_pct: float
    total_power_kw: float
    chiller_power_kw: float
    chwp_power_kw: float
    cwp_power_kw: float
    tower_power_kw: float
    predicted_cop: float
    coil_capacity_kw: float
    delivered_load_kw: float


def evaluate_optimal_operating_point(
    load_kw: float,
    zone_temp_c: float,
    t_chw_c: float,
    dry_bulb_c: float,
    wet_bulb_c: float | None,
    rh_pct: float | None,
    model: MonotonicChillerModel,
    spec: CoupledPlantSpec,
) -> _CoupledOpPoint:
    proj_model = model.project_monotonic()
    wb = (
        wet_bulb_c
        if wet_bulb_c is not None and math.isfinite(wet_bulb_c) and -25.0 < wet_bulb_c <= dry_bulb_c
        else estimate_wet_bulb_c(dry_bulb_c, rh_pct)
    )
    req_load = max(10.0, load_kw)

    min_safety_flow = 0.38
    min_interlock_hz = min_safety_flow * spec.chwp_max_hz
    min_dt_hz = (req_load / (_WATER_CAP_KW_PER_M3H_C * 7.8 * max(1.0, spec.chwp_rated_flow_m3h))) * spec.chwp_max_hz
    chwp_start = clamp(math.ceil(max(spec.chwp_min_hz, max(min_interlock_hz, min_dt_hz)) * 2.0) / 2.0, spec.chwp_min_hz, spec.chwp_max_hz)

    chwp_hz = chwp_start
    coil_cap = coil_capacity_limit_kw(spec, zone_temp_c, t_chw_c, chwp_hz)
    hz = chwp_start
    while hz <= spec.chwp_max_hz + 1e-9:
        clamped_hz = min(hz, spec.chwp_max_hz)
        cap = coil_capacity_limit_kw(spec, zone_temp_c, t_chw_c, clamped_hz)
        chwp_hz = clamped_hz
        coil_cap = cap
        if cap >= req_load * 1.03:
            break
        hz += 1.0
    if coil_cap < req_load * 0.97:
        chwp_hz = spec.chwp_max_hz
        coil_cap = coil_capacity_limit_kw(spec, zone_temp_c, t_chw_c, chwp_hz)

    delivered_kw = min(req_load, max(0.0, coil_cap))
    chwp_kw = variable_speed_pump_power_kw(spec.chwp_rated_kw, spec.chwp_static_head_frac, chwp_hz, spec.chwp_max_hz)

    best: _CoupledOpPoint | None = None
    cwp_floor = max(spec.cwp_min_hz, min_safety_flow * spec.cwp_max_hz)
    num_chillers = len(spec.chillers)
    cwp_coarse = []
    c = cwp_floor
    while c <= spec.cwp_max_hz + 1e-9:
        cwp_coarse.append(min(c, spec.cwp_max_hz))
        c += 5.0
    fan_coarse = []
    f = spec.tower_min_fan_pct
    while f <= spec.tower_max_fan_pct + 1e-9:
        fan_coarse.append(min(f, spec.tower_max_fan_pct))
        f += 12.0

    inv_cwp_max = 1.0 / spec.cwp_max_hz
    cwp_rated_flow = spec.cwp_rated_flow_m3h
    cwp_rated_kw = spec.cwp_rated_kw
    cwp_stat = max(0.0, min(0.5, spec.cwp_static_head_frac))
    cwp_one_minus_stat = 1.0 - cwp_stat
    twr_rated_kw = spec.tower_rated_kw
    design_rej = spec.tower_design_heat_rej_kw if spec.tower_design_heat_rej_kw > 0.0 else 1420.0
    inv_design_rej = 1.0 / design_rej
    twr_min_app = spec.tower_min_approach_c
    twr_coeff = spec.tower_load_approach_coeff
    twr_exp = spec.tower_nonlinear_exp
    twr_low_pen = spec.tower_low_fan_penalty_c
    e0 = proj_model.plr_const_e0
    e1 = proj_model.plr_linear_e1
    e2 = proj_model.plr_quad_e2
    cop_scale = proj_model.cop_scale
    ref_tcw = proj_model.ref_tcw_c
    w_cw = proj_model.condenser_mean_weight
    one_minus_w_cw = 1.0 - w_cw
    tchw_lift_delta = proj_model.tchw_slope_cop_per_c * (t_chw_c - proj_model.ref_tchw_c)
    tcw_slope = proj_model.tcw_slope_cop_per_c
    quad_loss = proj_model.condenser_quad_loss_per_c2

    for n_ch in range(1, num_chillers + 1):
        avail_cap = sum(spec.chillers[i].rated_cooling_kw for i in range(n_ch))
        if n_ch < num_chillers and delivered_kw > avail_cap * 0.99:
            continue
        if n_ch > 1 and delivered_kw < 0.68 * (avail_cap - spec.chillers[n_ch - 1].rated_cooling_kw):
            continue

        unit_nums: list[tuple[float, float]] = []
        for i in range(n_ch):
            rcap = spec.chillers[i].rated_cooling_kw
            share = rcap / avail_cap
            plr = (delivered_kw * share) / rcap
            eir = e0 + e1 * plr + e2 * plr * plr
            base_c = spec.chillers[i].base_cop
            base_c = 2.2 if base_c < 2.2 else (9.5 if base_c > 9.5 else base_c)
            unit_nums.append((rcap * eir, base_c + tchw_lift_delta))

        def eval_aux(cwp_hz: float, fan_pct: float, iters: int) -> tuple[float, float, float, float, float]:
            flow_frac = cwp_hz * inv_cwp_max
            flow_frac = 0.2 if flow_frac < 0.2 else (1.0 if flow_frac > 1.0 else flow_frac)
            cwp_flow = cwp_rated_flow * flow_frac
            inv_water_flow = 1.0 / (_WATER_CAP_KW_PER_M3H_C * cwp_flow)

            sf = fan_pct * 0.01
            sf = 0.05 if sf < 0.05 else (1.0 if sf > 1.0 else sf)
            sf_pow = twr_coeff / (sf ** 0.85)
            low_pen = twr_low_pen * (0.55 - sf) / 0.55 if sf < 0.55 else 0.0

            ch_kw = delivered_kw / proj_model.base_cop
            for _ in range(iters):
                heat_rej = delivered_kw + ch_kw
                hr = heat_rej * inv_design_rej
                hr = 0.05 if hr < 0.05 else (1.5 if hr > 1.5 else hr)
                cw_in = wb + twr_min_app + sf_pow * (hr ** twr_exp) + low_pen
                cw_out = cw_in + heat_rej * inv_water_flow
                cw_eff = one_minus_w_cw * cw_in + w_cw * cw_out
                cw_d = cw_eff - ref_tcw
                cw_over = cw_d if cw_d > 0.0 else 0.0
                cw_pen = tcw_slope * cw_d + quad_loss * cw_over * cw_over
                sum_p = 0.0
                for num, b_plus in unit_nums:
                    raw_c = b_plus - cw_pen
                    if raw_c < 1.8:
                        raw_c = 1.8
                    l_cop = cop_scale * raw_c
                    if l_cop < 1.5:
                        l_cop = 1.5
                    elif l_cop > 11.0:
                        l_cop = 11.0
                    sum_p += num / l_cop
                ch_kw = sum_p

            ff = cwp_hz * inv_cwp_max
            ff = 0.0 if ff < 0.0 else (1.0 if ff > 1.0 else ff)
            cwp_p = cwp_rated_kw * (cwp_stat + cwp_one_minus_stat * ff * ff * ff)
            twr_p = twr_rated_kw * sf * sf * sf
            tot = ch_kw + chwp_kw + cwp_p + twr_p
            cop_v = delivered_kw / ch_kw if ch_kw > 0.0 else 0.0
            return tot, ch_kw, cwp_p, twr_p, cop_v

        best_cwp, best_fan = cwp_floor, clamp(spec.tower_min_fan_pct, 25.0, 100.0)
        best_pt = _INF_COST
        for cwp_hz in cwp_coarse:
            for fan_pct in fan_coarse:
                tot, _, _, _, _ = eval_aux(cwp_hz, fan_pct, 1)
                if tot < best_pt:
                    best_pt = tot
                    best_cwp = cwp_hz
                    best_fan = fan_pct

        c_lo = max(cwp_floor, best_cwp - 4.0)
        c_hi = min(spec.cwp_max_hz, best_cwp + 4.0)
        f_lo = max(spec.tower_min_fan_pct, best_fan - 10.0)
        f_hi = min(spec.tower_max_fan_pct, best_fan + 10.0)
        c_cur = c_lo
        while c_cur <= c_hi + 1e-9:
            c_clamped = min(c_cur, spec.cwp_max_hz)
            f_cur = f_lo
            while f_cur <= f_hi + 1e-9:
                f_clamped = min(f_cur, spec.tower_max_fan_pct)
                tot, ch_kw, cwp_p, twr_p, cop_v = eval_aux(c_clamped, f_clamped, 2)
                if best is None or tot < best.total_power_kw:
                    best = _CoupledOpPoint(
                        feasible=coil_cap >= req_load * 0.97 and delivered_kw <= avail_cap * 1.02,
                        target_chillers=n_ch,
                        supply_temp_c=t_chw_c,
                        chwp_freq_hz=chwp_hz,
                        cwp_freq_hz=round(c_clamped, 4),
                        tower_fan_pct=round(f_clamped, 4),
                        total_power_kw=tot,
                        chiller_power_kw=ch_kw,
                        chwp_power_kw=chwp_kw,
                        cwp_power_kw=cwp_p,
                        tower_power_kw=twr_p,
                        predicted_cop=cop_v,
                        coil_capacity_kw=coil_cap,
                        delivered_load_kw=delivered_kw,
                    )
                f_cur += 4.0
            c_cur += 1.5

    if best is None:
        return _CoupledOpPoint(
            feasible=False,
            target_chillers=num_chillers,
            supply_temp_c=t_chw_c,
            chwp_freq_hz=chwp_hz,
            cwp_freq_hz=spec.cwp_max_hz,
            tower_fan_pct=spec.tower_max_fan_pct,
            total_power_kw=0.0,
            chiller_power_kw=0.0,
            chwp_power_kw=chwp_kw,
            cwp_power_kw=0.0,
            tower_power_kw=0.0,
            predicted_cop=0.0,
            coil_capacity_kw=coil_cap,
            delivered_load_kw=delivered_kw,
        )
    return best


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

    def __init__(self, calibration: CalibrationProfile | None = None) -> None:
        self.calibration = calibration

    def solve(self, req: SolverRequest) -> SolverResponse:
        site_cal = self.calibration.calibration_for_site(req.site_id) if self.calibration else SiteCalibration()
        if req.coupled_context is not None:
            return self._solve_coupled_mpc(req, req.coupled_context, site_cal)

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
        step_limit = cons.max_supply_temp_step_c * site_cal.supply_step_scale
        # Generate deterministic fine grid within [-step_limit, +step_limit] plus exact 0 and bounds.
        count = max(1, int(math.floor(step_limit / GRID_STEP_C + 1e-9)))
        deltas = {0.0, -step_limit, step_limit}
        for idx in range(-count, count + 1):
            deltas.add(round(idx * GRID_STEP_C, 4))

        best_penalized: _CandidateEval | None = None
        best_unpenalized: _CandidateEval | None = None
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
            if best_unpenalized is None or self._is_better(cand, best_unpenalized):
                best_unpenalized = cand
            if margin >= site_cal.comfort_margin_penalty_c - 1e-9:
                if best_penalized is None or self._is_better(cand, best_penalized):
                    best_penalized = cand

        best = best_penalized if best_penalized is not None else best_unpenalized
        if best is None:
            raise SolverInfeasibleError(
                "no HVAC setpoint candidate satisfies freeze floor, step limit, and comfort P90 bounds"
            )

        risk_level = "LOW"
        if best.comfort_margin_c <= max(0.5, 2.0 * rm.zone_temp_uncertainty_p90_c):
            risk_level = "MEDIUM"

        aux_targets = self._compute_auxiliary_targets(best.delta_c, best.comfort_margin_c, site_cal)
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
        site_cal: SiteCalibration | None = None,
    ) -> tuple[AuxiliaryTargetRecommendation, ...]:
        cal = site_cal or SiteCalibration()
        # Higher CHW supply temperature slightly lowers lift/flow demand, bounded within safe VFD curves.
        comfort_bias_hz = 1.5 if comfort_margin_c < 0.8 else 0.0
        chw_pump_hz = clamp(
            40.0 - 1.5 * supply_delta_c + comfort_bias_hz + cal.chw_pump_bias_hz,
            HARD_PUMP_FREQ_MIN_HZ,
            HARD_PUMP_FREQ_MAX_HZ,
        )
        cw_pump_hz = clamp(
            42.0 - 1.0 * supply_delta_c + comfort_bias_hz + cal.cw_pump_bias_hz,
            HARD_PUMP_FREQ_MIN_HZ,
            HARD_PUMP_FREQ_MAX_HZ,
        )
        tower_fan_pct = clamp(
            70.0 - 3.0 * supply_delta_c + cal.tower_fan_bias_pct,
            HARD_FAN_SPEED_MIN_PCT,
            HARD_FAN_SPEED_MAX_PCT,
        )

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

    def _solve_coupled_mpc(
        self, req: SolverRequest, ctx: CoupledContext, site_cal: SiteCalibration
    ) -> SolverResponse:
        base = req.baseline
        cons = req.constraints
        rm = req.response_model
        plant = ctx.plant
        model = MonotonicChillerModel(cop_scale=ctx.cop_scale).project_monotonic()

        min_supply = max(HARD_CHILLED_WATER_MIN_C, cons.supply_temp_min_c)
        max_supply = cons.supply_temp_max_c
        if max_supply < min_supply:
            raise SolverInfeasibleError(
                f"effective supply bounds [{min_supply:.2f}, {max_supply:.2f}] are empty"
            )
        max_step = max(0.25, cons.max_supply_temp_step_c * site_cal.supply_step_scale)
        p90_z = max(0.0, rm.zone_temp_uncertainty_p90_c)
        zone_floor = cons.zone_temp_min_c + p90_z
        zone_ceiling = cons.zone_temp_max_c - p90_z
        if zone_floor > zone_ceiling:
            raise SolverInfeasibleError("zone comfort bounds collapse under uncertainty margin")

        cur_supply = clamp(base.supply_temp_c, min_supply, max_supply)
        cur_zone = base.zone_temp_c

        horizon = list(ctx.horizon)
        if not horizon:
            total_cap = sum(ch.rated_cooling_kw for ch in plant.chillers)
            load_kw = ctx.current_load_kw or clamp(base.daily_energy_kwh * 4.5 / 24.0, 140.0, total_cap * 0.90)
            rate = ctx.tariff_rate_cny if ctx.tariff_rate_cny is not None else (base.daily_cost / base.daily_energy_kwh)
            wb = ctx.ambient_wet_bulb_c
            if wb is None:
                wb = estimate_wet_bulb_c(ctx.ambient_dry_bulb_c, ctx.rel_humidity_pct)
            horizon = [
                CoupledHorizonSlot(
                    duration_hours=1.0,
                    cooling_load_kw=load_kw,
                    ambient_dry_bulb_c=ctx.ambient_dry_bulb_c,
                    ambient_wet_bulb_c=wb,
                    rel_humidity_pct=ctx.rel_humidity_pct,
                    tariff_rate_cny=rate,
                )
                for _ in range(24)
            ]

        candidates: list[float] = []
        v = min_supply
        while v <= max_supply + 1e-9:
            val = round(min(v, max_supply), 4)
            if not candidates or abs(val - candidates[-1]) > 1e-4:
                candidates.append(val)
            v += 0.5
        if not any(abs(c - cur_supply) < 1e-4 for c in candidates):
            candidates.append(round(cur_supply, 4))
            candidates.sort()
        cur_s_idx = min(range(len(candidates)), key=lambda i: abs(candidates[i] - cur_supply))

        if not ctx.precooling_enabled:
            chosen_slots = self._solve_coupled_no_storage_dp(
                horizon, candidates, cur_s_idx, cur_zone, zone_floor, zone_ceiling, max_step, rm.zone_temp_delta_per_supply_c, model, plant
            )
        else:
            chosen_slots = self._solve_coupled_storage_dp(
                horizon, candidates, cur_s_idx, cur_zone, cons.zone_temp_min_c, cons.zone_temp_max_c, zone_floor, zone_ceiling, max_step, model, plant
            )

        slot0 = chosen_slots[0]
        total_hours = sum(sl["durationHours"] for sl in chosen_slots)
        total_e = sum(sl["predictedPowerKw"] * sl["durationHours"] for sl in chosen_slots)
        total_c = sum(sl["slotCostCny"] for sl in chosen_slots)
        scale_day = (24.0 / total_hours) if total_hours > 0.0 and abs(total_hours - 24.0) > 1e-3 else 1.0
        daily_e = max(0.0, total_e * scale_day)
        daily_c = max(0.0, total_c * scale_day)

        proj_z0 = slot0["projectedZoneTempC"]
        margin = min(zone_ceiling - proj_z0, proj_z0 - zone_floor)
        risk_level = "MEDIUM" if margin <= max(0.5, 2.0 * p90_z) else "LOW"

        aux_targets = (
            AuxiliaryTargetRecommendation(
                loop_key="chilled_water_pump_freq",
                capability="SET_FREQUENCY",
                target_value=round(slot0["chwpFreqHz"], 2),
                unit="Hz",
                rationale=f"coupled grey-box MPC: CHWP flow/coil optimum for {slot0['targetChillers']} chillers",
            ),
            AuxiliaryTargetRecommendation(
                loop_key="cooling_water_pump_freq",
                capability="SET_FREQUENCY",
                target_value=round(slot0["cwpFreqHz"], 2),
                unit="Hz",
                rationale=f"coupled grey-box MPC: condenser delta-T vs pump affinity (COP {slot0['predictedCop']:.2f})",
            ),
            AuxiliaryTargetRecommendation(
                loop_key="cooling_tower_fan_speed",
                capability="SET_FAN_SPEED",
                target_value=round(slot0["towerFanPct"], 1),
                unit="%",
                rationale="coupled grey-box MPC: nonlinear wet-bulb approach vs cubic fan power optimum",
            ),
        )
        rec_id = deterministic_uuidv7(f"{req.optimization_run_id}:recommendation:1")

        return SolverResponse(
            id=rec_id,
            supply_temp_c=slot0["supplyTempC"],
            projected_zone_temp_c=proj_z0,
            daily_energy_kwh=daily_e,
            daily_cost=daily_c,
            expected_saving_kwh_per_day=base.daily_energy_kwh - daily_e,
            expected_cost_saving_per_day=base.daily_cost - daily_c,
            energy_uncertainty_p90_kwh=rm.energy_uncertainty_p90_kwh,
            zone_temp_uncertainty_p90_c=rm.zone_temp_uncertainty_p90_c,
            risk_level=risk_level,
            comfort_margin_c=max(0.0, margin),
            restore_supply_temp_c=base.supply_temp_c,
            solver_version=SOLVER_VERSION,
            auxiliary_targets=aux_targets,
            coupled_plan={
                "targetRunningChillers": slot0["targetChillers"],
                "predictedCop": slot0["predictedCop"],
                "mpcSlots": chosen_slots,
            },
        )

    @staticmethod
    def _solve_coupled_no_storage_dp(
        horizon: list[CoupledHorizonSlot],
        candidates: list[float],
        cur_s_idx: int,
        cur_zone: float,
        zone_floor: float,
        zone_ceiling: float,
        max_step: float,
        zone_delta_per_c: float,
        model: MonotonicChillerModel,
        plant: CoupledPlantSpec,
    ) -> list[dict[str, Any]]:
        h_len = len(horizon)
        s_len = len(candidates)
        z_slope = zone_delta_per_c if zone_delta_per_c > 0.0 else 0.32
        table: list[list[_CoupledOpPoint]] = []
        proj_z: list[list[float]] = []
        feas: list[list[bool]] = []

        for t in range(h_len):
            sl = horizon[t]
            row_pts: list[_CoupledOpPoint] = []
            row_z: list[float] = []
            row_f: list[bool] = []
            for sp in candidates:
                z_est = cur_zone + z_slope * (sp - candidates[cur_s_idx])
                pt = evaluate_optimal_operating_point(
                    sl.cooling_load_kw, z_est, sp, sl.ambient_dry_bulb_c, sl.ambient_wet_bulb_c, sl.rel_humidity_pct, model, plant
                )
                if pt.delivered_load_kw < sl.cooling_load_kw * 0.97:
                    z_est += (sl.cooling_load_kw - pt.delivered_load_kw) * sl.duration_hours / plant.zone_thermal_mass_kwh_per_c
                row_pts.append(pt)
                row_z.append(z_est)
                row_f.append(pt.feasible and (zone_floor <= z_est <= zone_ceiling))
            if t == 0 and not any(
                row_f[si] and abs(candidates[si] - candidates[cur_s_idx]) <= max_step + 1e-9 for si in range(s_len)
            ):
                fb_sp = max(HARD_CHILLED_WATER_MIN_C, candidates[cur_s_idx] - max_step)
                fb_idx = min(range(s_len), key=lambda i: abs(candidates[i] - fb_sp))
                row_f[fb_idx] = True
            table.append(row_pts)
            proj_z.append(row_z)
            feas.append(row_f)

        switch_w = 1.4
        dp = [[_INF_COST] * s_len for _ in range(h_len)]
        parent = [[-1] * s_len for _ in range(h_len)]
        for si in range(s_len):
            d_s = candidates[si] - candidates[cur_s_idx]
            if abs(d_s) <= max_step + 1e-9 and feas[0][si]:
                dp[0][si] = table[0][si].total_power_kw * horizon[0].duration_hours * horizon[0].tariff_rate_cny + switch_w * d_s * d_s
                parent[0][si] = cur_s_idx

        for t in range(1, h_len):
            sl = horizon[t]
            for prev in range(s_len):
                if dp[t - 1][prev] >= _INF_COST * 0.5:
                    continue
                for si in range(s_len):
                    if not feas[t][si]:
                        continue
                    d_s = candidates[si] - candidates[prev]
                    if abs(d_s) > max_step + 1e-9:
                        continue
                    cost = dp[t - 1][prev] + table[t][si].total_power_kw * sl.duration_hours * sl.tariff_rate_cny + switch_w * d_s * d_s
                    if cost < dp[t][si]:
                        dp[t][si] = cost
                        parent[t][si] = prev

        best_end = min(range(s_len), key=lambda si: dp[h_len - 1][si])
        if dp[h_len - 1][best_end] >= _INF_COST * 0.5:
            fb_sp = max(HARD_CHILLED_WATER_MIN_C, candidates[cur_s_idx] - max_step)
            best_end = min(range(s_len), key=lambda i: abs(candidates[i] - fb_sp))

        chosen = [cur_s_idx] * h_len
        cur = best_end
        for t in range(h_len - 1, -1, -1):
            chosen[t] = cur if 0 <= cur < s_len else cur_s_idx
            cur = parent[t][chosen[t]] if 0 <= cur < s_len else cur_s_idx

        slots: list[dict[str, Any]] = []
        for t in range(h_len):
            si = chosen[t]
            pt = table[t][si]
            sl = horizon[t]
            slot_e = pt.total_power_kw * sl.duration_hours
            slot_c = slot_e * sl.tariff_rate_cny
            mode = "ECO"
            if sl.tariff_rate_cny >= 1.0:
                mode = "PEAK_SHAVE"
            elif candidates[si] <= plant.design_supply_temp_c - 0.5:
                mode = "NORMAL"
            slots.append({
                "durationHours": sl.duration_hours,
                "supplyTempC": candidates[si],
                "chwpFreqHz": pt.chwp_freq_hz,
                "cwpFreqHz": pt.cwp_freq_hz,
                "towerFanPct": pt.tower_fan_pct,
                "targetChillers": pt.target_chillers,
                "projectedZoneTempC": round(proj_z[t][si], 4),
                "predictedPowerKw": round(pt.total_power_kw, 4),
                "predictedCop": round(pt.predicted_cop, 4),
                "slotCostCny": round(slot_c, 4),
                "operatingMode": mode,
            })
        return slots

    def _solve_coupled_storage_dp(
        self,
        horizon: list[CoupledHorizonSlot],
        candidates: list[float],
        cur_s_idx: int,
        cur_zone: float,
        zone_min_c: float,
        zone_max_c: float,
        zone_floor: float,
        zone_ceiling: float,
        max_step: float,
        model: MonotonicChillerModel,
        plant: CoupledPlantSpec,
    ) -> list[dict[str, Any]]:
        h_len = len(horizon)
        s_len = len(candidates)

        eff_z_min = clamp(zone_floor + 0.2, zone_min_c + 0.1, plant.zone_setpoint_c)
        eff_z_max = clamp(zone_ceiling - 0.15, eff_z_min + 0.4, zone_max_c - 0.05)
        if eff_z_max <= eff_z_min:
            eff_z_min = clamp(plant.zone_setpoint_c - 1.5, 21.0, 24.0)
            eff_z_max = clamp(plant.zone_setpoint_c + 0.4, 22.0, 24.6)

        n_zones = max(5, int(round((eff_z_max - eff_z_min) / 0.15)) + 1)
        z_spacing = (eff_z_max - eff_z_min) / float(n_zones - 1)
        zone_grid = [eff_z_min + zi * z_spacing for zi in range(n_zones)]

        total_cap = sum(ch.rated_cooling_kw for ch in plant.chillers)
        min_floor_c = max(zone_min_c + 0.8, 22.0)

        base_table: list[list[_CoupledOpPoint]] = []
        charge_table: list[list[_CoupledOpPoint]] = []
        discharge_table: list[list[_CoupledOpPoint]] = []
        for t in range(h_len):
            sl = horizon[t]
            b_row: list[_CoupledOpPoint] = []
            c_row: list[_CoupledOpPoint] = []
            d_row: list[_CoupledOpPoint] = []
            for sp in candidates:
                b_pt = evaluate_optimal_operating_point(
                    sl.cooling_load_kw, plant.zone_setpoint_c, sp, sl.ambient_dry_bulb_c, sl.ambient_wet_bulb_c, sl.rel_humidity_pct, model, plant
                )
                b_row.append(b_pt)
                if sp < plant.design_supply_temp_c - 0.15:
                    ch_load = min(total_cap * 0.95, sl.cooling_load_kw + min(total_cap * 0.35, 175.0))
                    c_row.append(
                        evaluate_optimal_operating_point(
                            ch_load, plant.zone_setpoint_c - 0.7, sp, sl.ambient_dry_bulb_c, sl.ambient_wet_bulb_c, sl.rel_humidity_pct, model, plant
                        )
                    )
                else:
                    c_row.append(b_pt)
                if sp >= plant.design_supply_temp_c - 0.05:
                    dc_load = max(120.0, sl.cooling_load_kw * 0.72)
                    d_row.append(
                        evaluate_optimal_operating_point(
                            dc_load, plant.zone_setpoint_c - 0.8, sp, sl.ambient_dry_bulb_c, sl.ambient_wet_bulb_c, sl.rel_humidity_pct, model, plant
                        )
                    )
                else:
                    d_row.append(b_pt)
            base_table.append(b_row)
            charge_table.append(c_row)
            discharge_table.append(d_row)

        min_tariff = min(sl.tariff_rate_cny for sl in horizon)
        coil_cache = [
            [coil_capacity_limit_kw(plant, z_val, sp, plant.chwp_max_hz) for sp in candidates]
            for z_val in zone_grid
        ]

        def sim_step(
            t: int, z_from: float, sp: float, base_load: float, dt_h: float, si: int, coil_cap_override: float | None = None
        ) -> tuple[float, float, _CoupledOpPoint, bool]:
            subcool = max(0.0, plant.design_supply_temp_c - sp)
            eff_target = clamp(plant.zone_setpoint_c - 0.9 * subcool, min_floor_c, plant.zone_setpoint_c)
            corr_kw = 0.0
            if z_from > plant.zone_setpoint_c + 0.3:
                corr_kw = (z_from - plant.zone_setpoint_c - 0.3) * plant.zone_thermal_mass_kwh_per_c / 0.25
            elif subcool > 0.15 and z_from > eff_target + 0.1:
                max_pull = max(total_cap * 0.35, 250.0)
                corr_kw = min((z_from - eff_target) * plant.zone_thermal_mass_kwh_per_c / 1.8, max_pull)
            elif sp >= plant.design_supply_temp_c - 0.05 and z_from < plant.zone_setpoint_c - 0.15:
                frac = clamp((plant.zone_setpoint_c - 0.15 - z_from) / 2.0, 0.0, 0.45)
                corr_kw = -base_load * frac
            desired_kw = max(0.0, base_load + corr_kw)
            coil_cap = coil_cap_override if coil_cap_override is not None else coil_capacity_limit_kw(plant, z_from, sp, plant.chwp_max_hz)

            if desired_kw > base_load * 1.08 and subcool > 0.15:
                op = charge_table[t][si]
            elif desired_kw < base_load * 0.88:
                op = discharge_table[t][si]
            else:
                op = base_table[t][si]

            if desired_kw > coil_cap * 1.01 and sp > plant.design_supply_temp_c + 0.5:
                return z_from, 0.0, op, False

            delivered = min(desired_kw, min(coil_cap, total_cap))
            ch_p = op.chiller_power_kw
            cwp_p = op.cwp_power_kw
            twr_p = op.tower_power_kw
            if op.delivered_load_kw > 1.0:
                ratio = clamp(delivered / op.delivered_load_kw, 0.35, 1.45)
                ch_p *= 0.12 + 0.88 * (ratio ** 1.15)
                aux_s = clamp(0.65 + 0.35 * ratio, 0.75, 1.30)
                cwp_p *= aux_s
                twr_p *= aux_s
            tot_p = ch_p + op.chwp_power_kw + cwp_p + twr_p
            standing_loss = max(0.0, plant.zone_setpoint_c - z_from) * 14.0
            next_z = z_from + (base_load + standing_loss - delivered) * dt_h / plant.zone_thermal_mass_kwh_per_c
            if next_z > eff_z_max + 0.05 or next_z < eff_z_min - 0.05:
                return next_z, tot_p, op, False
            return clamp(next_z, eff_z_min, eff_z_max), tot_p, op, True

        switch_w = 1.8
        shadow_cny_per_c = (plant.zone_thermal_mass_kwh_per_c / max(1.0, model.base_cop)) * min_tariff * 0.25
        val_func: list[list[list[float]]] = [[[0.0] * n_zones for _ in range(s_len)] for _ in range(h_len + 1)]
        for si in range(s_len):
            for zi in range(n_zones):
                val_func[h_len][si][zi] = -shadow_cny_per_c * max(0.0, plant.zone_setpoint_c - zone_grid[zi])

        inv_z_spacing = 1.0 / z_spacing

        def interp_v(slc: list[float], z: float) -> float:
            zc = clamp(z, eff_z_min, eff_z_max)
            pos = (zc - eff_z_min) * inv_z_spacing
            lo = int(math.floor(pos))
            if lo < 0:
                lo = 0
            if lo >= n_zones - 1:
                return slc[n_zones - 1]
            w = pos - lo
            v_lo = slc[lo]
            v_hi = slc[lo + 1]
            if v_lo >= _INF_COST * 0.5 and v_hi >= _INF_COST * 0.5:
                return _INF_COST
            if v_lo >= _INF_COST * 0.5:
                return v_hi + 1e4 * (1.0 - w)
            if v_hi >= _INF_COST * 0.5:
                return v_lo + 1e4 * w
            return (1.0 - w) * v_lo + w * v_hi

        reachable_pairs = [
            [(si, switch_w * ((candidates[si] - candidates[prev]) ** 2)) for si in range(s_len) if abs(candidates[si] - candidates[prev]) <= max_step + 1e-9]
            for prev in range(s_len)
        ]

        base_q = [_INF_COST] * s_len
        for t in range(h_len - 1, -1, -1):
            sl = horizon[t]
            dt_h = sl.duration_hours
            tariff = sl.tariff_rate_cny
            base_load = sl.cooling_load_kw
            v_next = val_func[t + 1]
            v_curr = val_func[t]
            for zi in range(n_zones):
                z_from = zone_grid[zi]
                c_row_cap = coil_cache[zi]
                for si in range(s_len):
                    next_z, tot_p, _, ok = sim_step(t, z_from, candidates[si], base_load, dt_h, si, c_row_cap[si])
                    if not ok:
                        base_q[si] = _INF_COST
                        continue
                    fut = interp_v(v_next[si], next_z)
                    base_q[si] = _INF_COST if fut >= _INF_COST * 0.5 else (tot_p * dt_h * tariff + fut)
                for prev_s in range(s_len):
                    best_q = _INF_COST
                    for si, sw_pen in reachable_pairs[prev_s]:
                        bq = base_q[si]
                        if bq < _INF_COST * 0.5:
                            q = bq + sw_pen
                            if q < best_q:
                                best_q = q
                    v_curr[prev_s][zi] = best_q

        sim_z = clamp(cur_zone, eff_z_min, eff_z_max)
        cur_s = cur_s_idx
        if interp_v(val_func[0][cur_s], sim_z) >= _INF_COST * 0.5:
            return self._solve_coupled_no_storage_dp(
                horizon, candidates, cur_s_idx, cur_zone, zone_floor, zone_ceiling, max_step, 0.32, model, plant
            )

        slots: list[dict[str, Any]] = []
        for t in range(h_len):
            sl = horizon[t]
            dt_h = sl.duration_hours
            tariff = sl.tariff_rate_cny
            base_load = sl.cooling_load_kw

            best_s = -1
            best_q = _INF_COST
            best_next_z = sim_z
            best_tot_p = 0.0
            best_op = base_table[t][cur_s]
            for si, sw_pen in reachable_pairs[cur_s]:
                next_z, tot_p, op, ok = sim_step(t, sim_z, candidates[si], base_load, dt_h, si)
                if not ok:
                    continue
                fut = interp_v(val_func[t + 1][si], next_z)
                q = tot_p * dt_h * tariff + sw_pen + fut
                if q < best_q:
                    best_q = q
                    best_s = si
                    best_next_z = next_z
                    best_tot_p = tot_p
                    best_op = op
            if best_s < 0:
                best_s = cur_s
                best_op = base_table[t][cur_s]
                best_tot_p = best_op.total_power_kw
                best_next_z = sim_z

            sp = candidates[best_s]
            sim_z = best_next_z
            cur_s = best_s
            slot_e = best_tot_p * dt_h
            slot_c = slot_e * tariff
            mode = "NORMAL"
            if sp <= plant.design_supply_temp_c - 0.4 and tariff <= min_tariff + 0.1:
                mode = "PRE_COOL"
            elif tariff >= 1.0 and sp >= plant.design_supply_temp_c:
                mode = "PEAK_SHAVE"
            elif sp > plant.design_supply_temp_c + 0.3:
                mode = "ECO"
            slots.append({
                "durationHours": dt_h,
                "supplyTempC": sp,
                "chwpFreqHz": best_op.chwp_freq_hz,
                "cwpFreqHz": best_op.cwp_freq_hz,
                "towerFanPct": best_op.tower_fan_pct,
                "targetChillers": best_op.target_chillers,
                "projectedZoneTempC": round(sim_z, 4),
                "predictedPowerKw": round(best_tot_p, 4),
                "predictedCop": round(best_op.predicted_cop, 4),
                "slotCostCny": round(slot_c, 4),
                "operatingMode": mode,
            })
        return slots
