# Product

## Register

product

## Users

Operators of a herdr herd (coding agents in terminal panes). Primary surface is a
phone PWA over LAN, Tailscale, or a public HTTPS tunnel; secondary is the macOS
menubar panel on the machine that runs herdr. Context is glanceable and
interrupt-driven: someone is mid-task and an agent is blocked, or they need to
pair a phone and get back to work.

## Product Purpose

Merino is a control surface for live herdr agents. It shows which agents
are working or blocked, streams pane output, and (when enabled) sends approvals,
keys, and interrupts. Pairing is one-shot QR with revocable device grants.
Success is: see blocked state immediately, answer without opening a full IDE,
and never lose track of which session or device is trusted.

## Brand Personality

Instrumental, terse, sheep-herding. Calm when idle; loud only when blocked.
Three words: **precise, quiet, legible**.

## Anti-references

- Marketing SaaS dashboards (hero metrics, gradient cards, illustration chrome)
- "AI product" neon-on-black crypto aesthetics
- Nested settings cards and multi-modal confirmation theatre
- Polling CLI wrappers and status UIs that lie about liveness

## Design Principles

1. **Instrument panel, not brochure.** Function carries every screen; no
   enrichment art or hero copy.
2. **Exactly one scrolling region per screen.** Chrome is fixed; the list or
   terminal scrolls.
3. **Status is data.** Blocked/working/idle use dedicated hues only on the status
   mark and label, never as surface tints, and never colour-alone.
4. **Silent success, loud failure.** A sent message appearing in the stream is
   the receipt; refused writes name the rule inline.
5. **Phone-first trust.** Touch targets ≥44px, pairing is revocable, session
   switch is gated by the Mac operator.

## Accessibility & Inclusion

- WCAG AA against paper in light and dark (verified, not assumed)
- Visible `:focus-visible` rings; full keyboard reach including ⌘K palette
- Status always has a text label
- `prefers-reduced-motion: reduce` collapses motion to short opacity fades
- Layouts hold at 320 / 375 / 414 / 768 without horizontal scroll
