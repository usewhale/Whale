#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

from runtime_utils import (
    find_story,
    load_task_data,
    observed_fields_for,
    resolve_story_lifecycle,
)


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--task-file", required=True)
    parser.add_argument("--story-id", required=True)
    return parser.parse_args(argv)


def resolve_story_phase(task_file: Path, story_id: str) -> dict:
    data = load_task_data(task_file)
    story = find_story(data, story_id)
    lifecycle_state, next_phase, reason = resolve_story_lifecycle(story)
    return {
        "story_id": story_id,
        "lifecycle_state": lifecycle_state,
        "next_phase": next_phase,
        "observed_fields": observed_fields_for(story),
        "reason": reason,
    }


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv or sys.argv[1:])
    result = resolve_story_phase(
        task_file=Path(args.task_file),
        story_id=args.story_id,
    )
    print(json.dumps(result, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
