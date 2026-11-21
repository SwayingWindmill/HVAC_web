"""Unit and CLI tests for offline calibration trainer and bounded profile loader."""

from __future__ import annotations

from datetime import datetime, timezone
import json
from pathlib import Path
import tempfile
import threading
import unittest
import urllib.request

from ai_solver.calibration import (
    CalibrationProfile,
    MAX_COMFORT_MARGIN_PENALTY_C,
    MAX_PUMP_BIAS_HZ,
    MAX_TOWER_FAN_BIAS_PCT,
    MIN_SUPPLY_STEP_SCALE,
    SiteCalibration,
    load_calibration_profile,
)
from ai_solver.model import parse_solver_request
from ai_solver.server import create_server
from ai_solver.solver import HVACPhysicsSolver
from ai_solver.trainer import main as trainer_main, train_profile_from_lines
from tests.test_solver import valid_payload


class TrainerUnitTests(unittest.TestCase):
    def test_train_profile_from_jsonl_ignores_malformed_lines_and_separates_sites(self) -> None:
        site_a = "01990000-5000-7000-8000-000000000001"
        site_b = "01990000-5000-7000-8000-000000000002"
        lines = [
            "",
            "not valid json at all",
            json.dumps({"siteId": site_a, "sampleKind": "MANUAL_OVERRIDE"}),
            json.dumps({"siteId": site_a, "sampleKind": "POST_CONTROL_ALARM"}),
            json.dumps({"siteId": site_a, "sampleKind": "TRACKING_DRIFT"}),
            json.dumps({
                "siteId": site_a,
                "sampleKind": "GUARDRAIL_CLAMPED",
                "capability": "SET_FREQUENCY",
                "loopKey": "chilled_water_pump_freq",
                "clampedDelta": 3.0,
            }),
            json.dumps({
                "siteId": site_b,
                "sampleKind": "GUARDRAIL_CLAMPED",
                "capability": "SET_FAN_SPEED",
                "loopKey": "cooling_tower_fan_speed",
                "rawRecommendedValue": 20.0,
                "clampedValue": 26.0,
            }),
            json.dumps({"siteId": site_b}),  # non-negative control tick
        ]
        profile = train_profile_from_lines(
            lines,
            profile_id="site-ab-v1",
            now_utc=datetime(2026, 11, 21, 12, 0, 0, tzinfo=timezone.utc),
        )
        self.assertEqual(profile.profile_id, "site-ab-v1")
        self.assertEqual(profile.calibrated_at, "2026-11-21T12:00:00Z")
        self.assertEqual(profile.sample_count, 6)
        self.assertEqual(profile.negative_sample_count, 5)

        cal_a = profile.calibration_for_site(site_a)
        self.assertAlmostEqual(cal_a.comfort_margin_penalty_c, 0.23, places=4)
        self.assertAlmostEqual(cal_a.supply_step_scale, 0.88, places=4)
        self.assertAlmostEqual(cal_a.chw_pump_bias_hz, 1.5, places=4)
        self.assertAlmostEqual(cal_a.tower_fan_bias_pct, 0.0, places=4)

        cal_b = profile.calibration_for_site(site_b)
        self.assertAlmostEqual(cal_b.comfort_margin_penalty_c, 0.0, places=4)
        self.assertAlmostEqual(cal_b.tower_fan_bias_pct, 3.0, places=4)

    def test_adversarial_extreme_profile_values_are_strictly_clamped(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "extreme.json"
            path.write_text(
                json.dumps(
                    {
                        "schemaVersion": 1,
                        "profileId": "extreme-test",
                        "globalCalibration": {
                            "comfortMarginPenaltyC": 99.0,
                            "supplyStepScale": 0.01,
                            "chwPumpBiasHz": -50.0,
                            "cwPumpBiasHz": 40.0,
                            "towerFanBiasPct": 200.0,
                        },
                        "siteOverrides": {
                            "01990000-5000-7000-8000-000000000001": {
                                "comfortMarginPenaltyC": -3.0,
                                "supplyStepScale": 4.0,
                            }
                        },
                    }
                ),
                encoding="utf-8",
            )
            loaded = load_calibration_profile(path)
            g = loaded.calibration_for_site("unlisted-site")
            self.assertEqual(g.comfort_margin_penalty_c, MAX_COMFORT_MARGIN_PENALTY_C)
            self.assertEqual(g.supply_step_scale, MIN_SUPPLY_STEP_SCALE)
            self.assertEqual(g.chw_pump_bias_hz, -MAX_PUMP_BIAS_HZ)
            self.assertEqual(g.cw_pump_bias_hz, MAX_PUMP_BIAS_HZ)
            self.assertEqual(g.tower_fan_bias_pct, MAX_TOWER_FAN_BIAS_PCT)

            s = loaded.calibration_for_site("01990000-5000-7000-8000-000000000001")
            self.assertEqual(s.comfort_margin_penalty_c, 0.0)
            self.assertEqual(s.supply_step_scale, 1.0)

    def test_calibrated_solver_damps_step_applies_bias_and_keeps_hard_bounds(self) -> None:
        payload = valid_payload()
        site_id = payload["siteId"]
        req = parse_solver_request(payload)

        baseline_resp = HVACPhysicsSolver().solve(req)
        self.assertAlmostEqual(baseline_resp.supply_temp_c, 8.5, places=4)

        profile = CalibrationProfile(
            profile_id="tight-cal-v1",
            site_overrides={
                site_id: SiteCalibration(
                    comfort_margin_penalty_c=1.0,
                    supply_step_scale=0.6,
                    chw_pump_bias_hz=-5.0,
                    cw_pump_bias_hz=5.0,
                    tower_fan_bias_pct=10.0,
                )
            },
        )
        cal_resp = HVACPhysicsSolver(calibration=profile).solve(req)
        # Step is damped by 0.6 * 1.5 = 0.9 => supply <= 7.9 (instead of 8.5)
        self.assertLessEqual(cal_resp.supply_temp_c, 7.9 + 1e-4)
        self.assertGreaterEqual(cal_resp.comfort_margin_c, 1.0 - 1e-4)

        aux_by_loop = {item.loop_key: item.target_value for item in cal_resp.auxiliary_targets}
        self.assertGreaterEqual(aux_by_loop["chilled_water_pump_freq"], 28.0)
        self.assertLessEqual(aux_by_loop["cooling_water_pump_freq"], 50.0)
        self.assertLessEqual(aux_by_loop["cooling_tower_fan_speed"], 100.0)

    def test_cli_round_trip_and_readyz_reports_calibration_profile_id(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            jsonl_path = Path(tmp) / "samples.jsonl"
            out_path = Path(tmp) / "profile.json"
            jsonl_path.write_text(
                json.dumps({"siteId": "01990000-5000-7000-8000-000000000001", "sampleKind": "MANUAL_OVERRIDE"}) + "\n",
                encoding="utf-8",
            )
            exit_code = trainer_main(["--input", str(jsonl_path), "--output", str(out_path), "--profile-id", "cli-prof-1"])
            self.assertEqual(exit_code, 0)
            loaded = load_calibration_profile(out_path)
            self.assertEqual(loaded.profile_id, "cli-prof-1")

            srv = create_server("127.0.0.1:0", solver=HVACPhysicsSolver(calibration=loaded))
            host, port = srv.server_address[:2]
            t = threading.Thread(target=srv.serve_forever, daemon=True)
            t.start()
            try:
                with urllib.request.urlopen(f"http://{host}:{port}/readyz", timeout=3) as resp:
                    self.assertEqual(resp.status, 200)
                    body = json.loads(resp.read().decode("utf-8"))
                self.assertEqual(body["calibrationProfileId"], "cli-prof-1")
            finally:
                srv.shutdown()
                srv.server_close()
