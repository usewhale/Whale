#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

from runtime_utils import (
    load_task_data,
    observed_fields_for,
    priority_sort_key,
    resolve_story_lifecycle,
    validate_priority,
)


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--task-file", required=True)
    return parser.parse_args(argv)


def select_current_story(task_file: Path) -> dict:
    data = load_task_data(task_file)
    candidates: list[tuple[tuple[int, int, int], dict, str, str, str]] = []

    for index, story in enumerate(data.get("userStories", [])):
        priority_issue = validate_priority(story)
        if priority_issue:
            return {
                "selection_result": "inconsistent_state",
                "story_id": story.get("id"),
                "priority": story.get("priority"),
                "lifecycle_state": "inconsistent_state",
                "reason": priority_issue,
                "observed_fields": observed_fields_for(story),
            }

        lifecycle_state, _, reason = resolve_story_lifecycle(story)
        if story.get("blocked") is True:
            continue

        if story.get("passes") is True:
            if lifecycle_state == "inconsistent_state":
                return {
                    "selection_result": "inconsistent_state",
                    "story_id": story.get("id"),
                    "priority": story.get("priority"),
                    "lifecycle_state": lifecycle_state,
                    "reason": reason,
                    "observed_fields": observed_fields_for(story),
                }
            continue

        candidates.append(
            (
                priority_sort_key(story, index),
                story,
                lifecycle_state,
                reason,
                story.get("id"),
            )
        )

    if not candidates:
        return {
            "selection_result": "all_resolved",
            "story_id": None,
            "priority": None,
            "lifecycle_state": None,
            "reason": "all stories are completed via passes=true or blocked",
            "observed_fields": None,
        }

    _, story, lifecycle_state, reason, story_id = min(candidates, key=lambda item: item[0])
    if lifecycle_state == "inconsistent_state":
        return {
            "selection_result": "inconsistent_state",
            "story_id": story_id,
            "priority": story.get("priority"),
            "lifecycle_state": lifecycle_state,
            "reason": reason,
            "observed_fields": observed_fields_for(story),
        }

    return {
        "selection_result": "selected",
        "story_id": story_id,
        "priority": story.get("priority"),
        "lifecycle_state": lifecycle_state,
        "reason": reason,
        "observed_fields": observed_fields_for(story),
    }


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv or sys.argv[1:])
    result = select_current_story(Path(args.task_file))
    print(json.dumps(result, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
