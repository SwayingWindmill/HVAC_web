"""Fixed-schema A2UI result surface for EnergyAgent."""

from __future__ import annotations

from collections import Counter
from typing import Any, Annotated, Literal

from pydantic import BaseModel, ConfigDict, Field, TypeAdapter, model_validator


ENERGY_CATALOG_ID = "copilotkit://energyagent/fixed-catalog-v1"
RENDER_A2UI_TOOL_NAME = "render_a2ui"
FIXED_SECTION_ORDER = (
    "conclusion",
    "load_formation",
    "findings",
    "recommendations",
)
APPROVED_COMPONENT_NAMES = frozenset(
    {
        "ResultSection",
        "MetricCard",
        "EnergyTrendChart",
        "CategoryShareChart",
        "FindingCard",
        "FindingReviewActions",
        "RecommendationCard",
        "RecommendationBookmarkAction",
        "StatusBadge",
    }
)


class _Component(BaseModel):
    model_config = ConfigDict(extra="forbid", populate_by_name=True)

    id: str = Field(min_length=1, max_length=120)
    component: str


class ResultSection(_Component):
    component: Literal["ResultSection"] = "ResultSection"
    section_key: Literal[
        "report",
        "conclusion",
        "load_formation",
        "findings",
        "recommendations",
    ] = Field(alias="sectionKey")
    title: str = Field(min_length=1, max_length=160)
    description: str | None = Field(default=None, max_length=500)
    children: list[str] = Field(default_factory=list, max_length=24)


class MetricCard(_Component):
    component: Literal["MetricCard"] = "MetricCard"
    metric_key: Literal[
        "total_energy",
        "peak_demand",
        "peak_time",
        "period_change",
    ] = Field(alias="metricKey")
    label: str = Field(min_length=1, max_length=120)
    value: str = Field(min_length=1, max_length=80)
    unit: str | None = Field(default=None, max_length=40)
    supporting_text: str | None = Field(
        default=None,
        alias="supportingText",
        max_length=240,
    )
    tone: Literal["neutral", "positive", "negative", "warning"] = "neutral"
    read_only: Literal[True] = Field(default=True, alias="readOnly")


class TrendPoint(BaseModel):
    model_config = ConfigDict(extra="forbid", populate_by_name=True)

    label: str = Field(min_length=1, max_length=80)
    target_energy_kwh: float = Field(alias="targetEnergyKwh")
    comparison_energy_kwh: float = Field(alias="comparisonEnergyKwh")


class EnergyTrendChart(_Component):
    component: Literal["EnergyTrendChart"] = "EnergyTrendChart"
    title: str = Field(min_length=1, max_length=160)
    granularity: Literal["hourly", "daily"]
    points: list[TrendPoint] = Field(min_length=1, max_length=168)
    read_only: Literal[True] = Field(default=True, alias="readOnly")


class CategoryShareItem(BaseModel):
    model_config = ConfigDict(extra="forbid", populate_by_name=True)

    category: Literal["HVAC", "Lighting", "Power Equipment", "Other"]
    share_percent: float = Field(ge=0, le=100, alias="sharePercent")
    energy_kwh: float = Field(ge=0, alias="energyKwh")


class CategoryShareChart(_Component):
    component: Literal["CategoryShareChart"] = "CategoryShareChart"
    title: str = Field(min_length=1, max_length=160)
    items: list[CategoryShareItem] = Field(min_length=4, max_length=4)
    read_only: Literal[True] = Field(default=True, alias="readOnly")


class FindingCard(_Component):
    component: Literal["FindingCard"] = "FindingCard"
    finding_id: str = Field(min_length=1, max_length=80, alias="findingId")
    title: str = Field(min_length=1, max_length=160)
    summary: str = Field(min_length=1, max_length=600)
    evidence_refs: list[str] = Field(
        min_length=1,
        max_length=6,
        alias="evidenceRefs",
    )
    hypothesis: str | None = Field(default=None, max_length=400)
    review_actions: str = Field(min_length=1, alias="reviewActions")


class FindingReviewActions(_Component):
    component: Literal["FindingReviewActions"] = "FindingReviewActions"
    finding_id: str = Field(min_length=1, max_length=80, alias="findingId")
    current_status: Literal[
        "unreviewed",
        "confirmed",
        "ignored",
        "needs_review",
    ] = Field(default="unreviewed", alias="currentStatus")
    interactive: Literal[True] = True


class RecommendationCard(_Component):
    component: Literal["RecommendationCard"] = "RecommendationCard"
    recommendation_id: str = Field(
        min_length=1,
        max_length=80,
        alias="recommendationId",
    )
    finding_id: str = Field(min_length=1, max_length=80, alias="findingId")
    title: str = Field(min_length=1, max_length=160)
    description: str = Field(min_length=1, max_length=600)
    bookmark_action: str = Field(min_length=1, alias="bookmarkAction")


class RecommendationBookmarkAction(_Component):
    component: Literal[
        "RecommendationBookmarkAction"
    ] = "RecommendationBookmarkAction"
    recommendation_id: str = Field(
        min_length=1,
        max_length=80,
        alias="recommendationId",
    )
    bookmarked: bool = False
    interactive: Literal[True] = True


class StatusBadge(_Component):
    component: Literal["StatusBadge"] = "StatusBadge"
    label: str = Field(min_length=1, max_length=80)
    tone: Literal["neutral", "positive", "negative", "warning"] = "neutral"


EnergyComponent = Annotated[
    ResultSection
    | MetricCard
    | EnergyTrendChart
    | CategoryShareChart
    | FindingCard
    | FindingReviewActions
    | RecommendationCard
    | RecommendationBookmarkAction
    | StatusBadge,
    Field(discriminator="component"),
]
_COMPONENT_ADAPTER = TypeAdapter(EnergyComponent)


class EnergyA2UISurface(BaseModel):
    model_config = ConfigDict(extra="forbid", populate_by_name=True)

    surface_id: str = Field(min_length=1, max_length=160, alias="surfaceId")
    catalog_id: Literal[ENERGY_CATALOG_ID] = Field(
        default=ENERGY_CATALOG_ID,
        alias="catalogId",
    )
    components: list[EnergyComponent] = Field(min_length=1, max_length=80)

    @model_validator(mode="after")
    def validate_surface(self) -> "EnergyA2UISurface":
        identifiers = [component.id for component in self.components]
        duplicates = [
            identifier
            for identifier, count in Counter(identifiers).items()
            if count > 1
        ]
        if duplicates:
            raise ValueError(f"A2UI component IDs must be unique: {duplicates}")

        by_id = {component.id: component for component in self.components}
        root = by_id.get("root")
        if not isinstance(root, ResultSection) or root.section_key != "report":
            raise ValueError("A2UI root must be a ResultSection with sectionKey 'report'")

        expected_section_ids = [f"section-{key}" for key in FIXED_SECTION_ORDER]
        if root.children != expected_section_ids:
            raise ValueError(
                "A2UI root children must preserve the fixed Conclusion, Load Formation, "
                "Findings, Recommendations order"
            )
        for key, identifier in zip(FIXED_SECTION_ORDER, expected_section_ids, strict=True):
            section = by_id.get(identifier)
            if not isinstance(section, ResultSection) or section.section_key != key:
                raise ValueError(
                    f"A2UI top-level section {identifier!r} must use sectionKey {key!r}"
                )

        references: list[str] = []
        for component in self.components:
            if isinstance(component, ResultSection):
                references.extend(component.children)
            elif isinstance(component, FindingCard):
                references.append(component.review_actions)
            elif isinstance(component, RecommendationCard):
                references.append(component.bookmark_action)
        missing = sorted(set(references) - set(by_id))
        if missing:
            raise ValueError(f"A2UI component references are missing: {missing}")

        reachable: set[str] = set()
        pending = ["root"]
        while pending:
            identifier = pending.pop()
            if identifier in reachable:
                continue
            reachable.add(identifier)
            component = by_id[identifier]
            if isinstance(component, ResultSection):
                pending.extend(component.children)
            elif isinstance(component, FindingCard):
                pending.append(component.review_actions)
            elif isinstance(component, RecommendationCard):
                pending.append(component.bookmark_action)
        unreachable = sorted(set(by_id) - reachable)
        if unreachable:
            raise ValueError(f"A2UI surface contains unreachable components: {unreachable}")
        return self

    def tool_arguments(self) -> dict[str, Any]:
        return self.model_dump(by_alias=True, exclude_none=True, mode="json")


def validate_component(component: dict[str, Any]) -> dict[str, Any]:
    """Validate one component against the approved fixed catalog."""

    parsed = _COMPONENT_ADAPTER.validate_python(component)
    return parsed.model_dump(by_alias=True, exclude_none=True, mode="json")


def _round_energy(value: float) -> float:
    return float(f"{value:.0f}")


def _round_percent(value: float) -> float:
    return float(f"{value:.1f}")


def _metric_tone(direction: str) -> Literal["positive", "negative", "neutral"]:
    if direction == "down":
        return "positive"
    if direction == "up":
        return "negative"
    return "neutral"


def _trend_label(value: str, granularity: str) -> str:
    if granularity == "hourly":
        return value[5:16].replace("T", " ")
    return value[5:10]


def build_energy_a2ui_surface(
    *,
    surface_id: str,
    analysis: dict[str, Any],
    insights: dict[str, Any],
) -> EnergyA2UISurface:
    """Build and validate the deterministic fixed-schema result surface."""

    metrics = analysis["metrics"]
    direction = str(metrics["direction"])
    tone = _metric_tone(direction)
    trend = analysis["trend"]

    components: list[dict[str, Any]] = [
        {
            "id": "root",
            "component": "ResultSection",
            "sectionKey": "report",
            "title": f"Building {analysis['building_id']} energy analysis",
            "description": (
                f"{analysis['target_period']['start_at']} to "
                f"{analysis['target_period']['end_at']} · Asia/Shanghai"
            ),
            "children": [f"section-{key}" for key in FIXED_SECTION_ORDER],
        },
        {
            "id": "section-conclusion",
            "component": "ResultSection",
            "sectionKey": "conclusion",
            "title": "Conclusion",
            "description": "Authoritative metrics for the selected target period.",
            "children": [
                "status-direction",
                "metric-total-energy",
                "metric-peak-demand",
                "metric-peak-time",
                "metric-period-change",
            ],
        },
        {
            "id": "status-direction",
            "component": "StatusBadge",
            "label": direction.title(),
            "tone": tone,
        },
        {
            "id": "metric-total-energy",
            "component": "MetricCard",
            "metricKey": "total_energy",
            "label": "Total energy",
            "value": f"{metrics['target_total_energy_kwh']:.0f}",
            "unit": "kWh",
            "supportingText": (
                f"Previous period {metrics['comparison_total_energy_kwh']:.0f} kWh"
            ),
            "tone": "neutral",
            "readOnly": True,
        },
        {
            "id": "metric-peak-demand",
            "component": "MetricCard",
            "metricKey": "peak_demand",
            "label": "Peak demand",
            "value": f"{metrics['peak_demand_kw']:.0f}",
            "unit": "kW",
            "supportingText": (
                f"Previous peak {metrics['comparison_peak_demand_kw']:.0f} kW"
            ),
            "tone": "neutral",
            "readOnly": True,
        },
        {
            "id": "metric-peak-time",
            "component": "MetricCard",
            "metricKey": "peak_time",
            "label": "Peak time",
            "value": str(metrics["peak_at"]),
            "supportingText": "Business timezone Asia/Shanghai",
            "tone": "neutral",
            "readOnly": True,
        },
        {
            "id": "metric-period-change",
            "component": "MetricCard",
            "metricKey": "period_change",
            "label": "Change vs previous period",
            "value": f"{metrics['period_change_percent']:+.1f}",
            "unit": "%",
            "supportingText": f"Classified {direction}",
            "tone": tone,
            "readOnly": True,
        },
        {
            "id": "section-load_formation",
            "component": "ResultSection",
            "sectionKey": "load_formation",
            "title": "How the load formed",
            "description": "Target and comparison energy trend followed by category share.",
            "children": ["chart-energy-trend", "chart-category-share"],
        },
        {
            "id": "chart-energy-trend",
            "component": "EnergyTrendChart",
            "title": "Energy trend",
            "granularity": trend["granularity"],
            "points": [
                {
                    "label": _trend_label(point["start_at"], trend["granularity"]),
                    "targetEnergyKwh": _round_energy(point["target_energy_kwh"]),
                    "comparisonEnergyKwh": _round_energy(
                        point["comparison_energy_kwh"]
                    ),
                }
                for point in trend["points"]
            ],
            "readOnly": True,
        },
        {
            "id": "chart-category-share",
            "component": "CategoryShareChart",
            "title": "Energy share by category",
            "items": [
                {
                    "category": category["category"],
                    "sharePercent": _round_percent(category["share_percent"]),
                    "energyKwh": _round_energy(category["target_energy_kwh"]),
                }
                for category in analysis["categories"]
            ],
            "readOnly": True,
        },
        {
            "id": "section-findings",
            "component": "ResultSection",
            "sectionKey": "findings",
            "title": "What needs attention",
            "description": "Evidence-backed Findings with shared-state review actions.",
            "children": [
                f"finding-{index}" for index, _ in enumerate(insights["findings"], start=1)
            ],
        },
        {
            "id": "section-recommendations",
            "component": "ResultSection",
            "sectionKey": "recommendations",
            "title": "What to do next",
            "description": "Recommendations mapped to the returned Findings.",
            "children": [
                f"recommendation-{index}"
                for index, _ in enumerate(insights["recommendations"], start=1)
            ],
        },
    ]

    for index, finding in enumerate(insights["findings"], start=1):
        action_id = f"finding-review-{index}"
        components.extend(
            [
                {
                    "id": f"finding-{index}",
                    "component": "FindingCard",
                    "findingId": finding["finding_id"],
                    "title": finding["title"],
                    "summary": finding["summary"],
                    "evidenceRefs": finding["evidence_refs"],
                    "hypothesis": finding.get("hypothesis"),
                    "reviewActions": action_id,
                },
                {
                    "id": action_id,
                    "component": "FindingReviewActions",
                    "findingId": finding["finding_id"],
                    "currentStatus": "unreviewed",
                    "interactive": True,
                },
            ]
        )

    for index, recommendation in enumerate(insights["recommendations"], start=1):
        action_id = f"recommendation-bookmark-{index}"
        components.extend(
            [
                {
                    "id": f"recommendation-{index}",
                    "component": "RecommendationCard",
                    "recommendationId": recommendation["recommendation_id"],
                    "findingId": recommendation["finding_id"],
                    "title": recommendation["title"],
                    "description": recommendation["description"],
                    "bookmarkAction": action_id,
                },
                {
                    "id": action_id,
                    "component": "RecommendationBookmarkAction",
                    "recommendationId": recommendation["recommendation_id"],
                    "bookmarked": False,
                    "interactive": True,
                },
            ]
        )

    validated = [validate_component(component) for component in components]
    return EnergyA2UISurface.model_validate(
        {
            "surfaceId": surface_id,
            "catalogId": ENERGY_CATALOG_ID,
            "components": validated,
        }
    )
