from __future__ import annotations

import json
import tempfile
from pathlib import Path
from typing import Any


ALLOWED_VALIDATION_STATUSES = {None, "", "pending", "passed", "failed"}
ALLOWED_COMMIT_STATUSES = {None, "", "pending", "committed", "failed"}
LIFECYCLE_STATES = {
    "blocked",
    "completed",
    "ready_for_commit",
    "validation_failed",
    "ready_for_validation",
    "ready_for_development",
    "inconsistent_state",
}


def load_task_data(task_file: Path) -> dict[str, Any]:
    try:
        data = json.loads(task_file.read_text(encoding="utf-8"))
    except FileNotFoundError as exc:
        raise ValueError(f"Task file not found: {task_file}") from exc
    except json.JSONDecodeError as exc:
        raise ValueError(f"Task file is not valid JSON: {task_file}") from exc

    stories = data.get("userStories")
    if not isinstance(stories, list):
        raise ValueError("Task file must contain a userStories array")

    return data


def find_story(data: dict[str, Any], story_id: str) -> dict[str, Any]:
    for story in data.get("userStories", []):
        if story.get("id") == story_id:
            return story
    raise ValueError(f"Story not found: {story_id}")


def observed_fields_for(story: dict[str, Any]) -> dict[str, Any]:
    return {
        "priority": story.get("priority"),
        "developmentCompleted": story.get("developmentCompleted"),
        "validationStatus": story.get("validationStatus"),
        "passes": story.get("passes"),
        "notes": story.get("notes"),
        "retryCount": story.get("retryCount"),
        "commitStatus": story.get("commitStatus"),
        "blocked": story.get("blocked"),
    }


def priority_sort_key(story: dict[str, Any], index: int) -> tuple[int, int, int]:
    priority = story.get("priority")
    if priority is None:
        return (1, 0, index)
    return (0, priority, index)


def validate_priority(story: dict[str, Any]) -> str | None:
    priority = story.get("priority")
    if priority is None:
        return None
    if isinstance(priority, bool) or not isinstance(priority, int) or priority < 0:
        return "priority must be a non-negative integer when present"
    return None


def detect_inconsistent_state(story: dict[str, Any]) -> str | None:
    blocked = story.get("blocked")
    development_completed = story.get("developmentCompleted")
    validation_status = story.get("validationStatus")
    passes = story.get("passes")
    retry_count = story.get("retryCount")
    commit_status = story.get("commitStatus")

    priority_issue = validate_priority(story)
    if priority_issue:
        return priority_issue

    if blocked is not None and not isinstance(blocked, bool):
        return "blocked must be a boolean when present"

    if development_completed is not None and not isinstance(development_completed, bool):
        return "developmentCompleted must be a boolean when present"

    if validation_status not in ALLOWED_VALIDATION_STATUSES:
        return "validationStatus is not recognized"

    if passes is not None and not isinstance(passes, bool):
        return "passes must be a boolean when present"

    if retry_count is not None and (not isinstance(retry_count, int) or retry_count < 0):
        return "retryCount must be a non-negative integer when present"

    if commit_status not in ALLOWED_COMMIT_STATUSES:
        return "commitStatus is not recognized"

    if development_completed is not True and validation_status in {"passed", "failed"}:
        return "validationStatus=passed/failed requires developmentCompleted=true"

    if development_completed is not True and passes is True:
        return "passes=true requires developmentCompleted=true"

    if validation_status == "failed" and passes is not False:
        return "validationStatus=failed requires passes=false"

    if passes is True and validation_status != "passed":
        return "passes=true requires validationStatus=passed"

    if passes is True and commit_status != "committed":
        return "passes=true requires commitStatus=committed"

    if commit_status == "committed":
        if not (
            development_completed is True and validation_status == "passed"
        ):
            return "commitStatus=committed requires a passed validation state"
        if passes is not True:
            return "commitStatus=committed requires passes=true"

    if commit_status == "failed":
        if not (
            development_completed is True and validation_status == "passed"
        ):
            return "commitStatus=failed requires a passed validation state"
        if passes is not False:
            return "commitStatus=failed requires passes=false"

    if validation_status == "passed" and commit_status != "committed" and passes is not False:
        return "validationStatus=passed requires passes=false until commitStatus=committed"

    if (
        development_completed is True
        and validation_status == "passed"
        and commit_status == "committed"
        and passes is False
    ):
        return "fully completed story requires passes=true"

    return None


def resolve_story_lifecycle(
    story: dict[str, Any],
) -> tuple[str, str, str]:
    if story.get("blocked") is True:
        return "blocked", "none", "story is blocked"

    inconsistent_reason = detect_inconsistent_state(story)
    if inconsistent_reason:
        return "inconsistent_state", "none", inconsistent_reason

    development_completed = story.get("developmentCompleted")
    validation_status = story.get("validationStatus")
    passes = story.get("passes")
    commit_status = story.get("commitStatus")

    if (
        development_completed is True
        and validation_status == "passed"
        and commit_status == "committed"
        and passes is True
    ):
        return "completed", "none", "story is fully completed"

    if (
        development_completed is True
        and validation_status == "passed"
        and commit_status != "committed"
        and passes is False
    ):
        return "ready_for_commit", "committer", "story passed validation and awaits commit"

    if (
        development_completed is True
        and validation_status == "failed"
        and passes is False
    ):
        return "validation_failed", "developer", "story failed validation and needs fixes"

    if (
        development_completed is True
        and validation_status in {None, "", "pending"}
        and passes is not True
    ):
        return "ready_for_validation", "validator", "story completed development and awaits validation"

    return "ready_for_development", "developer", "story still needs development work"


def atomic_write_text(path: Path, content: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile(
        "w",
        encoding="utf-8",
        dir=path.parent,
        delete=False,
    ) as handle:
        handle.write(content)
        temp_path = Path(handle.name)
    temp_path.replace(path)


def atomic_write_json(path: Path, data: dict[str, Any]) -> None:
    content = json.dumps(data, ensure_ascii=False, indent=2) + "\n"
    json.loads(content)
    atomic_write_text(path, content)


def parse_json_list(raw: str, label: str) -> list[str]:
    try:
        value = json.loads(raw)
    except json.JSONDecodeError as exc:
        raise ValueError(f"{label} must be valid JSON") from exc

    if not isinstance(value, list):
        raise ValueError(f"{label} must decode to a JSON array")

    normalized: list[str] = []
    for item in value:
        if not isinstance(item, str):
            raise ValueError(f"{label} items must all be strings")
        cleaned = item.strip()
        if cleaned:
            normalized.append(cleaned)
    return normalized


def dedupe_preserve_order(items: list[str]) -> list[str]:
    seen: set[str] = set()
    ordered: list[str] = []
    for item in items:
        if item not in seen:
            ordered.append(item)
            seen.add(item)
    return ordered
