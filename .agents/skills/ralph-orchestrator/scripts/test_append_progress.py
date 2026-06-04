import sys
import tempfile
import unittest
from pathlib import Path


SCRIPT_DIR = Path(__file__).resolve().parent
if str(SCRIPT_DIR) not in sys.path:
    sys.path.insert(0, str(SCRIPT_DIR))

import append_progress as ap


class AppendProgressTests(unittest.TestCase):
    def test_renders_three_learning_categories(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            progress_file = Path(temp_dir) / "progress.txt"

            ap.append_progress(
                progress_file=progress_file,
                story_id="US-001",
                summary="Implemented the story",
                changed_files=["app/page.tsx", "app/test.ts"],
                pattern_candidates=["Keep runtime writes inside local scripts"],
                discovered_patterns=["Use existing app routing helpers"],
                traps_encountered=["Do not commit runtime files"],
                useful_context=["AGENTS.md points to frontend conventions"],
                merge_patterns=False,
                timestamp="2026-05-25 10:00",
            )

            content = progress_file.read_text(encoding="utf-8")

        self.assertIn("## 2026-05-25 10:00 - US-001", content)
        self.assertIn("- Summary: Implemented the story", content)
        self.assertIn("- Changed files: app/page.tsx, app/test.ts", content)
        self.assertIn("  - 发现的 patterns:\n    - Use existing app routing helpers", content)
        self.assertIn("  - 遇到的陷阱:\n    - Do not commit runtime files", content)
        self.assertIn(
            "  - 有用的上下文:\n    - AGENTS.md points to frontend conventions",
            content,
        )
        self.assertTrue(content.endswith("---\n"))

    def test_empty_learning_categories_render_none(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            progress_file = Path(temp_dir) / "progress.txt"

            ap.append_progress(
                progress_file=progress_file,
                story_id="US-001",
                summary="Implemented the story",
                changed_files=[],
                pattern_candidates=[],
                discovered_patterns=[],
                traps_encountered=[],
                useful_context=[],
                merge_patterns=False,
                timestamp="2026-05-25 10:00",
            )

            content = progress_file.read_text(encoding="utf-8")

        self.assertIn("  - 发现的 patterns:\n    - (无)", content)
        self.assertIn("  - 遇到的陷阱:\n    - (无)", content)
        self.assertIn("  - 有用的上下文:\n    - (无)", content)

    def test_legacy_pattern_candidates_only_maps_to_discovered_patterns(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            progress_file = Path(temp_dir) / "progress.txt"

            ap.append_progress(
                progress_file=progress_file,
                story_id="US-001",
                summary="Implemented the story",
                changed_files=[],
                pattern_candidates=["Keep runtime files out of commits"],
                merge_patterns=False,
                timestamp="2026-05-25 10:00",
            )

            content = progress_file.read_text(encoding="utf-8")

        self.assertIn("  - 发现的 patterns:\n    - Keep runtime files out of commits", content)
        self.assertIn("  - 遇到的陷阱:\n    - (无)", content)
        self.assertIn("  - 有用的上下文:\n    - (无)", content)

    def test_preserves_existing_history_when_appending(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            progress_file = Path(temp_dir) / "progress.txt"
            progress_file.write_text("## Existing\n- Previous work\n---\n", encoding="utf-8")

            ap.append_progress(
                progress_file=progress_file,
                story_id="US-002",
                summary="Added validation",
                changed_files=[],
                pattern_candidates=[],
                merge_patterns=False,
                timestamp="2026-05-25 10:05",
            )

            content = progress_file.read_text(encoding="utf-8")

        self.assertIn("## Existing\n- Previous work\n---\n", content)
        self.assertIn("## 2026-05-25 10:05 - US-002", content)

    def test_creates_codebase_patterns_at_top(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            progress_file = Path(temp_dir) / "progress.txt"
            progress_file.write_text("## Existing\n- Previous work\n---\n", encoding="utf-8")

            ap.append_progress(
                progress_file=progress_file,
                story_id="US-003",
                summary="Added runtime rules",
                changed_files=[],
                pattern_candidates=["Keep task runtime writes inside local scripts"],
                merge_patterns=True,
                timestamp="2026-05-25 10:10",
            )

            content = progress_file.read_text(encoding="utf-8")

        self.assertTrue(content.startswith("## Codebase Patterns\n"))
        self.assertIn("- Keep task runtime writes inside local scripts\n\n## Existing", content)
        self.assertIn("## 2026-05-25 10:10 - US-003", content)

    def test_merges_codebase_patterns_deduping_and_preserving_order(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            progress_file = Path(temp_dir) / "progress.txt"
            progress_file.write_text(
                "## Codebase Patterns\n"
                "- Reuse existing services before starting new ones\n"
                "\n"
                "## 2026-05-24 09:00 - US-000\n"
                "- Previous story\n"
                "---\n",
                encoding="utf-8",
            )

            ap.append_progress(
                progress_file=progress_file,
                story_id="US-003",
                summary="Merged progress rules",
                changed_files=["skill.md"],
                pattern_candidates=[
                    "Reuse existing services before starting new ones",
                    "Keep task runtime writes inside local scripts",
                ],
                merge_patterns=True,
                timestamp="2026-05-25 10:10",
            )

            content = progress_file.read_text(encoding="utf-8")

        expected_top = (
            "## Codebase Patterns\n"
            "- Reuse existing services before starting new ones\n"
            "- Keep task runtime writes inside local scripts\n\n"
        )
        self.assertIn(expected_top, content)
        self.assertEqual(content.count("## Codebase Patterns"), 1)
        self.assertEqual(content.count("- Reuse existing services before starting new ones"), 2)
        self.assertIn("## 2026-05-24 09:00 - US-000", content)
        self.assertIn("## 2026-05-25 10:10 - US-003", content)

    def test_only_traps_and_context_do_not_merge_top_patterns(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            progress_file = Path(temp_dir) / "progress.txt"
            progress_file.write_text(
                "## Codebase Patterns\n- Existing reusable pattern\n\n",
                encoding="utf-8",
            )

            result = ap.append_progress(
                progress_file=progress_file,
                story_id="US-004",
                summary="Recorded learnings",
                changed_files=[],
                pattern_candidates=[],
                discovered_patterns=[],
                traps_encountered=["Avoid changing runtime files by hand"],
                useful_context=["Use the local writer script"],
                merge_patterns=True,
                timestamp="2026-05-25 10:15",
            )

            content = progress_file.read_text(encoding="utf-8")

        self.assertFalse(result["merged_codebase_patterns"])
        top_section = content.split("## 2026-05-25 10:15 - US-004", 1)[0]
        self.assertIn("- Existing reusable pattern", top_section)
        self.assertNotIn("Avoid changing runtime files by hand", top_section)
        self.assertNotIn("Use the local writer script", top_section)
        self.assertIn("  - 遇到的陷阱:\n    - Avoid changing runtime files by hand", content)
        self.assertIn("  - 有用的上下文:\n    - Use the local writer script", content)

    def test_merge_happens_before_appending_current_story_section(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            progress_file = Path(temp_dir) / "progress.txt"

            ap.append_progress(
                progress_file=progress_file,
                story_id="US-005",
                summary="Implemented story",
                changed_files=[],
                pattern_candidates=["New top-level pattern"],
                discovered_patterns=["Entry-only discovery"],
                merge_patterns=True,
                timestamp="2026-05-25 10:20",
            )

            content = progress_file.read_text(encoding="utf-8")

        self.assertLess(
            content.index("- New top-level pattern"),
            content.index("## 2026-05-25 10:20 - US-005"),
        )
        self.assertIn(
            "## 2026-05-25 10:20 - US-005\n"
            "- Summary: Implemented story\n"
            "- Changed files: (none reported)\n"
            "- **Future iteration learnings:**\n"
            "  - 发现的 patterns:\n"
            "    - Entry-only discovery",
            content,
        )


if __name__ == "__main__":
    unittest.main()
