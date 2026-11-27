"""Unit and HTTP integration tests for ai-solver-service."""

from __future__ import annotations

import json
import threading
import unittest
import urllib.error
import urllib.request

from ai_solver.model import SolverInfeasibleError, SolverInputError, parse_solver_request
from ai_solver.server import create_server
from ai_solver.solver import (
    HVACPhysicsSolver,
    MonotonicChillerModel,
    SOLVER_VERSION,
    estimate_wet_bulb_c,
)


def valid_payload() -> dict:
    return {
        "tenantId": "01990000-3000-7000-8000-000000000001",
        "siteId": "01990000-5000-7000-8000-000000000001",
        "subjectType": "SITE",
        "subjectId": "01990000-5000-7000-8000-000000000001",
        "optimizationRunId": "01990000-1000-7000-8000-000000000001",
        "inputSnapshotId": "01990000-1100-7000-8000-000000000001",
        "deploymentRevisionId": "01990000-1960-7000-8000-000000000001",
        "objective": "MINIMIZE_COST",
        "horizon": "NEXT_24H",
        "horizonMinutes": 1440,
        "granularity": "HOUR_1",
        "validFrom": "2026-11-19T08:00:00Z",
        "baseline": {
            "dailyEnergyKWh": 4200.0,
            "dailyCost": 3360.0,
            "supplyTempC": 7.0,
            "zoneTempC": 23.0,
        },
        "constraints": {
            "supplyTempMinC": 6.0,
            "supplyTempMaxC": 12.0,
            "zoneTempMinC": 21.0,
            "zoneTempMaxC": 26.0,
            "maxSupplyTempStepC": 1.5,
        },
        "responseModel": {
            "dailyEnergyDeltaKWhPerSupplyTempC": -180.0,
            "zoneTempDeltaCPerSupplyTempC": 0.8,
            "energyUncertaintyP90KWh": 90.0,
            "zoneTempUncertaintyP90C": 0.4,
        },
    }


class SolverUnitTests(unittest.TestCase):
    def test_fine_grid_optimizes_step_and_auxiliary_targets(self) -> None:
        req = parse_solver_request(valid_payload())
        resp = HVACPhysicsSolver().solve(req)
        self.assertEqual(resp.solver_version, SOLVER_VERSION)
        self.assertAlmostEqual(resp.supply_temp_c, 8.5, places=4)
        self.assertAlmostEqual(resp.expected_saving_kwh_per_day, 270.0, places=4)
        self.assertEqual(resp.risk_level, "LOW")
        self.assertEqual(len(resp.auxiliary_targets), 3)
        for target in resp.auxiliary_targets:
            if target.capability == "SET_FREQUENCY":
                self.assertGreaterEqual(target.target_value, 28.0)
                self.assertLessEqual(target.target_value, 50.0)
            elif target.capability == "SET_FAN_SPEED":
                self.assertGreaterEqual(target.target_value, 25.0)
                self.assertLessEqual(target.target_value, 100.0)

    def test_enforces_5c_freeze_floor_even_if_lower_bound_passed(self) -> None:
        payload = valid_payload()
        payload["baseline"]["supplyTempC"] = 5.2
        payload["constraints"]["supplyTempMinC"] = 3.5
        payload["constraints"]["maxSupplyTempStepC"] = 1.0
        payload["responseModel"]["dailyEnergyDeltaKWhPerSupplyTempC"] = 120.0
        payload["responseModel"]["zoneTempDeltaCPerSupplyTempC"] = -0.3
        req = parse_solver_request(payload)
        resp = HVACPhysicsSolver().solve(req)
        self.assertGreaterEqual(resp.supply_temp_c, 5.0)

    def test_fine_grid_finds_feasible_intermediate_step_for_comfort_bound(self) -> None:
        payload = valid_payload()
        # +1.5 C would project zone temp 24.2 + 0.5 P90 = 24.7 > 24.5 max;
        # python grid at 0.1 C picks +1.2 C (zone 23.96 + 0.5 = 24.46 <= 24.5).
        payload["constraints"]["zoneTempMaxC"] = 24.5
        payload["responseModel"]["zoneTempUncertaintyP90C"] = 0.5
        req = parse_solver_request(payload)
        resp = HVACPhysicsSolver().solve(req)
        self.assertGreater(resp.supply_temp_c, 7.0)
        self.assertLess(resp.supply_temp_c, 8.5)
        self.assertEqual(resp.risk_level, "MEDIUM")

    def test_infeasible_comfort_raises_infeasible_error(self) -> None:
        payload = valid_payload()
        payload["baseline"]["zoneTempC"] = 29.0
        payload["constraints"]["zoneTempMaxC"] = 25.0
        req = parse_solver_request(payload)
        with self.assertRaises(SolverInfeasibleError):
            HVACPhysicsSolver().solve(req)

    def test_rejects_non_finite_and_invalid_uuid(self) -> None:
        payload = valid_payload()
        payload["tenantId"] = "not-a-uuid"
        with self.assertRaises(SolverInputError):
            parse_solver_request(payload)

        payload2 = valid_payload()
        payload2["baseline"]["supplyTempC"] = float("nan")
        with self.assertRaises(SolverInputError):
            parse_solver_request(payload2)


class SolverHTTPTests(unittest.TestCase):
    def setUp(self) -> None:
        self.server = create_server("127.0.0.1:0")
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        host, port = self.server.server_address[:2]
        self.base_url = f"http://{host}:{port}"

    def tearDown(self) -> None:
        self.server.shutdown()
        self.server.server_close()

    def test_health_ready_and_solve_roundtrip(self) -> None:
        with urllib.request.urlopen(self.base_url + "/healthz", timeout=2) as res:
            self.assertEqual(res.status, 200)
            self.assertEqual(json.loads(res.read())["status"], "ok")

        with urllib.request.urlopen(self.base_url + "/readyz", timeout=2) as res:
            self.assertEqual(res.status, 200)
            self.assertEqual(json.loads(res.read())["solverVersion"], SOLVER_VERSION)

        body = json.dumps(valid_payload()).encode("utf-8")
        req = urllib.request.Request(
            self.base_url + "/v1/solve",
            data=body,
            headers={
                "Content-Type": "application/json",
                "traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
            },
            method="POST",
        )
        with urllib.request.urlopen(req, timeout=2) as res:
            self.assertEqual(res.status, 200)
            self.assertEqual(res.headers.get("X-Solver-Version"), SOLVER_VERSION)
            self.assertEqual(res.headers.get("X-Trace-Id"), "4bf92f3577b34da6a3ce929d0e0e4736")
            parsed = json.loads(res.read())
            self.assertEqual(parsed["solverVersion"], SOLVER_VERSION)
            self.assertAlmostEqual(parsed["candidate"]["supplyTempC"], 8.5, places=4)
            self.assertEqual(len(parsed["auxiliaryTargets"]), 3)

    def test_malformed_json_and_oversized_payload_rejected(self) -> None:
        bad_req = urllib.request.Request(
            self.base_url + "/v1/solve",
            data=b"{bad-json",
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        with self.assertRaises(urllib.error.HTTPError) as ctx:
            urllib.request.urlopen(bad_req, timeout=2)
        self.assertEqual(ctx.exception.code, 400)

        huge_req = urllib.request.Request(
            self.base_url + "/v1/solve",
            data=b"x" * (260 * 1024),
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        with self.assertRaises(urllib.error.HTTPError) as ctx_large:
            urllib.request.urlopen(huge_req, timeout=2)
        self.assertEqual(ctx_large.exception.code, 413)


class CoupledMPCTests(unittest.TestCase):
    def test_wet_bulb_stull_and_missing_rh_fallback(self) -> None:
        wb = estimate_wet_bulb_c(30.0, 60.0)
        self.assertGreater(wb, 23.0)
        self.assertLess(wb, 24.5)
        for bad_rh in (None, 0.0, -5.0, float("nan")):
            fb = estimate_wet_bulb_c(32.0, bad_rh)
            self.assertGreater(fb, 15.0)
            self.assertLessEqual(fb, 32.0)

    def test_monotonic_chiller_partial_derivatives(self) -> None:
        model = MonotonicChillerModel(
            base_cop=5.8,
            tchw_slope_cop_per_c=-0.3,
            tcw_slope_cop_per_c=-0.1,
            plr_const_e0=-0.1,
            cop_scale=0.85,
        ).project_monotonic()
        p_low_tchw, cop_low_tchw = model.power_kw(400.0, 600.0, 5.5, 29.0, 33.5)
        p_high_tchw, cop_high_tchw = model.power_kw(400.0, 600.0, 9.0, 29.0, 33.5)
        self.assertLessEqual(p_high_tchw, p_low_tchw)
        self.assertGreaterEqual(cop_high_tchw, cop_low_tchw)

        p_cool_cw, _ = model.power_kw(400.0, 600.0, 7.0, 24.0, 28.0)
        p_hot_cw, _ = model.power_kw(400.0, 600.0, 7.0, 34.0, 38.0)
        self.assertGreaterEqual(p_hot_cw, p_cool_cw)

        p_part, _ = model.power_kw(250.0, 600.0, 7.0, 29.0, 33.5)
        p_full, _ = model.power_kw(600.0, 600.0, 7.0, 29.0, 33.5)
        self.assertGreater(p_full, p_part)

    def test_coupled_context_http_solve_tou_precooling_and_schema(self) -> None:
        srv = create_server("127.0.0.1:0")
        t = threading.Thread(target=srv.serve_forever, daemon=True)
        t.start()
        host, port = srv.server_address[:2]
        base_url = f"http://{host}:{port}"
        try:
            def run_solve(peak_price: float) -> dict:
                payload = valid_payload()
                payload["baseline"]["supplyTempC"] = 7.0
                payload["baseline"]["zoneTempC"] = 24.0
                payload["constraints"]["supplyTempMinC"] = 5.0
                payload["constraints"]["supplyTempMaxC"] = 11.0
                payload["constraints"]["zoneTempMinC"] = 21.0
                payload["constraints"]["zoneTempMaxC"] = 25.0
                payload["constraints"]["maxSupplyTempStepC"] = 1.5
                payload["responseModel"]["zoneTempUncertaintyP90C"] = 0.2
                horizon = []
                for h in range(24):
                    rate = 0.35 if h < 8 else (peak_price if h < 20 else 0.55)
                    horizon.append({
                        "durationHours": 1.0,
                        "coolingLoadKw": 240.0 if h < 8 else (780.0 if h < 20 else 240.0),
                        "ambientDryBulbC": 31.0,
                        "ambientWetBulbC": 26.0,
                        "tariffRateCny": rate,
                    })
                payload["coupledContext"] = {
                    "precoolingEnabled": True,
                    "copScale": 0.90,
                    "horizon": horizon,
                }
                req = urllib.request.Request(
                    base_url + "/v1/solve",
                    data=json.dumps(payload).encode("utf-8"),
                    headers={"Content-Type": "application/json"},
                    method="POST",
                )
                with urllib.request.urlopen(req, timeout=4) as res:
                    self.assertEqual(res.status, 200)
                    return json.loads(res.read())

            flat_res = run_solve(0.35)
            peak_res = run_solve(1.65)

            allowed_top_keys = {
                "id",
                "candidate",
                "expectedImpact",
                "uncertainty",
                "risk",
                "rollbackPlan",
                "verificationPlan",
                "solverVersion",
                "auxiliaryTargets",
            }
            self.assertTrue(set(peak_res.keys()).issubset(allowed_top_keys))
            flat_slots = flat_res["verificationPlan"]["mpcSlots"]
            peak_slots = peak_res["verificationPlan"]["mpcSlots"]
            self.assertEqual(len(peak_slots), 24)

            flat_valley_mean = sum(s["supplyTempC"] for s in flat_slots[1:8]) / 7.0
            peak_valley_mean = sum(s["supplyTempC"] for s in peak_slots[1:8]) / 7.0
            self.assertLess(peak_valley_mean, flat_valley_mean - 0.2)
            for s in peak_slots:
                self.assertGreaterEqual(s["supplyTempC"], 5.0)
                self.assertLessEqual(s["supplyTempC"], 11.0)
                self.assertLessEqual(s["projectedZoneTempC"] + 0.2, 25.0)
                self.assertGreaterEqual(s["projectedZoneTempC"] - 0.2, 21.0)
        finally:
            srv.shutdown()
            srv.server_close()


if __name__ == "__main__":
    unittest.main()
