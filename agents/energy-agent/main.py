"""LangGraph entry point for the explicit EnergyAgent workflow."""

from src.workflow.graph import build_energy_graph


graph = build_energy_graph()
