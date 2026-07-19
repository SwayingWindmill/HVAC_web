"""Serializable contracts for the explicit EnergyAgent workflow."""

from __future__ import annotations

from datetime import datetime
from typing import Any, Literal

from copilotkit import CopilotKitState
from pydantic import BaseModel, Field, model_validator
from typing_extensions import TypedDict

from src.energy.analysis import EnergyAnalysisResult, ScopeValidation
from src.energy.data import EnergyInterval
from src.investigation import InvestigationCommand, InvestigationState


ScopePreset = Literal["yesterday", "last_24_hours", "last_7_days", "custom"]
ScopeSource = Literal["canvas", "chat", "system"]
WorkflowStatus = Literal[
    "resolving",
    "validating",
    "loading_target",
    "loading_comparison",
    "calculating",
    "generating_insights",
    "updating",
    "rendering",
    "complete",
    "error",
    "mutation_applied",
    "discarded",
]


class AnalysisRequest(TypedDict, total=False):
    building_id: str
    preset: ScopePreset
    start_at: str
    end_at: str
    reference_time: str
    source: ScopeSource


class ResolvedScope(TypedDict):
    building_id: str
    start_at: str
    end_at: str
    timezone: str
    source: ScopeSource


class WorkflowError(TypedDict):
    code: str
    cause: str
    action: str
    stage: str


class SerializedValidation(TypedDict):
    valid: bool
    building_id: str
    start_at: str
    end_at: str
    comparison_start_at: str
    comparison_end_at: str
    error_code: str | None
    cause: str | None
    action: str | None


class FindingDraft(BaseModel):
    finding_id: str = Field(min_length=1, max_length=80)
    title: str = Field(min_length=1, max_length=160)
    summary: str = Field(min_length=1, max_length=600)
    evidence_refs: list[str] = Field(min_length=1, max_length=6)
    hypothesis: str | None = Field(default=None, max_length=400)


class RecommendationDraft(BaseModel):
    recommendation_id: str = Field(min_length=1, max_length=80)
    finding_id: str = Field(min_length=1, max_length=80)
    title: str = Field(min_length=1, max_length=160)
    description: str = Field(min_length=1, max_length=600)


class InsightBundle(BaseModel):
    unable_to_conclude: bool = False
    inability_reason: str | None = Field(default=None, max_length=600)
    findings: list[FindingDraft] = Field(default_factory=list, max_length=3)
    recommendations: list[RecommendationDraft] = Field(
        default_factory=list,
        max_length=3,
    )

    @model_validator(mode="after")
    def validate_bundle(self) -> "InsightBundle":
        if self.unable_to_conclude:
            if not self.inability_reason:
                raise ValueError("inability_reason is required when unable_to_conclude is true")
            if self.findings or self.recommendations:
                raise ValueError(
                    "unable_to_conclude responses may not include findings or recommendations"
                )
            return self

        if not self.findings:
            raise ValueError("at least one finding is required")
        if not self.recommendations:
            raise ValueError("at least one recommendation is required")

        finding_ids = [finding.finding_id for finding in self.findings]
        if len(set(finding_ids)) != len(finding_ids):
            raise ValueError("finding_id values must be unique")
        recommendation_ids = [
            recommendation.recommendation_id for recommendation in self.recommendations
        ]
        if len(set(recommendation_ids)) != len(recommendation_ids):
            raise ValueError("recommendation_id values must be unique")
        if any(
            recommendation.finding_id not in finding_ids
            for recommendation in self.recommendations
        ):
            raise ValueError("every recommendation must reference a returned finding")
        return self


class EnergyWorkflowInput(CopilotKitState, total=False):
    analysis_request: AnalysisRequest
    investigation: InvestigationState
    investigation_command: InvestigationCommand


class EnergyWorkflowOutput(CopilotKitState, total=False):
    workflow_status: WorkflowStatus
    investigation: InvestigationState
    error: WorkflowError | None
    execution_trace: list[str]


class EnergyWorkflowState(EnergyWorkflowInput, EnergyWorkflowOutput, total=False):
    resolved_scope: ResolvedScope
    validation: SerializedValidation
    analysis: dict[str, Any]
    insights: dict[str, Any]
    result_surface: dict[str, Any]
    target_series: tuple[EnergyInterval, ...]
    comparison_series: tuple[EnergyInterval, ...]
    analysis_result: EnergyAnalysisResult | None
    current_run_id: str | None
    mutation_only: bool
    stale_run_discarded: bool
    execution_trace: list[str]


def serialize_validation(validation: ScopeValidation) -> SerializedValidation:
    return {
        "valid": validation.valid,
        "building_id": validation.building_id,
        "start_at": validation.start_at.isoformat(),
        "end_at": validation.end_at.isoformat(),
        "comparison_start_at": validation.comparison_start_at.isoformat(),
        "comparison_end_at": validation.comparison_end_at.isoformat(),
        "error_code": validation.error_code,
        "cause": validation.cause,
        "action": validation.action,
    }


def _serialize_category(category: Any) -> dict[str, Any]:
    return {
        "category": category.category,
        "target_energy_kwh": category.target_energy_kwh,
        "comparison_energy_kwh": category.comparison_energy_kwh,
        "share_percent": category.share_percent,
        "change_percent": category.change_percent,
    }


def build_evidence_catalog(result: EnergyAnalysisResult) -> dict[str, dict[str, Any]]:
    evidence: dict[str, dict[str, Any]] = {
        "period_change": {
            "type": "period_change",
            "change_percent": result.period_change_percent,
            "direction": result.direction,
            "target_energy_kwh": result.target_total_energy_kwh,
            "comparison_energy_kwh": result.comparison_total_energy_kwh,
        },
        "peak_window": {
            "type": "peak_window",
            "start_at": result.evidence.peak_window.start_at.isoformat(),
            "end_at": result.evidence.peak_window.end_at.isoformat(),
            "demand_kw": result.evidence.peak_window.demand_kw,
            "comparison_peak_kw": result.comparison_peak_demand_kw,
        },
        "peak_contribution": {
            "type": "peak_contribution",
            "category": result.evidence.peak_contribution.category,
            "power_kw": result.evidence.peak_contribution.power_kw,
            "share_percent": result.evidence.peak_contribution.share_percent,
        },
        "overnight_baseload_ratio": {
            "type": "overnight_baseload_ratio",
            "ratio": result.evidence.overnight_baseload_ratio,
        },
        "dominant_category": {
            "type": "dominant_category",
            "category": result.evidence.dominant_category,
        },
    }
    for category in result.evidence.category_changes:
        key = f"category_change:{category.category.lower().replace(' ', '_')}"
        evidence[key] = {
            "type": "category_change",
            **_serialize_category(category),
        }
    for index, window in enumerate(result.evidence.high_load_windows, start=1):
        evidence[f"high_load_window:{index}"] = {
            "type": "high_load_window",
            "start_at": window.start_at.isoformat(),
            "end_at": window.end_at.isoformat(),
            "demand_kw": window.demand_kw,
        }
    return evidence


def serialize_analysis_result(result: EnergyAnalysisResult) -> dict[str, Any]:
    return {
        "building_id": result.building_id,
        "target_period": {
            "start_at": result.target_start_at.isoformat(),
            "end_at": result.target_end_at.isoformat(),
        },
        "comparison_period": {
            "start_at": result.comparison_start_at.isoformat(),
            "end_at": result.comparison_end_at.isoformat(),
        },
        "metrics": {
            "target_total_energy_kwh": result.target_total_energy_kwh,
            "comparison_total_energy_kwh": result.comparison_total_energy_kwh,
            "period_change_percent": result.period_change_percent,
            "direction": result.direction,
            "peak_demand_kw": result.peak_demand_kw,
            "peak_at": result.peak_at.isoformat(),
            "comparison_peak_demand_kw": result.comparison_peak_demand_kw,
            "comparison_peak_at": result.comparison_peak_at.isoformat(),
        },
        "trend": {
            "granularity": result.trend_granularity,
            "points": [
                {
                    "start_at": point.start_at.isoformat(),
                    "target_energy_kwh": point.target_energy_kwh,
                    "comparison_energy_kwh": point.comparison_energy_kwh,
                }
                for point in result.trend
            ],
        },
        "categories": [_serialize_category(category) for category in result.categories],
        "evidence": build_evidence_catalog(result),
    }


def parse_iso_datetime(value: str) -> datetime:
    normalized = value.strip().replace("Z", "+00:00")
    return datetime.fromisoformat(normalized)
