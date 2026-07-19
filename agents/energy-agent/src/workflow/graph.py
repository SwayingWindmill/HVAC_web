"""Explicit LangGraph workflow for deterministic EnergyAgent analysis."""

from __future__ import annotations

import json
from typing import Any, Callable, Literal

from langchain_core.messages import AIMessage, HumanMessage, SystemMessage
from langchain_openai import ChatOpenAI
from langgraph.graph import END, START, StateGraph
from pydantic import ValidationError

from src.a2ui import RENDER_A2UI_TOOL_NAME, build_energy_a2ui_surface
from src.energy.analysis import AnalysisError, EnergyAnalysisService
from src.investigation import (
    InvestigationState,
    InvestigationStateError,
    advance_analysis_run,
    apply_investigation_command,
    apply_scope,
    begin_analysis_run,
    complete_analysis_run,
    fail_analysis_run,
    is_run_write_eligible,
    new_identifier,
    normalize_investigation,
    prepare_run_reviews,
)
from src.workflow.contracts import (
    EnergyWorkflowInput,
    EnergyWorkflowOutput,
    EnergyWorkflowState,
    InsightBundle,
    WorkflowError,
    parse_iso_datetime,
    serialize_analysis_result,
    serialize_validation,
)
from src.workflow.scope import ScopeResolutionError, resolve_analysis_scope


RESOLVE_SCOPE = "resolve_scope"
VALIDATE_SCOPE = "validate_scope"
QUERY_TARGET = "query_target"
QUERY_COMPARISON = "query_comparison"
CALCULATE_ANALYSIS = "calculate_analysis"
GENERATE_INSIGHTS = "generate_insights"
UPDATE_INVESTIGATION = "update_investigation"
RENDER_RESULT = "render_result"
HANDLE_ERROR = "handle_error"

SUCCESS_TRACE = [
    RESOLVE_SCOPE,
    VALIDATE_SCOPE,
    QUERY_TARGET,
    QUERY_COMPARISON,
    CALCULATE_ANALYSIS,
    GENERATE_INSIGHTS,
    UPDATE_INVESTIGATION,
    RENDER_RESULT,
]

INSIGHT_SYSTEM_PROMPT = """
You are the interpretation stage of a fixed building-energy analysis workflow.
Quantitative calculations are already complete and authoritative.

Rules:
- Use only the supplied metrics and Evidence catalog.
- Never calculate or revise metrics.
- Never invent equipment, weather, events, occupancy changes, or factual causes.
- Every Finding must cite one or more exact Evidence keys in evidence_refs.
- Put possible causes only in the hypothesis field; they require verification.
- Every Recommendation must reference one returned finding_id.
- Return at most three Findings and three Recommendations.
- Prefer an explicit unable_to_conclude response when Evidence is insufficient.
""".strip()


Route = Literal["continue", "error", "stop"]


def _error(
    *,
    code: str,
    cause: str,
    action: str,
    stage: str,
) -> WorkflowError:
    return {
        "code": code,
        "cause": cause,
        "action": action,
        "stage": stage,
    }


def _route_after_node(state: EnergyWorkflowState) -> Route:
    if state.get("error"):
        return "error"
    if state.get("mutation_only") or state.get("stale_run_discarded"):
        return "stop"
    return "continue"


def _trace(
    state: EnergyWorkflowState,
    node: str,
    *,
    reset: bool = False,
) -> list[str]:
    return [node] if reset else [*state.get("execution_trace", []), node]


def _required_state_error(stage: str, field: str) -> WorkflowError:
    return _error(
        code="WORKFLOW_STATE_ERROR",
        cause=f"Workflow stage {stage} is missing required state field {field}.",
        action="Restart the analysis from the resolve_scope stage.",
        stage=stage,
    )


def _state_error(error: InvestigationStateError, stage: str) -> WorkflowError:
    return _error(
        code=error.code,
        cause=error.cause,
        action=error.action,
        stage=stage,
    )


def _model_error(error: Exception) -> WorkflowError:
    status_code = getattr(error, "status_code", None)
    response = getattr(error, "response", None)
    if status_code is None and response is not None:
        status_code = getattr(response, "status_code", None)
    authentication_failure = (
        status_code in {401, 403} or "auth" in type(error).__name__.lower()
    )
    if authentication_failure:
        return _error(
            code="MODEL_AUTHENTICATION_FAILED",
            cause="The Findings-and-Recommendations model rejected the configured credentials.",
            action="Verify OPENAI_API_KEY and restart the Agent process.",
            stage=GENERATE_INSIGHTS,
        )
    return _error(
        code="MODEL_REQUEST_FAILED",
        cause="The Findings-and-Recommendations model request failed or returned an invalid response.",
        action="Verify OPENAI_MODEL and provider availability, then rerun the analysis.",
        stage=GENERATE_INSIGHTS,
    )


def _build_default_insight_model() -> Any:
    from src.config import settings

    model = ChatOpenAI(
        model=settings.model_name,
        model_kwargs={"parallel_tool_calls": False},
    )
    return model.with_structured_output(InsightBundle)


def _validate_evidence_references(
    bundle: InsightBundle,
    evidence_catalog: dict[str, Any],
) -> None:
    allowed = set(evidence_catalog)
    for finding in bundle.findings:
        unknown = set(finding.evidence_refs) - allowed
        if unknown:
            raise ValueError(
                f"Finding {finding.finding_id!r} references unknown Evidence keys: {sorted(unknown)}"
            )


def _run_investigation(
    state: EnergyWorkflowState,
) -> tuple[InvestigationState | None, str | None]:
    return state.get("investigation"), state.get("current_run_id")


def _stale_update(
    state: EnergyWorkflowState,
    node: str,
    investigation: InvestigationState,
) -> dict[str, Any]:
    return {
        "workflow_status": "discarded",
        "investigation": investigation,
        "stale_run_discarded": True,
        "target_series": (),
        "comparison_series": (),
        "analysis_result": None,
        "analysis_request": None,
        "investigation_command": None,
        "error": None,
        "execution_trace": _trace(state, node),
    }


def build_energy_graph(
    *,
    service: EnergyAnalysisService | None = None,
    insight_model: Any | None = None,
    id_factory: Callable[[str], str] = new_identifier,
):
    """Compile the fixed EnergyAgent graph with injectable dependencies."""

    if service is None:
        from src.energy.runtime import energy_service

        service = energy_service
    model = insight_model or _build_default_insight_model()

    def resolve_scope_node(state: EnergyWorkflowState) -> dict[str, Any]:
        try:
            investigation = normalize_investigation(
                state.get("investigation"),
                id_factory=id_factory,
            )
        except InvestigationStateError as error:
            investigation = normalize_investigation(None, id_factory=id_factory)
            return {
                "workflow_status": "error",
                "investigation": investigation,
                "error": _state_error(error, RESOLVE_SCOPE),
                "mutation_only": False,
                "stale_run_discarded": False,
                "current_run_id": None,
                "analysis_request": None,
                "investigation_command": None,
                "execution_trace": _trace(state, RESOLVE_SCOPE, reset=True),
            }

        command = state.get("investigation_command")
        request = state.get("analysis_request")
        if command:
            try:
                investigation = apply_investigation_command(investigation, command)
            except InvestigationStateError as error:
                return {
                    "workflow_status": "error",
                    "investigation": investigation,
                    "error": _state_error(error, RESOLVE_SCOPE),
                    "mutation_only": False,
                    "stale_run_discarded": False,
                    "current_run_id": None,
                    "analysis_request": None,
                    "investigation_command": None,
                    "execution_trace": _trace(state, RESOLVE_SCOPE, reset=True),
                }
            if not request:
                return {
                    "workflow_status": "mutation_applied",
                    "investigation": investigation,
                    "mutation_only": True,
                    "stale_run_discarded": False,
                    "current_run_id": None,
                    "analysis_request": None,
                    "investigation_command": None,
                    "error": None,
                    "execution_trace": _trace(state, RESOLVE_SCOPE, reset=True),
                }

        try:
            scope = resolve_analysis_scope(
                request=request,
                messages=state.get("messages", []),
                dataset=service.dataset,
            )
            investigation = apply_scope(
                investigation,
                building_id=scope["building_id"],
                time_range={
                    "start_at": scope["start_at"],
                    "end_at": scope["end_at"],
                    "timezone": scope["timezone"],
                },
                actor="user" if scope["source"] in {"canvas", "chat"} else "agent",
            )
            return {
                "workflow_status": "resolving",
                "investigation": investigation,
                "resolved_scope": scope,
                "validation": {},
                "analysis": {},
                "insights": {},
                "result_surface": {},
                "target_series": (),
                "comparison_series": (),
                "analysis_result": None,
                "current_run_id": None,
                "mutation_only": False,
                "stale_run_discarded": False,
                "analysis_request": None,
                "investigation_command": None,
                "error": None,
                "execution_trace": _trace(state, RESOLVE_SCOPE, reset=True),
            }
        except (ScopeResolutionError, InvestigationStateError) as error:
            workflow_error = (
                _state_error(error, RESOLVE_SCOPE)
                if isinstance(error, InvestigationStateError)
                else _error(
                    code=error.code,
                    cause=error.cause,
                    action=error.action,
                    stage=RESOLVE_SCOPE,
                )
            )
            return {
                "workflow_status": "error",
                "investigation": investigation,
                "validation": {},
                "analysis": {},
                "insights": {},
                "result_surface": {},
                "target_series": (),
                "comparison_series": (),
                "analysis_result": None,
                "current_run_id": None,
                "mutation_only": False,
                "stale_run_discarded": False,
                "analysis_request": None,
                "investigation_command": None,
                "error": workflow_error,
                "execution_trace": _trace(state, RESOLVE_SCOPE, reset=True),
            }

    def validate_scope_node(state: EnergyWorkflowState) -> dict[str, Any]:
        scope = state.get("resolved_scope")
        investigation = state.get("investigation")
        if not scope or not investigation:
            missing = "resolved_scope" if not scope else "investigation"
            return {
                "workflow_status": "error",
                "error": _required_state_error(VALIDATE_SCOPE, missing),
                "execution_trace": _trace(state, VALIDATE_SCOPE),
            }
        try:
            investigation, run_id = begin_analysis_run(
                investigation,
                id_factory=id_factory,
            )
            validation = service.validate_analysis_scope(
                scope["building_id"],
                parse_iso_datetime(scope["start_at"]),
                parse_iso_datetime(scope["end_at"]),
            )
        except InvestigationStateError as error:
            return {
                "workflow_status": "error",
                "investigation": investigation,
                "error": _state_error(error, VALIDATE_SCOPE),
                "execution_trace": _trace(state, VALIDATE_SCOPE),
            }
        except (TypeError, ValueError):
            return {
                "workflow_status": "error",
                "investigation": investigation,
                "current_run_id": run_id,
                "error": _error(
                    code="INVALID_TIME_RANGE",
                    cause="The resolved scope contains invalid timestamps.",
                    action="Provide timezone-aware ISO 8601 start and end timestamps.",
                    stage=VALIDATE_SCOPE,
                ),
                "execution_trace": _trace(state, VALIDATE_SCOPE),
            }

        serialized = serialize_validation(validation)
        update: dict[str, Any] = {
            "workflow_status": "validating",
            "investigation": investigation,
            "current_run_id": run_id,
            "validation": serialized,
            "execution_trace": _trace(state, VALIDATE_SCOPE),
        }
        if not validation.valid:
            update["workflow_status"] = "error"
            update["error"] = _error(
                code=validation.error_code or "INVALID_TIME_RANGE",
                cause=validation.cause or "The analysis scope is invalid.",
                action=validation.action or "Correct the analysis scope and rerun.",
                stage=VALIDATE_SCOPE,
            )
        return update

    def query_target_node(state: EnergyWorkflowState) -> dict[str, Any]:
        validation = state.get("validation")
        investigation, run_id = _run_investigation(state)
        if not validation or not investigation or not run_id:
            missing = next(
                name
                for name, value in (
                    ("validation", validation),
                    ("investigation", investigation),
                    ("current_run_id", run_id),
                )
                if not value
            )
            return {
                "workflow_status": "error",
                "error": _required_state_error(QUERY_TARGET, missing),
                "execution_trace": _trace(state, QUERY_TARGET),
            }
        investigation, eligible = advance_analysis_run(
            investigation,
            run_id=run_id,
            status="loading_data",
        )
        if not eligible:
            return _stale_update(state, QUERY_TARGET, investigation)
        try:
            series = service.query_energy_series(
                validation["building_id"],
                parse_iso_datetime(validation["start_at"]),
                parse_iso_datetime(validation["end_at"]),
            )
            return {
                "workflow_status": "loading_target",
                "investigation": investigation,
                "target_series": series,
                "execution_trace": _trace(state, QUERY_TARGET),
            }
        except AnalysisError as error:
            return {
                "workflow_status": "error",
                "investigation": investigation,
                "error": _error(
                    code=error.code,
                    cause=error.cause,
                    action=error.action,
                    stage=QUERY_TARGET,
                ),
                "execution_trace": _trace(state, QUERY_TARGET),
            }

    def query_comparison_node(state: EnergyWorkflowState) -> dict[str, Any]:
        validation = state.get("validation")
        investigation, run_id = _run_investigation(state)
        if not validation or not investigation or not run_id:
            missing = next(
                name
                for name, value in (
                    ("validation", validation),
                    ("investigation", investigation),
                    ("current_run_id", run_id),
                )
                if not value
            )
            return {
                "workflow_status": "error",
                "error": _required_state_error(QUERY_COMPARISON, missing),
                "execution_trace": _trace(state, QUERY_COMPARISON),
            }
        investigation, eligible = advance_analysis_run(
            investigation,
            run_id=run_id,
            status="loading_data",
        )
        if not eligible:
            return _stale_update(state, QUERY_COMPARISON, investigation)
        try:
            series = service.query_energy_series(
                validation["building_id"],
                parse_iso_datetime(validation["comparison_start_at"]),
                parse_iso_datetime(validation["comparison_end_at"]),
            )
            return {
                "workflow_status": "loading_comparison",
                "investigation": investigation,
                "comparison_series": series,
                "execution_trace": _trace(state, QUERY_COMPARISON),
            }
        except AnalysisError as error:
            return {
                "workflow_status": "error",
                "investigation": investigation,
                "error": _error(
                    code=error.code,
                    cause=error.cause,
                    action=error.action,
                    stage=QUERY_COMPARISON,
                ),
                "execution_trace": _trace(state, QUERY_COMPARISON),
            }

    def calculate_analysis_node(state: EnergyWorkflowState) -> dict[str, Any]:
        target = state.get("target_series")
        comparison = state.get("comparison_series")
        investigation, run_id = _run_investigation(state)
        if target is None or comparison is None or not investigation or not run_id:
            missing = next(
                name
                for name, value in (
                    ("target_series", target),
                    ("comparison_series", comparison),
                    ("investigation", investigation),
                    ("current_run_id", run_id),
                )
                if value is None or value is False
            )
            return {
                "workflow_status": "error",
                "error": _required_state_error(CALCULATE_ANALYSIS, missing),
                "execution_trace": _trace(state, CALCULATE_ANALYSIS),
            }
        investigation, eligible = advance_analysis_run(
            investigation,
            run_id=run_id,
            status="calculating",
        )
        if not eligible:
            return _stale_update(state, CALCULATE_ANALYSIS, investigation)
        try:
            result = service.calculate_energy_analysis(target, comparison)
            return {
                "workflow_status": "calculating",
                "investigation": investigation,
                "analysis_result": result,
                "analysis": serialize_analysis_result(result),
                "execution_trace": _trace(state, CALCULATE_ANALYSIS),
            }
        except AnalysisError as error:
            return {
                "workflow_status": "error",
                "investigation": investigation,
                "error": _error(
                    code=error.code,
                    cause=error.cause,
                    action=error.action,
                    stage=CALCULATE_ANALYSIS,
                ),
                "execution_trace": _trace(state, CALCULATE_ANALYSIS),
            }

    def generate_insights_node(state: EnergyWorkflowState) -> dict[str, Any]:
        analysis = state.get("analysis")
        investigation, run_id = _run_investigation(state)
        if not analysis or not investigation or not run_id:
            missing = next(
                name
                for name, value in (
                    ("analysis", analysis),
                    ("investigation", investigation),
                    ("current_run_id", run_id),
                )
                if not value
            )
            return {
                "workflow_status": "error",
                "error": _required_state_error(GENERATE_INSIGHTS, missing),
                "execution_trace": _trace(state, GENERATE_INSIGHTS),
            }
        investigation, eligible = advance_analysis_run(
            investigation,
            run_id=run_id,
            status="generating_insights",
        )
        if not eligible:
            return _stale_update(state, GENERATE_INSIGHTS, investigation)

        model_input = {
            "building_id": analysis["building_id"],
            "target_period": analysis["target_period"],
            "comparison_period": analysis["comparison_period"],
            "metrics": analysis["metrics"],
            "categories": analysis["categories"],
            "evidence": analysis["evidence"],
        }
        try:
            response = model.invoke(
                [
                    SystemMessage(content=INSIGHT_SYSTEM_PROMPT),
                    HumanMessage(
                        content=json.dumps(
                            model_input,
                            ensure_ascii=False,
                            separators=(",", ":"),
                        )
                    ),
                ],
                config={
                    "metadata": {
                        "copilotkit:emit-messages": False,
                        "copilotkit:emit-tool-calls": False,
                        "energyagent:stage": GENERATE_INSIGHTS,
                    }
                },
            )
            bundle = (
                response
                if isinstance(response, InsightBundle)
                else InsightBundle.model_validate(response)
            )
            _validate_evidence_references(bundle, analysis["evidence"])
            return {
                "workflow_status": "generating_insights",
                "investigation": investigation,
                "insights": bundle.model_dump(mode="json"),
                "execution_trace": _trace(state, GENERATE_INSIGHTS),
            }
        except (ValidationError, ValueError, TypeError, KeyError) as error:
            return {
                "workflow_status": "error",
                "investigation": investigation,
                "error": _model_error(error),
                "execution_trace": _trace(state, GENERATE_INSIGHTS),
            }
        except Exception as error:
            return {
                "workflow_status": "error",
                "investigation": investigation,
                "error": _model_error(error),
                "execution_trace": _trace(state, GENERATE_INSIGHTS),
            }

    def update_investigation_node(state: EnergyWorkflowState) -> dict[str, Any]:
        insights = state.get("insights")
        investigation, run_id = _run_investigation(state)
        if not insights or not investigation or not run_id:
            missing = next(
                name
                for name, value in (
                    ("insights", insights),
                    ("investigation", investigation),
                    ("current_run_id", run_id),
                )
                if not value
            )
            return {
                "workflow_status": "error",
                "error": _required_state_error(UPDATE_INVESTIGATION, missing),
                "execution_trace": _trace(state, UPDATE_INVESTIGATION),
            }
        if not is_run_write_eligible(investigation, run_id):
            return _stale_update(state, UPDATE_INVESTIGATION, investigation)
        if insights["unable_to_conclude"]:
            return {
                "workflow_status": "error",
                "investigation": investigation,
                "error": _error(
                    code="INSUFFICIENT_EVIDENCE",
                    cause=insights["inability_reason"]
                    or "The available Evidence cannot support reviewable Findings.",
                    action="Verify the selected scope and underlying measurements, then rerun.",
                    stage=UPDATE_INVESTIGATION,
                ),
                "execution_trace": _trace(state, UPDATE_INVESTIGATION),
            }
        investigation, eligible = prepare_run_reviews(
            investigation,
            run_id=run_id,
            findings=insights["findings"],
            recommendations=insights["recommendations"],
        )
        if not eligible:
            return _stale_update(state, UPDATE_INVESTIGATION, investigation)
        return {
            "workflow_status": "updating",
            "investigation": investigation,
            "target_series": (),
            "comparison_series": (),
            "analysis_result": None,
            "execution_trace": _trace(state, UPDATE_INVESTIGATION),
        }

    def render_result_node(state: EnergyWorkflowState) -> dict[str, Any]:
        analysis = state.get("analysis")
        insights = state.get("insights")
        investigation, run_id = _run_investigation(state)
        if not analysis or not insights or not investigation or not run_id:
            missing = next(
                name
                for name, value in (
                    ("analysis", analysis),
                    ("insights", insights),
                    ("investigation", investigation),
                    ("current_run_id", run_id),
                )
                if not value
            )
            return {
                "workflow_status": "error",
                "error": _required_state_error(RENDER_RESULT, missing),
                "execution_trace": _trace(state, RENDER_RESULT),
            }
        if not is_run_write_eligible(investigation, run_id):
            return _stale_update(state, RENDER_RESULT, investigation)

        surface_id = id_factory("surface")
        try:
            result_surface = build_energy_a2ui_surface(
                surface_id=surface_id,
                analysis=analysis,
                insights=insights,
            )
        except (ValidationError, ValueError, TypeError, KeyError):
            return {
                "workflow_status": "error",
                "investigation": investigation,
                "error": _error(
                    code="A2UI_SCHEMA_INVALID",
                    cause=(
                        "The deterministic result payload did not satisfy the fixed "
                        "EnergyAgent A2UI catalog."
                    ),
                    action="Inspect the render_result builder and fixed catalog contract.",
                    stage=RENDER_RESULT,
                ),
                "execution_trace": _trace(state, RENDER_RESULT),
            }
        investigation, eligible = complete_analysis_run(
            investigation,
            run_id=run_id,
            result_surface_id=surface_id,
        )
        if not eligible:
            return _stale_update(state, RENDER_RESULT, investigation)

        metrics = analysis["metrics"]
        message = (
            f"Analysis complete for Building {analysis['building_id']}. "
            f"Total energy was {metrics['target_total_energy_kwh']:.0f} kWh, "
            f"{metrics['period_change_percent']:+.1f}% versus the previous period "
            f"({metrics['direction']}). Generated {len(insights['findings'])} Findings "
            f"and {len(insights['recommendations'])} Recommendations."
        )
        return {
            "workflow_status": "complete",
            "investigation": investigation,
            "result_surface": result_surface.tool_arguments(),
            "current_run_id": None,
            "messages": [
                AIMessage(
                    content=message,
                    tool_calls=[
                        {
                            "name": RENDER_A2UI_TOOL_NAME,
                            "args": result_surface.tool_arguments(),
                            "id": f"render-{run_id}",
                            "type": "tool_call",
                        }
                    ],
                )
            ],
            "execution_trace": _trace(state, RENDER_RESULT),
        }

    def handle_error_node(state: EnergyWorkflowState) -> dict[str, Any]:
        error = state.get("error") or _error(
            code="WORKFLOW_STATE_ERROR",
            cause="The workflow entered the error path without an error payload.",
            action="Restart the analysis from the beginning.",
            stage=HANDLE_ERROR,
        )
        investigation = state.get("investigation")
        run_id = state.get("current_run_id")
        if investigation:
            investigation, _ = fail_analysis_run(
                investigation,
                run_id=run_id,
                error_message=f"[{error['code']}] {error['cause']}",
            )
        return {
            "workflow_status": "error",
            "investigation": investigation,
            "error": error,
            "analysis": {},
            "insights": {},
            "result_surface": {},
            "target_series": (),
            "comparison_series": (),
            "analysis_result": None,
            "current_run_id": None,
            "mutation_only": False,
            "stale_run_discarded": False,
            "analysis_request": None,
            "investigation_command": None,
            "messages": [
                AIMessage(
                    content=(
                        f"Analysis could not continue: {error['cause']} "
                        f"Corrective action: {error['action']}"
                    )
                )
            ],
            "execution_trace": _trace(state, HANDLE_ERROR),
        }

    builder = StateGraph(
        EnergyWorkflowState,
        input_schema=EnergyWorkflowInput,
        output_schema=EnergyWorkflowOutput,
    )
    builder.add_node(RESOLVE_SCOPE, resolve_scope_node)
    builder.add_node(VALIDATE_SCOPE, validate_scope_node)
    builder.add_node(QUERY_TARGET, query_target_node)
    builder.add_node(QUERY_COMPARISON, query_comparison_node)
    builder.add_node(CALCULATE_ANALYSIS, calculate_analysis_node)
    builder.add_node(GENERATE_INSIGHTS, generate_insights_node)
    builder.add_node(UPDATE_INVESTIGATION, update_investigation_node)
    builder.add_node(RENDER_RESULT, render_result_node)
    builder.add_node(HANDLE_ERROR, handle_error_node)

    builder.add_edge(START, RESOLVE_SCOPE)
    for source, target in (
        (RESOLVE_SCOPE, VALIDATE_SCOPE),
        (VALIDATE_SCOPE, QUERY_TARGET),
        (QUERY_TARGET, QUERY_COMPARISON),
        (QUERY_COMPARISON, CALCULATE_ANALYSIS),
        (CALCULATE_ANALYSIS, GENERATE_INSIGHTS),
        (GENERATE_INSIGHTS, UPDATE_INVESTIGATION),
        (UPDATE_INVESTIGATION, RENDER_RESULT),
    ):
        builder.add_conditional_edges(
            source,
            _route_after_node,
            {
                "continue": target,
                "error": HANDLE_ERROR,
                "stop": END,
            },
        )
    builder.add_conditional_edges(
        RENDER_RESULT,
        _route_after_node,
        {
            "continue": END,
            "error": HANDLE_ERROR,
            "stop": END,
        },
    )
    builder.add_edge(HANDLE_ERROR, END)
    return builder.compile()
