"""Approved deterministic energy tool surface."""

from __future__ import annotations

from datetime import datetime
from typing import Sequence

from src.energy.analysis import (
    EnergyAnalysisResult,
    ScopeValidation,
)
from src.energy.data import Building, EnergyInterval
from src.energy.runtime import energy_service


def list_buildings() -> list[Building]:
    """Return the buildings available in the immutable mock portfolio."""

    return list(energy_service.list_buildings())


def validate_analysis_scope(
    building_id: str,
    start_at: datetime,
    end_at: datetime,
) -> ScopeValidation:
    """Validate target and immediately preceding comparison periods."""

    return energy_service.validate_analysis_scope(building_id, start_at, end_at)


def query_energy_series(
    building_id: str,
    start_at: datetime,
    end_at: datetime,
) -> list[EnergyInterval]:
    """Return raw 15-minute observations for one validated range."""

    return list(energy_service.query_energy_series(building_id, start_at, end_at))


def calculate_energy_analysis(
    target_series: Sequence[EnergyInterval],
    comparison_series: Sequence[EnergyInterval],
) -> EnergyAnalysisResult:
    """Calculate metrics and Evidence without language-model involvement."""

    return energy_service.calculate_energy_analysis(target_series, comparison_series)
