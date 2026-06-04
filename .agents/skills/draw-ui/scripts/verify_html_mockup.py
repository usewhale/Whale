#!/usr/bin/env python3
from __future__ import annotations

import argparse
import os
import subprocess
import sys
from pathlib import Path


SCRIPT_DIR = Path(__file__).resolve().parent


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description=(
            "Open an HTML mockup in an isolated agent-browser session, capture a "
            "screenshot, then compare it against a reference image."
        )
    )
    parser.add_argument("--html", required=True, help="Path or file:// URL for the HTML file.")
    parser.add_argument("--reference", required=True, help="Reference mockup image path.")
    parser.add_argument("--out-dir", required=True, help="Directory for screenshots and comparison output.")
    parser.add_argument("--viewport", default="1024x1536", help="Viewport size, for example 1024x1536.")
    parser.add_argument("--full-page", action="store_true", help="Capture only a full-page screenshot.")
    parser.add_argument("--session", default="ui-design-verify", help="agent-browser session name.")
    return parser.parse_args()


def require_file(path: Path, label: str) -> None:
    if not path.is_file():
        raise SystemExit(f"{label} file not found: {path}")


def html_url(raw: str) -> str:
    if raw.startswith("file://"):
        return raw
    return Path(raw).expanduser().resolve().as_uri()


def parse_viewport(value: str) -> tuple[str, str]:
    if "x" not in value:
        raise SystemExit("Viewport must look like 1024x1536")
    width, height = value.split("x", 1)
    if not width.isdigit() or not height.isdigit():
        raise SystemExit("Viewport must look like 1024x1536")
    return width, height


def agent_browser_base(session: str) -> list[str]:
    command = ["agent-browser", "--session", session]
    executable_path = os.getenv("AGENT_BROWSER_EXECUTABLE_PATH", "").strip()
    if executable_path:
        command.extend(["--executable-path", executable_path])
    command.append("--allow-file-access")
    return command


def run_agent_browser(session: str, *args: str) -> None:
    subprocess.run([*agent_browser_base(session), *args], check=True)


def main() -> int:
    args = parse_args()
    html_path = Path(args.html).expanduser() if not args.html.startswith("file://") else None
    reference_path = Path(args.reference).expanduser()
    out_dir = Path(args.out_dir).expanduser()

    if html_path is not None:
        require_file(html_path, "HTML")
    require_file(reference_path, "Reference image")

    viewport_w, viewport_h = parse_viewport(args.viewport)
    out_dir.mkdir(parents=True, exist_ok=True)

    screenshot = out_dir / "browser-screenshot.png"
    full_screenshot = out_dir / "browser-full-page.png"

    run_agent_browser(args.session, "set", "viewport", viewport_w, viewport_h)
    run_agent_browser(args.session, "open", html_url(args.html))

    if args.full_page:
        run_agent_browser(args.session, "screenshot", str(screenshot), "--full")
    else:
        run_agent_browser(args.session, "screenshot", str(screenshot))
        run_agent_browser(args.session, "screenshot", str(full_screenshot), "--full")

    compare_script = SCRIPT_DIR / "compare_mockup.py"
    subprocess.run(
        [
            sys.executable,
            str(compare_script),
            "--reference",
            str(reference_path),
            "--candidate",
            str(screenshot),
            "--out-dir",
            str(out_dir),
            "--prefix",
            "verify",
        ],
        check=True,
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
