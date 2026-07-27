#!/usr/bin/env python3
"""Subset the Nerd Fonts "Symbols Nerd Font Mono" face into two WOFF2 files,
split along the two Unicode Private Use Area blocks Nerd Fonts occupies, so
the browser's own unicode-range font matching decides which (if either) ever
has to be downloaded.

Why two files instead of one: almost the entire byte cost of this font is
Material Design Icons, which Nerd Fonts had to move into the Supplementary
Private Use Area-A (U+F0000-FFFFD) once it outgrew the classic Private Use
Area (U+E000-F8FF) that Font Awesome / Devicons / Codicons / Powerline /
Seti / Octicons / etc. all still live in. A TUI screen that never draws an
MDI-family glyph should never have to fetch MDI's ~1 MB of outlines. Two
@font-face rules under the same font-family, each scoped to one block via
`unicode-range`, let the browser fetch only the block(s) actually referenced
by the glyphs on screen — that is a browser-native lazy-load, not something
this app has to implement.

Reproduce:

    python3 -m venv /tmp/nf-subset-venv
    /tmp/nf-subset-venv/bin/pip install fonttools brotli zopfli
    /tmp/nf-subset-venv/bin/python3 frontend/scripts/subset-nerd-font-symbols.py \
        --src /path/to/SymbolsNerdFontMono-Regular.ttf \
        --outdir frontend/public/fonts

`SymbolsNerdFontMono-Regular.ttf` is the "Mono" variant from the
NerdFontsSymbolsOnly.zip asset of a github.com/ryanoasis/nerd-fonts release
(github.com/ryanoasis/nerd-fonts/releases) — Mono, not the proportional
Symbols face, so glyph advance widths line up with the surrounding monospace
grid.

The two block boundaries (E000-F8FF, F0000-FFFFD) are the standard Unicode
"Private Use Area" and "Supplementary Private Use Area-A" block ranges, not
figures specific to this font or this script, so this reproduces identically
against any Nerd Fonts release that still uses them.
"""

from __future__ import annotations

import argparse
import os
import sys

from fontTools import subset
from fontTools.ttLib import TTFont

# (output filename, CSS unicode-range value, fontTools --unicodes range)
BLOCKS = [
    ("SymbolsNerdFontMono-PUA.woff2", "U+E000-F8FF", "U+E000-F8FF"),
    ("SymbolsNerdFontMono-SuppPUA-A.woff2", "U+F0000-FFFFD", "U+F0000-FFFFD"),
]


def subset_block(src: str, out_path: str, unicodes_range: str) -> None:
    font = TTFont(src)
    options = subset.Options()
    # No hinting program or localized names are worth keeping for glyphs
    # that are all plain vector icon outlines; layout features (ligatures
    # etc.) don't apply to a symbols-only face either.
    options.name_IDs = [1, 2, 4, 6]  # family, subfamily, full name, PostScript name
    options.name_legacy = False
    options.layout_features = []
    options.hinting = False
    options.flavor = "woff2"

    # These four are what actually shrink the file, and a first pass without
    # them shipped 2.2 MB. Measured on the v3.4.0 Symbols Nerd Font Mono, with
    # every one of the 16 private-use codepoints observed in live agent output
    # still present afterwards:
    #
    #   base PUA   1057 kB -> 596 kB
    #   MDI block  1168 kB -> 489 kB
    #
    # desubroutinize trades a little size in CFF for a lot in WOFF2's own
    # Brotli pass; the dropped tables are shaping and metadata that a
    # symbols-only face on a monospace grid never consults.
    options.desubroutinize = True
    options.glyph_names = False
    options.legacy_kern = False
    options.drop_tables += ["DSIG", "GSUB", "GPOS", "MATH", "BASE", "JSTF"]

    subsetter = subset.Subsetter(options=options)
    subsetter.populate(unicodes=subset.parse_unicodes(unicodes_range))
    subsetter.subset(font)
    font.save(out_path)


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--src", required=True, help="SymbolsNerdFontMono-Regular.ttf")
    ap.add_argument("--outdir", required=True, help="output directory")
    args = ap.parse_args()

    os.makedirs(args.outdir, exist_ok=True)
    for filename, _css_range, subset_range in BLOCKS:
        out_path = os.path.join(args.outdir, filename)
        subset_block(args.src, out_path, subset_range)
        size = os.path.getsize(out_path)
        print(f"wrote {out_path} ({size:,} bytes)", file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
