"""Unit and HTTP integration tests for ai-solver-service."""

from __future__ import annotations

import json
import threading
import unittest
import urllib.error
import urllib.request

from ai_solver.model import SolverInfeasibleError, SolverInputError, parse_solver_request
from ai_solver.server import create_server
from ai_solver.solver import HVACPhysicsSolver, SOLVER_VERSION


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


if __name__ == "__main__":
    unittest.main()
