"""Deterministic resolution of supported EnergyAgent analysis scopes."""

from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime, time, timedelta
import re
from typing import Sequence

from langchain_core.messages import BaseMessage, HumanMessage

from src.energy.data import BUSINESS_TIMEZONE, MockEnergyDataset, align_to_interval
from src.workflow.contracts import AnalysisRequest, ResolvedScope, ScopePreset, parse_iso_datetime


@dataclass(frozen=True, slots=True)
class ScopeResolutionError(ValueError):
    code: str
    cause: str
    action: str

    def __str__(self) -> str:
        return f"[{self.code}] {self.cause}\nAction: {self.action}"


_BUILDING_PATTERNS = (
    re.compile(r"\bbuilding\s*([abc])\b", re.IGNORECASE),
    re.compile(r"([abc])\s*(?:栋|樓|楼|號樓|号楼)", re.IGNORECASE),
    re.compile(r"(?:建筑|樓宇|楼宇)\s*([abc])", re.IGNORECASE),
)
_ISO_PATTERN = re.compile(
    r"\d{4}-\d{2}-\d{2}(?:[T ]\d{2}:\d{2}(?::\d{2})?(?:Z|[+-]\d{2}:\d{2})?)?"
)


def _message_text(message: BaseMessage) -> str:
    if isinstance(message.content, str):
        return message.content
    if isinstance(message.content, list):
        parts: list[str] = []
        for item in message.content:
            if isinstance(item, str):
                parts.append(item)
            elif isinstance(item, dict) and isinstance(item.get("text"), str):
                parts.append(item["text"])
        return " ".join(parts)
    return str(message.content)


def latest_user_message(messages: Sequence[BaseMessage]) -> str | None:
    for message in reversed(messages):
        if isinstance(message, HumanMessage) or getattr(message, "type", None) == "human":
            text = _message_text(message).strip()
            if text:
                return text
    return None


def _extract_building(text: str) -> str | None:
    for pattern in _BUILDING_PATTERNS:
        match = pattern.search(text)
        if match:
            return match.group(1).upper()
    return None


def _extract_preset(text: str) -> ScopePreset | None:
    normalized = re.sub(r"\s+", " ", text.lower()).strip()
    if "昨天" in normalized or "yesterday" in normalized:
        return "yesterday"
    if (
        re.search(r"(?:last|past)\s*24\s*hours?", normalized)
        or re.search(r"(?:最近|过去|過去)\s*24\s*(?:小时|小時)", normalized)
    ):
        return "last_24_hours"
    if (
        re.search(r"(?:last|past)\s*7\s*days?", normalized)
        or re.search(r"(?:最近|过去|過去)\s*7\s*天", normalized)
    ):
        return "last_7_days"
    return None


def _parse_chat_datetime(value: str) -> datetime:
    parsed = parse_iso_datetime(value.replace(" ", "T", 1) if " " in value else value)
    if parsed.tzinfo is None or parsed.utcoffset() is None:
        if "T" in value or " " in value:
            raise ScopeResolutionError(
                "INVALID_TIME_RANGE",
                "Custom date-time values in chat must include an explicit timezone offset.",
                "Include an offset such as +08:00, or provide date-only boundaries.",
            )
        parsed = datetime.combine(parsed.date(), time.min, tzinfo=BUSINESS_TIMEZONE)
    return parsed.astimezone(BUSINESS_TIMEZONE)


def _extract_custom_range(text: str) -> tuple[datetime, datetime] | None:
    matches = _ISO_PATTERN.findall(text)
    if len(matches) < 2:
        return None
    return _parse_chat_datetime(matches[0]), _parse_chat_datetime(matches[1])


def _resolve_preset(
    preset: ScopePreset,
    reference_time: datetime,
) -> tuple[datetime, datetime]:
    anchor = align_to_interval(reference_time)
    if preset == "last_24_hours":
        return anchor - timedelta(hours=24), anchor
    if preset == "last_7_days":
        return anchor - timedelta(days=7), anchor
    if preset == "yesterday":
        today = datetime.combine(anchor.date(), time.min, tzinfo=BUSINESS_TIMEZONE)
        return today - timedelta(days=1), today
    raise ScopeResolutionError(
        "INVALID_TIME_RANGE",
        "The custom preset requires explicit start_at and end_at values.",
        "Provide both custom boundaries as timezone-aware ISO 8601 timestamps.",
    )


def _reference_time(request: AnalysisRequest, dataset: MockEnergyDataset) -> datetime:
    raw_reference = request.get("reference_time")
    if not raw_reference:
        return dataset.end_at
    try:
        parsed = parse_iso_datetime(raw_reference)
    except ValueError as error:
        raise ScopeResolutionError(
            "INVALID_TIME_RANGE",
            "reference_time is not a valid ISO 8601 timestamp.",
            "Provide reference_time with an explicit timezone offset.",
        ) from error
    if parsed.tzinfo is None or parsed.utcoffset() is None:
        raise ScopeResolutionError(
            "INVALID_TIME_RANGE",
            "reference_time must be timezone-aware.",
            "Provide reference_time with an explicit timezone offset.",
        )
    return parsed.astimezone(BUSINESS_TIMEZONE)


def _resolve_explicit_request(
    request: AnalysisRequest,
    dataset: MockEnergyDataset,
) -> ResolvedScope:
    building_id = request.get("building_id", "").strip().upper()
    if not building_id:
        raise ScopeResolutionError(
            "SCOPE_RESOLUTION_REQUIRED",
            "The analysis request does not specify a Building.",
            "Specify Building A, B, or C before starting analysis.",
        )

    preset = request.get("preset")
    raw_start = request.get("start_at")
    raw_end = request.get("end_at")
    if raw_start or raw_end:
        if not raw_start or not raw_end:
            raise ScopeResolutionError(
                "SCOPE_RESOLUTION_REQUIRED",
                "A custom range requires both start_at and end_at.",
                "Provide both timezone-aware ISO 8601 boundaries.",
            )
        try:
            start_at = parse_iso_datetime(raw_start)
            end_at = parse_iso_datetime(raw_end)
        except ValueError as error:
            raise ScopeResolutionError(
                "INVALID_TIME_RANGE",
                "The custom range contains an invalid ISO 8601 timestamp.",
                "Correct start_at and end_at and include explicit timezone offsets.",
            ) from error
    elif preset:
        start_at, end_at = _resolve_preset(preset, _reference_time(request, dataset))
    else:
        raise ScopeResolutionError(
            "SCOPE_RESOLUTION_REQUIRED",
            "The analysis request does not specify a supported time range.",
            "Choose yesterday, last 24 hours, last 7 days, or a custom range.",
        )

    return {
        "building_id": building_id,
        "start_at": start_at.isoformat(),
        "end_at": end_at.isoformat(),
        "timezone": "Asia/Shanghai",
        "source": request.get("source", "canvas"),
    }


def _resolve_chat_request(
    text: str,
    dataset: MockEnergyDataset,
) -> ResolvedScope:
    building_id = _extract_building(text)
    preset = _extract_preset(text)
    custom_range = _extract_custom_range(text)

    missing: list[str] = []
    if not building_id:
        missing.append("Building A, B, or C")
    if not preset and not custom_range:
        missing.append("yesterday, last 24 hours, last 7 days, or a custom range")
    if missing:
        raise ScopeResolutionError(
            "SCOPE_RESOLUTION_REQUIRED",
            "The natural-language request is incomplete.",
            "Specify " + " and ".join(missing) + ".",
        )

    if custom_range:
        start_at, end_at = custom_range
    else:
        start_at, end_at = _resolve_preset(preset, dataset.end_at)

    return {
        "building_id": building_id,
        "start_at": start_at.isoformat(),
        "end_at": end_at.isoformat(),
        "timezone": "Asia/Shanghai",
        "source": "chat",
    }


def resolve_analysis_scope(
    *,
    request: AnalysisRequest | None,
    messages: Sequence[BaseMessage],
    dataset: MockEnergyDataset,
) -> ResolvedScope:
    """Resolve an explicit canvas request or the latest complete chat request."""

    if request:
        return _resolve_explicit_request(request, dataset)

    text = latest_user_message(messages)
    if not text:
        raise ScopeResolutionError(
            "SCOPE_RESOLUTION_REQUIRED",
            "No analysis request was provided.",
            "Specify a Building and one supported time range.",
        )
    return _resolve_chat_request(text, dataset)
