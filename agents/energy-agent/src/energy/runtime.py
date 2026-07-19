"""Process-scoped initialization of the immutable energy dataset."""

from __future__ import annotations

from datetime import datetime

from src.config import RuntimeConfigurationError, settings
from src.energy.analysis import EnergyAnalysisService
from src.energy.data import BUSINESS_TIMEZONE, generate_mock_dataset


def _initialize_energy_service() -> EnergyAnalysisService:
    try:
        dataset = generate_mock_dataset(
            seed=settings.energy_data_seed,
            reference_time=datetime.now(BUSINESS_TIMEZONE),
        )
        return EnergyAnalysisService(dataset)
    except RuntimeConfigurationError:
        raise
    except Exception as error:
        raise RuntimeConfigurationError(
            "DATASET_INITIALIZATION_FAILED",
            "The immutable mock energy dataset could not be initialized.",
            "Verify ENERGY_DATA_SEED and restart the Agent process.",
        ) from error


energy_service = _initialize_energy_service()
