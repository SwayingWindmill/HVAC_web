from __future__ import annotations

from contextlib import nullcontext
from copy import deepcopy
from dataclasses import replace
from datetime import datetime, timedelta
import unittest
from unittest.mock import patch

from langchain_core.messages import HumanMessage

from src.energy.analysis import EnergyAnalysisService
from src.energy.data import BUSINESS_TIMEZONE, generate_mock_dataset
from src.investigation import (
    advance_analysis_run,
    apply_scope,
    begin_analysis_run,
    create_investigation,
)
from src.workflow.contracts import serialize_analysis_result, serialize_validation
from src.workflow.graph import (
    CALCULATE_ANALYSIS,
    GENERATE_INSIGHTS,
    HANDLE_ERROR,
    QUERY_COMPARISON,
    QUERY_TARGET,
    RENDER_RESULT,
    RESOLVE_SCOPE,
    UPDATE_INVESTIGATION,
    VALIDATE_SCOPE,
    build_energy_graph,
)
from tests.test_workflow import (
    COPILOTKIT_STATE,
    AuthenticationFailure,
    FakeInsightModel,
    VALID_INSIGHTS,
)


REFERENCE_TIME = datetime(2026, 7, 16, tzinfo=BUSINESS_TIMEZONE)
SEED = 20260716


class SequenceIdFactory:
    def __init__(self) -> None:
        self.counts: dict[str, int] = {}

    def __call__(self, prefix: str) -> str:
        current = self.counts.get(prefix, 0) + 1
        self.counts[prefix] = current
        return f"{prefix}-{current}"


class FaultInjectingService:
    def __init__(self, base: EnergyAnalysisService, fault: str) -> None:
        self.base = base
        self.dataset = base.dataset
        self.fault = fault
        self.query_count = 0

    def validate_analysis_scope(self, building_id, start_at, end_at):
        return self.base.validate_analysis_scope(building_id, start_at, end_at)

    def query_energy_series(self, building_id, start_at, end_at):
        self.query_count += 1
        series = self.base.query_energy_series(building_id, start_at, end_at)
        if self.fault == "incomplete_target" and self.query_count == 1:
            return series[:-1]
        if self.fault == "incomplete_comparison" and self.query_count == 2:
            return series[:-1]
        if self.fault == "invalid_measurement" and self.query_count == 1:
            corrupted = list(series)
            corrupted[0] = replace(corrupted[0], hvac_kw=-1.0)
            return tuple(corrupted)
        return series

    def calculate_energy_analysis(self, target_series, comparison_series):
        return self.base.calculate_energy_analysis(target_series, comparison_series)


class AnalysisFailurePathTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        dataset = generate_mock_dataset(seed=SEED, reference_time=REFERENCE_TIME)
        cls.base_service = EnergyAnalysisService(dataset)
        cls.dataset = dataset

    def invoke(self, *, request: dict, fault: str | None = None):
        service = (
            FaultInjectingService(self.base_service, fault)
            if fault
            else self.base_service
        )
        model = FakeInsightModel()
        graph = build_energy_graph(
            service=service,
            insight_model=model,
            id_factory=SequenceIdFactory(),
        )
        output = graph.invoke(
            {
                "messages": [HumanMessage(content="Run the requested analysis.")],
                "copilotkit": COPILOTKIT_STATE,
                "analysis_request": request,
            }
        )
        return output, model

    def assert_no_partial_result(self, output: dict, expected_code: str) -> None:
        self.assertEqual(output["workflow_status"], "error")
        self.assertEqual(output["error"]["code"], expected_code)
        self.assertTrue(output["error"]["cause"])
        self.assertTrue(output["error"]["action"])
        investigation = output["investigation"]
        self.assertEqual(investigation["status"], "error")
        self.assertIsNotNone(investigation["building_id"])
        self.assertIsNotNone(investigation["time_range"])
        self.assertIsNone(investigation["active_run_id"])
        self.assertIsNone(investigation["result_surface_id"])
        self.assertEqual(investigation["findings"], [])
        self.assertEqual(investigation["recommendations"], [])
        self.assertIn(expected_code, investigation["validation_error"])
        self.assertNotIn("analysis", output)
        self.assertNotIn("insights", output)
        self.assertNotIn("result_surface", output)
        for message in output.get("messages", []):
            self.assertFalse(
                any(
                    call.get("name") == "render_a2ui"
                    for call in getattr(message, "tool_calls", [])
                )
            )

    def test_all_required_analysis_error_codes_have_no_partial_result(self) -> None:
        end = self.dataset.end_at
        valid_start = end - timedelta(days=1)
        cases = [
            (
                "BUILDING_NOT_FOUND",
                {
                    "building_id": "Z",
                    "start_at": valid_start.isoformat(),
                    "end_at": end.isoformat(),
                    "source": "canvas",
                },
                None,
                VALIDATE_SCOPE,
            ),
            (
                "INVALID_TIME_RANGE",
                {
                    "building_id": "A",
                    "start_at": end.isoformat(),
                    "end_at": end.isoformat(),
                    "source": "canvas",
                },
                None,
                VALIDATE_SCOPE,
            ),
            (
                "TARGET_OUT_OF_RANGE",
                {
                    "building_id": "A",
                    "start_at": valid_start.isoformat(),
                    "end_at": (end + timedelta(minutes=15)).isoformat(),
                    "source": "canvas",
                },
                None,
                VALIDATE_SCOPE,
            ),
            (
                "COMPARISON_OUT_OF_RANGE",
                {
                    "building_id": "A",
                    "start_at": (self.dataset.start_at + timedelta(days=1)).isoformat(),
                    "end_at": (self.dataset.start_at + timedelta(days=3)).isoformat(),
                    "source": "canvas",
                },
                None,
                VALIDATE_SCOPE,
            ),
            (
                "INCOMPLETE_TARGET_DATA",
                {
                    "building_id": "A",
                    "start_at": valid_start.isoformat(),
                    "end_at": end.isoformat(),
                    "source": "canvas",
                },
                "incomplete_target",
                CALCULATE_ANALYSIS,
            ),
            (
                "INCOMPLETE_COMPARISON_DATA",
                {
                    "building_id": "A",
                    "start_at": valid_start.isoformat(),
                    "end_at": end.isoformat(),
                    "source": "canvas",
                },
                "incomplete_comparison",
                CALCULATE_ANALYSIS,
            ),
            (
                "INVALID_MEASUREMENT",
                {
                    "building_id": "A",
                    "start_at": valid_start.isoformat(),
                    "end_at": end.isoformat(),
                    "source": "canvas",
                },
                "invalid_measurement",
                CALCULATE_ANALYSIS,
            ),
        ]

        for expected_code, request, fault, expected_stage in cases:
            with self.subTest(expected_code=expected_code):
                output, model = self.invoke(request=request, fault=fault)
                self.assert_no_partial_result(output, expected_code)
                self.assertEqual(output["error"]["stage"], expected_stage)
                self.assertEqual(output["execution_trace"][-1], HANDLE_ERROR)
                self.assertEqual(model.calls, [])

    def test_post_calculation_failures_also_publish_no_partial_result(self) -> None:
        request = {
            "building_id": "B",
            "start_at": (self.dataset.end_at - timedelta(days=1)).isoformat(),
            "end_at": self.dataset.end_at.isoformat(),
            "source": "canvas",
        }
        invalid_evidence = deepcopy(VALID_INSIGHTS)
        invalid_evidence["findings"][0]["evidence_refs"] = ["not-real"]
        unable = {
            "unable_to_conclude": True,
            "inability_reason": "The available Evidence is insufficient.",
            "findings": [],
            "recommendations": [],
        }
        cases = [
            (
                "MODEL_AUTHENTICATION_FAILED",
                FakeInsightModel(error=AuthenticationFailure("unauthorized")),
                None,
            ),
            (
                "MODEL_REQUEST_FAILED",
                FakeInsightModel(response=invalid_evidence),
                None,
            ),
            (
                "INSUFFICIENT_EVIDENCE",
                FakeInsightModel(response=unable),
                None,
            ),
            (
                "A2UI_SCHEMA_INVALID",
                FakeInsightModel(),
                ValueError("invalid surface"),
            ),
        ]

        for expected_code, model, render_error in cases:
            with self.subTest(expected_code=expected_code):
                graph = build_energy_graph(
                    service=self.base_service,
                    insight_model=model,
                    id_factory=SequenceIdFactory(),
                )
                context = (
                    patch(
                        "src.workflow.graph.build_energy_a2ui_surface",
                        side_effect=render_error,
                    )
                    if render_error
                    else nullcontext()
                )
                with context:
                    output = graph.invoke(
                        {
                            "messages": [],
                            "copilotkit": COPILOTKIT_STATE,
                            "analysis_request": request,
                        }
                    )
                self.assert_no_partial_result(output, expected_code)
                self.assertEqual(output["execution_trace"][-1], HANDLE_ERROR)
                self.assertEqual(len(model.calls), 1)


class StaleRunWorkflowTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        dataset = generate_mock_dataset(seed=SEED, reference_time=REFERENCE_TIME)
        cls.service = EnergyAnalysisService(dataset)
        cls.dataset = dataset
        cls.model = FakeInsightModel()
        cls.graph = build_energy_graph(
            service=cls.service,
            insight_model=cls.model,
            id_factory=SequenceIdFactory(),
        )
        cls.start_at = dataset.end_at - timedelta(days=1)
        cls.end_at = dataset.end_at
        state = create_investigation(investigation_id="inv-stale")
        state = apply_scope(
            state,
            building_id="B",
            time_range={
                "start_at": cls.start_at.isoformat(),
                "end_at": cls.end_at.isoformat(),
                "timezone": "Asia/Shanghai",
            },
            actor="user",
        )
        cls.current_investigation, cls.current_run_id = begin_analysis_run(
            state,
            run_id="run-current",
        )
        validation = cls.service.validate_analysis_scope("B", cls.start_at, cls.end_at)
        cls.validation = serialize_validation(validation)
        target = cls.service.query_energy_series("B", cls.start_at, cls.end_at)
        comparison = cls.service.query_energy_series(
            "B",
            validation.comparison_start_at,
            validation.comparison_end_at,
        )
        cls.analysis_result = cls.service.calculate_energy_analysis(target, comparison)
        cls.analysis = serialize_analysis_result(cls.analysis_result)

    def stale_state(self, **updates):
        state = {
            "messages": [],
            "copilotkit": COPILOTKIT_STATE,
            "investigation": deepcopy(self.current_investigation),
            "current_run_id": "run-stale",
            "execution_trace": [],
            **updates,
        }
        return state

    def assert_discarded(self, output: dict, node: str) -> None:
        self.assertEqual(output["workflow_status"], "discarded")
        self.assertTrue(output["stale_run_discarded"])
        self.assertEqual(output["execution_trace"], [node])
        self.assertEqual(output["investigation"], self.current_investigation)
        self.assertEqual(output["investigation"]["active_run_id"], self.current_run_id)
        self.assertIsNone(output["investigation"]["result_surface_id"])
        self.assertIsNone(output["error"])

    def test_stale_query_calculate_and_model_nodes_stop_before_work(self) -> None:
        with patch.object(
            self.service,
            "query_energy_series",
            side_effect=AssertionError("stale query must not execute"),
        ):
            self.assert_discarded(
                self.graph.nodes[QUERY_TARGET].invoke(
                    self.stale_state(validation=self.validation)
                ),
                QUERY_TARGET,
            )
            self.assert_discarded(
                self.graph.nodes[QUERY_COMPARISON].invoke(
                    self.stale_state(validation=self.validation)
                ),
                QUERY_COMPARISON,
            )

        with patch.object(
            self.service,
            "calculate_energy_analysis",
            side_effect=AssertionError("stale calculation must not execute"),
        ):
            self.assert_discarded(
                self.graph.nodes[CALCULATE_ANALYSIS].invoke(
                    self.stale_state(target_series=(), comparison_series=())
                ),
                CALCULATE_ANALYSIS,
            )

        before_calls = len(self.model.calls)
        self.assert_discarded(
            self.graph.nodes[GENERATE_INSIGHTS].invoke(
                self.stale_state(analysis=self.analysis)
            ),
            GENERATE_INSIGHTS,
        )
        self.assertEqual(len(self.model.calls), before_calls)

    def test_stale_unable_to_conclude_is_discarded_before_error(self) -> None:
        output = self.graph.nodes[UPDATE_INVESTIGATION].invoke(
            self.stale_state(
                insights={
                    "unable_to_conclude": True,
                    "inability_reason": "Old run had insufficient Evidence.",
                    "findings": [],
                    "recommendations": [],
                }
            )
        )
        self.assert_discarded(output, UPDATE_INVESTIGATION)

    def test_stale_render_never_builds_or_replaces_surface(self) -> None:
        with patch(
            "src.workflow.graph.build_energy_a2ui_surface",
            side_effect=AssertionError("stale render must not build a surface"),
        ):
            output = self.graph.nodes[RENDER_RESULT].invoke(
                self.stale_state(analysis=self.analysis, insights=VALID_INSIGHTS)
            )
        self.assert_discarded(output, RENDER_RESULT)

    def test_scope_change_revokes_the_previous_run_immediately(self) -> None:
        invalidated = apply_scope(
            self.current_investigation,
            building_id="A",
            time_range={
                "start_at": self.start_at.isoformat(),
                "end_at": self.end_at.isoformat(),
                "timezone": "Asia/Shanghai",
            },
            actor="user",
        )
        output = self.graph.nodes[QUERY_TARGET].invoke(
            {
                "messages": [],
                "copilotkit": COPILOTKIT_STATE,
                "investigation": invalidated,
                "current_run_id": self.current_run_id,
                "validation": self.validation,
                "execution_trace": [],
            }
        )
        self.assertEqual(output["workflow_status"], "discarded")
        self.assertEqual(output["investigation"]["status"], "ready")
        self.assertEqual(output["investigation"]["building_id"], "A")
        self.assertIsNone(output["investigation"]["active_run_id"])


class RerunWorkflowTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        dataset = generate_mock_dataset(seed=SEED, reference_time=REFERENCE_TIME)
        cls.service = EnergyAnalysisService(dataset)
        cls.dataset = dataset
        cls.start_at = dataset.end_at - timedelta(days=7)
        cls.end_at = dataset.end_at

    def request(self, building_id: str) -> dict:
        return {
            "building_id": building_id,
            "start_at": self.start_at.isoformat(),
            "end_at": self.end_at.isoformat(),
            "source": "canvas",
        }

    def invoke(self, graph, *, investigation=None, request=None, command=None, messages=None):
        state = {
            "messages": messages or [],
            "copilotkit": COPILOTKIT_STATE,
        }
        if investigation is not None:
            state["investigation"] = investigation
        if request is not None:
            state["analysis_request"] = request
        if command is not None:
            state["investigation_command"] = command
        return graph.invoke(state)

    def test_rerun_replaces_run_and_surface_and_resets_review_state(self) -> None:
        ids = SequenceIdFactory()
        model = FakeInsightModel()
        graph = build_energy_graph(
            service=self.service,
            insight_model=model,
            id_factory=ids,
        )
        first = self.invoke(graph, request=self.request("B"))
        first_surface = first["investigation"]["result_surface_id"]
        first_render_call = first["messages"][-1].tool_calls[0]

        finding_id = first["investigation"]["findings"][0]["finding_id"]
        recommendation_id = first["investigation"]["recommendations"][0][
            "recommendation_id"
        ]
        reviewed = self.invoke(
            graph,
            investigation=first["investigation"],
            messages=first["messages"],
            command={
                "type": "review_finding",
                "finding_id": finding_id,
                "review_status": "confirmed",
            },
        )
        bookmarked = self.invoke(
            graph,
            investigation=reviewed["investigation"],
            messages=reviewed["messages"],
            command={
                "type": "bookmark_recommendation",
                "recommendation_id": recommendation_id,
                "bookmarked": True,
            },
        )

        rerun = self.invoke(
            graph,
            investigation=bookmarked["investigation"],
            messages=bookmarked["messages"],
            request=self.request("B"),
        )
        second_surface = rerun["investigation"]["result_surface_id"]
        second_render_call = rerun["messages"][-1].tool_calls[0]

        self.assertEqual(rerun["workflow_status"], "complete")
        self.assertNotEqual(second_surface, first_surface)
        self.assertNotEqual(second_render_call["id"], first_render_call["id"])
        self.assertEqual(
            rerun["investigation"]["findings"][0]["review_status"],
            "unreviewed",
        )
        self.assertFalse(rerun["investigation"]["recommendations"][0]["bookmarked"])
        self.assertEqual(len(model.calls), 2)

    def test_error_scope_can_be_corrected_and_rerun_successfully(self) -> None:
        ids = SequenceIdFactory()
        model = FakeInsightModel()
        graph = build_energy_graph(
            service=self.service,
            insight_model=model,
            id_factory=ids,
        )
        failed = self.invoke(
            graph,
            request={
                "building_id": "A",
                "start_at": (self.dataset.end_at - timedelta(days=1)).isoformat(),
                "end_at": (self.dataset.end_at + timedelta(minutes=15)).isoformat(),
                "source": "canvas",
            },
        )
        self.assertEqual(failed["investigation"]["status"], "error")
        self.assertEqual(failed["error"]["code"], "TARGET_OUT_OF_RANGE")

        recovered = self.invoke(
            graph,
            investigation=failed["investigation"],
            messages=failed["messages"],
            request=self.request("A"),
        )
        self.assertEqual(recovered["workflow_status"], "complete")
        self.assertEqual(recovered["investigation"]["status"], "complete")
        self.assertIsNone(recovered["investigation"]["validation_error"])
        self.assertIsNotNone(recovered["investigation"]["result_surface_id"])
        root = recovered["messages"][-1].tool_calls[0]["args"]["components"][0]
        direction = next(
            component
            for component in recovered["messages"][-1].tool_calls[0]["args"][
                "components"
            ]
            if component["id"] == "status-direction"
        )
        self.assertIn("Building A", root["title"])
        self.assertEqual(direction["label"], "Down")
        self.assertEqual(len(model.calls), 1)

    def test_active_run_cannot_be_restarted_without_invalidation(self) -> None:
        state = create_investigation(investigation_id="inv-active-rerun")
        state = apply_scope(
            state,
            building_id="C",
            time_range={
                "start_at": self.start_at.isoformat(),
                "end_at": self.end_at.isoformat(),
                "timezone": "Asia/Shanghai",
            },
            actor="user",
        )
        state, _ = begin_analysis_run(state, run_id="run-active")
        state, eligible = advance_analysis_run(
            state,
            run_id="run-active",
            status="loading_data",
        )
        self.assertTrue(eligible)
        with self.assertRaisesRegex(ValueError, "INVALID_STATE_TRANSITION"):
            begin_analysis_run(state, run_id="run-overlap")


if __name__ == "__main__":
    unittest.main()
