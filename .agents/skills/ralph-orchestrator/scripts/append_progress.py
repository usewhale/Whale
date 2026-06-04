#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import sys
from datetime import datetime
from pathlib import Path
from typing import Any

from runtime_utils import atomic_write_text, dedupe_preserve_order, parse_json_list


def clean_string_list(items: list[str]) -> list[str]:
    return [item.strip() for item in items if item.strip()]


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--progress-file", required=True)
    parser.add_argument("--story-id", required=True)
    parser.add_argument("--summary", required=True)
    parser.add_argument("--changed-files-json", required=True)
    parser.add_argument("--pattern-candidates-json", default="[]")
    parser.add_argument("--discovered-patterns-json")
    parser.add_argument("--traps-encountered-json", default="[]")
    parser.add_argument("--useful-context-json", default="[]")
    parser.add_argument("--merge-codebase-patterns", action="store_true")
    return parser.parse_args(argv)


def render_progress_section(
    story_id: str,
    summary: str,
    changed_files: list[str],
    discovered_patterns: list[str],
    traps_encountered: list[str],
    useful_context: list[str],
    timestamp: str | None = None,
) -> str:
    def extend_learning_category(lines: list[str], label: str, items: list[str]) -> None:
        lines.append(f"  - {label}:")
        if items:
            lines.extend(f"    - {item}" for item in items)
        else:
            lines.append("    - (无)")

    rendered_timestamp = timestamp or datetime.now().strftime("%Y-%m-%d %H:%M")
    section_lines = [
        f"## {rendered_timestamp} - {story_id}",
        f"- Summary: {summary.strip() or '(no summary provided)'}",
        f"- Changed files: {', '.join(changed_files) if changed_files else '(none reported)'}",
        "- **Future iteration learnings:**",
    ]

    extend_learning_category(section_lines, "发现的 patterns", discovered_patterns)
    extend_learning_category(section_lines, "遇到的陷阱", traps_encountered)
    extend_learning_category(section_lines, "有用的上下文", useful_context)

    section_lines.append("---")
    return "\n".join(section_lines)


def merge_codebase_patterns(content: str, pattern_candidates: list[str]) -> str:
    patterns = dedupe_preserve_order(clean_string_list(pattern_candidates))
    if not patterns:
        return content

    lines = content.splitlines()
    heading = "## Codebase Patterns"
    start_index = next((idx for idx, line in enumerate(lines) if line.strip() == heading), None)

    if start_index is None:
        section_lines = [heading, *(f"- {pattern}" for pattern in patterns), ""]
        prefix = "\n".join(section_lines)
        if not content:
            return prefix
        separator = "" if content.startswith("\n") else "\n"
        return prefix + separator + content

    end_index = len(lines)
    for idx in range(start_index + 1, len(lines)):
        if lines[idx].startswith("## "):
            end_index = idx
            break

    existing_patterns: list[str] = []
    for line in lines[start_index + 1 : end_index]:
        stripped = line.strip()
        if stripped.startswith("- "):
            existing_patterns.append(stripped[2:].strip())

    merged_patterns = dedupe_preserve_order(existing_patterns + patterns)
    replacement = [heading, *(f"- {pattern}" for pattern in merged_patterns), ""]
    new_lines = lines[:start_index] + replacement + lines[end_index:]
    return "\n".join(new_lines)


def append_progress(
    progress_file: Path,
    story_id: str,
    summary: str,
    changed_files: list[str],
    pattern_candidates: list[str],
    merge_patterns: bool,
    discovered_patterns: list[str] | None = None,
    traps_encountered: list[str] | None = None,
    useful_context: list[str] | None = None,
    timestamp: str | None = None,
) -> dict[str, Any]:
    existing_content = ""
    if progress_file.exists():
        existing_content = progress_file.read_text(encoding="utf-8")

    cleaned_pattern_candidates = dedupe_preserve_order(clean_string_list(pattern_candidates))
    rendered_discovered_patterns = (
        clean_string_list(discovered_patterns)
        if discovered_patterns is not None
        else cleaned_pattern_candidates
    )
    rendered_traps_encountered = clean_string_list(traps_encountered or [])
    rendered_useful_context = clean_string_list(useful_context or [])

    content = existing_content
    should_merge_patterns = merge_patterns and bool(cleaned_pattern_candidates)
    if should_merge_patterns:
        content = merge_codebase_patterns(content, cleaned_pattern_candidates)

    section = render_progress_section(
        story_id=story_id,
        summary=summary,
        changed_files=changed_files,
        discovered_patterns=rendered_discovered_patterns,
        traps_encountered=rendered_traps_encountered,
        useful_context=rendered_useful_context,
        timestamp=timestamp,
    )

    if content:
        if not content.endswith("\n"):
            content += "\n"
        if not content.endswith("\n\n"):
            content += "\n"
        content += section + "\n"
    else:
        content = section + "\n"

    atomic_write_text(progress_file, content)

    return {
        "story_id": story_id,
        "progress_file": str(progress_file),
        "merged_codebase_patterns": should_merge_patterns,
        "appended_section_heading": section.splitlines()[0],
    }


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv or sys.argv[1:])
    pattern_candidates = parse_json_list(
        args.pattern_candidates_json,
        "pattern-candidates-json",
    )
    discovered_patterns = (
        parse_json_list(args.discovered_patterns_json, "discovered-patterns-json")
        if args.discovered_patterns_json is not None
        else None
    )
    result = append_progress(
        progress_file=Path(args.progress_file),
        story_id=args.story_id,
        summary=args.summary,
        changed_files=parse_json_list(args.changed_files_json, "changed-files-json"),
        pattern_candidates=pattern_candidates,
        discovered_patterns=discovered_patterns,
        traps_encountered=parse_json_list(
            args.traps_encountered_json,
            "traps-encountered-json",
        ),
        useful_context=parse_json_list(args.useful_context_json, "useful-context-json"),
        merge_patterns=args.merge_codebase_patterns,
    )
    print(json.dumps(result, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
