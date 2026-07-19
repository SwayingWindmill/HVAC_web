"""Deterministic LangGraph entry point used only by acceptance recording."""

from tests.acceptance_support import AcceptanceInsightModel

from src.workflow.graph import build_energy_graph


graph = build_energy_graph(insight_model=AcceptanceInsightModel())
