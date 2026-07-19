from datetime import datetime, timedelta
from pathlib import Path
import unittest

from langchain_core.messages import HumanMessage

from src.energy.analysis import EnergyAnalysisService
from src.energy.data import BUSINESS_TIMEZONE, generate_mock_dataset
from src.workflow.graph import RESOLVE_SCOPE, SUCCESS_TRACE, build_energy_graph
from tests.test_workflow import COPILOTKIT_STATE, FakeInsightModel


REFERENCE_TIME = datetime(2026, 7, 16, tzinfo=BUSINESS_TIMEZONE)
SEED = 20260716
MONOREPO_ROOT = Path(__file__).resolve().parents[3]
REFERENCE_APP_ROOT = MONOREPO_ROOT / "references" / "energy-agent-next"


class CanvasWorkflowIntegrationTests(unittest.TestCase):
    def setUp(self) -> None:
        dataset = generate_mock_dataset(seed=SEED, reference_time=REFERENCE_TIME)
        self.service = EnergyAnalysisService(dataset)
        self.model = FakeInsightModel()
        self.graph = build_energy_graph(
            service=self.service,
            insight_model=self.model,
            id_factory=lambda prefix: f"{prefix}-canvas",
        )
        self.start_at = dataset.end_at - timedelta(days=7)
        self.end_at = dataset.end_at
        self.scope_command = {
            "type": "set_scope",
            "building_id": "B",
            "time_range": {
                "start_at": self.start_at.isoformat(),
                "end_at": self.end_at.isoformat(),
                "timezone": "Asia/Shanghai",
            },
        }

    def apply_scope(self):
        return self.graph.invoke(
            {
                "messages": [],
                "copilotkit": COPILOTKIT_STATE,
                "investigation_command": self.scope_command,
            }
        )

    def analyze(self, investigation):
        return self.graph.invoke(
            {
                "messages": [],
                "copilotkit": COPILOTKIT_STATE,
                "investigation": investigation,
                "analysis_request": {
                    "building_id": "B",
                    "source": "canvas",
                    "start_at": self.start_at.isoformat(),
                    "end_at": self.end_at.isoformat(),
                },
            }
        )

    def test_apply_scope_is_mutation_only_and_analyze_is_explicit(self) -> None:
        scoped = self.apply_scope()
        self.assertEqual(scoped["workflow_status"], "mutation_applied")
        self.assertEqual(scoped["execution_trace"], [RESOLVE_SCOPE])
        self.assertEqual(scoped["investigation"]["status"], "ready")
        self.assertEqual(scoped["investigation"]["building_id"], "B")
        self.assertIsNone(scoped["investigation"]["result_surface_id"])

        completed = self.analyze(scoped["investigation"])
        self.assertEqual(completed["workflow_status"], "complete")
        self.assertEqual(completed["execution_trace"], SUCCESS_TRACE)
        self.assertEqual(completed["investigation"]["status"], "complete")
        self.assertIsNotNone(completed["investigation"]["result_surface_id"])

    def test_review_and_bookmark_mutations_preserve_surface(self) -> None:
        completed = self.analyze(self.apply_scope()["investigation"])
        investigation = completed["investigation"]
        surface_id = investigation["result_surface_id"]
        finding_id = investigation["findings"][0]["finding_id"]
        recommendation_id = investigation["recommendations"][0]["recommendation_id"]

        reviewed = self.graph.invoke(
            {
                "messages": completed["messages"],
                "copilotkit": COPILOTKIT_STATE,
                "investigation": investigation,
                "investigation_command": {
                    "type": "review_finding",
                    "finding_id": finding_id,
                    "review_status": "confirmed",
                },
            }
        )
        self.assertEqual(reviewed["workflow_status"], "mutation_applied")
        self.assertEqual(reviewed["execution_trace"], [RESOLVE_SCOPE])
        self.assertEqual(reviewed["investigation"]["result_surface_id"], surface_id)
        self.assertEqual(reviewed["investigation"]["findings"][0]["review_status"], "confirmed")

        bookmarked = self.graph.invoke(
            {
                "messages": reviewed["messages"],
                "copilotkit": COPILOTKIT_STATE,
                "investigation": reviewed["investigation"],
                "investigation_command": {
                    "type": "bookmark_recommendation",
                    "recommendation_id": recommendation_id,
                    "bookmarked": True,
                },
            }
        )
        self.assertEqual(bookmarked["workflow_status"], "mutation_applied")
        self.assertEqual(bookmarked["investigation"]["result_surface_id"], surface_id)
        self.assertTrue(bookmarked["investigation"]["recommendations"][0]["bookmarked"])

    def test_scope_change_invalidates_surface_and_review_state(self) -> None:
        completed = self.analyze(self.apply_scope()["investigation"])
        changed = self.graph.invoke(
            {
                "messages": completed["messages"],
                "copilotkit": COPILOTKIT_STATE,
                "investigation": completed["investigation"],
                "investigation_command": {
                    **self.scope_command,
                    "building_id": "A",
                },
            }
        )
        self.assertEqual(changed["workflow_status"], "mutation_applied")
        self.assertEqual(changed["investigation"]["status"], "ready")
        self.assertEqual(changed["investigation"]["building_id"], "A")
        self.assertIsNone(changed["investigation"]["result_surface_id"])
        self.assertEqual(changed["investigation"]["findings"], [])
        self.assertEqual(changed["investigation"]["recommendations"], [])


@unittest.skipUnless(
    (REFERENCE_APP_ROOT / "src" / "app").exists(),
    "The standalone EnergyAgent Next.js canvas is replaced by the HVAC Web host UI.",
)
class CanvasSourceContractTests(unittest.TestCase):
    def test_canvas_order_and_development_inspector(self) -> None:
        root = REFERENCE_APP_ROOT
        page = (root / "src" / "app" / "page.tsx").read_text(encoding="utf-8")
        self.assertLess(page.index("<AnalysisScope"), page.index("<AnalysisProgress"))
        self.assertLess(page.index("<AnalysisProgress"), page.index("<ResultSurfaceHost"))
        self.assertLess(page.index("<ResultSurfaceHost"), page.index("<InvestigationStateSummary"))
        self.assertIn('process.env.NODE_ENV === "development"', page)
        self.assertIn("<CopilotChat", page)

    def test_canvas_actions_use_explicit_graph_inputs(self) -> None:
        root = REFERENCE_APP_ROOT
        provider = (root / "src" / "components" / "energy-agent-provider.tsx").read_text(
            encoding="utf-8"
        )
        scope = (root / "src" / "components" / "analysis-scope.tsx").read_text(
            encoding="utf-8"
        )
        catalog = (root / "src" / "a2ui" / "energy-catalog.tsx").read_text(
            encoding="utf-8"
        )

        for command in ("set_scope", "review_finding", "bookmark_recommendation"):
            self.assertIn(command, provider)
        self.assertIn("analysis_request", provider)
        self.assertIn("agent.setState", provider)
        self.assertIn("agent.runAgent", provider)
        self.assertIn("Apply scope", scope)
        self.assertIn("Analyze energy", scope)
        self.assertIn("Yesterday", scope)
        self.assertIn("Last 24 hours", scope)
        self.assertIn("Last 7 days", scope)
        self.assertIn("useEnergyA2UIActions", catalog)
        self.assertIn("actions.reviewFinding", catalog)
        self.assertIn("actions.setRecommendationBookmark", catalog)

    def test_progress_covers_the_documented_lifecycle(self) -> None:
        root = REFERENCE_APP_ROOT
        progress = (root / "src" / "components" / "analysis-progress.tsx").read_text(
            encoding="utf-8"
        )
        for status in (
            "validating",
            "loading_data",
            "calculating",
            "generating_insights",
            "complete",
        ):
            self.assertIn(status, progress)

    def test_error_progress_and_surface_expose_stable_failure_details(self) -> None:
        root = REFERENCE_APP_ROOT
        progress = (root / "src" / "components" / "analysis-progress.tsx").read_text(
            encoding="utf-8"
        )
        surface = (root / "src" / "components" / "result-surface-host.tsx").read_text(
            encoding="utf-8"
        )
        for stage in (
            "validate_scope",
            "query_target",
            "query_comparison",
            "calculate_analysis",
            "generate_insights",
            "update_investigation",
            "render_result",
        ):
            self.assertIn(stage, progress)
        self.assertIn("workflowError.code", progress)
        self.assertIn("workflowError.stage", progress)
        self.assertIn("No partial result was published", surface)
        self.assertIn("workflowError?.cause", surface)
        self.assertIn("workflowError?.action", surface)

    def test_agent_readiness_disables_actions_until_health_is_ready(self) -> None:
        root = REFERENCE_APP_ROOT
        provider = (root / "src" / "components" / "energy-agent-provider.tsx").read_text(
            encoding="utf-8"
        )
        scope = (root / "src" / "components" / "analysis-scope.tsx").read_text(
            encoding="utf-8"
        )
        self.assertIn('fetch("/api/health"', provider)
        self.assertIn('"checking" | "ready" | "unreachable"', provider)
        self.assertIn("setInterval", provider)
        self.assertIn("if (!agentReady)", provider)
        self.assertIn("Agent unavailable", scope)
        self.assertIn("!agentReady || busy", scope)

    def test_result_surface_supports_activity_and_raw_tool_call_protocols(self) -> None:
        root = REFERENCE_APP_ROOT
        surface = (root / "src" / "components" / "result-surface-host.tsx").read_text(
            encoding="utf-8"
        )
        self.assertIn("a2ui-surface", surface)
        self.assertIn("render_a2ui", surface)
        self.assertIn("operationsFromToolArguments", surface)
        self.assertIn('version: "v0.9"', surface)
        self.assertIn("createSurface", surface)
        self.assertIn("updateComponents", surface)

    def test_authoritative_chat_scope_remains_an_applied_canvas_scope(self) -> None:
        root = REFERENCE_APP_ROOT
        scope = (root / "src" / "components" / "analysis-scope.tsx").read_text(
            encoding="utf-8"
        )
        self.assertIn("controlsMatchShared", scope)
        self.assertIn("lastSubmittedSignature.current", scope)
        self.assertIn("Math.max(analysisReferenceEnd(), synchronizedEnd)", scope)
        self.assertIn("return { range: synchronized, error: null }", scope)


if __name__ == "__main__":
    unittest.main()
