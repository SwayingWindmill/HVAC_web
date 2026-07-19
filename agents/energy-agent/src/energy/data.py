"""Immutable deterministic mock energy data for the EnergyAgent MVP."""

from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime, timedelta
import math
import random
from typing import Final, Literal
from zoneinfo import ZoneInfo


BUSINESS_TIMEZONE: Final = ZoneInfo("Asia/Shanghai")
INTERVAL_DURATION: Final = timedelta(minutes=15)
INTERVAL_HOURS: Final = 0.25
COVERAGE_DAYS: Final = 60
INTERVALS_PER_DAY: Final = 96

CategoryName = Literal["HVAC", "Lighting", "Power Equipment", "Other"]
CATEGORY_NAMES: Final[tuple[CategoryName, ...]] = (
    "HVAC",
    "Lighting",
    "Power Equipment",
    "Other",
)


@dataclass(frozen=True, slots=True)
class Building:
    """A selectable building in the mock portfolio."""

    building_id: str
    name: str
    timezone: str = "Asia/Shanghai"


@dataclass(frozen=True, slots=True)
class EnergyInterval:
    """One immutable 15-minute category-power observation."""

    building_id: str
    timestamp: datetime
    hvac_kw: float
    lighting_kw: float
    power_equipment_kw: float
    other_kw: float

    @property
    def total_power_kw(self) -> float:
        return (
            self.hvac_kw
            + self.lighting_kw
            + self.power_equipment_kw
            + self.other_kw
        )

    @property
    def total_energy_kwh(self) -> float:
        return self.total_power_kw * INTERVAL_HOURS

    def category_power_kw(self, category: CategoryName) -> float:
        values = {
            "HVAC": self.hvac_kw,
            "Lighting": self.lighting_kw,
            "Power Equipment": self.power_equipment_kw,
            "Other": self.other_kw,
        }
        return values[category]


@dataclass(frozen=True, slots=True)
class MockEnergyDataset:
    """An immutable in-memory snapshot generated once per Agent process."""

    seed: int
    start_at: datetime
    end_at: datetime
    buildings: tuple[Building, ...]
    intervals: tuple[EnergyInterval, ...]

    def query(
        self,
        building_id: str,
        start_at: datetime,
        end_at: datetime,
    ) -> tuple[EnergyInterval, ...]:
        return tuple(
            interval
            for interval in self.intervals
            if interval.building_id == building_id
            and start_at <= interval.timestamp < end_at
        )


_BUILDINGS: Final[tuple[Building, ...]] = (
    Building("A", "Building A"),
    Building("B", "Building B"),
    Building("C", "Building C"),
)

_BUILDING_SCALE: Final[dict[str, float]] = {
    "A": 1.00,
    "B": 1.14,
    "C": 0.88,
}


def align_to_interval(timestamp: datetime) -> datetime:
    """Floor a timezone-aware timestamp to a 15-minute boundary."""

    if timestamp.tzinfo is None or timestamp.utcoffset() is None:
        raise ValueError("reference_time must be timezone-aware")
    localized = timestamp.astimezone(BUSINESS_TIMEZONE)
    minute = localized.minute - localized.minute % 15
    return localized.replace(minute=minute, second=0, microsecond=0)


def _occupancy(timestamp: datetime) -> float:
    hour = timestamp.hour + timestamp.minute / 60
    if timestamp.weekday() >= 5:
        return 0.28 if 9 <= hour < 18 else 0.05
    if 6 <= hour < 8:
        return (hour - 6) / 2
    if 8 <= hour < 18:
        return 1.0
    if 18 <= hour < 21:
        return max(0.0, 1 - (hour - 18) / 3)
    return 0.0


def _scenario_factors(
    building_id: str,
    day_index: int,
    timestamp: datetime,
) -> tuple[float, float, float, float]:
    """Apply the documented A/B/C scenario to the most recent seven days."""

    if day_index < COVERAGE_DAYS - 7:
        return (1.0, 1.0, 1.0, 1.0)

    hour = timestamp.hour + timestamp.minute / 60
    if building_id == "A":
        return (0.87, 0.95, 0.96, 1.0)
    if building_id == "B":
        afternoon_boost = 1.05 if 13 <= hour < 18 else 1.0
        return (1.16 * afternoon_boost, 1.03, 1.03, 1.02)
    return (1.01, 1.0, 1.0, 1.0)


def _generate_interval(
    *,
    building_id: str,
    timestamp: datetime,
    day_index: int,
    random_source: random.Random,
) -> EnergyInterval:
    hour = timestamp.hour + timestamp.minute / 60
    occupancy = _occupancy(timestamp)
    afternoon_peak = math.exp(-((hour - 15.0) / 2.2) ** 2)
    seasonal_wave = math.sin(2 * math.pi * day_index / 30)
    building_scale = _BUILDING_SCALE[building_id]

    hvac = building_scale * (
        34.0
        + 62.0 * occupancy
        + 35.0 * afternoon_peak
        + 8.0 * seasonal_wave
    )
    lighting = building_scale * (10.0 + 31.0 * occupancy)
    equipment = building_scale * (18.0 + 27.0 * occupancy)
    other = building_scale * (9.0 + 1.5 * math.sin(2 * math.pi * hour / 24))

    factors = _scenario_factors(building_id, day_index, timestamp)
    values = [hvac, lighting, equipment, other]
    adjusted = [
        value * factor * (1 + random_source.uniform(-0.025, 0.025))
        for value, factor in zip(values, factors, strict=True)
    ]

    return EnergyInterval(
        building_id=building_id,
        timestamp=timestamp,
        hvac_kw=adjusted[0],
        lighting_kw=adjusted[1],
        power_equipment_kw=adjusted[2],
        other_kw=adjusted[3],
    )


def generate_mock_dataset(
    *,
    seed: int,
    reference_time: datetime,
) -> MockEnergyDataset:
    """Generate a reproducible 60-day, 15-minute portfolio snapshot."""

    end_at = align_to_interval(reference_time)
    start_at = end_at - timedelta(days=COVERAGE_DAYS)
    intervals: list[EnergyInterval] = []

    for building_index, building in enumerate(_BUILDINGS):
        random_source = random.Random(seed + (building_index + 1) * 100_003)
        for interval_index in range(COVERAGE_DAYS * INTERVALS_PER_DAY):
            timestamp = start_at + interval_index * INTERVAL_DURATION
            day_index = interval_index // INTERVALS_PER_DAY
            intervals.append(
                _generate_interval(
                    building_id=building.building_id,
                    timestamp=timestamp,
                    day_index=day_index,
                    random_source=random_source,
                )
            )

    return MockEnergyDataset(
        seed=seed,
        start_at=start_at,
        end_at=end_at,
        buildings=_BUILDINGS,
        intervals=tuple(intervals),
    )
