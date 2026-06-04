#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from typing import Any

from runtime_utils import atomic_write_json, find_story, load_task_data


ALLOWED_FIELDS = {
    "developmentCompleted",
    "validationStatus",
    "passes",
    "notes",
    "retryCount",
    "commitStatus",
}
BOOLEAN_FIELDS = {"developmentCompleted", "passes"}
INTEGER_FIELDS = {"retryCount"}
VALIDATION_STATUSES = {"", "pending", "passed", "failed"}
COMMIT_STATUSES = {"", "pending", "committed", "failed"}


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--task-file", required=True)
    parser.add_argument("--story-id", required=True)
    parser.add_argument("--set", action="append", default=[])
    parser.add_argument("--increment", action="append", default=[])
    return parser.parse_args(argv)


def parse_assignment(raw: str) -> tuple[str, str]:
    if "=" not in raw:
        raise ValueError(f"Assignment must use field=value syntax: {raw}")
    field, value = raw.split("=", 1)
    field = field.strip()
    if not field:
        raise ValueError("Assignment field name cannot be empty")
    return field, value


def coerce_set_value(field: str, raw_value: str) -> Any:
    if field not in ALLOWED_FIELDS:
        raise ValueError(f"Field is not allowed for --set: {field}")

    if field in BOOLEAN_FIELDS:
        lowered = raw_value.lower()
        if lowered not in {"true", "false"}:
            raise ValueError(f"{field} must be true or false")
        return lowered == "true"

    if field in INTEGER_FIELDS:
        try:
            value = int(raw_value)
        except ValueError as exc:
            raise ValueError(f"{field} must be an integer") from exc
        if value < 0:
            raise ValueError(f"{field} cannot be negative")
        return value

    if field == "validationStatus":
        if raw_value not in VALIDATION_STATUSES:
            raise ValueError(
                "validationStatus must be one of: '', pending, passed, failed"
            )
        return raw_value

    if field == "commitStatus":
        if raw_value not in COMMIT_STATUSES:
            raise ValueError(
                "commitStatus must be one of: '', pending, committed, failed"
            )
        return raw_value

    return raw_value


def coerce_increment_value(field: str, raw_value: str) -> int:
    if field not in ALLOWED_FIELDS:
        raise ValueError(f"Field is not allowed for --increment: {field}")
    if field != "retryCount":
        raise ValueError(f"Only retryCount supports --increment, not {field}")
    try:
        value = int(raw_value)
    except ValueError as exc:
        raise ValueError("retryCount increment must be an integer") from exc
    return value


def apply_updates(
    task_file: Path,
    story_id: str,
    set_assignments: list[str],
    increment_assignments: list[str],
) -> dict[str, Any]:
    updates: dict[str, Any] = {}
    increments: dict[str, int] = {}

    for assignment in set_assignments:
        field, raw_value = parse_assignment(assignment)
        if field in increments:
            raise ValueError(f"Field cannot be both set and incremented: {field}")
        updates[field] = coerce_set_value(field, raw_value)

    for assignment in increment_assignments:
        field, raw_value = parse_assignment(assignment)
        if field in updates:
            raise ValueError(f"Field cannot be both set and incremented: {field}")
        if field in increments:
            raise ValueError(f"Field cannot be incremented more than once: {field}")
        increments[field] = coerce_increment_value(field, raw_value)

    if not updates and not increments:
        raise ValueError("At least one --set or --increment operation is required")

    data = load_task_data(task_file)
    story = find_story(data, story_id)

    for field, value in updates.items():
        story[field] = value

    for field, increment in increments.items():
        current = story.get(field, 0)
        if not isinstance(current, int):
            raise ValueError(f"{field} must be an integer before incrementing")
        new_value = current + increment
        if new_value < 0:
            raise ValueError(f"{field} cannot become negative")
        story[field] = new_value

    atomic_write_json(task_file, data)

    return {
        "story_id": story_id,
        "updated_fields": {field: story.get(field) for field in sorted(set(updates) | set(increments))},
        "task_file": str(task_file),
    }


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv or sys.argv[1:])
    result = apply_updates(
        task_file=Path(args.task_file),
        story_id=args.story_id,
        set_assignments=args.set,
        increment_assignments=args.increment,
    )
    print(json.dumps(result, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
