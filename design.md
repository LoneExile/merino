# Design — Herdr Tunnel

A locked design system for this app. Every screen reads this file before emitting
code. Do not regenerate per screen — extend or amend this file when the system
needs to grow.

The product is a **control surface for AI coding agents running in terminal
panes**, used primarily from a phone over a tunnel. It is an instrument panel,
not a marketing page. Function carries every screen.

## Mark

A **sheep**. The product herds agents; the name is `herdr`. One parametric
drawing in `assets/sheep/gen_sheep.py` produces every appearance of it, so the
app icon and the menu-bar icon are provably the same animal rather than two
drawings that drift.

Two renderings, from one geometry:

- **App / PWA / favicon** — colour. Cobalt fleece (`--color-accent` dark) with
  a near-black head, legs and drawn outline, on a mid-graphite tile. The tile
  is deliberately lighter than the app's own paper: a near-black head on a
  near-black ground is invisible, which is the single thing that separates a
  readable sheep from a blue blob.
- **macOS menu bar** — a flat black silhouette on transparency. Not a style
  choice: macOS treats a *template* image as a stencil and re-tints it, which
  is the only way one icon reads correctly on a light bar, a dark bar, and a
  highlighted bar. The silhouette skips the outline pass entirely — drawing an
  outline in the fill colour fattens every shape and welds the legs together.

The art direction came from a generated reference (xAI Grok), then was redrawn
as vector rather than traced. Tracing a JPEG inherits its soft edges, and more
importantly a traced blob cannot be re-posed — the menu bar needs the same
animal in eleven poses.

**Small sizes are the whole constraint.** There is no separate simplified mark:
a single-colour silhouette was tried for favicon sizes and lost a head-to-head
comparison at 16/20/24/32 px, because the dark head is what gives the shape a
front. Favicons are rendered at their target size from the same drawing, never
downscaled from the 192 px icon — that downscale is what made the tab icon a
smudge. The geometry earns its legibility from a near-flat fleece underside so
the legs read as legs, three legs rather than four so they do not merge, and a
head that breaks the fleece outline. Every one of those was fixed after looking
at the mark at actual size, not at 512 px.

**The mark animates to report state**, and only in the menu bar:

| Herd state | Sheep |
| --- | --- |
| Any agent blocked | jumps — it needs you |
| Any agent working | walks |
| Neither | stands still, no timer running |

Idle is genuinely static. An animation loop that never sleeps is a battery
bug, and a permanently-moving icon stops carrying information.

## Genre

**modern-minimal** — developer tool / instrument panel register.

## Theme

**Cobalt** (catalog), with one deliberate extension: Cobalt ships as a light
theme carrying a single dark graphite band. This app needs a full dark mode, so
**dark mode is that graphite register applied to the whole surface** — the same
palette, not a second theme. Light and dark are one system seen at two
lightnesses, which is why the token names are identical in both.

- Paper band · cool light (`L 98.5%`, hue ~250) / graphite (`L 19%`) in dark
- Display style · grotesk-sans (Space Grotesk 500/600)
- Accent hue · electric cobalt `oklch(58% 0.20 256)`, lifted to
  `oklch(70% 0.17 254)` on dark so it clears contrast against graphite

Exact values live in `frontend/src/tokens.css`. Screens reference tokens by
name — never raw colour values.

## Structure — bespoke app shell

No catalog macrostructure describes an application shell. The 21 macrostructures
are page-shapes for marketing and content sites: they assume a hero, a scroll,
and a CTA. This product has a persistent chrome, a list, and a live terminal.
Forcing Workbench (a *guided tour of screenshots*) onto it would be theatre.

So the structure is **bespoke**, the theme is **catalog Cobalt**, and the shape
is fixed here instead:

```
┌─ Rail ─────────────────────────────┐   Fixed. Never scrolls.
│  wordmark · session · ⌘K · theme   │
├────────────────────────────────────┤
│  Needs you        (blocked first)  │   Scrolls. The only scrolling region
│  Working                           │   on the dashboard.
│  Idle                              │
└────────────────────────────────────┘

┌─ Rail ─────────────────────────────┐   Pane view replaces the list;
│  ‹ back · agent · status · ⋯       │   it does not stack below it.
├────────────────────────────────────┤
│                                    │
│  terminal output (graphite)        │   Scrolls. Pinned to bottom by
│                                    │   default; unpins when the user
├────────────────────────────────────┤   scrolls up.
│  composer                     Send │   Fixed. Safe-area padded.
└────────────────────────────────────┘
```

**The load-bearing rule: exactly one scrolling region per screen.** Everything
else is fixed. The original UI failed this — the composer lived at the bottom of
a long document, so reaching it meant scrolling past the whole terminal buffer.
On a phone that is the difference between usable and not.

Heights use `dvh`, never `vh`; the composer carries
`env(safe-area-inset-bottom)`.

## Typography

Self-hosted via `@fontsource` — no external font CDN. The app is served behind
a strict `default-src 'self'` CSP and must work offline on a LAN; a Google Fonts
link would punch a hole in both.

- Display · Space Grotesk 500/600, tracking `-0.02em`
- Body · Inter 400/500/600
- Mono · JetBrains Mono 400/500 — terminal output, labels, keyboard hints,
  pane and session IDs

Mono is not decoration here. Terminal output is column-aligned by the agent that
wrote it; a proportional face would corrupt it.

Labels (eyebrows, status, meta) are mono, UPPERCASE, `0.06em` tracking.

## Spacing

4-point named scale in `tokens.css`. Screens use `var(--space-md)`, never raw
values.

## Motion

Composed and sparse. This is an instrument panel: motion reports state, it never
performs.

- Easings · `--ease-out: cubic-bezier(0.16, 1, 0.3, 1)` only
- Durations · `--dur-fast: 120ms`, `--dur-base: 200ms`
- Allowed · sheet slide-up, status-dot pulse while working, one-shot
  highlight when a pane becomes blocked
- Banned · scroll reveals (this is an app, not a landing page), bounce,
  overshoot, parallax, autoplay, skeleton shimmer
- `prefers-reduced-motion: reduce` collapses every transform to an opacity
  crossfade ≤ 150 ms and stops the pulse

## Microinteractions stance

- **Silent success.** A sent message appears in the terminal — that is the
  receipt. No toast.
- **Loud failure.** A refused write states which rule refused it, inline.
- Optimistic composer clear, restored on error with the text intact. Never make
  someone retype a message the server rejected.
- Hover tooltips delay 800 ms; focus tooltips 0 ms.
- Destructive or privileged actions (rename, session switch) confirm inline in
  place, never in a modal that hides the thing being changed.

## Status colour

Status is **data**, not decoration, and is the one place a second hue is allowed
past the single-accent rule — a dashboard that cannot distinguish *blocked* from
*working* at a glance has failed at its only job.

- `--status-blocked` · needs you
- `--status-working` · running
- `--status-idle` · parked

Status colour appears **only** on the status dot and the section label. It never
tints a surface, never colours body text, and never appears in chrome. Never
encoded by colour alone: every status carries a text label.

## CTA voice

- Primary · solid cobalt, 6 px radius, never a pill, never a gradient
- Secondary · 1 px `--color-rule` border, transparent ground
- Destructive · ink text, `--status-blocked` border, filled only on confirm
- Labels name the action: *Send*, *Interrupt*, *Rename pane*. Never *Submit*,
  *OK*, *Click here*.

## What every screen MUST share

- The rail: wordmark, session, ⌘K, theme control — same order, same place
- Exactly one scrolling region
- Mono for anything the machine wrote
- The accent, under 5 % of any viewport
- 6 px control radius, 10 px surface radius
- 44 px minimum touch target (48 px on `pointer: coarse`)

## What screens MAY differ on

- Whether the primary region is a list or a terminal
- Which actions the rail's overflow carries
- Density — the terminal is tighter than the list

## Accessibility floor

- WCAG AA against the actual paper in both modes — verified, not assumed
- Visible `:focus-visible` ring at ≥ 3:1, never animated
- Every control reachable by keyboard; ⌘K palette is focus-trapped with Esc
- Status announced as text, never colour alone
- Renders correctly at 320 / 375 / 414 / 768 px with no horizontal scroll

## Per-screen allowances

- **No enrichment anywhere.** No illustration, no hero art, no decorative
  imagery. Function carries the page.
- No marketing footer. The app has a rail and a body; that is all.
