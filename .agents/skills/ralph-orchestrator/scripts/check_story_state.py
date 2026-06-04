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


PHASES = {"developer", "validator", "committer"}


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--task-file", required=True)
    parser.add_argument("--story-id", required=True)
    parser.add_argument("--phase", required=True, choices=sorted(PHASES))
    return parser.parse_args(argv)


def evaluate_phase(story: dict, phase: str) -> tuple[str, str]:
    lifecycle_state, _, lifecycle_reason = resolve_story_lifecycle(story)

    if lifecycle_state == "inconsistent_state":
        return "inconsistent_state", lifecycle_reason

    if phase == "developer":
        if lifecycle_state == "ready_for_validation":
            return "advance", "developer phase completed"
        if story.get("developmentCompleted") is True:
            return "phase_incomplete", "developer phase must leave the story ready for validation"
        return "phase_incomplete", "developmentCompleted=true was not observed"

    if phase == "validator":
        if lifecycle_state == "ready_for_commit":
            return "advance", "validator marked the story as passed"
        if lifecycle_state == "validation_failed":
            return "retry_developer", "validator marked the story as failed"
        return "phase_incomplete", "validator result fields are still pending"

    if lifecycle_state == "completed":
        return "advance", "committer recorded a successful commit"
    if story.get("commitStatus") == "failed":
        return "retry_committer", "committer recorded a failed commit attempt"
    if lifecycle_state == "ready_for_commit":
        return "phase_incomplete", "commitStatus is still pending"
    return "inconsistent_state", "committer phase requires a passed validation state"


def check_story_state(task_file: Path, story_id: str, phase: str) -> dict:
    data = load_task_data(task_file)
    story = find_story(data, story_id)
    result, reason = evaluate_phase(story, phase)
    lifecycle_state, next_phase, _ = resolve_story_lifecycle(story)
    return {
        "story_id": story_id,
        "checked_phase": phase,
        "result": result,
        "lifecycle_state": lifecycle_state,
        "next_phase": next_phase,
        "observed_fields": observed_fields_for(story),
        "reason": reason,
    }


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv or sys.argv[1:])
    result = check_story_state(
        task_file=Path(args.task_file),
        story_id=args.story_id,
        phase=args.phase,
    )
    print(json.dumps(result, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
