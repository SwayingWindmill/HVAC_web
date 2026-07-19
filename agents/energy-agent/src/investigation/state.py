"""Authoritative shared Investigation state and mutation rules."""

from __future__ import annotations

from copy import deepcopy
from dataclasses import dataclass
from typing import Callable, Literal, NotRequired
from uuid import uuid4

from typing_extensions import TypedDict


InvestigationStatus = Literal[
    "idle",
    "ready",
    "validating",
    "loading_data",
    "calculating",
    "generating_insights",
    "complete",
    "error",
]
StateActor = Literal["user", "agent", "system"]
FindingReviewStatus = Literal[
    "unreviewed",
    "confirmed",
    "ignored",
    "needs_review",
]


class TimeRange(TypedDict):
    start_at: str
    end_at: str
    timezone: str


class FindingReview(TypedDict):
    finding_id: str
    title: str
    review_status: FindingReviewStatus


class RecommendationReview(TypedDict):
    recommendation_id: str
    title: str
    bookmarked: bool


class InvestigationState(TypedDict):
    investigation_id: str
    status: InvestigationStatus
    building_id: str | None
    time_range: TimeRange | None
    active_run_id: str | None
    result_surface_id: str | None
    findings: list[FindingReview]
    recommendations: list[RecommendationReview]
    validation_error: str | None
    last_updated_by: StateActor | None


class InvestigationCommand(TypedDict):
    type: Literal["set_scope", "review_finding", "bookmark_recommendation"]
    building_id: NotRequired[str]
    time_range: NotRequired[TimeRange]
    finding_id: NotRequired[str]
    review_status: NotRequired[FindingReviewStatus]
    recommendation_id: NotRequired[str]
    bookmarked: NotRequired[bool]


@dataclass(frozen=True, slots=True)
class InvestigationStateError(ValueError):
    code: str
    cause: str
    action: str

    def __str__(self) -> str:
        return f"[{self.code}] {self.cause}\nAction: {self.action}"


_ALLOWED_TRANSITIONS: dict[InvestigationStatus, frozenset[InvestigationStatus]] = {
    "idle": frozenset({"idle", "ready"}),
    "ready": frozenset({"ready", "validating"}),
    "validating": frozenset({"validating", "loading_data", "error"}),
    "loading_data": frozenset({"loading_data", "calculating", "error"}),
    "calculating": frozenset({"calculating", "generating_insights", "error"}),
    "generating_insights": frozenset(
        {"generating_insights", "complete", "error"}
    ),
    "complete": frozenset({"complete", "ready", "validating"}),
    "error": frozenset({"error", "ready", "validating"}),
}


def new_identifier(prefix: str) -> str:
    return f"{prefix}_{uuid4().hex}"


def create_investigation(
    *,
    investigation_id: str | None = None,
    id_factory: Callable[[str], str] = new_identifier,
) -> InvestigationState:
    state: InvestigationState = {
        "investigation_id": investigation_id or id_factory("inv"),
        "status": "idle",
        "building_id": None,
        "time_range": None,
        "active_run_id": None,
        "result_surface_id": None,
        "findings": [],
        "recommendations": [],
        "validation_error": None,
        "last_updated_by": None,
    }
    assert_investigation_invariants(state)
    return state


def normalize_investigation(
    value: InvestigationState | None,
    *,
    id_factory: Callable[[str], str] = new_identifier,
) -> InvestigationState:
    if not value:
        return create_investigation(id_factory=id_factory)
    state = deepcopy(value)
    assert_investigation_invariants(state)
    return state


def assert_investigation_invariants(state: InvestigationState) -> None:
    required_keys = {
        "investigation_id",
        "status",
        "building_id",
        "time_range",
        "active_run_id",
        "result_surface_id",
        "findings",
        "recommendations",
        "validation_error",
        "last_updated_by",
    }
    missing = required_keys - set(state)
    if missing:
        raise InvestigationStateError(
            "INVALID_INVESTIGATION_STATE",
            f"Investigation state is missing required fields: {sorted(missing)}.",
            "Restore the complete InvestigationState contract before continuing.",
        )

    if not isinstance(state["investigation_id"], str) or not state["investigation_id"]:
        raise InvestigationStateError(
            "INVALID_INVESTIGATION_STATE",
            "investigation_id must be a non-empty string.",
            "Create the Investigation through create_investigation().",
        )

    status = state["status"]
    if status not in _ALLOWED_TRANSITIONS:
        raise InvestigationStateError(
            "INVALID_INVESTIGATION_STATE",
            f"Unknown Investigation status {status!r}.",
            "Use one of the documented lifecycle statuses.",
        )

    time_range = state["time_range"]
    if time_range is not None:
        if set(time_range) != {"start_at", "end_at", "timezone"}:
            raise InvestigationStateError(
                "INVALID_INVESTIGATION_STATE",
                "time_range must contain only start_at, end_at, and timezone.",
                "Provide the canonical TimeRange shape.",
            )
        if not all(isinstance(time_range[key], str) and time_range[key] for key in time_range):
            raise InvestigationStateError(
                "INVALID_INVESTIGATION_STATE",
                "Every TimeRange value must be a non-empty string.",
                "Provide timezone-aware ISO 8601 boundaries and a timezone name.",
            )

    finding_ids = [item["finding_id"] for item in state["findings"]]
    if len(finding_ids) != len(set(finding_ids)):
        raise InvestigationStateError(
            "INVALID_INVESTIGATION_STATE",
            "Finding review identifiers must be unique.",
            "Remove duplicate Finding review entries.",
        )
    recommendation_ids = [
        item["recommendation_id"] for item in state["recommendations"]
    ]
    if len(recommendation_ids) != len(set(recommendation_ids)):
        raise InvestigationStateError(
            "INVALID_INVESTIGATION_STATE",
            "Recommendation review identifiers must be unique.",
            "Remove duplicate Recommendation review entries.",
        )

    for finding in state["findings"]:
        if finding["review_status"] not in {
            "unreviewed",
            "confirmed",
            "ignored",
            "needs_review",
        }:
            raise InvestigationStateError(
                "INVALID_INVESTIGATION_STATE",
                f"Finding {finding['finding_id']!r} has an invalid review status.",
                "Use unreviewed, confirmed, ignored, or needs_review.",
            )
    for recommendation in state["recommendations"]:
        if not isinstance(recommendation["bookmarked"], bool):
            raise InvestigationStateError(
                "INVALID_INVESTIGATION_STATE",
                f"Recommendation {recommendation['recommendation_id']!r} has a non-boolean bookmark value.",
                "Use true or false for bookmarked.",
            )
    if state["last_updated_by"] not in {None, "user", "agent", "system"}:
        raise InvestigationStateError(
            "INVALID_INVESTIGATION_STATE",
            "last_updated_by is not a supported actor.",
            "Use user, agent, system, or null.",
        )

    if status == "idle":
        if state["building_id"] is not None or state["time_range"] is not None:
            raise InvestigationStateError(
                "INVALID_INVESTIGATION_STATE",
                "idle Investigations cannot have a selected scope.",
                "Move the Investigation to ready when a scope is selected.",
            )

    if status != "idle" and (
        state["building_id"] is None or state["time_range"] is None
    ):
        raise InvestigationStateError(
            "INVALID_INVESTIGATION_STATE",
            f"{status} Investigations require a complete selected scope.",
            "Set the Building and TimeRange before advancing lifecycle state.",
        )

    if status in {
        "validating",
        "loading_data",
        "calculating",
        "generating_insights",
    } and not state["active_run_id"]:
        raise InvestigationStateError(
            "INVALID_INVESTIGATION_STATE",
            f"{status} requires an active_run_id.",
            "Begin a new Analysis Run before advancing the lifecycle.",
        )

    if status == "complete":
        if not state["result_surface_id"]:
            raise InvestigationStateError(
                "INVALID_INVESTIGATION_STATE",
                "complete requires a result_surface_id.",
                "Publish a result surface before marking the Investigation complete.",
            )
        if not state["findings"] or not state["recommendations"]:
            raise InvestigationStateError(
                "INVALID_INVESTIGATION_STATE",
                "complete requires at least one Finding and one Recommendation.",
                "Generate reviewable Findings and Recommendations before completion.",
            )

    if status == "error" and not state["validation_error"]:
        raise InvestigationStateError(
            "INVALID_INVESTIGATION_STATE",
            "error requires a validation or runtime error message.",
            "Record the specific error before entering error state.",
        )

    if status != "error" and state["validation_error"] is not None:
        raise InvestigationStateError(
            "INVALID_INVESTIGATION_STATE",
            "validation_error may only be populated in error state.",
            "Clear validation_error before leaving error state.",
        )

    if state["active_run_id"] and status not in {
        "validating",
        "loading_data",
        "calculating",
        "generating_insights",
    }:
        raise InvestigationStateError(
            "INVALID_INVESTIGATION_STATE",
            f"{status} may not retain an active_run_id.",
            "Clear or finish the active Analysis Run before entering this state.",
        )


def _transition(
    state: InvestigationState,
    status: InvestigationStatus,
    *,
    actor: StateActor,
) -> InvestigationState:
    current = state["status"]
    if status not in _ALLOWED_TRANSITIONS[current]:
        raise InvestigationStateError(
            "INVALID_STATE_TRANSITION",
            f"Investigation cannot transition from {current} to {status}.",
            "Follow the documented lifecycle order or return to ready after invalidation.",
        )
    updated = deepcopy(state)
    updated["status"] = status
    updated["last_updated_by"] = actor
    assert_investigation_invariants(updated)
    return updated


def apply_scope(
    state: InvestigationState,
    *,
    building_id: str,
    time_range: TimeRange,
    actor: StateActor,
) -> InvestigationState:
    normalized_building = building_id.strip().upper()
    if not normalized_building:
        raise InvestigationStateError(
            "INVALID_SCOPE_MUTATION",
            "A scope mutation requires a Building identifier.",
            "Select Building A, B, or C.",
        )
    if time_range.get("timezone") != "Asia/Shanghai":
        raise InvestigationStateError(
            "INVALID_SCOPE_MUTATION",
            "The MVP business timezone is Asia/Shanghai.",
            "Convert scope boundaries to Asia/Shanghai before updating shared state.",
        )

    updated = deepcopy(state)
    updated.update(
        {
            "status": "ready",
            "building_id": normalized_building,
            "time_range": deepcopy(time_range),
            "active_run_id": None,
            "result_surface_id": None,
            "findings": [],
            "recommendations": [],
            "validation_error": None,
            "last_updated_by": actor,
        }
    )
    assert_investigation_invariants(updated)
    return updated


def begin_analysis_run(
    state: InvestigationState,
    *,
    run_id: str | None = None,
    id_factory: Callable[[str], str] = new_identifier,
) -> tuple[InvestigationState, str]:
    if state["building_id"] is None or state["time_range"] is None:
        raise InvestigationStateError(
            "INVALID_STATE_TRANSITION",
            "An Analysis Run requires a selected Building and TimeRange.",
            "Set a complete scope before starting analysis.",
        )
    if state["status"] not in {"ready", "complete", "error"}:
        raise InvestigationStateError(
            "INVALID_STATE_TRANSITION",
            f"A new Analysis Run cannot start while the Investigation is {state['status']}.",
            "Wait for the active run to finish or change scope to revoke it before rerunning.",
        )
    updated = deepcopy(state)
    selected_run_id = run_id or id_factory("run")
    updated.update(
        {
            "status": "validating",
            "active_run_id": selected_run_id,
            "result_surface_id": None,
            "findings": [],
            "recommendations": [],
            "validation_error": None,
            "last_updated_by": "system",
        }
    )
    assert_investigation_invariants(updated)
    return updated, selected_run_id


def is_run_write_eligible(state: InvestigationState, run_id: str | None) -> bool:
    return bool(run_id and state["active_run_id"] == run_id)


def advance_analysis_run(
    state: InvestigationState,
    *,
    run_id: str,
    status: Literal[
        "validating",
        "loading_data",
        "calculating",
        "generating_insights",
    ],
) -> tuple[InvestigationState, bool]:
    if not is_run_write_eligible(state, run_id):
        return deepcopy(state), False
    return _transition(state, status, actor="agent"), True


def prepare_run_reviews(
    state: InvestigationState,
    *,
    run_id: str,
    findings: list[dict[str, object]],
    recommendations: list[dict[str, object]],
) -> tuple[InvestigationState, bool]:
    if not is_run_write_eligible(state, run_id):
        return deepcopy(state), False
    prepared_findings: list[FindingReview] = [
        {
            "finding_id": str(item["finding_id"]),
            "title": str(item["title"]),
            "review_status": "unreviewed",
        }
        for item in findings
    ]
    prepared_recommendations: list[RecommendationReview] = [
        {
            "recommendation_id": str(item["recommendation_id"]),
            "title": str(item["title"]),
            "bookmarked": False,
        }
        for item in recommendations
    ]
    updated = deepcopy(state)
    updated["findings"] = prepared_findings
    updated["recommendations"] = prepared_recommendations
    updated["last_updated_by"] = "agent"
    assert_investigation_invariants(updated)
    return updated, True


def complete_analysis_run(
    state: InvestigationState,
    *,
    run_id: str,
    result_surface_id: str,
) -> tuple[InvestigationState, bool]:
    if not is_run_write_eligible(state, run_id):
        return deepcopy(state), False
    updated = deepcopy(state)
    updated.update(
        {
            "status": "complete",
            "active_run_id": None,
            "result_surface_id": result_surface_id,
            "validation_error": None,
            "last_updated_by": "agent",
        }
    )
    assert_investigation_invariants(updated)
    return updated, True


def fail_analysis_run(
    state: InvestigationState,
    *,
    run_id: str | None,
    error_message: str,
) -> tuple[InvestigationState, bool]:
    if run_id is None or not is_run_write_eligible(state, run_id):
        return deepcopy(state), False
    updated = deepcopy(state)
    updated.update(
        {
            "status": "error",
            "active_run_id": None,
            "result_surface_id": None,
            "findings": [],
            "recommendations": [],
            "validation_error": error_message,
            "last_updated_by": "system",
        }
    )
    assert_investigation_invariants(updated)
    return updated, True


def review_finding(
    state: InvestigationState,
    *,
    finding_id: str,
    review_status: FindingReviewStatus,
) -> InvestigationState:
    updated = deepcopy(state)
    for finding in updated["findings"]:
        if finding["finding_id"] == finding_id:
            finding["review_status"] = review_status
            updated["last_updated_by"] = "user"
            assert_investigation_invariants(updated)
            return updated
    raise InvestigationStateError(
        "FINDING_NOT_FOUND",
        f"Finding {finding_id!r} does not exist in the current Investigation.",
        "Refresh the current result surface before updating review status.",
    )


def bookmark_recommendation(
    state: InvestigationState,
    *,
    recommendation_id: str,
    bookmarked: bool,
) -> InvestigationState:
    updated = deepcopy(state)
    for recommendation in updated["recommendations"]:
        if recommendation["recommendation_id"] == recommendation_id:
            recommendation["bookmarked"] = bookmarked
            updated["last_updated_by"] = "user"
            assert_investigation_invariants(updated)
            return updated
    raise InvestigationStateError(
        "RECOMMENDATION_NOT_FOUND",
        f"Recommendation {recommendation_id!r} does not exist in the current Investigation.",
        "Refresh the current result surface before changing the bookmark.",
    )


def apply_investigation_command(
    state: InvestigationState,
    command: InvestigationCommand,
) -> InvestigationState:
    command_type = command.get("type")
    if command_type == "set_scope":
        building_id = command.get("building_id")
        time_range = command.get("time_range")
        if not isinstance(building_id, str) or not isinstance(time_range, dict):
            raise InvestigationStateError(
                "INVALID_SCOPE_MUTATION",
                "set_scope requires building_id and time_range.",
                "Provide the complete selected scope from the canvas.",
            )
        return apply_scope(
            state,
            building_id=building_id,
            time_range=time_range,
            actor="user",
        )
    if command_type == "review_finding":
        finding_id = command.get("finding_id")
        review_status = command.get("review_status")
        if not isinstance(finding_id, str) or review_status not in {
            "unreviewed",
            "confirmed",
            "ignored",
            "needs_review",
        }:
            raise InvestigationStateError(
                "INVALID_REVIEW_MUTATION",
                "review_finding requires a valid finding_id and review_status.",
                "Use unreviewed, confirmed, ignored, or needs_review.",
            )
        return review_finding(
            state,
            finding_id=finding_id,
            review_status=review_status,
        )
    if command_type == "bookmark_recommendation":
        recommendation_id = command.get("recommendation_id")
        bookmarked = command.get("bookmarked")
        if not isinstance(recommendation_id, str) or not isinstance(bookmarked, bool):
            raise InvestigationStateError(
                "INVALID_BOOKMARK_MUTATION",
                "bookmark_recommendation requires recommendation_id and bookmarked.",
                "Provide the current Recommendation identifier and a boolean value.",
            )
        return bookmark_recommendation(
            state,
            recommendation_id=recommendation_id,
            bookmarked=bookmarked,
        )
    raise InvestigationStateError(
        "INVALID_INVESTIGATION_COMMAND",
        f"Unsupported Investigation command {command_type!r}.",
        "Use set_scope, review_finding, or bookmark_recommendation.",
    )
