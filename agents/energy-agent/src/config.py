"""Runtime configuration for the EnergyAgent process."""

from dataclasses import dataclass
import os
from pathlib import Path

from dotenv import load_dotenv


ROOT_DIR = Path(__file__).resolve().parents[2]
load_dotenv(ROOT_DIR / ".env.local", override=False)


class RuntimeConfigurationError(RuntimeError):
    """A startup error with a stable code and corrective action."""

    def __init__(self, code: str, cause: str, action: str) -> None:
        super().__init__(f"[{code}] {cause}\nAction: {action}")
        self.code = code
        self.cause = cause
        self.action = action


def _required(name: str, code: str) -> str:
    value = os.environ.get(name, "").strip()
    if not value:
        raise RuntimeConfigurationError(
            code,
            f"Required environment variable {name} is missing.",
            f"Set {name} in the shell environment or root .env.local file.",
        )
    return value


def _energy_data_seed() -> int:
    raw_value = os.environ.get("ENERGY_DATA_SEED", "20260716").strip()
    try:
        return int(raw_value)
    except ValueError as error:
        raise RuntimeConfigurationError(
            "INVALID_ENERGY_DATA_SEED",
            "ENERGY_DATA_SEED must be an integer.",
            "Set ENERGY_DATA_SEED to an integer such as 20260716.",
        ) from error


@dataclass(frozen=True)
class Settings:
    model_name: str
    energy_data_seed: int
    log_level: str


_required("OPENAI_API_KEY", "MISSING_OPENAI_API_KEY")
settings = Settings(
    model_name=_required("OPENAI_MODEL", "MISSING_OPENAI_MODEL"),
    energy_data_seed=_energy_data_seed(),
    log_level=os.environ.get("LOG_LEVEL", "info").strip() or "info",
)
