import importlib
import os
import sys
import unittest
from unittest.mock import patch

from fastapi.testclient import TestClient

from src.http import app


class HealthRouteTests(unittest.TestCase):
    def test_health_route_reports_ready(self) -> None:
        response = TestClient(app).get("/health")

        self.assertEqual(response.status_code, 200)
        self.assertEqual(
            response.json(),
            {
                "status": "ok",
                "service": "energyagent-agent",
            },
        )


class ConfigurationTests(unittest.TestCase):
    def _import_config(self, environment: dict[str, str]):
        sys.modules.pop("src.config", None)
        with (
            patch.dict(os.environ, environment, clear=True),
            patch("dotenv.load_dotenv"),
        ):
            return importlib.import_module("src.config")

    def test_documented_defaults_and_required_values_load(self) -> None:
        config = self._import_config(
            {
                "OPENAI_API_KEY": "x",
                "OPENAI_MODEL": "test-model",
            }
        )

        self.assertEqual(config.settings.model_name, "test-model")
        self.assertEqual(config.settings.energy_data_seed, 20260716)
        self.assertEqual(config.settings.log_level, "info")

    def test_missing_api_key_has_stable_error(self) -> None:
        with self.assertRaisesRegex(RuntimeError, "MISSING_OPENAI_API_KEY"):
            self._import_config({"OPENAI_MODEL": "test-model"})

    def test_missing_model_has_stable_error(self) -> None:
        with self.assertRaisesRegex(RuntimeError, "MISSING_OPENAI_MODEL"):
            self._import_config({"OPENAI_API_KEY": "x"})

    def test_invalid_seed_has_stable_error(self) -> None:
        with self.assertRaisesRegex(RuntimeError, "INVALID_ENERGY_DATA_SEED"):
            self._import_config(
                {
                    "OPENAI_API_KEY": "x",
                    "OPENAI_MODEL": "test-model",
                    "ENERGY_DATA_SEED": "not-an-integer",
                }
            )


if __name__ == "__main__":
    unittest.main()
