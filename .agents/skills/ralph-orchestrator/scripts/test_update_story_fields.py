import json
import sys
import tempfile
import unittest
from pathlib import Path


SCRIPT_DIR = Path(__file__).resolve().parent
if str(SCRIPT_DIR) not in sys.path:
    sys.path.insert(0, str(SCRIPT_DIR))

import update_story_fields as usf


class UpdateStoryFieldsTests(unittest.TestCase):
    def write_task_file(self, root: Path) -> Path:
        task_file = root / "prd.json"
        task_file.write_text(
            json.dumps(
                {
                    "userStories": [
                        {
                            "id": "US-001",
                            "developmentCompleted": False,
                            "validationStatus": "pending",
                            "passes": False,
                            "notes": "",
                            "retryCount": 0,
                            "commitStatus": "pending",
                        },
                        {
                            "id": "US-002",
                            "developmentCompleted": False,
                            "validationStatus": "pending",
                            "passes": False,
                            "notes": "",
                            "retryCount": 2,
                            "commitStatus": "pending",
                        },
                    ]
                },
                ensure_ascii=False,
                indent=2,
            ),
            encoding="utf-8",
        )
        return task_file

    def test_updates_only_selected_story(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            task_file = self.write_task_file(Path(temp_dir))

            usf.apply_updates(
                task_file=task_file,
                story_id="US-001",
                set_assignments=["developmentCompleted=true", "notes=Done"],
                increment_assignments=[],
            )

            data = json.loads(task_file.read_text(encoding="utf-8"))

        first, second = data["userStories"]
        self.assertTrue(first["developmentCompleted"])
        self.assertEqual(first["notes"], "Done")
        self.assertFalse(second["developmentCompleted"])
        self.assertEqual(second["notes"], "")

    def test_rejects_disallowed_field(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            task_file = self.write_task_file(Path(temp_dir))

            with self.assertRaisesRegex(ValueError, "Field is not allowed"):
                usf.apply_updates(
                    task_file=task_file,
                    story_id="US-001",
                    set_assignments=["title=Not allowed"],
                    increment_assignments=[],
                )

    def test_retry_count_increment_is_applied(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            task_file = self.write_task_file(Path(temp_dir))

            result = usf.apply_updates(
                task_file=task_file,
                story_id="US-002",
                set_assignments=["validationStatus=failed", "passes=false"],
                increment_assignments=["retryCount=1"],
            )

            data = json.loads(task_file.read_text(encoding="utf-8"))

        self.assertEqual(result["updated_fields"]["retryCount"], 3)
        self.assertEqual(data["userStories"][1]["retryCount"], 3)

    def test_written_file_remains_valid_json(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            task_file = self.write_task_file(Path(temp_dir))

            usf.apply_updates(
                task_file=task_file,
                story_id="US-001",
                set_assignments=["commitStatus=failed", "notes="],
                increment_assignments=[],
            )

            loaded = json.loads(task_file.read_text(encoding="utf-8"))

        self.assertEqual(loaded["userStories"][0]["commitStatus"], "failed")


if __name__ == "__main__":
    unittest.main()
