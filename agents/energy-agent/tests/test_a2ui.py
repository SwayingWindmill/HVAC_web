from copy import deepcopy
from datetime import datetime, timedelta
from pathlib import Path
import unittest
from unittest.mock import patch

from langchain_core.messages import HumanMessage
from pydantic import ValidationError

from src.a2ui import (
    APPROVED_COMPONENT_NAMES,
    ENERGY_CATALOG_ID,
    FIXED_SECTION_ORDER,
    RENDER_A2UI_TOOL_NAME,
    EnergyA2UISurface,
    build_energy_a2ui_surface,
    validate_component,
)
from src.energy.analysis import EnergyAnalysisService
from src.energy.data import BUSINESS_TIMEZONE, generate_mock_dataset
from src.workflow.contracts import serialize_analysis_result
from src.workflow.graph import (
    HANDLE_ERROR,
    RENDER_RESULT,
    SUCCESS_TRACE,
    build_energy_graph,
)
from tests.test_workflow import COPILOTKIT_STATE, FakeInsightModel, VALID_INSIGHTS


MONOREPO_ROOT = Path(__file__).resolve().parents[3]
REFERENCE_APP_ROOT = MONOREPO_ROOT / "references" / "energy-agent-next"


REFERENCE_TIME = datetime(2026, 7, 16, tzinfo=BUSINESS_TIMEZONE)
SEED = 20260716


class FixedA2UICatalogTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        dataset = generate_mock_dataset(seed=SEED, reference_time=REFERENCE_TIME)
        cls.service = EnergyAnalysisService(dataset)
        target_end = dataset.end_at
        target_start = target_end - timedelta(days=7)
        validation = cls.service.validate_analysis_scope("B", target_start, target_end)
        target = cls.service.query_energy_series("B", target_start, target_end)
        comparison = cls.service.query_energy_series(
            "B",
            validation.comparison_start_at,
            validation.comparison_end_at,
        )
        cls.analysis = serialize_analysis_result(
            cls.service.calculate_energy_analysis(target, comparison)
        )

    def surface(self) -> EnergyA2UISurface:
        return build_energy_a2ui_surface(
            surface_id="surface-fixed",
            analysis=self.analysis,
            insights=VALID_INSIGHTS,
        )

    def test_surface_uses_only_the_nine_approved_components(self) -> None:
        surface = self.surface()
        component_names = {component.component for component in surface.components}
        self.assertEqual(component_names, APPROVED_COMPONENT_NAMES)
        self.assertEqual(surface.catalog_id, ENERGY_CATALOG_ID)

    def test_top_level_sections_are_fixed_and_cannot_be_reordered(self) -> None:
        surface = self.surface()
        payload = surface.tool_arguments()
        root = payload["components"][0]
        self.assertEqual(
            root["children"],
            [f"section-{key}" for key in FIXED_SECTION_ORDER],
        )

        reordered = deepcopy(payload)
        reordered["components"][0]["children"] = list(
            reversed(reordered["components"][0]["children"])
        )
        with self.assertRaises(ValidationError):
            EnergyA2UISurface.model_validate(reordered)

    def test_surface_rejects_unknown_components_duplicate_ids_and_broken_refs(self) -> None:
        with self.assertRaises(ValidationError):
            validate_component({"id": "unknown", "component": "Text", "text": "no"})

        payload = self.surface().tool_arguments()
        duplicate = deepcopy(payload)
        duplicate["components"][1]["id"] = "root"
        with self.assertRaises(ValidationError):
            EnergyA2UISurface.model_validate(duplicate)

        broken = deepcopy(payload)
        broken["components"][0]["children"][0] = "missing-section"
        with self.assertRaises(ValidationError):
            EnergyA2UISurface.model_validate(broken)

    def test_presentation_rounding_occurs_only_in_surface_payload(self) -> None:
        payload = self.surface().tool_arguments()
        by_id = {component["id"]: component for component in payload["components"]}

        self.assertEqual(
            by_id["metric-total-energy"]["value"],
            f"{self.analysis['metrics']['target_total_energy_kwh']:.0f}",
        )
        self.assertEqual(
            by_id["metric-peak-demand"]["value"],
            f"{self.analysis['metrics']['peak_demand_kw']:.0f}",
        )
        self.assertEqual(
            by_id["metric-period-change"]["value"],
            f"{self.analysis['metrics']['period_change_percent']:+.1f}",
        )

        trend = by_id["chart-energy-trend"]
        self.assertTrue(trend["readOnly"])
        self.assertTrue(
            all(
                float(point["targetEnergyKwh"]).is_integer()
                and float(point["comparisonEnergyKwh"]).is_integer()
                for point in trend["points"]
            )
        )
        shares = by_id["chart-category-share"]
        self.assertTrue(shares["readOnly"])
        self.assertTrue(
            all(round(item["sharePercent"], 1) == item["sharePercent"] for item in shares["items"])
        )

    def test_review_and_bookmark_catalog_components_are_interactive(self) -> None:
        payload = self.surface().tool_arguments()
        review = next(
            component
            for component in payload["components"]
            if component["component"] == "FindingReviewActions"
        )
        bookmark = next(
            component
            for component in payload["components"]
            if component["component"] == "RecommendationBookmarkAction"
        )
        self.assertTrue(review["interactive"])
        self.assertTrue(bookmark["interactive"])

    def test_graph_emits_one_deterministic_render_a2ui_tool_call(self) -> None:
        graph = build_energy_graph(
            service=self.service,
            insight_model=FakeInsightModel(),
            id_factory=lambda prefix: f"{prefix}-fixed",
        )
        output = graph.invoke(
            {
                "messages": [HumanMessage(content="Analyze Building B over the last 7 days.")],
                "copilotkit": COPILOTKIT_STATE,
            }
        )

        self.assertEqual(output["execution_trace"], SUCCESS_TRACE)
        self.assertEqual(output["workflow_status"], "complete")
        self.assertNotIn("result_surface", output)
        self.assertEqual(output["investigation"]["result_surface_id"], "surface-fixed")
        message = output["messages"][-1]
        self.assertEqual(len(message.tool_calls), 1)
        tool_call = message.tool_calls[0]
        self.assertEqual(tool_call["name"], RENDER_A2UI_TOOL_NAME)
        self.assertEqual(tool_call["id"], "render-run-fixed")
        self.assertEqual(tool_call["args"]["surfaceId"], "surface-fixed")
        self.assertEqual(tool_call["args"]["catalogId"], ENERGY_CATALOG_ID)

    def test_invalid_surface_becomes_stable_workflow_error(self) -> None:
        graph = build_energy_graph(
            service=self.service,
            insight_model=FakeInsightModel(),
            id_factory=lambda prefix: f"{prefix}-fixed",
        )
        with patch(
            "src.workflow.graph.build_energy_a2ui_surface",
            side_effect=ValueError("invalid fixed surface"),
        ):
            output = graph.invoke(
                {
                    "messages": [
                        HumanMessage(content="Analyze Building B over the last 7 days.")
                    ],
                    "copilotkit": COPILOTKIT_STATE,
                }
            )

        self.assertEqual(output["workflow_status"], "error")
        self.assertEqual(output["error"]["code"], "A2UI_SCHEMA_INVALID")
        self.assertEqual(output["error"]["stage"], RENDER_RESULT)
        self.assertEqual(output["execution_trace"][-2:], [RENDER_RESULT, HANDLE_ERROR])
        self.assertEqual(output["investigation"]["status"], "error")
        self.assertIsNone(output["investigation"]["result_surface_id"])

    @unittest.skipUnless(
        (REFERENCE_APP_ROOT / "src" / "app").exists(),
        "The standalone EnergyAgent Next.js shell is not included in the HVAC-hosted deployment.",
    )
    def test_runtime_and_canvas_use_fixed_catalog_without_tool_injection(self) -> None:
        repository_root = REFERENCE_APP_ROOT
        route = (
            repository_root / "src" / "app" / "api" / "copilotkit" / "[[...slug]]" / "route.ts"
        ).read_text(encoding="utf-8")
        layout = (repository_root / "src" / "app" / "layout.tsx").read_text(
            encoding="utf-8"
        )
        host = (
            repository_root / "src" / "components" / "result-surface-host.tsx"
        ).read_text(encoding="utf-8")

        self.assertIn("schema: energyCatalogSchema", route)
        self.assertIn("injectA2UITool: false", route)
        self.assertIn('a2uiToolNames: ["render_a2ui"]', route)
        self.assertIn("catalog: energyCatalog", layout)
        self.assertIn('activityType: "a2ui-surface"', layout)
        self.assertIn("render: () => null", layout)
        self.assertIn("A2UIRenderer", host)
        self.assertIn("a2ui_operations", host)
        self.assertIn("result_surface_id", host)


if __name__ == "__main__":
    unittest.main()
