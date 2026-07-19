from __future__ import annotations

import json
from pathlib import Path
import re
import unittest


class AcceptanceArtifactTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        monorepo_root = Path(__file__).resolve().parents[3]
        cls.root = monorepo_root / "references" / "energy-agent-next"
        cls.agent_root = monorepo_root / "agents" / "energy-agent"
        cls.evidence = json.loads(
            (cls.root / "docs" / "acceptance" / "evidence.json").read_text(
                encoding="utf-8"
            )
        )

    def test_acceptance_graph_is_isolated_from_production_graph(self) -> None:
        production = (self.agent_root / "main.py").read_text(encoding="utf-8")
        acceptance = (self.agent_root / "langgraph.acceptance.json").read_text(
            encoding="utf-8"
        )
        support = (self.agent_root / "tests" / "acceptance_support.py").read_text(
            encoding="utf-8"
        )
        self.assertIn("graph = build_energy_graph()", production)
        self.assertNotIn("AcceptanceInsightModel", production)
        self.assertIn("tests/acceptance_agent.py:graph", acceptance)
        self.assertIn("AcceptanceInsightModel", support)

    def test_package_exposes_repeatable_record_and_verify_commands(self) -> None:
        package = json.loads((self.root / "package.json").read_text(encoding="utf-8"))
        self.assertEqual(package["scripts"]["record:acceptance"], "node scripts/record-acceptance.mjs")
        self.assertEqual(
            package["scripts"]["test:acceptance"],
            "uv run --project ../../agents/energy-agent python scripts/verify-acceptance.py",
        )
        self.assertIn("@playwright/test", package["devDependencies"])

    def test_structured_evidence_contains_all_scenarios_and_primary_journey(self) -> None:
        self.assertEqual(set(self.evidence["scenarios"]), {"A", "B", "C"})
        self.assertEqual(self.evidence["scenarios"]["A"]["direction_label"], "Down")
        self.assertEqual(self.evidence["scenarios"]["B"]["direction_label"], "Up")
        self.assertEqual(self.evidence["scenarios"]["C"]["direction_label"], "Stable")
        self.assertEqual(len(self.evidence["primary_journey"]), 5)
        self.assertFalse(self.evidence["error_path"]["partial_result_published"])
        self.assertEqual(self.evidence["stale_run"]["workflow_status"], "discarded")

    def test_acceptance_matrix_covers_every_canonical_row(self) -> None:
        canonical = (self.root / "docs" / "specs" / "mvp-acceptance.md").read_text(
            encoding="utf-8"
        )
        matrix = (self.root / "docs" / "acceptance" / "matrix.md").read_text(
            encoding="utf-8"
        )
        canonical_ids = re.findall(r"^\| ([A-Z]+-\d{3}) \|", canonical, re.MULTILINE)
        matrix_ids = re.findall(r"^\| ([A-Z]+-\d{3}) \|", matrix, re.MULTILINE)
        self.assertEqual(matrix_ids, canonical_ids)
        self.assertEqual(len(matrix_ids), 56)
        self.assertNotIn("Pending", matrix)

    def test_visual_artifacts_and_recording_assertions_exist(self) -> None:
        acceptance = self.root / "docs" / "acceptance"
        required = {
            "agent-unavailable.png",
            "primary-journey.webm",
            "primary-journey-desktop.png",
            "primary-journey-responsive.png",
            "recording-log.json",
        }
        for name in required:
            with self.subTest(name=name):
                path = acceptance / name
                self.assertTrue(path.is_file())
                self.assertGreater(path.stat().st_size, 100)
        log = json.loads((acceptance / "recording-log.json").read_text(encoding="utf-8"))
        self.assertTrue(log["assertions"]["agent_unavailable_actions_disabled"])
        self.assertEqual(log["assertions"]["building_b_direction"], "Up")
        self.assertEqual(
            log["assertions"]["finding_reviews"], ["confirmed", "needs_review"]
        )
        self.assertTrue(log["assertions"]["recommendation_bookmarked"])
        self.assertTrue(log["assertions"]["scope_invalidation"])
        self.assertEqual(log["assertions"]["building_a_direction"], "Down")
        self.assertTrue(log["assertions"]["responsive_same_route"])
        self.assertFalse(log["assertions"]["secret_marker_leaked"])


if __name__ == "__main__":
    unittest.main()
