from datetime import datetime, timedelta
import ast
import json
from pathlib import Path
import unittest

from langchain_core.messages import AIMessage, HumanMessage

from src.energy.analysis import EnergyAnalysisService
from src.energy.data import BUSINESS_TIMEZONE, generate_mock_dataset
from src.investigation import (
    advance_analysis_run,
    apply_scope,
    begin_analysis_run,
    complete_analysis_run,
    create_investigation,
    prepare_run_reviews,
)
from src.workflow.contracts import EnergyWorkflowOutput, InsightBundle
from src.workflow.graph import (
    CALCULATE_ANALYSIS,
    GENERATE_INSIGHTS,
    HANDLE_ERROR,
    QUERY_COMPARISON,
    QUERY_TARGET,
    RENDER_RESULT,
    RESOLVE_SCOPE,
    SUCCESS_TRACE,
    UPDATE_INVESTIGATION,
    VALIDATE_SCOPE,
    build_energy_graph,
)
from src.workflow.scope import ScopeResolutionError, resolve_analysis_scope


MONOREPO_ROOT = Path(__file__).resolve().parents[3]
REFERENCE_APP_ROOT = MONOREPO_ROOT / "references" / "energy-agent-next"


REFERENCE_TIME = datetime(2026, 7, 16, tzinfo=BUSINESS_TIMEZONE)
SEED = 20260716
COPILOTKIT_STATE = {
    "actions": [],
    "context": [],
    "intercepted_tool_calls": None,
    "original_ai_message_id": None,
}
VALID_INSIGHTS = {
    "unable_to_conclude": False,
    "inability_reason": None,
    "findings": [
        {
            "finding_id": "finding-period-change",
            "title": "Energy increased versus the previous period",
            "summary": "The target period used more energy and HVAC led the increase.",
            "evidence_refs": ["period_change", "category_change:hvac"],
            "hypothesis": "A possible operational cause requires verification.",
        }
    ],
    "recommendations": [
        {
            "recommendation_id": "recommendation-review-hvac",
            "finding_id": "finding-period-change",
            "title": "Review HVAC operating schedules",
            "description": "Compare HVAC schedules during the identified high-load windows.",
        }
    ],
}


class FakeInsightModel:
    def __init__(self, response=None, error: Exception | None = None) -> None:
        self.response = response if response is not None else VALID_INSIGHTS
        self.error = error
        self.calls: list[dict] = []

    def invoke(self, messages, config=None):
        self.calls.append({"messages": messages, "config": config})
        if self.error:
            raise self.error
        return self.response


class AuthenticationFailure(RuntimeError):
    status_code = 401


class RecordingService:
    def __init__(self, service: EnergyAnalysisService) -> None:
        self._service = service
        self.dataset = service.dataset
        self.calls: list[str] = []

    def validate_analysis_scope(self, building_id, start_at, end_at):
        self.calls.append("validate")
        return self._service.validate_analysis_scope(building_id, start_at, end_at)

    def query_energy_series(self, building_id, start_at, end_at):
        if not any(call == "query_target" for call in self.calls):
            self.calls.append("query_target")
        else:
            self.calls.append("query_comparison")
        return self._service.query_energy_series(building_id, start_at, end_at)

    def calculate_energy_analysis(self, target_series, comparison_series):
        self.calls.append("calculate")
        return self._service.calculate_energy_analysis(target_series, comparison_series)


class WorkflowTestCase(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        dataset = generate_mock_dataset(seed=SEED, reference_time=REFERENCE_TIME)
        cls.service = EnergyAnalysisService(dataset)

    def input_state(
        self,
        *,
        request=None,
        message="Analyze Building B over the last 7 days.",
        investigation=None,
        command=None,
    ):
        state = {
            "messages": [HumanMessage(content=message)],
            "copilotkit": COPILOTKIT_STATE,
        }
        if request is not None:
            state["analysis_request"] = request
        if investigation is not None:
            state["investigation"] = investigation
        if command is not None:
            state["investigation_command"] = command
        return state

    def completed_investigation(self):
        state = create_investigation(investigation_id="inv-test")
        state = apply_scope(
            state,
            building_id="B",
            time_range={
                "start_at": "2026-07-09T00:00:00+08:00",
                "end_at": "2026-07-16T00:00:00+08:00",
                "timezone": "Asia/Shanghai",
            },
            actor="user",
        )
        state, run_id = begin_analysis_run(state, run_id="run-test")
        for status in ("loading_data", "calculating", "generating_insights"):
            state, eligible = advance_analysis_run(
                state,
                run_id=run_id,
                status=status,
            )
            self.assertTrue(eligible)
        state, eligible = prepare_run_reviews(
            state,
            run_id=run_id,
            findings=[
                {
                    "finding_id": "finding-period-change",
                    "title": "Energy increased versus the previous period",
                }
            ],
            recommendations=[
                {
                    "recommendation_id": "recommendation-review-hvac",
                    "title": "Review HVAC operating schedules",
                }
            ],
        )
        self.assertTrue(eligible)
        state, eligible = complete_analysis_run(
            state,
            run_id=run_id,
            result_surface_id="surface-test",
        )
        self.assertTrue(eligible)
        return state


class ScopeResolutionTests(WorkflowTestCase):
    def test_chinese_last_seven_days(self) -> None:
        scope = resolve_analysis_scope(
            request=None,
            messages=[HumanMessage(content="分析 B 栋最近 7 天")],
            dataset=self.service.dataset,
        )
        self.assertEqual(scope["building_id"], "B")
        self.assertEqual(scope["source"], "chat")
        self.assertEqual(
            datetime.fromisoformat(scope["end_at"])
            - datetime.fromisoformat(scope["start_at"]),
            timedelta(days=7),
        )

    def test_english_yesterday_uses_business_calendar_day(self) -> None:
        scope = resolve_analysis_scope(
            request=None,
            messages=[HumanMessage(content="Analyze Building A yesterday")],
            dataset=self.service.dataset,
        )
        start = datetime.fromisoformat(scope["start_at"])
        end = datetime.fromisoformat(scope["end_at"])
        self.assertEqual(start.hour, 0)
        self.assertEqual(end.hour, 0)
        self.assertEqual(end - start, timedelta(days=1))
        self.assertEqual(scope["timezone"], "Asia/Shanghai")
        self.assertEqual(start.utcoffset(), timedelta(hours=8))

    def test_chat_custom_date_range(self) -> None:
        scope = resolve_analysis_scope(
            request=None,
            messages=[
                HumanMessage(
                    content="Analyze Building C from 2026-07-01 to 2026-07-08"
                )
            ],
            dataset=self.service.dataset,
        )
        self.assertEqual(scope["start_at"], "2026-07-01T00:00:00+08:00")
        self.assertEqual(scope["end_at"], "2026-07-08T00:00:00+08:00")

    def test_explicit_canvas_request_takes_precedence(self) -> None:
        scope = resolve_analysis_scope(
            request={
                "building_id": "A",
                "preset": "last_24_hours",
                "source": "canvas",
            },
            messages=[HumanMessage(content="Analyze Building B over the last 7 days")],
            dataset=self.service.dataset,
        )
        self.assertEqual(scope["building_id"], "A")
        self.assertEqual(scope["source"], "canvas")
        self.assertEqual(
            datetime.fromisoformat(scope["end_at"])
            - datetime.fromisoformat(scope["start_at"]),
            timedelta(hours=24),
        )

    def test_incomplete_request_does_not_resolve(self) -> None:
        with self.assertRaises(ScopeResolutionError) as caught:
            resolve_analysis_scope(
                request=None,
                messages=[HumanMessage(content="Analyze Building A")],
                dataset=self.service.dataset,
            )
        self.assertEqual(caught.exception.code, "SCOPE_RESOLUTION_REQUIRED")


class ExplicitGraphTests(WorkflowTestCase):
    def test_success_path_is_fixed_and_model_runs_once(self) -> None:
        recording_service = RecordingService(self.service)
        model = FakeInsightModel()
        graph = build_energy_graph(service=recording_service, insight_model=model)

        output = graph.invoke(self.input_state())

        self.assertEqual(output["workflow_status"], "complete")
        self.assertEqual(output["execution_trace"], SUCCESS_TRACE)
        self.assertEqual(
            recording_service.calls,
            ["validate", "query_target", "query_comparison", "calculate"],
        )
        self.assertEqual(len(model.calls), 1)
        investigation = output["investigation"]
        self.assertEqual(investigation["status"], "complete")
        self.assertEqual(investigation["building_id"], "B")
        self.assertEqual(investigation["time_range"]["timezone"], "Asia/Shanghai")
        self.assertIsNone(investigation["active_run_id"])
        self.assertTrue(investigation["result_surface_id"].startswith("surface_"))
        self.assertEqual(
            investigation["findings"],
            [
                {
                    "finding_id": "finding-period-change",
                    "title": "Energy increased versus the previous period",
                    "review_status": "unreviewed",
                }
            ],
        )
        self.assertEqual(
            investigation["recommendations"],
            [
                {
                    "recommendation_id": "recommendation-review-hvac",
                    "title": "Review HVAC operating schedules",
                    "bookmarked": False,
                }
            ],
        )
        for internal_key in (
            "resolved_scope",
            "validation",
            "analysis",
            "insights",
            "result_surface",
            "target_series",
            "comparison_series",
            "analysis_result",
            "current_run_id",
        ):
            self.assertNotIn(internal_key, output)
        self.assertIsInstance(output["messages"][-1], AIMessage)

    def test_lifecycle_state_is_emitted_by_each_graph_stage(self) -> None:
        graph = build_energy_graph(
            service=self.service,
            insight_model=FakeInsightModel(),
            id_factory=lambda prefix: f"{prefix}-stream",
        )
        updates = list(graph.stream(self.input_state(), stream_mode="updates"))
        statuses = [
            update[node]["investigation"]["status"]
            for update in updates
            for node in update
            if "investigation" in update[node]
        ]
        self.assertEqual(
            statuses,
            [
                "ready",
                "validating",
                "loading_data",
                "loading_data",
                "calculating",
                "generating_insights",
                "generating_insights",
                "complete",
            ],
        )
        final_investigation = updates[-1][RENDER_RESULT]["investigation"]
        self.assertEqual(final_investigation["result_surface_id"], "surface-stream")
        self.assertIsNone(final_investigation["active_run_id"])

    def test_mutation_only_scope_update_invalidates_without_analysis(self) -> None:
        model = FakeInsightModel()
        graph = build_energy_graph(service=self.service, insight_model=model)
        output = graph.invoke(
            self.input_state(
                investigation=self.completed_investigation(),
                command={
                    "type": "set_scope",
                    "building_id": "C",
                    "time_range": {
                        "start_at": "2026-07-08T00:00:00+08:00",
                        "end_at": "2026-07-15T00:00:00+08:00",
                        "timezone": "Asia/Shanghai",
                    },
                },
            )
        )
        investigation = output["investigation"]
        self.assertEqual(output["workflow_status"], "mutation_applied")
        self.assertEqual(output["execution_trace"], [RESOLVE_SCOPE])
        self.assertEqual(investigation["status"], "ready")
        self.assertEqual(investigation["building_id"], "C")
        self.assertIsNone(investigation["result_surface_id"])
        self.assertEqual(investigation["findings"], [])
        self.assertEqual(investigation["recommendations"], [])
        self.assertEqual(model.calls, [])

    def test_mutation_only_review_and_bookmark_preserve_result_identity(self) -> None:
        graph = build_energy_graph(service=self.service, insight_model=FakeInsightModel())
        completed = self.completed_investigation()
        reviewed = graph.invoke(
            self.input_state(
                investigation=completed,
                command={
                    "type": "review_finding",
                    "finding_id": "finding-period-change",
                    "review_status": "confirmed",
                },
            )
        )["investigation"]
        bookmarked = graph.invoke(
            self.input_state(
                investigation=reviewed,
                command={
                    "type": "bookmark_recommendation",
                    "recommendation_id": "recommendation-review-hvac",
                    "bookmarked": True,
                },
            )
        )["investigation"]
        self.assertEqual(reviewed["status"], "complete")
        self.assertEqual(reviewed["result_surface_id"], "surface-test")
        self.assertEqual(reviewed["findings"][0]["review_status"], "confirmed")
        self.assertTrue(bookmarked["recommendations"][0]["bookmarked"])
        self.assertEqual(bookmarked["last_updated_by"], "user")

    def test_output_schema_exposes_only_shared_investigation_state(self) -> None:
        self.assertEqual(
            set(EnergyWorkflowOutput.__annotations__),
            {
                "messages",
                "copilotkit",
                "workflow_status",
                "investigation",
                "error",
                "execution_trace",
            },
        )
        for forbidden in (
            "analysis",
            "insights",
            "result_surface",
            "resolved_scope",
            "validation",
            "target_series",
            "comparison_series",
        ):
            self.assertNotIn(forbidden, EnergyWorkflowOutput.__annotations__)

    @unittest.skipUnless(
        (REFERENCE_APP_ROOT / "src" / "app").exists(),
        "The standalone EnergyAgent Next.js provider is replaced by the HVAC Web host UI.",
    )
    def test_frontend_subscribes_to_shared_state_updates(self) -> None:
        provider = (
            REFERENCE_APP_ROOT
            / "src"
            / "components"
            / "energy-agent-provider.tsx"
        ).read_text(encoding="utf-8")
        self.assertIn("useAgent", provider)
        self.assertIn("UseAgentUpdate.OnStateChanged", provider)
        self.assertIn("UseAgentUpdate.OnRunStatusChanged", provider)
        self.assertIn("agent.state", provider)
        self.assertIn("graphState.investigation", provider)

    def test_only_evidence_and_aggregates_are_sent_to_model(self) -> None:
        model = FakeInsightModel()
        graph = build_energy_graph(service=self.service, insight_model=model)
        graph.invoke(self.input_state())

        call = model.calls[0]
        payload = json.loads(call["messages"][1].content)
        self.assertEqual(
            set(payload),
            {
                "building_id",
                "target_period",
                "comparison_period",
                "metrics",
                "categories",
                "evidence",
            },
        )
        serialized = call["messages"][1].content
        self.assertNotIn("hvac_kw", serialized)
        self.assertNotIn("power_equipment_kw", serialized)
        self.assertNotIn("trend", payload)
        self.assertFalse(
            call["config"]["metadata"]["copilotkit:emit-messages"]
        )
        self.assertFalse(
            call["config"]["metadata"]["copilotkit:emit-tool-calls"]
        )

    def test_invalid_building_stops_before_query_and_model(self) -> None:
        recording_service = RecordingService(self.service)
        model = FakeInsightModel()
        graph = build_energy_graph(service=recording_service, insight_model=model)
        output = graph.invoke(
            self.input_state(
                request={
                    "building_id": "Z",
                    "preset": "last_7_days",
                    "source": "canvas",
                }
            )
        )

        self.assertEqual(output["workflow_status"], "error")
        self.assertEqual(output["error"]["code"], "BUILDING_NOT_FOUND")
        self.assertEqual(
            output["execution_trace"],
            [RESOLVE_SCOPE, VALIDATE_SCOPE, HANDLE_ERROR],
        )
        self.assertEqual(recording_service.calls, ["validate"])
        self.assertEqual(model.calls, [])

    def test_incomplete_chat_request_stops_at_resolution(self) -> None:
        model = FakeInsightModel()
        graph = build_energy_graph(service=self.service, insight_model=model)
        output = graph.invoke(self.input_state(message="Analyze Building A"))

        self.assertEqual(output["error"]["code"], "SCOPE_RESOLUTION_REQUIRED")
        self.assertEqual(
            output["execution_trace"],
            [RESOLVE_SCOPE, HANDLE_ERROR],
        )
        self.assertEqual(model.calls, [])
        self.assertIn("Corrective action", output["messages"][-1].content)

    def test_unknown_evidence_reference_is_a_model_request_failure(self) -> None:
        response = json.loads(json.dumps(VALID_INSIGHTS))
        response["findings"][0]["evidence_refs"] = ["invented_evidence"]
        model = FakeInsightModel(response=response)
        graph = build_energy_graph(service=self.service, insight_model=model)
        output = graph.invoke(self.input_state())

        self.assertEqual(output["error"]["code"], "MODEL_REQUEST_FAILED")
        self.assertEqual(
            output["execution_trace"],
            [
                RESOLVE_SCOPE,
                VALIDATE_SCOPE,
                QUERY_TARGET,
                QUERY_COMPARISON,
                CALCULATE_ANALYSIS,
                GENERATE_INSIGHTS,
                HANDLE_ERROR,
            ],
        )
        self.assertNotIn(UPDATE_INVESTIGATION, output["execution_trace"])
        self.assertNotIn(RENDER_RESULT, output["execution_trace"])

    def test_model_authentication_failure_has_stable_code(self) -> None:
        model = FakeInsightModel(error=AuthenticationFailure("unauthorized"))
        graph = build_energy_graph(service=self.service, insight_model=model)
        output = graph.invoke(self.input_state())
        self.assertEqual(output["error"]["code"], "MODEL_AUTHENTICATION_FAILED")
        self.assertEqual(output["error"]["stage"], GENERATE_INSIGHTS)

    def test_graph_declares_only_the_fixed_nodes(self) -> None:
        graph = build_energy_graph(service=self.service, insight_model=FakeInsightModel())
        nodes = set(graph.get_graph().nodes)
        self.assertEqual(
            nodes,
            {
                "__start__",
                "__end__",
                RESOLVE_SCOPE,
                VALIDATE_SCOPE,
                QUERY_TARGET,
                QUERY_COMPARISON,
                CALCULATE_ANALYSIS,
                GENERATE_INSIGHTS,
                UPDATE_INVESTIGATION,
                RENDER_RESULT,
                HANDLE_ERROR,
            },
        )

    def test_main_exports_the_explicit_graph_without_create_agent(self) -> None:
        source = (Path(__file__).parents[1] / "main.py").read_text(encoding="utf-8")
        self.assertIn("graph = build_energy_graph()", source)
        self.assertNotIn("create_agent", source)

    def test_model_invoke_exists_only_in_interpretation_node(self) -> None:
        source_path = Path(__file__).parents[1] / "src" / "workflow" / "graph.py"
        tree = ast.parse(source_path.read_text(encoding="utf-8"))
        invoke_functions: list[str] = []

        class Visitor(ast.NodeVisitor):
            def __init__(self) -> None:
                self.function_stack: list[str] = []

            def visit_FunctionDef(self, node: ast.FunctionDef) -> None:
                self.function_stack.append(node.name)
                self.generic_visit(node)
                self.function_stack.pop()

            def visit_Call(self, node: ast.Call) -> None:
                if isinstance(node.func, ast.Attribute) and node.func.attr == "invoke":
                    invoke_functions.append(self.function_stack[-1])
                self.generic_visit(node)

        Visitor().visit(tree)
        self.assertEqual(invoke_functions, ["generate_insights_node"])


class InsightContractTests(unittest.TestCase):
    def test_recommendations_must_map_to_findings(self) -> None:
        invalid = json.loads(json.dumps(VALID_INSIGHTS))
        invalid["recommendations"][0]["finding_id"] = "missing-finding"
        with self.assertRaises(ValueError):
            InsightBundle.model_validate(invalid)

    def test_at_most_three_findings(self) -> None:
        invalid = json.loads(json.dumps(VALID_INSIGHTS))
        invalid["findings"] = invalid["findings"] * 4
        with self.assertRaises(ValueError):
            InsightBundle.model_validate(invalid)


if __name__ == "__main__":
    unittest.main()
