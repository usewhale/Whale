import json
import sys
import tempfile
import unittest
from pathlib import Path


SCRIPT_DIR = Path(__file__).resolve().parent
if str(SCRIPT_DIR) not in sys.path:
    sys.path.insert(0, str(SCRIPT_DIR))

import resolve_story_phase as rsp


class ResolveStoryPhaseTests(unittest.TestCase):
    def write_task_file(self, root: Path, story: dict) -> Path:
        task_file = root / "prd.json"
        task_file.write_text(
            json.dumps({"userStories": [story]}, ensure_ascii=False, indent=2),
            encoding="utf-8",
        )
        return task_file

    def test_new_story_is_ready_for_development(self) -> None:
        story = {"id": "US-001", "passes": False, "blocked": False}
        with tempfile.TemporaryDirectory() as temp_dir:
            result = rsp.resolve_story_phase(self.write_task_file(Path(temp_dir), story), "US-001")

        self.assertEqual(result["lifecycle_state"], "ready_for_development")
        self.assertEqual(result["next_phase"], "developer")

    def test_development_completed_story_is_ready_for_validation(self) -> None:
        story = {
            "id": "US-002",
            "developmentCompleted": True,
            "validationStatus": "pending",
            "passes": False,
            "blocked": False,
        }
        with tempfile.TemporaryDirectory() as temp_dir:
            result = rsp.resolve_story_phase(self.write_task_file(Path(temp_dir), story), "US-002")

        self.assertEqual(result["lifecycle_state"], "ready_for_validation")
        self.assertEqual(result["next_phase"], "validator")

    def test_failed_validation_story_returns_to_developer(self) -> None:
        story = {
            "id": "US-003",
            "developmentCompleted": True,
            "validationStatus": "failed",
            "passes": False,
            "blocked": False,
        }
        with tempfile.TemporaryDirectory() as temp_dir:
            result = rsp.resolve_story_phase(self.write_task_file(Path(temp_dir), story), "US-003")

        self.assertEqual(result["lifecycle_state"], "validation_failed")
        self.assertEqual(result["next_phase"], "developer")

    def test_passed_story_is_ready_for_commit(self) -> None:
        story = {
            "id": "US-004",
            "developmentCompleted": True,
            "validationStatus": "passed",
            "passes": False,
            "commitStatus": "pending",
            "blocked": False,
        }
        with tempfile.TemporaryDirectory() as temp_dir:
            result = rsp.resolve_story_phase(self.write_task_file(Path(temp_dir), story), "US-004")

        self.assertEqual(result["lifecycle_state"], "ready_for_commit")
        self.assertEqual(result["next_phase"], "committer")

    def test_completed_story_returns_completed_state(self) -> None:
        story = {
            "id": "US-005",
            "developmentCompleted": True,
            "validationStatus": "passed",
            "passes": True,
            "commitStatus": "committed",
            "blocked": False,
        }
        with tempfile.TemporaryDirectory() as temp_dir:
            result = rsp.resolve_story_phase(self.write_task_file(Path(temp_dir), story), "US-005")

        self.assertEqual(result["lifecycle_state"], "completed")
        self.assertEqual(result["next_phase"], "none")

    def test_blocked_story_returns_blocked_state(self) -> None:
        story = {"id": "US-006", "blocked": True, "passes": False}
        with tempfile.TemporaryDirectory() as temp_dir:
            result = rsp.resolve_story_phase(self.write_task_file(Path(temp_dir), story), "US-006")

        self.assertEqual(result["lifecycle_state"], "blocked")
        self.assertEqual(result["next_phase"], "none")

    def test_inconsistent_story_is_reported(self) -> None:
        story = {
            "id": "US-007",
            "developmentCompleted": True,
            "validationStatus": "passed",
            "passes": True,
            "commitStatus": "pending",
            "blocked": False,
        }
        with tempfile.TemporaryDirectory() as temp_dir:
            result = rsp.resolve_story_phase(self.write_task_file(Path(temp_dir), story), "US-007")

        self.assertEqual(result["lifecycle_state"], "inconsistent_state")
        self.assertEqual(result["next_phase"], "none")

    def test_fully_completed_story_without_passes_true_is_inconsistent(self) -> None:
        story = {
            "id": "US-008",
            "developmentCompleted": True,
            "validationStatus": "passed",
            "passes": False,
            "commitStatus": "committed",
            "blocked": False,
        }
        with tempfile.TemporaryDirectory() as temp_dir:
            result = rsp.resolve_story_phase(self.write_task_file(Path(temp_dir), story), "US-008")

        self.assertEqual(result["lifecycle_state"], "inconsistent_state")
        self.assertEqual(result["next_phase"], "none")

    def test_commit_requires_passes_true_to_complete(self) -> None:
        story = {
            "id": "US-009",
            "developmentCompleted": False,
            "validationStatus": "passed",
            "passes": True,
            "blocked": False,
        }
        with tempfile.TemporaryDirectory() as temp_dir:
            result = rsp.resolve_story_phase(self.write_task_file(Path(temp_dir), story), "US-009")

        self.assertEqual(result["lifecycle_state"], "inconsistent_state")
        self.assertEqual(result["next_phase"], "none")


if __name__ == "__main__":
    unittest.main()
