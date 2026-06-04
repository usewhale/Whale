#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import os
import re
import sys
from datetime import datetime
from pathlib import Path

from draw_image_backend import DEFAULT_BASE_URL, DEFAULT_MODEL, DrawImageBackend


DEFAULT_OUTPUT_ROOT = Path.cwd() / "images"


# Type currently controls output naming/metadata only. The image API contract
# has not confirmed aspect-ratio or size fields.
ASPECT_RATIOS = {
    "ultrawide": "21:9",
    "wide": "16:9",
    "square": "1:1",
    "portrait": "3:4",
    "classic": "4:3",
}


def sanitize_name(value: str, fallback: str = "image") -> str:
    value = value.strip()
    value = re.sub(r"[\\/:*?\"<>|]+", "-", value)
    value = re.sub(r"\s+", "-", value)
    value = re.sub(r"-+", "-", value).strip("-_.")
    if not value:
        return fallback
    return value[:80]


def build_output_path(*, output_arg: str, image_type: str, topic: str, explicit_name: str, ext: str) -> Path:
    if output_arg:
        out = Path(output_arg).expanduser().resolve()
        if out.suffix:
            return out
        return out.with_suffix(ext)
    now = datetime.now()
    day_dir = DEFAULT_OUTPUT_ROOT / now.strftime("%Y-%m-%d")
    day_dir.mkdir(parents=True, exist_ok=True)
    base_name = sanitize_name(explicit_name or topic, fallback=image_type)
    return day_dir / f"{now.strftime('%Y%m%d-%H%M%S')}__{image_type}__{base_name}{ext}"


def metadata_path_for(image_path: Path) -> Path:
    return image_path.with_suffix(image_path.suffix + ".json")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Generate images via the draw image backend.")
    parser.add_argument("--type", choices=sorted(ASPECT_RATIOS.keys()), default="wide", help="Output naming preset; not sent to the API.")
    parser.add_argument("--prompt", required=True, help="Full prompt for image generation.")
    parser.add_argument("--ref", action="append", default=[], help="Reference image path. Repeat to upload multiple references.")
    parser.add_argument("--name", default="", help="Optional short output name.")
    parser.add_argument("-o", "--output", default="", help="Output image path.")
    parser.add_argument("--model", default=os.getenv("DRAW_MODEL", DEFAULT_MODEL), help="Model override.")
    parser.add_argument("--base-url", default=os.getenv("DRAW_BASE_URL", DEFAULT_BASE_URL), help="OpenAI-compatible base URL.")
    return parser.parse_args()


def resolve_api_key() -> str:
    return os.getenv("DRAW_API_KEY", "").strip()


def resolve_refs(refs: list[str]) -> list[Path]:
    paths: list[Path] = []
    for ref in refs:
        path = Path(ref).expanduser().resolve()
        if not path.is_file():
            print(f"[ERROR] Reference image does not exist or is not a file: {path}", file=sys.stderr)
            raise SystemExit(1)
        paths.append(path)
    return paths


def main() -> int:
    args = parse_args()
    api_key = resolve_api_key()
    if not api_key:
        print("[ERROR] No DRAW_API_KEY found. Set it as an environment variable.", file=sys.stderr)
        return 1

    output_path = build_output_path(
        output_arg=args.output,
        image_type=args.type,
        topic=args.name or "image",
        explicit_name=args.name,
        ext=".png",
    )
    final_path = output_path if output_path.suffix else output_path.with_suffix(".png")
    final_path.parent.mkdir(parents=True, exist_ok=True)

    refs = resolve_refs(args.ref)
    mode = "edit" if refs else "generate"
    backend = DrawImageBackend(api_key=api_key, base_url=args.base_url, model=args.model)

    try:
        if refs:
            backend.edit(prompt=args.prompt, refs=refs, output_path=final_path)
        else:
            backend.generate(prompt=args.prompt, output_path=final_path)
    except Exception as exc:
        print(f"[ERROR] Image generation failed: {exc}", file=sys.stderr)
        return 1

    meta_path = metadata_path_for(final_path)
    metadata = {
        "created_at": datetime.now().isoformat(timespec="seconds"),
        "type": args.type,
        "aspect_ratio": ASPECT_RATIOS[args.type],
        "prompt": args.prompt,
        "refs": [str(path) for path in refs],
        "provider": "aicodelink-compatible",
        "mode": mode,
        "model": args.model,
        "base_url": backend.base_url,
        "output_path": str(final_path),
    }
    meta_path.write_text(json.dumps(metadata, ensure_ascii=False, indent=2), encoding="utf-8")

    print(f"output_path={final_path}")
    print(f"metadata_path={meta_path}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
