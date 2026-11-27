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
from ai_solver.trainer import (
    CANONICAL_SAMPLE_KINDS,
    SAMPLE_KIND_GUARDRAIL_CLAMPED,
    SAMPLE_KIND_GUARDRAIL_VETOED,
    SAMPLE_KIND_MANUAL_OVERRIDE,
    SAMPLE_KIND_MONITOR_TRIPPED,
    SAMPLE_KIND_TRACKING_DEVIATION,
    main as trainer_main,
    train_profile_from_lines,
)
from tests.test_solver import valid_payload


class TrainerUnitTests(unittest.TestCase):
    def test_train_profile_from_jsonl_ignores_malformed_lines_and_separates_sites(self) -> None:
        site_a = "01990000-5000-7000-8000-000000000001"
        site_b = "01990000-5000-7000-8000-000000000002"
        lines = [
            "",
            "not valid json at all",
            json.dumps({"siteId": site_a, "sampleKind": SAMPLE_KIND_MANUAL_OVERRIDE}),
            json.dumps({"siteId": site_a, "sampleKind": SAMPLE_KIND_MONITOR_TRIPPED}),
            json.dumps({"siteId": site_a, "sampleKind": SAMPLE_KIND_TRACKING_DEVIATION}),
            json.dumps({"siteId": site_a, "sampleKind": SAMPLE_KIND_GUARDRAIL_VETOED}),
            json.dumps({
                "siteId": site_a,
                "sampleKind": SAMPLE_KIND_GUARDRAIL_CLAMPED,
                "capability": "SET_FREQUENCY",
                "loopKey": "chilled_water_pump_freq",
                "clampedDelta": 3.0,
            }),
            json.dumps({
                "siteId": site_b,
                "sampleKind": SAMPLE_KIND_GUARDRAIL_CLAMPED,
                "capability": "SET_FAN_SPEED",
                "loopKey": "cooling_tower_fan_speed",
                "rawRecommendedValue": 20.0,
                "appliedOrClampedValue": 26.0,
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
        self.assertEqual(profile.sample_count, 7)
        self.assertEqual(profile.negative_sample_count, 6)
        self.assertEqual(
            profile.sample_kind_counts,
            {
                SAMPLE_KIND_GUARDRAIL_CLAMPED: 2,
                SAMPLE_KIND_GUARDRAIL_VETOED: 1,
                SAMPLE_KIND_MANUAL_OVERRIDE: 1,
                SAMPLE_KIND_TRACKING_DEVIATION: 1,
                SAMPLE_KIND_MONITOR_TRIPPED: 1,
            },
        )

        cal_a = profile.calibration_for_site(site_a)
        # 0.08 (MANUAL_OVERRIDE) + 0.15 (MONITOR_TRIPPED) + 0.05 (GUARDRAIL_VETOED) = 0.28
        self.assertAlmostEqual(cal_a.comfort_margin_penalty_c, 0.28, places=4)
        # 1.0 - (0.04 + 0.06 + 0.02 + 0.03) = 0.85
        self.assertAlmostEqual(cal_a.supply_step_scale, 0.85, places=4)
        self.assertAlmostEqual(cal_a.chw_pump_bias_hz, 1.5, places=4)
        self.assertAlmostEqual(cal_a.tower_fan_bias_pct, 0.0, places=4)

        cal_b = profile.calibration_for_site(site_b)
        self.assertAlmostEqual(cal_b.comfort_margin_penalty_c, 0.0, places=4)
        self.assertAlmostEqual(cal_b.tower_fan_bias_pct, 3.0, places=4)

    def test_go_negative_sample_jsonl_contract_and_ignores_stale_enum_names(self) -> None:
        repo_root = Path(__file__).resolve().parents[3]
        fixture_path = (
            repo_root
            / "services"
            / "optimization-service"
            / "internal"
            / "optimization"
            / "testdata"
            / "flywheel-negative-samples.contract.jsonl"
        )
        contract_lines = fixture_path.read_text(encoding="utf-8").splitlines()
        self.assertEqual(len(contract_lines), 5)

        stale_lines = [
            json.dumps({"sampleKind": "POST_CONTROL_ALARM"}),
            json.dumps({"sampleKind": "TRACKING_DRIFT"}),
        ]
        profile = train_profile_from_lines(
            contract_lines + stale_lines,
            profile_id="contract-check-v1",
            now_utc=datetime(2026, 11, 27, 8, 0, 0, tzinfo=timezone.utc),
        )
        self.assertEqual(profile.sample_count, 7)
        self.assertEqual(profile.negative_sample_count, 5)
        self.assertEqual(set(profile.sample_kind_counts.keys()), set(CANONICAL_SAMPLE_KINDS))
        for kind in CANONICAL_SAMPLE_KINDS:
            self.assertEqual(profile.sample_kind_counts[kind], 1, msg=f"expected count=1 for {kind}")

        self.assertAlmostEqual(profile.global_calibration.comfort_margin_penalty_c, 0.28, places=4)
        self.assertAlmostEqual(profile.global_calibration.supply_step_scale, 0.85, places=4)

        with tempfile.TemporaryDirectory() as tmp:
            out_path = Path(tmp) / "contract-profile.json"
            raw_json = json.loads(json.dumps(profile.to_dict()))
            self.assertEqual(raw_json["sampleKindCounts"], profile.sample_kind_counts)
            self.assertEqual(trainer_main(["--input", str(fixture_path), "--output", str(out_path)]), 0)
            reloaded = load_calibration_profile(out_path)
            self.assertEqual(reloaded.negative_sample_count, 5)
            self.assertEqual(reloaded.sample_kind_counts, profile.sample_kind_counts)

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
