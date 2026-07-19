from copy import deepcopy
import json
import unittest

from src.investigation import (
    InvestigationStateError,
    advance_analysis_run,
    apply_investigation_command,
    apply_scope,
    assert_investigation_invariants,
    begin_analysis_run,
    bookmark_recommendation,
    complete_analysis_run,
    create_investigation,
    fail_analysis_run,
    prepare_run_reviews,
    review_finding,
)


TIME_RANGE_A = {
    "start_at": "2026-07-09T00:00:00+08:00",
    "end_at": "2026-07-16T00:00:00+08:00",
    "timezone": "Asia/Shanghai",
}
TIME_RANGE_B = {
    "start_at": "2026-07-08T00:00:00+08:00",
    "end_at": "2026-07-15T00:00:00+08:00",
    "timezone": "Asia/Shanghai",
}


def fixed_id(prefix: str) -> str:
    return f"{prefix}_fixed"


def active_state():
    state = create_investigation(investigation_id="inv_fixed")
    state = apply_scope(
        state,
        building_id="A",
        time_range=TIME_RANGE_A,
        actor="user",
    )
    state, run_id = begin_analysis_run(
        state,
        run_id="run_current",
    )
    return state, run_id


def complete_state():
    state, run_id = active_state()
    for status in ("loading_data", "calculating", "generating_insights"):
        state, eligible = advance_analysis_run(
            state,
            run_id=run_id,
            status=status,
        )
        assert eligible
    state, eligible = prepare_run_reviews(
        state,
        run_id=run_id,
        findings=[{"finding_id": "finding-1", "title": "Finding one"}],
        recommendations=[
            {"recommendation_id": "recommendation-1", "title": "Recommendation one"}
        ],
    )
    assert eligible
    state, eligible = complete_analysis_run(
        state,
        run_id=run_id,
        result_surface_id="surface-fixed",
    )
    assert eligible
    return state


class InvestigationStateTests(unittest.TestCase):
    def test_initial_state_has_exact_authoritative_shape(self) -> None:
        state = create_investigation(id_factory=fixed_id)
        self.assertEqual(
            state,
            {
                "investigation_id": "inv_fixed",
                "status": "idle",
                "building_id": None,
                "time_range": None,
                "active_run_id": None,
                "result_surface_id": None,
                "findings": [],
                "recommendations": [],
                "validation_error": None,
                "last_updated_by": None,
            },
        )
        assert_investigation_invariants(state)

    def test_normal_lifecycle_and_completion_invariants(self) -> None:
        state = create_investigation(investigation_id="inv-fixed")
        state = apply_scope(
            state,
            building_id="b",
            time_range=TIME_RANGE_A,
            actor="user",
        )
        self.assertEqual(state["status"], "ready")
        self.assertEqual(state["building_id"], "B")
        self.assertEqual(state["last_updated_by"], "user")

        state, run_id = begin_analysis_run(state, run_id="run-fixed")
        self.assertEqual(run_id, "run-fixed")
        self.assertEqual(state["status"], "validating")
        self.assertEqual(state["active_run_id"], run_id)

        expected = ["loading_data", "calculating", "generating_insights"]
        for status in expected:
            state, eligible = advance_analysis_run(
                state,
                run_id=run_id,
                status=status,
            )
            self.assertTrue(eligible)
            self.assertEqual(state["status"], status)

        state, eligible = prepare_run_reviews(
            state,
            run_id=run_id,
            findings=[{"finding_id": "finding-1", "title": "Finding one"}],
            recommendations=[
                {
                    "recommendation_id": "recommendation-1",
                    "title": "Recommendation one",
                }
            ],
        )
        self.assertTrue(eligible)
        self.assertEqual(state["findings"][0]["review_status"], "unreviewed")
        self.assertFalse(state["recommendations"][0]["bookmarked"])

        state, eligible = complete_analysis_run(
            state,
            run_id=run_id,
            result_surface_id="surface-fixed",
        )
        self.assertTrue(eligible)
        self.assertEqual(state["status"], "complete")
        self.assertIsNone(state["active_run_id"])
        self.assertEqual(state["result_surface_id"], "surface-fixed")
        assert_investigation_invariants(state)

    def test_invalid_lifecycle_and_completion_are_rejected(self) -> None:
        state = create_investigation(investigation_id="inv-fixed")
        with self.assertRaises(InvestigationStateError) as caught:
            begin_analysis_run(state, run_id="run-fixed")
        self.assertEqual(caught.exception.code, "INVALID_STATE_TRANSITION")

        state, run_id = active_state()
        state, _ = advance_analysis_run(
            state,
            run_id=run_id,
            status="loading_data",
        )
        state, _ = advance_analysis_run(
            state,
            run_id=run_id,
            status="calculating",
        )
        state, _ = advance_analysis_run(
            state,
            run_id=run_id,
            status="generating_insights",
        )
        with self.assertRaises(InvestigationStateError) as caught:
            complete_analysis_run(
                state,
                run_id=run_id,
                result_surface_id="surface-fixed",
            )
        self.assertEqual(caught.exception.code, "INVALID_INVESTIGATION_STATE")

    def test_scope_change_revokes_run_and_clears_result_and_reviews(self) -> None:
        completed = complete_state()
        completed["active_run_id"] = None
        changed = apply_scope(
            completed,
            building_id="C",
            time_range=TIME_RANGE_B,
            actor="user",
        )
        self.assertEqual(changed["status"], "ready")
        self.assertEqual(changed["building_id"], "C")
        self.assertEqual(changed["time_range"], TIME_RANGE_B)
        self.assertIsNone(changed["active_run_id"])
        self.assertIsNone(changed["result_surface_id"])
        self.assertEqual(changed["findings"], [])
        self.assertEqual(changed["recommendations"], [])
        self.assertIsNone(changed["validation_error"])

        running, _ = active_state()
        invalidated = apply_scope(
            running,
            building_id="B",
            time_range=TIME_RANGE_B,
            actor="user",
        )
        self.assertEqual(invalidated["status"], "ready")
        self.assertIsNone(invalidated["active_run_id"])

    def test_stale_run_cannot_publish_or_fail_current_state(self) -> None:
        state, current_run_id = active_state()
        before = deepcopy(state)

        advanced, eligible = advance_analysis_run(
            state,
            run_id="run-stale",
            status="loading_data",
        )
        self.assertFalse(eligible)
        self.assertEqual(advanced, before)

        reviewed, eligible = prepare_run_reviews(
            state,
            run_id="run-stale",
            findings=[{"finding_id": "stale", "title": "Stale"}],
            recommendations=[
                {"recommendation_id": "stale", "title": "Stale"}
            ],
        )
        self.assertFalse(eligible)
        self.assertEqual(reviewed, before)

        completed, eligible = complete_analysis_run(
            state,
            run_id="run-stale",
            result_surface_id="surface-stale",
        )
        self.assertFalse(eligible)
        self.assertEqual(completed, before)

        failed, eligible = fail_analysis_run(
            state,
            run_id="run-stale",
            error_message="stale failure",
        )
        self.assertFalse(eligible)
        self.assertEqual(failed, before)
        self.assertEqual(state["active_run_id"], current_run_id)

    def test_error_state_preserves_scope_and_requires_message(self) -> None:
        state, run_id = active_state()
        state, eligible = fail_analysis_run(
            state,
            run_id=run_id,
            error_message="[TARGET_OUT_OF_RANGE] Target outside dataset.",
        )
        self.assertTrue(eligible)
        self.assertEqual(state["status"], "error")
        self.assertEqual(state["building_id"], "A")
        self.assertEqual(state["time_range"], TIME_RANGE_A)
        self.assertIsNone(state["active_run_id"])
        self.assertIsNone(state["result_surface_id"])
        self.assertEqual(state["findings"], [])
        self.assertEqual(state["recommendations"], [])
        self.assertIn("TARGET_OUT_OF_RANGE", state["validation_error"])

        invalid = deepcopy(state)
        invalid["validation_error"] = None
        with self.assertRaises(InvestigationStateError):
            assert_investigation_invariants(invalid)

    def test_complete_and_error_states_can_begin_a_clean_rerun(self) -> None:
        completed = complete_state()
        rerunning, run_id = begin_analysis_run(completed, run_id="run-rerun-complete")
        self.assertEqual(run_id, "run-rerun-complete")
        self.assertEqual(rerunning["status"], "validating")
        self.assertEqual(rerunning["active_run_id"], run_id)
        self.assertIsNone(rerunning["result_surface_id"])
        self.assertEqual(rerunning["findings"], [])
        self.assertEqual(rerunning["recommendations"], [])

        failed, failed_run_id = active_state()
        failed, eligible = fail_analysis_run(
            failed,
            run_id=failed_run_id,
            error_message="[MODEL_REQUEST_FAILED] Provider unavailable.",
        )
        self.assertTrue(eligible)
        recovered, recovery_run_id = begin_analysis_run(
            failed,
            run_id="run-rerun-error",
        )
        self.assertEqual(recovery_run_id, "run-rerun-error")
        self.assertEqual(recovered["status"], "validating")
        self.assertIsNone(recovered["validation_error"])
        self.assertEqual(recovered["building_id"], failed["building_id"])
        self.assertEqual(recovered["time_range"], failed["time_range"])

    def test_user_review_and_bookmark_mutations_are_authoritative(self) -> None:
        state = complete_state()
        state = review_finding(
            state,
            finding_id="finding-1",
            review_status="confirmed",
        )
        self.assertEqual(state["findings"][0]["review_status"], "confirmed")
        self.assertEqual(state["last_updated_by"], "user")

        state = bookmark_recommendation(
            state,
            recommendation_id="recommendation-1",
            bookmarked=True,
        )
        self.assertTrue(state["recommendations"][0]["bookmarked"])
        self.assertEqual(state["last_updated_by"], "user")

    def test_commands_apply_scope_review_and_bookmark(self) -> None:
        state = create_investigation(investigation_id="inv-fixed")
        state = apply_investigation_command(
            state,
            {
                "type": "set_scope",
                "building_id": "B",
                "time_range": TIME_RANGE_A,
            },
        )
        self.assertEqual(state["status"], "ready")
        self.assertEqual(state["building_id"], "B")

        state = complete_state()
        state = apply_investigation_command(
            state,
            {
                "type": "review_finding",
                "finding_id": "finding-1",
                "review_status": "needs_review",
            },
        )
        state = apply_investigation_command(
            state,
            {
                "type": "bookmark_recommendation",
                "recommendation_id": "recommendation-1",
                "bookmarked": True,
            },
        )
        self.assertEqual(state["findings"][0]["review_status"], "needs_review")
        self.assertTrue(state["recommendations"][0]["bookmarked"])

    def test_invalid_review_actor_and_bookmark_shapes_are_rejected(self) -> None:
        state = complete_state()

        invalid_review = deepcopy(state)
        invalid_review["findings"][0]["review_status"] = "approved"
        with self.assertRaises(InvestigationStateError):
            assert_investigation_invariants(invalid_review)

        invalid_bookmark = deepcopy(state)
        invalid_bookmark["recommendations"][0]["bookmarked"] = "yes"
        with self.assertRaises(InvestigationStateError):
            assert_investigation_invariants(invalid_bookmark)

        invalid_actor = deepcopy(state)
        invalid_actor["last_updated_by"] = "model"
        with self.assertRaises(InvestigationStateError):
            assert_investigation_invariants(invalid_actor)

    def test_shared_state_contains_no_computed_metrics_or_chart_data(self) -> None:
        state = complete_state()
        self.assertEqual(
            set(state),
            {
                "investigation_id",
                "status",
                "building_id",
                "time_range",
                "active_run_id",
                "result_surface_id",
                "findings",
                "recommendations",
                "validation_error",
                "last_updated_by",
            },
        )
        serialized = json.dumps(state, sort_keys=True)
        for forbidden in (
            "target_series",
            "comparison_series",
            "analysis_result",
            "metrics",
            "trend",
            "categories",
            "evidence",
            "hvac_kw",
        ):
            self.assertNotIn(forbidden, serialized)


if __name__ == "__main__":
    unittest.main()
