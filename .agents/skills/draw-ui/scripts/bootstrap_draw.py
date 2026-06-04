#!/usr/bin/env python3
from __future__ import annotations

import subprocess
import sys
from pathlib import Path


SCRIPT_DIR = Path(__file__).resolve().parent


def expand_frame_arg(args: list[str]) -> list[str]:
    expanded: list[str] = []
    frame_path = ""
    index = 0

    while index < len(args):
        arg = args[index]
        if arg == "--frame":
            if index + 1 >= len(args):
                print("[ERROR] --frame requires a path", file=sys.stderr)
                raise SystemExit(2)
            frame_path = args[index + 1]
            index += 2
            continue
        expanded.append(arg)
        index += 1

    if frame_path:
        return ["--ref", frame_path, *expanded]
    return expanded


def main() -> int:
    generate_script = SCRIPT_DIR / "generate_image.py"
    args = expand_frame_arg(sys.argv[1:])
    completed = subprocess.run([sys.executable, str(generate_script), *args])
    return completed.returncode


if __name__ == "__main__":
    raise SystemExit(main())
