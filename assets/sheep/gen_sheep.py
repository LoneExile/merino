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

# --- the sheep, in 100x100 units, facing right -------------------------------

# Fleece is a union of circles: overlapping discs read as a soft mass at any
# size, where a hand-drawn bezier fleece turns to mush below about 32px.
# Bottom row is deliberately shallow: the fleece needs a near-flat underside so
# the legs read as attached to a body. With a rounded bottom the legs start
# inside the mass and look like fringe.
FLEECE = [
    (36, 39, 18),
    (23, 44, 13),
    (50, 37, 16),
    (30, 28, 12),
    (46, 26, 11),
    (28, 50, 12),
    (44, 50, 12),
    (53, 44, 11),
]
FLEECE_BOTTOM = 62

LEG_X = (29, 40, 52, 61)
LEG_TOP = 58
LEG_LEN = 26
LEG_W = 7.0

# The head must break the fleece outline, not sit on it. At 22px in a menu bar
# the silhouette is all there is, and a round mass with a bump reads as a
# sheep only when the bump is unmistakably a head on a neck.
HEAD = (76, 48, 12.0, 12.5)   # cx, cy, rx, ry
MUZZLE = (85, 53, 7.0, 5.6)   # cx, cy, rx, ry
EAR = (70, 37, 6.2, 3.6, -34)  # cx, cy, rx, ry, rotation
EYE = (79, 45, 2.0)
TAIL = (18, 38, 5.5)


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
        f'stroke-width="{LEG_W}" stroke-linecap="round" />'
    )


def sheep_body(fg: str, accent: str, legs, bob: float, tuck: float = 0.0) -> str:
    """The sheep itself, no background.

    fg paints fleece and legs; accent paints the head. When the two are equal
    the result is a single flat silhouette, which is exactly what a macOS
    template icon must be.
    """
    parts = [f'<g stroke="{fg}" fill="{fg}">']
    parts.extend(leg(x, a, bob, tuck) for x, a in zip(LEG_X, legs))
    parts.append("</g>")

    parts.append(f'<g fill="{fg}" stroke="none">')
    parts.append(f'<circle cx="{TAIL[0]}" cy="{TAIL[1] + bob:.2f}" r="{TAIL[2]}" />')
    for cx, cy, r in FLEECE:
        parts.append(f'<circle cx="{cx}" cy="{cy + bob:.2f}" r="{r}" />')
    parts.append("</g>")

    ex, ey, erx, ery, erot = EAR
    hx, hy, hrx, hry = HEAD
    parts.append(f'<g fill="{accent}" stroke="none">')
    parts.append(
        f'<ellipse cx="{ex}" cy="{ey + bob:.2f}" rx="{erx}" ry="{ery}" '
        f'transform="rotate({erot} {ex} {ey + bob:.2f})" />'
    )
    parts.append(f'<ellipse cx="{hx}" cy="{hy + bob:.2f}" rx="{hrx}" ry="{hry}" />')
    mx, my, mrx, mry = MUZZLE
    parts.append(f'<ellipse cx="{mx}" cy="{my + bob:.2f}" rx="{mrx}" ry="{mry}" />')
    parts.append("</g>")

    # The eye is punched in the ground colour on the colour icon. On a flat
    # silhouette it would be invisible, so callers pass eye=None there.
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
    body = sheep_body(WOOL, COBALT, legs=(-9, 7, -7, 9), bob=0) + eye_dot(GROUND, 0)
    scale = 0.68 if maskable else 0.84
    radius = 0 if maskable else 22
    return svg(size, body, GROUND, radius, scale, 50, 52)


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
    ((-30, 24, -24, 30), 0.0),
    ((-16, 12, -12, 16), 1.1),
    ((-2, -2, 2, 2), 1.6),
    ((24, -30, 30, -24), 0.0),
    ((12, -16, 16, -12), 1.1),
    ((2, 2, -2, -2), 1.6),
]

# Jump: crouch, launch, tuck at apex, extend, land. The bob carries the arc;
# the tuck sells it as effort rather than a hover.
JUMP = [
    ((-8, 8, -8, 8), 4.0, 0.30),
    ((-34, 34, -30, 30), -4.0, 0.42),
    ((-44, 44, -40, 40), -10.0, 0.58),
    ((-34, 34, -30, 30), -4.0, 0.42),
    ((-8, 8, -8, 8), 2.5, 0.22),
]

IDLE = ((-5, 4, -4, 5), 0.0)


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

    w("tray-idle.svg", tray_frame(*IDLE))
    for i, (legs, bob) in enumerate(WALK):
        w(f"tray-walk-{i}.svg", tray_frame(legs, bob))
    for i, (legs, bob, tuck) in enumerate(JUMP):
        w(f"tray-jump-{i}.svg", tray_frame(legs, bob, tuck))

    print(f"wrote {len(written)} svg files to {out}")


if __name__ == "__main__":
    main()
