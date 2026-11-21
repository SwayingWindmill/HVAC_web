"""Safe bounded calibration profiles for HVACPhysicsSolver."""

from __future__ import annotations

from dataclasses import dataclass, field
import json
import math
from pathlib import Path
from typing import Any

MAX_COMFORT_MARGIN_PENALTY_C = 1.5
MIN_SUPPLY_STEP_SCALE = 0.5
MAX_SUPPLY_STEP_SCALE = 1.0
MAX_PUMP_BIAS_HZ = 5.0
MAX_TOWER_FAN_BIAS_PCT = 10.0


def clamp_finite(raw: Any, lo: float, hi: float, default: float) -> float:
    if isinstance(raw, bool) or not isinstance(raw, (int, float)):
        return default
    val = float(raw)
    if not math.isfinite(val):
        return default
    return max(lo, min(hi, val))


@dataclass(frozen=True)
class SiteCalibration:
    comfort_margin_penalty_c: float = 0.0
    supply_step_scale: float = 1.0
    chw_pump_bias_hz: float = 0.0
    cw_pump_bias_hz: float = 0.0
    tower_fan_bias_pct: float = 0.0

    def clamped(self) -> "SiteCalibration":
        return SiteCalibration(
            comfort_margin_penalty_c=round(
                clamp_finite(self.comfort_margin_penalty_c, 0.0, MAX_COMFORT_MARGIN_PENALTY_C, 0.0), 4
            ),
            supply_step_scale=round(
                clamp_finite(self.supply_step_scale, MIN_SUPPLY_STEP_SCALE, MAX_SUPPLY_STEP_SCALE, 1.0), 4
            ),
            chw_pump_bias_hz=round(clamp_finite(self.chw_pump_bias_hz, -MAX_PUMP_BIAS_HZ, MAX_PUMP_BIAS_HZ, 0.0), 4),
            cw_pump_bias_hz=round(clamp_finite(self.cw_pump_bias_hz, -MAX_PUMP_BIAS_HZ, MAX_PUMP_BIAS_HZ, 0.0), 4),
            tower_fan_bias_pct=round(
                clamp_finite(self.tower_fan_bias_pct, -MAX_TOWER_FAN_BIAS_PCT, MAX_TOWER_FAN_BIAS_PCT, 0.0), 4
            ),
        )

    def to_dict(self) -> dict[str, float]:
        c = self.clamped()
        return {
            "comfortMarginPenaltyC": c.comfort_margin_penalty_c,
            "supplyStepScale": c.supply_step_scale,
            "chwPumpBiasHz": c.chw_pump_bias_hz,
            "cwPumpBiasHz": c.cw_pump_bias_hz,
            "towerFanBiasPct": c.tower_fan_bias_pct,
        }

    @classmethod
    def from_dict(cls, raw: Any) -> "SiteCalibration":
        if not isinstance(raw, dict):
            return cls()
        return cls(
            comfort_margin_penalty_c=clamp_finite(
                raw.get("comfortMarginPenaltyC"), 0.0, MAX_COMFORT_MARGIN_PENALTY_C, 0.0
            ),
            supply_step_scale=clamp_finite(raw.get("supplyStepScale"), MIN_SUPPLY_STEP_SCALE, MAX_SUPPLY_STEP_SCALE, 1.0),
            chw_pump_bias_hz=clamp_finite(raw.get("chwPumpBiasHz"), -MAX_PUMP_BIAS_HZ, MAX_PUMP_BIAS_HZ, 0.0),
            cw_pump_bias_hz=clamp_finite(raw.get("cwPumpBiasHz"), -MAX_PUMP_BIAS_HZ, MAX_PUMP_BIAS_HZ, 0.0),
            tower_fan_bias_pct=clamp_finite(
                raw.get("towerFanBiasPct"), -MAX_TOWER_FAN_BIAS_PCT, MAX_TOWER_FAN_BIAS_PCT, 0.0
            ),
        ).clamped()


@dataclass(frozen=True)
class CalibrationProfile:
    schema_version: int = 1
    profile_id: str = ""
    calibrated_at: str = ""
    sample_count: int = 0
    negative_sample_count: int = 0
    global_calibration: SiteCalibration = field(default_factory=SiteCalibration)
    site_overrides: dict[str, SiteCalibration] = field(default_factory=dict)

    def calibration_for_site(self, site_id: str) -> SiteCalibration:
        key = (site_id or "").strip()
        if key and key in self.site_overrides:
            return self.site_overrides[key].clamped()
        return self.global_calibration.clamped()

    def to_dict(self) -> dict[str, Any]:
        return {
            "schemaVersion": self.schema_version,
            "profileId": self.profile_id,
            "calibratedAt": self.calibrated_at,
            "sampleCount": self.sample_count,
            "negativeSampleCount": self.negative_sample_count,
            "globalCalibration": self.global_calibration.to_dict(),
            "siteOverrides": {
                site_id: cal.to_dict() for site_id, cal in sorted(self.site_overrides.items())
            },
        }

    @classmethod
    def from_dict(cls, raw: Any) -> "CalibrationProfile":
        if not isinstance(raw, dict):
            raise ValueError("calibration profile JSON must be an object")
        schema_version = raw.get("schemaVersion", 1)
        if isinstance(schema_version, bool) or not isinstance(schema_version, int) or schema_version != 1:
            raise ValueError(f"unsupported calibration profile schemaVersion: {schema_version!r}")
        raw_profile_id = str(raw.get("profileId") or "").strip()
        if not raw_profile_id:
            raise ValueError("calibration profile profileId must be non-empty")
        raw_overrides = raw.get("siteOverrides") or {}
        if not isinstance(raw_overrides, dict):
            raise ValueError("calibration profile siteOverrides must be an object")
        overrides: dict[str, SiteCalibration] = {}
        for site_id, cal_raw in raw_overrides.items():
            key = str(site_id).strip()
            if key:
                overrides[key] = SiteCalibration.from_dict(cal_raw)
        sample_count = raw.get("sampleCount", 0)
        neg_count = raw.get("negativeSampleCount", 0)
        return cls(
            schema_version=1,
            profile_id=raw_profile_id,
            calibrated_at=str(raw.get("calibratedAt") or "").strip(),
            sample_count=int(sample_count) if isinstance(sample_count, int) and not isinstance(sample_count, bool) and sample_count >= 0 else 0,
            negative_sample_count=int(neg_count) if isinstance(neg_count, int) and not isinstance(neg_count, bool) and neg_count >= 0 else 0,
            global_calibration=SiteCalibration.from_dict(raw.get("globalCalibration")),
            site_overrides=overrides,
        )


def load_calibration_profile(path: str | Path) -> CalibrationProfile:
    text = Path(path).read_text(encoding="utf-8")
    payload = json.loads(text)
    return CalibrationProfile.from_dict(payload)


def save_calibration_profile(profile: CalibrationProfile, path: str | Path) -> None:
    target = Path(path)
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(json.dumps(profile.to_dict(), indent=2, sort_keys=False) + "\n", encoding="utf-8")
