"""Offline safety-penalized calibration CLI consuming Wave B negative-sample JSONL datasets."""

from __future__ import annotations

import argparse
from dataclasses import dataclass, field
from datetime import datetime, timezone
import json
import math
from pathlib import Path
import sys
from typing import Any, Iterable

from ai_solver.calibration import CalibrationProfile, SiteCalibration, save_calibration_profile


@dataclass
class _Accumulator:
    total: int = 0
    negatives: int = 0
    manual_overrides: int = 0
    post_control_alarms: int = 0
    tracking_drifts: int = 0
    guardrail_clamped: int = 0
    chw_freq_clamps: list[float] = field(default_factory=list)
    cw_freq_clamps: list[float] = field(default_factory=list)
    fan_speed_clamps: list[float] = field(default_factory=list)

    def add_record(self, rec: dict[str, Any]) -> None:
        self.total += 1
        kind = str(rec.get("sampleKind") or "").strip().upper()
        if not kind:
            return
        self.negatives += 1
        if kind == "MANUAL_OVERRIDE":
            self.manual_overrides += 1
        elif kind == "POST_CONTROL_ALARM":
            self.post_control_alarms += 1
        elif kind == "TRACKING_DRIFT":
            self.tracking_drifts += 1
        elif kind == "GUARDRAIL_CLAMPED":
            self.guardrail_clamped += 1
            self._record_clamped_bias(rec)

    def _record_clamped_bias(self, rec: dict[str, Any]) -> None:
        cap = str(rec.get("capability") or rec.get("targetCapability") or "").strip().upper()
        loop_key = str(rec.get("loopKey") or "").strip().lower()
        delta = rec.get("clampedDelta")
        if isinstance(delta, bool) or not isinstance(delta, (int, float)) or not math.isfinite(float(delta)):
            raw_val = rec.get("rawRecommendedValue")
            clamped_val = rec.get("clampedValue")
            if (
                not isinstance(raw_val, bool)
                and isinstance(raw_val, (int, float))
                and not isinstance(clamped_val, bool)
                and isinstance(clamped_val, (int, float))
            ):
                delta = float(clamped_val) - float(raw_val)
            else:
                return
        shift = float(delta)
        if not math.isfinite(shift):
            return
        if cap == "SET_FAN_SPEED" or "fan" in loop_key:
            self.fan_speed_clamps.append(shift)
        elif cap == "SET_FREQUENCY" or "pump" in loop_key:
            if "cooling" in loop_key or "cw_" in loop_key:
                self.cw_freq_clamps.append(shift)
            else:
                self.chw_freq_clamps.append(shift)

    def to_calibration(self) -> SiteCalibration:
        # Safety-first damping: overrides/alarms tighten required comfort margin and shrink max step.
        comfort_penalty = 0.08 * self.manual_overrides + 0.15 * self.post_control_alarms
        step_reduction = 0.04 * self.manual_overrides + 0.06 * self.post_control_alarms + 0.02 * self.tracking_drifts
        step_scale = 1.0 - step_reduction

        chw_bias = 0.5 * _mean(self.chw_freq_clamps)
        cw_bias = 0.5 * _mean(self.cw_freq_clamps)
        fan_bias = 0.5 * _mean(self.fan_speed_clamps)

        return SiteCalibration(
            comfort_margin_penalty_c=comfort_penalty,
            supply_step_scale=step_scale,
            chw_pump_bias_hz=chw_bias,
            cw_pump_bias_hz=cw_bias,
            tower_fan_bias_pct=fan_bias,
        ).clamped()


def _mean(values: list[float]) -> float:
    if not values:
        return 0.0
    return sum(values) / float(len(values))


def train_profile_from_lines(
    lines: Iterable[str],
    *,
    profile_id: str | None = None,
    now_utc: datetime | None = None,
) -> CalibrationProfile:
    ts = (now_utc or datetime.now(timezone.utc)).astimezone(timezone.utc)
    resolved_profile_id = (profile_id or "").strip() or f"calibrated-{ts.strftime('%Y%m%dT%H%M%SZ')}"

    global_acc = _Accumulator()
    site_accs: dict[str, _Accumulator] = {}

    for line in lines:
        raw_line = line.strip()
        if not raw_line:
            continue
        try:
            parsed = json.loads(raw_line)
        except json.JSONDecodeError:
            continue
        if not isinstance(parsed, dict):
            continue
        global_acc.add_record(parsed)
        site_id = str(parsed.get("siteId") or "").strip()
        if site_id:
            acc = site_accs.get(site_id)
            if acc is None:
                acc = _Accumulator()
                site_accs[site_id] = acc
            acc.add_record(parsed)

    site_overrides = {site_id: acc.to_calibration() for site_id, acc in sorted(site_accs.items())}
    return CalibrationProfile(
        schema_version=1,
        profile_id=resolved_profile_id,
        calibrated_at=ts.isoformat().replace("+00:00", "Z"),
        sample_count=global_acc.total,
        negative_sample_count=global_acc.negatives,
        global_calibration=global_acc.to_calibration(),
        site_overrides=site_overrides,
    )


def train_profile_from_file(
    input_path: str | Path,
    output_path: str | Path,
    *,
    profile_id: str | None = None,
    now_utc: datetime | None = None,
) -> CalibrationProfile:
    text = Path(input_path).read_text(encoding="utf-8")
    profile = train_profile_from_lines(text.splitlines(), profile_id=profile_id, now_utc=now_utc)
    save_calibration_profile(profile, output_path)
    return profile


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Fit bounded HVAC calibration profile from Wave B JSONL samples.")
    parser.add_argument("--input", required=True, help="Path to exported Wave B negative-samples .jsonl file")
    parser.add_argument("--output", required=True, help="Output path for fitted calibration profile JSON")
    parser.add_argument("--profile-id", default="", help="Optional calibration profile identifier")
    args = parser.parse_args(argv)

    profile = train_profile_from_file(
        args.input,
        args.output,
        profile_id=args.profile_id or None,
    )
    print(
        json.dumps(
            {
                "profileId": profile.profile_id,
                "sampleCount": profile.sample_count,
                "negativeSampleCount": profile.negative_sample_count,
                "siteCount": len(profile.site_overrides),
                "output": str(args.output),
            },
            separators=(",", ":"),
        )
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
