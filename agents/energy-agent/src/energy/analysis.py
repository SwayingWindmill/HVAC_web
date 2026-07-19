"""Deterministic scope validation and energy calculations."""

from __future__ import annotations

from collections import defaultdict
from dataclasses import dataclass
from datetime import datetime, timedelta
import math
from typing import Final, Literal, Sequence

from src.energy.data import (
    BUSINESS_TIMEZONE,
    CATEGORY_NAMES,
    INTERVAL_DURATION,
    INTERVAL_HOURS,
    Building,
    CategoryName,
    EnergyInterval,
    MockEnergyDataset,
)


AnalysisErrorCode = Literal[
    "BUILDING_NOT_FOUND",
    "INVALID_TIME_RANGE",
    "TARGET_OUT_OF_RANGE",
    "COMPARISON_OUT_OF_RANGE",
    "INCOMPLETE_TARGET_DATA",
    "INCOMPLETE_COMPARISON_DATA",
    "INVALID_MEASUREMENT",
]
Direction = Literal["down", "stable", "up"]
TrendGranularity = Literal["hourly", "daily"]


_ERROR_ACTIONS: Final[dict[AnalysisErrorCode, str]] = {
    "BUILDING_NOT_FOUND": "Select one of the buildings returned by list_buildings().",
    "INVALID_TIME_RANGE": "Use timezone-aware, 15-minute-aligned timestamps with end_at after start_at.",
    "TARGET_OUT_OF_RANGE": "Choose a target period contained within the available 60-day dataset window.",
    "COMPARISON_OUT_OF_RANGE": "Choose a target period whose immediately preceding equal-duration period is also available.",
    "INCOMPLETE_TARGET_DATA": "Choose another target period or restore every expected 15-minute target interval.",
    "INCOMPLETE_COMPARISON_DATA": "Choose another period or restore every expected 15-minute comparison interval.",
    "INVALID_MEASUREMENT": "Correct negative, non-finite, duplicate, or inconsistent measurement values before analysis.",
}


class AnalysisError(ValueError):
    """An analysis failure with a stable code and corrective action."""

    def __init__(self, code: AnalysisErrorCode, cause: str) -> None:
        self.code = code
        self.cause = cause
        self.action = _ERROR_ACTIONS[code]
        super().__init__(f"[{code}] {cause}\nAction: {self.action}")


@dataclass(frozen=True, slots=True)
class ScopeValidation:
    valid: bool
    building_id: str
    start_at: datetime
    end_at: datetime
    comparison_start_at: datetime
    comparison_end_at: datetime
    error_code: AnalysisErrorCode | None = None
    cause: str | None = None
    action: str | None = None


@dataclass(frozen=True, slots=True)
class TrendPoint:
    start_at: datetime
    target_energy_kwh: float
    comparison_energy_kwh: float


@dataclass(frozen=True, slots=True)
class CategoryAnalysis:
    category: CategoryName
    target_energy_kwh: float
    comparison_energy_kwh: float
    share_percent: float
    change_percent: float | None


@dataclass(frozen=True, slots=True)
class LoadWindow:
    start_at: datetime
    end_at: datetime
    demand_kw: float


@dataclass(frozen=True, slots=True)
class PeakContribution:
    category: CategoryName
    power_kw: float
    share_percent: float


@dataclass(frozen=True, slots=True)
class AnalysisEvidence:
    peak_window: LoadWindow
    peak_contribution: PeakContribution
    overnight_baseload_ratio: float
    dominant_category: CategoryName
    category_changes: tuple[CategoryAnalysis, ...]
    high_load_windows: tuple[LoadWindow, ...]
    comparison_direction: Direction


@dataclass(frozen=True, slots=True)
class EnergyAnalysisResult:
    building_id: str
    target_start_at: datetime
    target_end_at: datetime
    comparison_start_at: datetime
    comparison_end_at: datetime
    target_total_energy_kwh: float
    comparison_total_energy_kwh: float
    period_change_percent: float
    direction: Direction
    peak_demand_kw: float
    peak_at: datetime
    comparison_peak_demand_kw: float
    comparison_peak_at: datetime
    trend_granularity: TrendGranularity
    trend: tuple[TrendPoint, ...]
    categories: tuple[CategoryAnalysis, ...]
    evidence: AnalysisEvidence


def classify_direction(change_percent: float) -> Direction:
    if change_percent <= -3.0:
        return "down"
    if change_percent >= 3.0:
        return "up"
    return "stable"


def _normalize_timestamp(timestamp: datetime) -> datetime:
    if timestamp.tzinfo is None or timestamp.utcoffset() is None:
        raise AnalysisError(
            "INVALID_TIME_RANGE",
            "start_at and end_at must be timezone-aware timestamps.",
        )
    return timestamp.astimezone(BUSINESS_TIMEZONE)


def _is_interval_aligned(timestamp: datetime) -> bool:
    return (
        timestamp.minute % 15 == 0
        and timestamp.second == 0
        and timestamp.microsecond == 0
    )


def _invalid_scope(
    *,
    code: AnalysisErrorCode,
    cause: str,
    building_id: str,
    start_at: datetime,
    end_at: datetime,
    comparison_start_at: datetime,
) -> ScopeValidation:
    return ScopeValidation(
        valid=False,
        building_id=building_id,
        start_at=start_at,
        end_at=end_at,
        comparison_start_at=comparison_start_at,
        comparison_end_at=start_at,
        error_code=code,
        cause=cause,
        action=_ERROR_ACTIONS[code],
    )


class EnergyAnalysisService:
    """Read-only access to one immutable dataset and deterministic calculations."""

    def __init__(self, dataset: MockEnergyDataset) -> None:
        self.dataset = dataset
        self._building_ids = frozenset(
            building.building_id for building in dataset.buildings
        )
        grouped: dict[str, list[EnergyInterval]] = defaultdict(list)
        for interval in dataset.intervals:
            grouped[interval.building_id].append(interval)
        self._series_by_building = {
            building_id: tuple(series) for building_id, series in grouped.items()
        }

    def list_buildings(self) -> tuple[Building, ...]:
        return self.dataset.buildings

    def validate_analysis_scope(
        self,
        building_id: str,
        start_at: datetime,
        end_at: datetime,
    ) -> ScopeValidation:
        try:
            normalized_start = _normalize_timestamp(start_at)
            normalized_end = _normalize_timestamp(end_at)
        except AnalysisError as error:
            safe_start = start_at
            safe_end = end_at
            return _invalid_scope(
                code=error.code,
                cause=error.cause,
                building_id=building_id,
                start_at=safe_start,
                end_at=safe_end,
                comparison_start_at=safe_start,
            )

        duration = normalized_end - normalized_start
        comparison_start = normalized_start - max(duration, timedelta(0))

        if building_id not in self._building_ids:
            return _invalid_scope(
                code="BUILDING_NOT_FOUND",
                cause=f"Building {building_id!r} does not exist in the mock portfolio.",
                building_id=building_id,
                start_at=normalized_start,
                end_at=normalized_end,
                comparison_start_at=comparison_start,
            )

        if (
            duration <= timedelta(0)
            or not _is_interval_aligned(normalized_start)
            or not _is_interval_aligned(normalized_end)
        ):
            return _invalid_scope(
                code="INVALID_TIME_RANGE",
                cause="The analysis range must be positive and aligned to 15-minute boundaries.",
                building_id=building_id,
                start_at=normalized_start,
                end_at=normalized_end,
                comparison_start_at=comparison_start,
            )

        if (
            normalized_start < self.dataset.start_at
            or normalized_end > self.dataset.end_at
        ):
            return _invalid_scope(
                code="TARGET_OUT_OF_RANGE",
                cause="The target period is outside the available dataset window.",
                building_id=building_id,
                start_at=normalized_start,
                end_at=normalized_end,
                comparison_start_at=comparison_start,
            )

        if comparison_start < self.dataset.start_at:
            return _invalid_scope(
                code="COMPARISON_OUT_OF_RANGE",
                cause="The immediately preceding equal-duration comparison period is unavailable.",
                building_id=building_id,
                start_at=normalized_start,
                end_at=normalized_end,
                comparison_start_at=comparison_start,
            )

        return ScopeValidation(
            valid=True,
            building_id=building_id,
            start_at=normalized_start,
            end_at=normalized_end,
            comparison_start_at=comparison_start,
            comparison_end_at=normalized_start,
        )

    def query_energy_series(
        self,
        building_id: str,
        start_at: datetime,
        end_at: datetime,
    ) -> tuple[EnergyInterval, ...]:
        if building_id not in self._building_ids:
            raise AnalysisError(
                "BUILDING_NOT_FOUND",
                f"Building {building_id!r} does not exist in the mock portfolio.",
            )
        normalized_start = _normalize_timestamp(start_at)
        normalized_end = _normalize_timestamp(end_at)
        if normalized_end <= normalized_start:
            raise AnalysisError(
                "INVALID_TIME_RANGE",
                "The query range must have end_at after start_at.",
            )
        return tuple(
            interval
            for interval in self._series_by_building[building_id]
            if normalized_start <= interval.timestamp < normalized_end
        )

    def calculate_energy_analysis(
        self,
        target_series: Sequence[EnergyInterval],
        comparison_series: Sequence[EnergyInterval],
    ) -> EnergyAnalysisResult:
        return calculate_energy_analysis(target_series, comparison_series)


def _assert_measurements_valid(series: Sequence[EnergyInterval]) -> None:
    for interval in series:
        if interval.timestamp.tzinfo is None or interval.timestamp.utcoffset() is None:
            raise AnalysisError(
                "INVALID_MEASUREMENT",
                "Every measurement timestamp must be timezone-aware.",
            )
        values = (
            interval.hvac_kw,
            interval.lighting_kw,
            interval.power_equipment_kw,
            interval.other_kw,
        )
        if any(not math.isfinite(value) or value < 0 for value in values):
            raise AnalysisError(
                "INVALID_MEASUREMENT",
                f"Measurement at {interval.timestamp.isoformat()} contains a negative or non-finite value.",
            )


def _assert_contiguous(
    series: Sequence[EnergyInterval],
    code: Literal["INCOMPLETE_TARGET_DATA", "INCOMPLETE_COMPARISON_DATA"],
) -> None:
    if not series:
        raise AnalysisError(code, "The requested series contains no measurements.")
    for previous, current in zip(series, series[1:], strict=False):
        if current.timestamp - previous.timestamp != INTERVAL_DURATION:
            raise AnalysisError(
                code,
                "The requested series is missing one or more 15-minute intervals.",
            )


def _series_energy(series: Sequence[EnergyInterval]) -> float:
    return sum(interval.total_power_kw * INTERVAL_HOURS for interval in series)


def _category_energy(
    series: Sequence[EnergyInterval], category: CategoryName
) -> float:
    return sum(
        interval.category_power_kw(category) * INTERVAL_HOURS
        for interval in series
    )


def _percent_change(target: float, comparison: float) -> float:
    if comparison <= 0 or not math.isfinite(comparison):
        raise AnalysisError(
            "INVALID_MEASUREMENT",
            "Comparison energy must be positive and finite to calculate period change.",
        )
    if not math.isfinite(target):
        raise AnalysisError(
            "INVALID_MEASUREMENT",
            "Target energy must be finite to calculate period change.",
        )
    return (target - comparison) / comparison * 100


def _optional_percent_change(target: float, comparison: float) -> float | None:
    if comparison == 0:
        return 0.0 if target == 0 else None
    return (target - comparison) / comparison * 100


def _bucket_start(timestamp: datetime, granularity: TrendGranularity) -> datetime:
    if granularity == "hourly":
        return timestamp.replace(minute=0, second=0, microsecond=0)
    return timestamp.replace(hour=0, minute=0, second=0, microsecond=0)


def _build_trend(
    target: Sequence[EnergyInterval],
    comparison: Sequence[EnergyInterval],
) -> tuple[TrendGranularity, tuple[TrendPoint, ...]]:
    duration = len(target) * INTERVAL_DURATION
    granularity: TrendGranularity = (
        "hourly" if duration <= timedelta(hours=48) else "daily"
    )
    comparison_offset = target[0].timestamp - comparison[0].timestamp
    target_buckets: dict[datetime, float] = defaultdict(float)
    comparison_buckets: dict[datetime, float] = defaultdict(float)

    for interval in target:
        target_buckets[_bucket_start(interval.timestamp, granularity)] += (
            interval.total_energy_kwh
        )
    for interval in comparison:
        aligned_timestamp = interval.timestamp + comparison_offset
        comparison_buckets[_bucket_start(aligned_timestamp, granularity)] += (
            interval.total_energy_kwh
        )

    points = tuple(
        TrendPoint(
            start_at=start_at,
            target_energy_kwh=target_buckets[start_at],
            comparison_energy_kwh=comparison_buckets[start_at],
        )
        for start_at in sorted(target_buckets)
    )
    return granularity, points


def _high_load_windows(
    series: Sequence[EnergyInterval], count: int = 3
) -> tuple[LoadWindow, ...]:
    ranked = sorted(series, key=lambda interval: interval.total_power_kw, reverse=True)
    selected: list[EnergyInterval] = []
    for candidate in ranked:
        if all(
            abs(candidate.timestamp - existing.timestamp) >= timedelta(hours=1)
            for existing in selected
        ):
            selected.append(candidate)
        if len(selected) == count:
            break
    return tuple(
        LoadWindow(
            start_at=interval.timestamp,
            end_at=interval.timestamp + INTERVAL_DURATION,
            demand_kw=interval.total_power_kw,
        )
        for interval in selected
    )


def calculate_energy_analysis(
    target_series: Sequence[EnergyInterval],
    comparison_series: Sequence[EnergyInterval],
) -> EnergyAnalysisResult:
    """Calculate all quantitative outputs without language-model involvement."""

    target = tuple(target_series)
    comparison = tuple(comparison_series)
    _assert_measurements_valid(target)
    _assert_measurements_valid(comparison)
    _assert_contiguous(target, "INCOMPLETE_TARGET_DATA")
    _assert_contiguous(comparison, "INCOMPLETE_COMPARISON_DATA")

    if len(target) < len(comparison):
        raise AnalysisError(
            "INCOMPLETE_TARGET_DATA",
            "The target series has fewer intervals than its comparison series.",
        )
    if len(comparison) < len(target):
        raise AnalysisError(
            "INCOMPLETE_COMPARISON_DATA",
            "The comparison series has fewer intervals than its target series.",
        )

    building_ids = {interval.building_id for interval in (*target, *comparison)}
    if len(building_ids) != 1:
        raise AnalysisError(
            "INVALID_MEASUREMENT",
            "Target and comparison measurements must belong to one building.",
        )

    expected_target_start = comparison[-1].timestamp + INTERVAL_DURATION
    if target[0].timestamp != expected_target_start:
        raise AnalysisError(
            "INCOMPLETE_TARGET_DATA",
            "Target data does not begin immediately after the comparison period.",
        )

    target_total = _series_energy(target)
    comparison_total = _series_energy(comparison)
    if target_total <= 0 or not math.isfinite(target_total):
        raise AnalysisError(
            "INVALID_MEASUREMENT",
            "Target energy must be positive and finite to calculate category shares.",
        )
    period_change = _percent_change(target_total, comparison_total)
    direction = classify_direction(period_change)

    peak = max(target, key=lambda interval: interval.total_power_kw)
    comparison_peak = max(
        comparison, key=lambda interval: interval.total_power_kw
    )
    peak_category = max(
        CATEGORY_NAMES,
        key=lambda category: peak.category_power_kw(category),
    )

    category_results = tuple(
        CategoryAnalysis(
            category=category,
            target_energy_kwh=(target_category := _category_energy(target, category)),
            comparison_energy_kwh=(
                comparison_category := _category_energy(comparison, category)
            ),
            share_percent=target_category / target_total * 100,
            change_percent=_optional_percent_change(
                target_category,
                comparison_category,
            ),
        )
        for category in CATEGORY_NAMES
    )
    dominant_category = max(
        category_results,
        key=lambda result: result.target_energy_kwh,
    ).category

    overnight = [
        interval for interval in target if 0 <= interval.timestamp.hour < 6
    ]
    overnight_average_kw = (
        sum(interval.total_power_kw for interval in overnight) / len(overnight)
        if overnight
        else 0.0
    )
    overnight_baseload_ratio = overnight_average_kw / peak.total_power_kw

    trend_granularity, trend = _build_trend(target, comparison)
    peak_window = LoadWindow(
        start_at=peak.timestamp,
        end_at=peak.timestamp + INTERVAL_DURATION,
        demand_kw=peak.total_power_kw,
    )
    peak_contribution = PeakContribution(
        category=peak_category,
        power_kw=peak.category_power_kw(peak_category),
        share_percent=peak.category_power_kw(peak_category)
        / peak.total_power_kw
        * 100,
    )

    return EnergyAnalysisResult(
        building_id=next(iter(building_ids)),
        target_start_at=target[0].timestamp,
        target_end_at=target[-1].timestamp + INTERVAL_DURATION,
        comparison_start_at=comparison[0].timestamp,
        comparison_end_at=comparison[-1].timestamp + INTERVAL_DURATION,
        target_total_energy_kwh=target_total,
        comparison_total_energy_kwh=comparison_total,
        period_change_percent=period_change,
        direction=direction,
        peak_demand_kw=peak.total_power_kw,
        peak_at=peak.timestamp,
        comparison_peak_demand_kw=comparison_peak.total_power_kw,
        comparison_peak_at=comparison_peak.timestamp,
        trend_granularity=trend_granularity,
        trend=trend,
        categories=category_results,
        evidence=AnalysisEvidence(
            peak_window=peak_window,
            peak_contribution=peak_contribution,
            overnight_baseload_ratio=overnight_baseload_ratio,
            dominant_category=dominant_category,
            category_changes=category_results,
            high_load_windows=_high_load_windows(target),
            comparison_direction=direction,
        ),
    )
