import json
import sys
import tempfile
import unittest
from pathlib import Path


SCRIPT_DIR = Path(__file__).resolve().parent
if str(SCRIPT_DIR) not in sys.path:
    sys.path.insert(0, str(SCRIPT_DIR))

import select_current_story as scs


class SelectCurrentStoryTests(unittest.TestCase):
    def write_task_file(self, root: Path, stories: list[dict]) -> Path:
        task_file = root / "prd.json"
        task_file.write_text(
            json.dumps({"userStories": stories}, ensure_ascii=False, indent=2),
            encoding="utf-8",
        )
        return task_file

    def test_selects_lowest_priority_unblocked_unfinished_story(self) -> None:
        stories = [
            {
                "id": "US-001",
                "priority": 4,
                "passes": False,
                "blocked": False,
            },
            {
                "id": "US-002",
                "priority": 2,
                "developmentCompleted": True,
                "validationStatus": "pending",
                "passes": False,
                "blocked": False,
            },
            {
                "id": "US-003",
                "priority": 1,
                "developmentCompleted": True,
                "validationStatus": "passed",
                "passes": True,
                "commitStatus": "committed",
                "blocked": False,
            },
        ]
        with tempfile.TemporaryDirectory() as temp_dir:
            result = scs.select_current_story(self.write_task_file(Path(temp_dir), stories))

        self.assertEqual(result["selection_result"], "selected")
        self.assertEqual(result["story_id"], "US-002")
        self.assertEqual(result["lifecycle_state"], "ready_for_validation")

    def test_priority_drives_selection_over_pending_commit_of_lower_priority_story(self) -> None:
        stories = [
            {
                "id": "US-010",
                "priority": 5,
                "developmentCompleted": True,
                "validationStatus": "passed",
                "passes": False,
                "commitStatus": "pending",
                "blocked": False,
            },
            {
                "id": "US-011",
                "priority": 1,
                "passes": False,
                "blocked": False,
            },
        ]
        with tempfile.TemporaryDirectory() as temp_dir:
            result = scs.select_current_story(self.write_task_file(Path(temp_dir), stories))

        self.assertEqual(result["story_id"], "US-011")
        self.assertEqual(result["lifecycle_state"], "ready_for_development")

    def test_blocked_story_is_skipped_even_when_higher_priority(self) -> None:
        stories = [
            {"id": "US-015", "priority": 0, "passes": False, "blocked": True},
            {"id": "US-016", "priority": 1, "passes": False, "blocked": False},
        ]
        with tempfile.TemporaryDirectory() as temp_dir:
            result = scs.select_current_story(self.write_task_file(Path(temp_dir), stories))

        self.assertEqual(result["selection_result"], "selected")
        self.assertEqual(result["story_id"], "US-016")

    def test_missing_priority_falls_back_to_original_order(self) -> None:
        stories = [
            {"id": "US-020", "passes": False, "blocked": False},
            {"id": "US-021", "passes": False, "blocked": False},
        ]
        with tempfile.TemporaryDirectory() as temp_dir:
            result = scs.select_current_story(self.write_task_file(Path(temp_dir), stories))

        self.assertEqual(result["story_id"], "US-020")

    def test_inconsistent_highest_priority_story_stops_selection(self) -> None:
        stories = [
            {
                "id": "US-030",
                "priority": 1,
                "developmentCompleted": False,
                "validationStatus": "passed",
                "passes": True,
                "blocked": False,
            },
            {
                "id": "US-031",
                "priority": 2,
                "passes": False,
                "blocked": False,
            },
        ]
        with tempfile.TemporaryDirectory() as temp_dir:
            result = scs.select_current_story(self.write_task_file(Path(temp_dir), stories))

        self.assertEqual(result["selection_result"], "inconsistent_state")
        self.assertEqual(result["story_id"], "US-030")

    def test_inconsistent_completed_story_is_reported_before_selection(self) -> None:
        stories = [
            {
                "id": "US-035",
                "priority": 9,
                "developmentCompleted": True,
                "validationStatus": "passed",
                "passes": True,
                "commitStatus": "pending",
                "blocked": False,
            },
            {
                "id": "US-036",
                "priority": 1,
                "passes": False,
                "blocked": False,
            },
        ]
        with tempfile.TemporaryDirectory() as temp_dir:
            result = scs.select_current_story(self.write_task_file(Path(temp_dir), stories))

        self.assertEqual(result["selection_result"], "inconsistent_state")
        self.assertEqual(result["story_id"], "US-035")

    def test_all_resolved_is_reported(self) -> None:
        stories = [
            {
                "id": "US-040",
                "priority": 1,
                "developmentCompleted": True,
                "validationStatus": "passed",
                "passes": True,
                "commitStatus": "committed",
                "blocked": False,
            },
            {
                "id": "US-041",
                "priority": 2,
                "passes": False,
                "blocked": True,
            },
        ]
        with tempfile.TemporaryDirectory() as temp_dir:
            result = scs.select_current_story(self.write_task_file(Path(temp_dir), stories))

        self.assertEqual(result["selection_result"], "all_resolved")
        self.assertIsNone(result["story_id"])


if __name__ == "__main__":
    unittest.main()
