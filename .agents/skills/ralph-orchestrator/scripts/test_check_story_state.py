import json
import sys
import tempfile
import unittest
from pathlib import Path


SCRIPT_DIR = Path(__file__).resolve().parent
if str(SCRIPT_DIR) not in sys.path:
    sys.path.insert(0, str(SCRIPT_DIR))

import check_story_state as css


class CheckStoryStateTests(unittest.TestCase):
    def write_task_file(self, root: Path, story: dict) -> Path:
        task_file = root / "prd.json"
        task_file.write_text(
            json.dumps({"userStories": [story]}, ensure_ascii=False, indent=2),
            encoding="utf-8",
        )
        return task_file

    def test_developer_phase_advances_when_completed(self) -> None:
        story = {
            "id": "US-001",
            "developmentCompleted": True,
            "validationStatus": "pending",
            "passes": False,
            "notes": "",
            "retryCount": 0,
            "commitStatus": "pending",
            "blocked": False,
        }
        with tempfile.TemporaryDirectory() as temp_dir:
            result = css.check_story_state(
                task_file=self.write_task_file(Path(temp_dir), story),
                story_id="US-001",
                phase="developer",
            )

        self.assertEqual(result["result"], "advance")
        self.assertEqual(result["lifecycle_state"], "ready_for_validation")

    def test_validator_phase_advances_when_passed(self) -> None:
        story = {
            "id": "US-001",
            "developmentCompleted": True,
            "validationStatus": "passed",
            "passes": False,
            "notes": "",
            "retryCount": 0,
            "commitStatus": "pending",
            "blocked": False,
        }
        with tempfile.TemporaryDirectory() as temp_dir:
            result = css.check_story_state(
                task_file=self.write_task_file(Path(temp_dir), story),
                story_id="US-001",
                phase="validator",
            )

        self.assertEqual(result["result"], "advance")
        self.assertEqual(result["lifecycle_state"], "ready_for_commit")

    def test_validator_phase_requests_developer_retry_when_failed(self) -> None:
        story = {
            "id": "US-001",
            "developmentCompleted": True,
            "validationStatus": "failed",
            "passes": False,
            "notes": "Failed validation",
            "retryCount": 1,
            "commitStatus": "pending",
            "blocked": False,
        }
        with tempfile.TemporaryDirectory() as temp_dir:
            result = css.check_story_state(
                task_file=self.write_task_file(Path(temp_dir), story),
                story_id="US-001",
                phase="validator",
            )

        self.assertEqual(result["result"], "retry_developer")
        self.assertEqual(result["lifecycle_state"], "validation_failed")

    def test_committer_phase_advances_when_commit_recorded(self) -> None:
        story = {
            "id": "US-001",
            "developmentCompleted": True,
            "validationStatus": "passed",
            "passes": True,
            "notes": "",
            "retryCount": 0,
            "commitStatus": "committed",
            "blocked": False,
        }
        with tempfile.TemporaryDirectory() as temp_dir:
            result = css.check_story_state(
                task_file=self.write_task_file(Path(temp_dir), story),
                story_id="US-001",
                phase="committer",
            )

        self.assertEqual(result["result"], "advance")
        self.assertEqual(result["lifecycle_state"], "completed")

    def test_committer_phase_requests_retry_on_failed_commit(self) -> None:
        story = {
            "id": "US-001",
            "developmentCompleted": True,
            "validationStatus": "passed",
            "passes": False,
            "notes": "",
            "retryCount": 0,
            "commitStatus": "failed",
            "blocked": False,
        }
        with tempfile.TemporaryDirectory() as temp_dir:
            result = css.check_story_state(
                task_file=self.write_task_file(Path(temp_dir), story),
                story_id="US-001",
                phase="committer",
            )

        self.assertEqual(result["result"], "retry_committer")
        self.assertEqual(result["lifecycle_state"], "ready_for_commit")

    def test_committer_phase_requires_passes_true_after_successful_commit(self) -> None:
        story = {
            "id": "US-001",
            "developmentCompleted": True,
            "validationStatus": "passed",
            "passes": False,
            "notes": "",
            "retryCount": 0,
            "commitStatus": "committed",
            "blocked": False,
        }
        with tempfile.TemporaryDirectory() as temp_dir:
            result = css.check_story_state(
                task_file=self.write_task_file(Path(temp_dir), story),
                story_id="US-001",
                phase="committer",
            )

        self.assertEqual(result["result"], "inconsistent_state")
        self.assertEqual(result["lifecycle_state"], "inconsistent_state")

    def test_reports_inconsistent_field_combination(self) -> None:
        story = {
            "id": "US-001",
            "developmentCompleted": True,
            "validationStatus": "failed",
            "passes": True,
            "notes": "",
            "retryCount": 0,
            "commitStatus": "pending",
            "blocked": False,
        }
        with tempfile.TemporaryDirectory() as temp_dir:
            result = css.check_story_state(
                task_file=self.write_task_file(Path(temp_dir), story),
                story_id="US-001",
                phase="validator",
            )

        self.assertEqual(result["result"], "inconsistent_state")
        self.assertEqual(result["lifecycle_state"], "inconsistent_state")


if __name__ == "__main__":
    unittest.main()
