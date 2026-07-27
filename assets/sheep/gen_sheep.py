#!/usr/bin/env python3
"""Generate every sheep asset from one parametric drawing.

There is exactly one sheep in this project. It appears as the PWA/app icon in
colour, and as a monochrome macOS menu-bar template icon that walks, jumps or
stands still depending on what the herd is doing. Keeping both from a single
generator is the only way those two stay recognisably the same animal.

Emits SVG. Rasterising is done separately by render.mjs (headless Chrome),
because this machine has no rsvg/cairo/Pillow.

Geometry lives in a 100x100 unit box and is scaled at render time, so every
size is the same drawing rather than four hand-tuned ones.

Usage:  python3 gen_sheep.py <outdir>
"""

import math
import os
import sys

# Design tokens, converted from frontend/src/tokens.css. A manifest and a PNG
# cannot carry oklch(), so these are the computed sRGB of the exact same
# tokens — not a second palette.
GROUND = "#10141a"  # --color-paper        dark  oklch(19%   0.014 260)
WOOL = "#ebeff2"  # --color-ink          dark  oklch(95%   0.006 250)
COBALT = "#56a7ff"  # --color-accent       dark  oklch(72%   0.16  254)
# Head, legs and the drawn outline. A notch darker than the graphite ground so
# the sheep reads as sitting ON the tile rather than cut out of it — the move
# the reference art uses to separate a dark head from a dark background.
INK_DARK = "#141821"
# The icon TILE, not the app background. The head and legs are near-black, so
# the tile has to be light enough for them to read — the reference art uses a
# mid grey for exactly this reason. --color-paper-3 (dark) converted.
TILE = "#31363f"

# --- the sheep, in 100x100 units, facing right -------------------------------

# Fleece is a union of circles: overlapping discs read as a soft mass at any
# size, where a hand-drawn bezier fleece turns to mush below about 32px.
# Fleece: one solid body with a few big scallops on top, NOT a pile of equal
# circles. Eight same-sized discs read as a cloud at 512px and as grey mush at
# 32; four large bumps over a slab keep a recognisable outline all the way
# down. The underside is deliberately flat so the legs attach to a body.
FLEECE_BODY = (40, 45, 30, 22)  # cx, cy, rx, ry — the slab
FLEECE = [
    (20, 40, 12),
    (26, 29, 13),
    (41, 25, 14),
    (56, 30, 13),
    (62, 42, 12),
]
FLEECE_BOTTOM = 62

# Three legs, not four. The fourth sits behind the third at this angle and only
# ever reads as a thicker smudge; dropping it buys negative space, which is
# what makes a small icon legible.
LEG_X = (28, 42, 58)
LEG_TOP = 60
LEG_LEN = 24
LEG_W = 7.5
# Fat underlay stroke that becomes the drawn outline around the whole sheep.
OUTLINE_W = 7.0

# The head must break the fleece outline, not sit on it. At 22px in a menu bar
# the silhouette is all there is, and a round mass with a bump reads as a
# sheep only when the bump is unmistakably a head on a neck.
HEAD = (75, 47, 13.5, 14.0)   # cx, cy, rx, ry
MUZZLE = (86, 54, 8.0, 6.2)   # cx, cy, rx, ry
EAR = (67, 36, 7.0, 4.0, -38)  # cx, cy, rx, ry, rotation
EYE = (79, 44, 2.3)
TAIL = (14, 40, 6.0)


def leg(x: float, angle_deg: float, bob: float, tuck: float = 0.0) -> str:
    """One leg as a rounded stroke, swinging from its hip.

    tuck shortens it, which is what a jumping animal's legs do and what keeps
    the jump frames from looking like a sheep being levitated.
    """
    a = math.radians(angle_deg)
    length = LEG_LEN * (1.0 - tuck)
    x0, y0 = x, LEG_TOP + bob
    x1 = x0 + math.sin(a) * length
    y1 = y0 + math.cos(a) * length
    return (
        f'<line x1="{x0:.2f}" y1="{y0:.2f}" x2="{x1:.2f}" y2="{y1:.2f}" '
        f'stroke-linecap="round" />'
    )


def _fleece_shapes(bob: float) -> str:
    bx, by, brx, bry = FLEECE_BODY
    out = [
        f'<rect x="{bx - brx}" y="{by - bry + bob:.2f}" width="{brx * 2}" '
        f'height="{bry * 2}" rx="{bry}" />'
    ]
    out += [f'<circle cx="{cx}" cy="{cy + bob:.2f}" r="{r}" />' for cx, cy, r in FLEECE]
    out.append(f'<circle cx="{TAIL[0]}" cy="{TAIL[1] + bob:.2f}" r="{TAIL[2]}" />')
    return "".join(out)


def _head_shapes(bob: float) -> str:
    ex, ey, erx, ery, erot = EAR
    hx, hy, hrx, hry = HEAD
    mx, my, mrx, mry = MUZZLE
    return (
        f'<ellipse cx="{ex}" cy="{ey + bob:.2f}" rx="{erx}" ry="{ery}" '
        f'transform="rotate({erot} {ex} {ey + bob:.2f})" />'
        f'<ellipse cx="{hx}" cy="{hy + bob:.2f}" rx="{hrx}" ry="{hry}" />'
        f'<ellipse cx="{mx}" cy="{my + bob:.2f}" rx="{mrx}" ry="{mry}" />'
    )


def sheep_body(fleece: str, dark: str, legs, bob: float, tuck: float = 0.0,
               outline: str | None = None) -> str:
    """The sheep, drawn in the grok reference's language.

    A cloud of overlapping shapes has no single outline path, so the outline is
    drawn by painting every shape TWICE: once underneath with a fat stroke in
    the outline colour, then again on top with the fill. The union of the fat
    strokes is exactly the silhouette's border, which is what gives the clean
    outlined cloud without computing a real boolean union.

    Passing outline=None and fleece==dark collapses the whole thing to one flat
    silhouette, which is what a macOS template icon must be.
    """
    parts = []

    # Underlay: same geometry, fat stroke, paints the outline. Skipped entirely
    # when no outline is wanted — drawing it in the fill colour would silently
    # fatten every shape and weld the legs into one blob, which is exactly what
    # a flat silhouette must not do.
    if outline is not None:
        parts.append(f'<g fill="{outline}" stroke="{outline}" '
                     f'stroke-width="{OUTLINE_W}" stroke-linejoin="round">')
        parts.append(f'<g stroke-width="{LEG_W + OUTLINE_W}">')
        parts.extend(leg(x, a, bob, tuck) for x, a in zip(LEG_X, legs))
        parts.append("</g>")
        parts.append(_fleece_shapes(bob))
        parts.append(_head_shapes(bob))
        parts.append("</g>")

    # Legs and head sit dark on top of the fleece.
    parts.append(f'<g fill="{dark}" stroke="{dark}" stroke-width="{LEG_W}" '
                 f'stroke-linejoin="round">')
    parts.extend(leg(x, a, bob, tuck) for x, a in zip(LEG_X, legs))
    parts.append("</g>")

    parts.append(f'<g fill="{fleece}" stroke="none">{_fleece_shapes(bob)}</g>')
    parts.append(f'<g fill="{dark}" stroke="none">{_head_shapes(bob)}</g>')
    return "".join(parts)


def eye_dot(colour: str, bob: float) -> str:
    x, y, r = EYE
    return f'<circle cx="{x}" cy="{y + bob:.2f}" r="{r}" fill="{colour}" />'


def svg(size: int, body: str, bg: str | None, radius: float, scale: float, dx: float, dy: float) -> str:
    bg_rect = (
        f'<rect width="100" height="100" rx="{radius}" fill="{bg}" />' if bg else ""
    )
    return (
        f'<svg xmlns="http://www.w3.org/2000/svg" width="{size}" height="{size}" '
        f'viewBox="0 0 100 100" shape-rendering="geometricPrecision">'
        f"{bg_rect}"
        f'<g transform="translate({dx:.2f} {dy:.2f}) scale({scale:.4f}) translate(-50 -50) translate(50 50)">'
        f'<g transform="translate(-50 -50)">{body}</g>'
        f"</g></svg>"
    )


def app_icon(size: int, maskable: bool) -> str:
    """Colour icon: cobalt-headed sheep on graphite.

    Maskable variants must survive an aggressive circular crop, so the sheep is
    scaled to sit inside the 80% safe zone and the ground runs full-bleed.
    """
    body = sheep_body(COBALT, INK_DARK, legs=(-9, 2, 9), bob=0, outline=INK_DARK) + eye_dot(WOOL, 0)
    scale = 0.68 if maskable else 0.84
    radius = 0 if maskable else 22
    return svg(size, body, TILE, radius, scale, 50, 52)


# Deep blue for the head and legs on the TILE-LESS favicon. On the app icon
# they are near-black, which only works because it sits on a graphite tile; with
# no tile behind it a near-black head disappears into a dark browser tab strip.
# A dark BLUE stays legible on white and on black alike, and still reads as part
# of the same animal.
HEAD_BARE = "#14508f"
EYE_BARE = "#cfe4ff"


def favicon(size: int) -> str:
    """Browser-tab mark: the sheep on full transparency, no tile.

    A tile is right for a home-screen icon and wrong for a tab: the browser
    already supplies a background, and a baked-in graphite square just sits
    there as a rectangle in whatever colour the tab strip happens to be. The
    previous version carried one, and its rounded corners were the "white area"
    that showed against dark chrome.

    Same geometry as the app icon; only the colour roles change, because the
    tile is what made near-black safe.
    """
    body = sheep_body(COBALT, HEAD_BARE, legs=(-9, 2, 9), bob=0)
    body += f'<circle cx="{EYE[0]}" cy="{EYE[1]}" r="{EYE[2]}" fill="{EYE_BARE}" />'
    return svg(size, body, None, 0, 0.94, 50, 52)


# There is no separate small-size mark. An earlier revision carried a
# single-colour silhouette for favicon sizes, on the usual reasoning that
# two-tone art dies at 16px. Compared head to head at 16/20/24/32 against the
# current drawing, it lost at every size: the dark head is what gives the shape
# a front, and without it the silhouette is a blue blob. One artwork it is.


def tray_frame(legs, bob: float, tuck: float = 0.0, size: int = 44) -> str:
    """macOS template icon: one flat black silhouette on transparency.

    Black-on-transparent is required, not a style choice — macOS discards the
    colour of a template image and re-tints it to match the menu bar, so it
    inverts correctly in dark mode and while the bar is highlighted.
    """
    body = sheep_body("#000000", "#000000", legs=legs, bob=bob, tuck=tuck)
    return svg(size, body, None, 0, 0.92, 50, 52)


# --- pose sets ---------------------------------------------------------------

# A walk cycle is two diagonal pairs in opposition. Six frames is the fewest
# that still reads as walking rather than twitching.
WALK = [
    ((-30, 26, -22), 0.0),
    ((-16, 14, -10), 1.1),
    ((-2, 0, 2), 1.6),
    ((26, -30, 22), 0.0),
    ((14, -16, 10), 1.1),
    ((2, 0, -2), 1.6),
]

# Jump: crouch, launch, tuck at apex, extend, land. The bob carries the arc;
# the tuck sells it as effort rather than a hover.
JUMP = [
    ((-8, 8, -6), 4.0, 0.30),
    ((-36, 34, -30), -4.5, 0.42),
    ((-46, 44, -40), -11.0, 0.58),
    ((-36, 34, -30), -4.5, 0.42),
    ((-8, 8, -6), 2.5, 0.22),
]

IDLE = ((-5, 2, 5), 0.0)


def main() -> None:
    out = sys.argv[1] if len(sys.argv) > 1 else "."
    os.makedirs(out, exist_ok=True)
    written = []

    def w(name: str, content: str) -> None:
        path = os.path.join(out, name)
        with open(path, "w") as fh:
            fh.write(content)
        written.append(name)

    for size in (192, 512):
        w(f"icon-{size}.svg", app_icon(size, maskable=False))
    w("icon-512-maskable.svg", app_icon(512, maskable=True))
    w("apple-touch-icon.svg", app_icon(180, maskable=False))

    for n in (32, 64):
        w(f"favicon-{n}.svg", favicon(n))

    w("tray-idle.svg", tray_frame(*IDLE))
    for i, (legs, bob) in enumerate(WALK):
        w(f"tray-walk-{i}.svg", tray_frame(legs, bob))
    for i, (legs, bob, tuck) in enumerate(JUMP):
        w(f"tray-jump-{i}.svg", tray_frame(legs, bob, tuck))

    print(f"wrote {len(written)} svg files to {out}")


if __name__ == "__main__":
    main()
