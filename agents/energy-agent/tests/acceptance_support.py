"""Shared deterministic insight model for acceptance evidence and recording."""

from __future__ import annotations

import json
from typing import Any


class AcceptanceInsightModel:
    """Return reviewable deterministic insights without an external model request."""

    def __init__(self) -> None:
        self.calls = 0

    def invoke(self, messages: list[Any], config: dict[str, Any] | None = None) -> dict[str, Any]:
        self.calls += 1
        payload = json.loads(messages[-1].content)
        metrics = payload["metrics"]
        direction = metrics["direction"]
        building_id = payload["building_id"]
        direction_copy = {
            "up": ("increased", "Review the increase"),
            "down": ("decreased", "Validate the improvement"),
            "stable": ("remained stable", "Maintain the current operating pattern"),
        }
        verb, recommendation_title = direction_copy[direction]
        return {
            "unable_to_conclude": False,
            "inability_reason": None,
            "findings": [
                {
                    "finding_id": "finding-period-change",
                    "title": f"Building {building_id} energy {verb}",
                    "summary": (
                        f"The deterministic comparison classified Building {building_id} as "
                        f"{direction} for the selected target period."
                    ),
                    "evidence_refs": ["period_change", "category_change:hvac"],
                    "hypothesis": (
                        "A possible operational cause requires independent verification."
                    ),
                },
                {
                    "finding_id": "finding-peak-window",
                    "title": "Peak demand window is reviewable",
                    "summary": "The peak interval and its leading category are available as Evidence.",
                    "evidence_refs": ["peak_window", "peak_contribution"],
                    "hypothesis": None,
                },
            ],
            "recommendations": [
                {
                    "recommendation_id": "recommendation-period-change",
                    "finding_id": "finding-period-change",
                    "title": recommendation_title,
                    "description": (
                        "Compare operating schedules and controls during the Evidence-backed "
                        "high-load windows before changing equipment settings."
                    ),
                },
                {
                    "recommendation_id": "recommendation-peak-window",
                    "finding_id": "finding-peak-window",
                    "title": "Inspect the peak operating window",
                    "description": (
                        "Review the identified peak interval and verify whether the dominant "
                        "category matches expected operations."
                    ),
                },
            ],
        }
