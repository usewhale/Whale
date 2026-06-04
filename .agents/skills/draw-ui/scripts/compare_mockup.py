#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import math
from pathlib import Path

from PIL import Image, ImageChops, ImageEnhance, ImageStat


def load_rgb(path: Path) -> Image.Image:
    return Image.open(path).convert("RGB")


def fit_to_reference(candidate: Image.Image, reference: Image.Image) -> Image.Image:
    if candidate.size == reference.size:
        return candidate
    return candidate.resize(reference.size, Image.Resampling.LANCZOS)


def rms(diff: Image.Image) -> float:
    stat = ImageStat.Stat(diff)
    return math.sqrt(sum(value * value for value in stat.rms) / len(stat.rms))


def mean_abs(diff: Image.Image) -> float:
    stat = ImageStat.Stat(diff)
    return sum(stat.mean) / len(stat.mean)


def make_heatmap(diff: Image.Image) -> Image.Image:
    gray = diff.convert("L")
    boosted = ImageEnhance.Contrast(gray).enhance(2.2)
    boosted = ImageEnhance.Brightness(boosted).enhance(1.5)
    heat = Image.new("RGB", diff.size, (255, 255, 255))
    red = Image.new("RGB", diff.size, (255, 55, 55))
    heat.paste(red, mask=boosted)
    return heat


def main() -> int:
    parser = argparse.ArgumentParser(description="Compare a browser screenshot against a reference UI mockup.")
    parser.add_argument("--reference", type=Path, required=True)
    parser.add_argument("--candidate", type=Path, required=True)
    parser.add_argument("--out-dir", type=Path, required=True)
    parser.add_argument("--prefix", default="comparison")
    parser.add_argument(
        "--clip",
        action="append",
        default=[],
        help="Named crop to compare, format name:x,y,w,h in reference coordinates. Repeatable.",
    )
    args = parser.parse_args()

    reference = load_rgb(args.reference)
    candidate = fit_to_reference(load_rgb(args.candidate), reference)

    args.out_dir.mkdir(parents=True, exist_ok=True)

    def compare_region(name: str, ref_img: Image.Image, cand_img: Image.Image) -> dict:
        diff = ImageChops.difference(ref_img, cand_img)
        normalized_candidate = args.out_dir / f"{args.prefix}-{name}-candidate.png"
        diff_path = args.out_dir / f"{args.prefix}-{name}-diff.png"
        heatmap_path = args.out_dir / f"{args.prefix}-{name}-heatmap.png"
        cand_img.save(normalized_candidate)
        diff.save(diff_path)
        make_heatmap(diff).save(heatmap_path)
        return {
            "name": name,
            "size": ref_img.size,
            "rms_diff": round(rms(diff), 3),
            "mean_abs_diff": round(mean_abs(diff), 3),
            "candidate": str(normalized_candidate),
            "diff": str(diff_path),
            "heatmap": str(heatmap_path),
        }

    regions = [compare_region("full", reference, candidate)]

    for raw_clip in args.clip:
        try:
            name, raw_box = raw_clip.split(":", 1)
            x, y, w, h = [int(part) for part in raw_box.split(",")]
        except Exception as exc:
            raise SystemExit(f"Invalid --clip value {raw_clip!r}; expected name:x,y,w,h") from exc
        box = (x, y, x + w, y + h)
        regions.append(compare_region(name, reference.crop(box), candidate.crop(box)))

    metrics_path = args.out_dir / f"{args.prefix}-metrics.json"
    metrics = {
        "reference": str(args.reference),
        "candidate": str(args.candidate),
        "reference_size": reference.size,
        "candidate_size_original": Image.open(args.candidate).size,
        "candidate_size_compared": candidate.size,
        "regions": regions,
    }
    metrics_path.write_text(json.dumps(metrics, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps(metrics, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
