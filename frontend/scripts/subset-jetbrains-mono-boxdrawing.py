#!/usr/bin/env python3
"""Subset JetBrains Mono box-drawing (+ a few TUI symbol blocks) to WOFF2.

Why this exists
---------------
The dashboard imports `@fontsource/jetbrains-mono/latin-{400,500}.css` only.
That latin pack's cmap stops around Latin Extended — it does NOT include
box-drawing (U+2500–259F), block elements, geometric shapes, arrows, or
braille. A TUI frame then paints `─│┌┐` from the next face in
`--font-mono` (ui-monospace / SF Mono / Menlo on macOS), whose advance is
not JetBrains Mono's 600 units. After ~100–290 columns the right edge of
the frame walks visibly off.

This script cuts a ~10 kB WOFF2 per weight that covers only those
codepoints, all still at the 600-unit cell width. Two @font-face rules in
`frontend/src/app.css` register them under the same family name
("JetBrains Mono") with a tight unicode-range, so the browser stitches one
logical font: latin text from the small @fontsource pack, line art from
this subset. Panes that never paint a box glyph never download it.

Reproduce
---------

    # Official release (SIL OFL 1.1):
    #   https://github.com/JetBrains/JetBrainsMono/releases
    # Extract fonts/ttf/JetBrainsMono-Regular.ttf and
    # fonts/ttf/JetBrainsMono-Medium.ttf from the zip.

    python3 -m venv /tmp/jbm-subset-venv
    /tmp/jbm-subset-venv/bin/pip install fonttools brotli
    /tmp/jbm-subset-venv/bin/python3 \\
        frontend/scripts/subset-jetbrains-mono-boxdrawing.py \\
        --regular /path/to/JetBrainsMono-Regular.ttf \\
        --medium  /path/to/JetBrainsMono-Medium.ttf \\
        --outdir  frontend/public/fonts

Outputs (committed under frontend/public/fonts/):

    JetBrainsMono-BoxDrawing-400.woff2
    JetBrainsMono-BoxDrawing-500.woff2

License: SIL OFL 1.1 — see frontend/public/fonts/JetBrainsMono-OFL.txt.
"""

from __future__ import annotations

import argparse
import sys
from pathlib import Path

from fontTools import subset
from fontTools.ttLib import TTFont

# Bullets/ellipsis, arrows, box-drawing, block elements, geometric shapes,
# braille (status bars). Keep this list in lockstep with the unicode-range
# on the matching @font-face rules in frontend/src/app.css.
RANGE_SPEC = (
    "U+2022,U+2023,U+2026,"
    "U+2190-21FF,"
    "U+2500-259F,"
    "U+25A0-25FF,"
    "U+2800-28FF"
)

WEIGHTS = (
    ("regular", 400, "JetBrainsMono-BoxDrawing-400.woff2"),
    ("medium", 500, "JetBrainsMono-BoxDrawing-500.woff2"),
)


def expand(spec: str) -> list[int]:
    cps: set[int] = set()
    for part in spec.split(","):
        part = part.strip().upper().replace("U+", "")
        if not part:
            continue
        if "-" in part:
            a, b = part.split("-", 1)
            cps.update(range(int(a, 16), int(b, 16) + 1))
        else:
            cps.add(int(part, 16))
    return sorted(cps)


def subset_face(src: Path, out: Path, unicodes: list[int]) -> None:
    opts = subset.Options()
    opts.layout_features = ["*"]
    opts.name_IDs = ["*"]
    opts.name_legacy = True
    opts.name_languages = ["*"]
    opts.notdef_outline = True
    opts.recalc_bounds = True
    opts.canonical_order = True
    opts.flavor = "woff2"
    font = subset.load_font(str(src), opts)
    subsetter = subset.Subsetter(options=opts)
    subsetter.populate(unicodes=unicodes)
    subsetter.subset(font)
    out.parent.mkdir(parents=True, exist_ok=True)
    subset.save_font(font, str(out), opts)

    # Sanity: every box-drawing codepoint present, cell width still 600.
    check = TTFont(out)
    cmap = check.getBestCmap() or {}
    missing = [f"U+{cp:04X}" for cp in range(0x2500, 0x2580) if cp not in cmap]
    if missing:
        raise SystemExit(f"{out.name}: missing box-drawing glyphs: {missing[:8]}…")
    hmtx = check["hmtx"].metrics
    box_glyph = cmap[0x2500]
    advance, _ = hmtx[box_glyph]
    if advance != 600:
        raise SystemExit(
            f"{out.name}: box-drawing advance is {advance}, want 600 "
            f"(must match JetBrains Mono latin cell width)"
        )
    print(
        f"wrote {out} ({out.stat().st_size} B) "
        f"cmap={len(cmap)} box-advance={advance}"
    )


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--regular", required=True, type=Path, help="JetBrainsMono-Regular.ttf")
    ap.add_argument("--medium", required=True, type=Path, help="JetBrainsMono-Medium.ttf")
    ap.add_argument("--outdir", required=True, type=Path)
    args = ap.parse_args()

    unicodes = expand(RANGE_SPEC)
    sources = {"regular": args.regular, "medium": args.medium}
    for key, _weight, filename in WEIGHTS:
        src = sources[key]
        if not src.is_file():
            print(f"missing source: {src}", file=sys.stderr)
            return 2
        subset_face(src, args.outdir / filename, unicodes)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

