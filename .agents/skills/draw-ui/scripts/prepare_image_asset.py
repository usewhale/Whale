#!/usr/bin/env python3
from __future__ import annotations

import argparse
from pathlib import Path

from PIL import Image


def foreground_mask(img: Image.Image, *, threshold: int) -> Image.Image:
    rgb = img.convert("RGB")
    return Image.eval(rgb, lambda px: 0 if px >= threshold else 255).convert("L")


def trim(img: Image.Image, *, threshold: int, padding: int) -> Image.Image:
    mask = foreground_mask(img, threshold=threshold)
    bbox = mask.getbbox()
    if bbox is None:
        return img

    left, top, right, bottom = bbox
    left = max(0, left - padding)
    top = max(0, top - padding)
    right = min(img.width, right + padding)
    bottom = min(img.height, bottom + padding)
    return img.crop((left, top, right, bottom))


def trim_alpha(img: Image.Image, *, padding: int) -> Image.Image:
    rgba = img.convert("RGBA")
    alpha = rgba.getchannel("A")
    bbox = alpha.getbbox()
    if bbox is None:
        return rgba

    left, top, right, bottom = bbox
    left = max(0, left - padding)
    top = max(0, top - padding)
    right = min(rgba.width, right + padding)
    bottom = min(rgba.height, bottom + padding)
    return rgba.crop((left, top, right, bottom))


def white_to_alpha(img: Image.Image, *, threshold: int, feather: int) -> Image.Image:
    rgba = img.convert("RGBA")
    pixels = rgba.load()

    for y in range(rgba.height):
        for x in range(rgba.width):
            red, green, blue, alpha = pixels[x, y]
            brightness = max(red, green, blue)
            if red >= threshold and green >= threshold and blue >= threshold:
                pixels[x, y] = (255, 255, 255, 0)
                continue
            if feather > 0 and brightness >= threshold - feather:
                new_alpha = int(alpha * (threshold - brightness + feather) / feather)
                pixels[x, y] = (red, green, blue, max(0, min(alpha, new_alpha)))

    return rgba


def parse_hex_color(value: str) -> tuple[int, int, int]:
    raw = value.strip().lstrip("#")
    if len(raw) != 6:
        raise ValueError("key color must be a 6-digit hex color, for example #00ff00")
    return int(raw[0:2], 16), int(raw[2:4], 16), int(raw[4:6], 16)


def key_color_to_alpha(
    img: Image.Image,
    *,
    key_color: tuple[int, int, int],
    threshold: int,
    feather: int,
    despill: bool,
    edge_contract: int,
) -> Image.Image:
    rgba = img.convert("RGBA")
    pixels = rgba.load()
    kr, kg, kb = key_color

    for y in range(rgba.height):
        for x in range(rgba.width):
            red, green, blue, alpha = pixels[x, y]
            distance = ((red - kr) ** 2 + (green - kg) ** 2 + (blue - kb) ** 2) ** 0.5
            if distance <= threshold:
                pixels[x, y] = (red, green, blue, 0)
                continue
            if feather > 0 and distance <= threshold + feather:
                new_alpha = int(alpha * (distance - threshold) / feather)
                pixels[x, y] = (red, green, blue, max(0, min(alpha, new_alpha)))

    if despill:
        pixels = rgba.load()
        for y in range(rgba.height):
            for x in range(rgba.width):
                red, green, blue, alpha = pixels[x, y]
                if alpha == 0:
                    continue
                # Remove green spill on semi-transparent and edge pixels.
                green_excess = green - max(red, blue)
                if green_excess <= 0:
                    continue
                edge_factor = 1.0 - (alpha / 255.0)
                reduction = int(green_excess * (0.72 + 0.28 * edge_factor))
                green = max(max(red, blue), green - reduction)
                pixels[x, y] = (red, green, blue, alpha)

    if edge_contract > 0:
        rgba = contract_alpha_edge(rgba, pixels=edge_contract)

    return rgba


def contract_alpha_edge(img: Image.Image, *, pixels: int) -> Image.Image:
    rgba = img.convert("RGBA")
    alpha = rgba.getchannel("A")
    try:
        from PIL import ImageFilter
        for _ in range(pixels):
            alpha = alpha.filter(ImageFilter.MinFilter(3))
        rgba.putalpha(alpha)
    except Exception:
        pass
    return rgba


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Prepare AI-generated UI assets by trimming white padding and optionally making white background transparent."
    )
    parser.add_argument("input", type=Path)
    parser.add_argument("output", type=Path)
    parser.add_argument("--no-trim", action="store_true", help="Keep original canvas size.")
    parser.add_argument("--alpha", action="store_true", help="Convert near-white background to transparent alpha.")
    parser.add_argument("--key-color", help="Convert a chroma key color such as #00ff00 to transparent alpha.")
    parser.add_argument("--threshold", type=int, default=248, help="Near-white threshold, 0-255.")
    parser.add_argument("--key-threshold", type=int, default=28, help="Chroma key distance threshold.")
    parser.add_argument("--feather", type=int, default=12, help="Soft alpha range below threshold.")
    parser.add_argument("--despill", action="store_true", help="Reduce green spill on chroma-keyed edges.")
    parser.add_argument("--edge-contract", type=int, default=0, help="Shrink alpha edge by this many pixels after keying.")
    parser.add_argument("--padding", type=int, default=24, help="Padding kept around trimmed foreground.")
    args = parser.parse_args()

    img = Image.open(args.input).convert("RGBA")
    if args.key_color:
        img = key_color_to_alpha(
            img,
            key_color=parse_hex_color(args.key_color),
            threshold=args.key_threshold,
            feather=args.feather,
            despill=args.despill,
            edge_contract=args.edge_contract,
        )
        if not args.no_trim:
            img = trim_alpha(img, padding=args.padding)
    elif not args.no_trim:
        img = trim(img, threshold=args.threshold, padding=args.padding)
    if args.alpha:
        img = white_to_alpha(img, threshold=args.threshold, feather=args.feather)

    args.output.parent.mkdir(parents=True, exist_ok=True)
    img.save(args.output)
    print(f"wrote {args.output}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
