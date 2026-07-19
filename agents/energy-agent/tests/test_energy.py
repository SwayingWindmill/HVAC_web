from dataclasses import FrozenInstanceError, replace
from datetime import datetime, timedelta
import ast
import importlib
import json
import math
import os
from pathlib import Path
import sys
import unittest
from unittest.mock import patch

from src.energy.analysis import (
    AnalysisError,
    EnergyAnalysisService,
    calculate_energy_analysis,
    classify_direction,
)
from src.energy.data import (
    BUSINESS_TIMEZONE,
    CATEGORY_NAMES,
    COVERAGE_DAYS,
    INTERVALS_PER_DAY,
    EnergyInterval,
    generate_mock_dataset,
)


FIXTURES = Path(__file__).parent / "fixtures"
REFERENCE_TIME = datetime(2026, 7, 16, tzinfo=BUSINESS_TIMEZONE)
SEED = 20260716


def load_json(name: str):
    return json.loads((FIXTURES / name).read_text(encoding="utf-8"))


def interval_from_row(row) -> EnergyInterval:
    timestamp, hvac, lighting, equipment, other = row
    return EnergyInterval(
        building_id="A",
        timestamp=datetime.fromisoformat(timestamp),
        hvac_kw=hvac,
        lighting_kw=lighting,
        power_equipment_kw=equipment,
        other_kw=other,
    )


class MockDatasetTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.dataset = generate_mock_dataset(seed=SEED, reference_time=REFERENCE_TIME)
        cls.service = EnergyAnalysisService(cls.dataset)

    def test_fixed_seed_and_reference_time_are_reproducible(self) -> None:
        duplicate = generate_mock_dataset(seed=SEED, reference_time=REFERENCE_TIME)
        self.assertEqual(self.dataset, duplicate)

    def test_snapshot_has_three_buildings_and_sixty_days(self) -> None:
        self.assertEqual([building.building_id for building in self.dataset.buildings], ["A", "B", "C"])
        self.assertEqual(self.dataset.end_at - self.dataset.start_at, timedelta(days=COVERAGE_DAYS))
        self.assertEqual(
            len(self.dataset.intervals),
            3 * COVERAGE_DAYS * INTERVALS_PER_DAY,
        )
        for building_id in ("A", "B", "C"):
            self.assertEqual(
                sum(i.building_id == building_id for i in self.dataset.intervals),
                COVERAGE_DAYS * INTERVALS_PER_DAY,
            )

    def test_every_interval_has_valid_categories_and_timezone(self) -> None:
        for interval in self.dataset.intervals:
            self.assertEqual(interval.timestamp.tzinfo.key, "Asia/Shanghai")
            for category in CATEGORY_NAMES:
                value = interval.category_power_kw(category)
                self.assertTrue(math.isfinite(value))
                self.assertGreaterEqual(value, 0)

    def test_query_is_left_closed_and_right_open(self) -> None:
        start = self.dataset.end_at - timedelta(hours=1)
        result = self.service.query_energy_series("A", start, self.dataset.end_at)
        self.assertEqual(len(result), 4)
        self.assertEqual(result[0].timestamp, start)
        self.assertEqual(result[-1].timestamp, self.dataset.end_at - timedelta(minutes=15))

    def test_intervals_are_immutable(self) -> None:
        with self.assertRaises(FrozenInstanceError):
            self.dataset.intervals[0].hvac_kw = 0


class ScopeValidationTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.dataset = generate_mock_dataset(seed=SEED, reference_time=REFERENCE_TIME)
        cls.service = EnergyAnalysisService(cls.dataset)

    def test_valid_scope_resolves_immediately_preceding_period(self) -> None:
        end = self.dataset.end_at
        start = end - timedelta(days=7)
        result = self.service.validate_analysis_scope("B", start, end)
        self.assertTrue(result.valid)
        self.assertEqual(result.comparison_start_at, start - timedelta(days=7))
        self.assertEqual(result.comparison_end_at, start)

    def test_all_scope_error_codes(self) -> None:
        cases = [
            ("Z", self.dataset.end_at - timedelta(days=1), self.dataset.end_at, "BUILDING_NOT_FOUND"),
            ("A", self.dataset.end_at, self.dataset.end_at, "INVALID_TIME_RANGE"),
            ("A", self.dataset.end_at - timedelta(days=1), self.dataset.end_at + timedelta(minutes=15), "TARGET_OUT_OF_RANGE"),
            ("A", self.dataset.start_at + timedelta(days=1), self.dataset.start_at + timedelta(days=3), "COMPARISON_OUT_OF_RANGE"),
        ]
        for building, start, end, expected in cases:
            with self.subTest(expected=expected):
                validation = self.service.validate_analysis_scope(building, start, end)
                self.assertFalse(validation.valid)
                self.assertEqual(validation.error_code, expected)
                self.assertTrue(validation.cause)
                self.assertTrue(validation.action)

    def test_naive_and_unaligned_ranges_are_invalid(self) -> None:
        naive = datetime(2026, 7, 1)
        self.assertEqual(
            self.service.validate_analysis_scope("A", naive, naive + timedelta(days=1)).error_code,
            "INVALID_TIME_RANGE",
        )
        start = self.dataset.end_at - timedelta(days=1, minutes=5)
        self.assertEqual(
            self.service.validate_analysis_scope("A", start, self.dataset.end_at).error_code,
            "INVALID_TIME_RANGE",
        )


class CalculationTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        fixture = load_json("calculation_fixture.json")
        cls.target = tuple(interval_from_row(row) for row in fixture["target"])
        cls.comparison = tuple(interval_from_row(row) for row in fixture["comparison"])
        cls.expected = fixture["expected"]

    def test_golden_total_peak_change_and_shares(self) -> None:
        result = calculate_energy_analysis(self.target, self.comparison)
        self.assertEqual(result.target_total_energy_kwh, self.expected["target_energy_kwh"])
        self.assertEqual(result.comparison_total_energy_kwh, self.expected["comparison_energy_kwh"])
        self.assertEqual(result.period_change_percent, self.expected["change_percent"])
        self.assertEqual(result.peak_demand_kw, self.expected["peak_demand_kw"])
        self.assertEqual(result.peak_at, datetime.fromisoformat(self.expected["peak_at"]))
        self.assertAlmostEqual(sum(category.share_percent for category in result.categories), 100.0)
        self.assertEqual(result.trend_granularity, "hourly")
        self.assertEqual(len(result.trend), 1)

    def test_full_precision_is_preserved(self) -> None:
        target = tuple(replace(item, hvac_kw=item.hvac_kw + 0.123456) for item in self.target)
        result = calculate_energy_analysis(target, self.comparison)
        expected = sum(item.total_power_kw * 0.25 for item in target)
        self.assertEqual(result.target_total_energy_kwh, expected)
        self.assertNotEqual(result.target_total_energy_kwh, round(result.target_total_energy_kwh))

    def test_direction_boundaries(self) -> None:
        self.assertEqual(classify_direction(-3.0), "down")
        self.assertEqual(classify_direction(-2.999), "stable")
        self.assertEqual(classify_direction(2.999), "stable")
        self.assertEqual(classify_direction(3.0), "up")

    def test_exactly_forty_eight_hours_aggregates_hourly(self) -> None:
        dataset = generate_mock_dataset(seed=SEED, reference_time=REFERENCE_TIME)
        service = EnergyAnalysisService(dataset)
        end = dataset.end_at
        start = end - timedelta(hours=48)
        target = service.query_energy_series("C", start, end)
        comparison = service.query_energy_series("C", start - timedelta(hours=48), start)
        result = calculate_energy_analysis(target, comparison)
        self.assertEqual(result.trend_granularity, "hourly")
        self.assertEqual(len(result.trend), 48)

    def test_long_range_aggregates_daily(self) -> None:
        dataset = generate_mock_dataset(seed=SEED, reference_time=REFERENCE_TIME)
        service = EnergyAnalysisService(dataset)
        end = dataset.end_at
        start = end - timedelta(days=7)
        target = service.query_energy_series("C", start, end)
        comparison = service.query_energy_series("C", start - timedelta(days=7), start)
        result = calculate_energy_analysis(target, comparison)
        self.assertEqual(result.trend_granularity, "daily")
        self.assertEqual(len(result.trend), 7)

    def test_evidence_is_structured(self) -> None:
        result = calculate_energy_analysis(self.target, self.comparison)
        self.assertEqual(result.evidence.comparison_direction, "up")
        self.assertEqual(result.evidence.dominant_category, "HVAC")
        self.assertEqual(len(result.evidence.high_load_windows), 1)
        self.assertGreater(result.evidence.peak_contribution.share_percent, 0)
        self.assertGreaterEqual(result.evidence.overnight_baseload_ratio, 0)

    def test_incomplete_and_invalid_measurements_fail_without_result(self) -> None:
        fixture = load_json("error_cases.json")
        for name, case in fixture.items():
            target = list(self.target)
            comparison = list(self.comparison)
            if "remove_target_index" in case:
                target.pop(case["remove_target_index"])
            if "remove_comparison_index" in case:
                comparison.pop(case["remove_comparison_index"])
            if "target_index" in case:
                value = float("nan") if case["value"] == "NaN" else case["value"]
                target[case["target_index"]] = replace(
                    target[case["target_index"]],
                    **{case["field"]: value},
                )

            with self.subTest(case=name), self.assertRaises(AnalysisError) as caught:
                calculate_energy_analysis(target, comparison)
            self.assertEqual(caught.exception.code, case["expected_error"])
            self.assertTrue(caught.exception.action)

    def test_zero_category_change_is_explicitly_unavailable(self) -> None:
        comparison = tuple(replace(item, other_kw=0.0) for item in self.comparison)
        target = tuple(replace(item, other_kw=1.0) for item in self.target)
        result = calculate_energy_analysis(target, comparison)
        other = next(item for item in result.categories if item.category == "Other")
        self.assertIsNone(other.change_percent)

    def test_all_zero_target_is_rejected(self) -> None:
        target = tuple(
            replace(
                item,
                hvac_kw=0.0,
                lighting_kw=0.0,
                power_equipment_kw=0.0,
                other_kw=0.0,
            )
            for item in self.target
        )
        with self.assertRaises(AnalysisError) as caught:
            calculate_energy_analysis(target, self.comparison)
        self.assertEqual(caught.exception.code, "INVALID_MEASUREMENT")


class ScenarioTests(unittest.TestCase):
    def test_a_b_c_golden_scenarios(self) -> None:
        fixture = load_json("scenario_expectations.json")
        reference = datetime.fromisoformat(fixture["reference_time"])
        dataset = generate_mock_dataset(seed=fixture["seed"], reference_time=reference)
        service = EnergyAnalysisService(dataset)
        end = dataset.end_at
        start = end - timedelta(days=fixture["target_days"])

        for building_id, expected in fixture["buildings"].items():
            with self.subTest(building=building_id):
                target = service.query_energy_series(building_id, start, end)
                comparison = service.query_energy_series(
                    building_id,
                    start - (end - start),
                    start,
                )
                result = calculate_energy_analysis(target, comparison)
                self.assertEqual(result.direction, expected["direction"])
                self.assertGreaterEqual(result.period_change_percent, expected["minimum_change_percent"])
                self.assertLessEqual(result.period_change_percent, expected["maximum_change_percent"])
                self.assertEqual(result.evidence.dominant_category, expected["dominant_category"])
                if "minimum_overnight_baseload_ratio" in expected:
                    self.assertGreaterEqual(
                        result.evidence.overnight_baseload_ratio,
                        expected["minimum_overnight_baseload_ratio"],
                    )
                if "peak_hour_start" in expected:
                    self.assertGreaterEqual(result.peak_at.hour, expected["peak_hour_start"])
                    self.assertLess(result.peak_at.hour, expected["peak_hour_end"])
                peak_change = (result.peak_demand_kw - result.comparison_peak_demand_kw) / result.comparison_peak_demand_kw * 100
                if expected["peak_change"] == "lower":
                    self.assertLess(peak_change, 0)
                elif expected["peak_change"] == "higher":
                    self.assertGreater(peak_change, 0)
                else:
                    self.assertLess(abs(peak_change), 3)

                if building_id == "B":
                    category_changes = {item.category: item.change_percent for item in result.categories}
                    self.assertEqual(max(category_changes, key=category_changes.get), "HVAC")


class RuntimeInitializationTests(unittest.TestCase):
    def test_dataset_initialization_failure_has_stable_runtime_error(self) -> None:
        sys.modules.pop("src.energy.runtime", None)
        sys.modules.pop("src.config", None)
        with (
            patch.dict(
                os.environ,
                {
                    "OPENAI_API_KEY": "x",
                    "OPENAI_MODEL": "test-model",
                    "ENERGY_DATA_SEED": str(SEED),
                },
                clear=True,
            ),
            patch("src.energy.data.generate_mock_dataset", side_effect=RuntimeError("boom")),
            self.assertRaisesRegex(RuntimeError, "DATASET_INITIALIZATION_FAILED"),
        ):
            importlib.import_module("src.energy.runtime")
        sys.modules.pop("src.energy.runtime", None)
        sys.modules.pop("src.config", None)


class ToolBoundaryTests(unittest.TestCase):
    def test_only_approved_tool_functions_are_declared(self) -> None:
        source = (Path(__file__).parents[1] / "src" / "energy" / "tools.py").read_text(encoding="utf-8")
        tree = ast.parse(source)
        functions = {
            node.name
            for node in tree.body
            if isinstance(node, ast.FunctionDef)
        }
        self.assertEqual(
            functions,
            {
                "list_buildings",
                "validate_analysis_scope",
                "query_energy_series",
                "calculate_energy_analysis",
            },
        )
        self.assertNotIn("open(", source)
        self.assertNotIn("read_text", source)

    def test_approved_tools_execute_against_process_snapshot(self) -> None:
        for module_name in ("src.energy.tools", "src.energy.runtime", "src.config"):
            sys.modules.pop(module_name, None)
        try:
            with patch.dict(
                os.environ,
                {
                    "OPENAI_API_KEY": "x",
                    "OPENAI_MODEL": "test-model",
                    "ENERGY_DATA_SEED": str(SEED),
                },
                clear=True,
            ):
                tools = importlib.import_module("src.energy.tools")
                runtime = importlib.import_module("src.energy.runtime")
                buildings = tools.list_buildings()
                self.assertIsInstance(buildings, list)
                self.assertEqual([building.building_id for building in buildings], ["A", "B", "C"])

                end = runtime.energy_service.dataset.end_at
                start = end - timedelta(days=7)
                validation = tools.validate_analysis_scope("B", start, end)
                self.assertTrue(validation.valid)
                target = tools.query_energy_series("B", start, end)
                comparison = tools.query_energy_series(
                    "B",
                    validation.comparison_start_at,
                    validation.comparison_end_at,
                )
                self.assertIsInstance(target, list)
                result = tools.calculate_energy_analysis(target, comparison)
                self.assertEqual(result.direction, "up")
        finally:
            for module_name in ("src.energy.tools", "src.energy.runtime", "src.config"):
                sys.modules.pop(module_name, None)


if __name__ == "__main__":
    unittest.main()
