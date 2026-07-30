/**
 * Escape handling for the pane composer.
 *
 * Its own module rather than a helper inside PaneView.tsx: `.tsx` cannot be
 * loaded by `node --experimental-strip-types`, so anything defined there is
 * unreachable from this repo's `__checks__` scripts. Escape in the composer
 * now arbitrates between four different consumers, which is real logic and
 * exactly the kind of thing that should be provable without a phone.
 */

/** What an Escape keypress in the composer should do. */
export type ComposerEscapeAction =
  /** Close the slash-command typeahead. */
  | "slash"
  /** Collapse the fullscreen writing surface, keeping the draft. */
  | "collapse"
  /** Forward Escape to the terminal as a TUI key. */
  | "pane"
  /** Discard the current draft. */
  | "clear"
  /** Nothing to do; let the event through. */
  | "none";

export interface ComposerEscapeState {
  /** The slash typeahead is showing hits. */
  slashOpen: boolean;
  /** The composer is in fullscreen compose mode. */
  expanded: boolean;
  /** The draft is empty (after trimming is the caller's choice). */
  empty: boolean;
  /** This transport and pane may send raw keys to the terminal. */
  canKeys: boolean;
}

/**
 * Escape precedence, most specific first:
 *
 *   1. slash menu open   -> close it. An in-progress typeahead is the most
 *                           local interaction and must never be swallowed.
 *   2. expanded          -> collapse, leaving the draft alone. Backing out of
 *                           a mode comes before acting on content, which is
 *                           how the sheets and the palette already behave.
 *   3. empty + canKeys   -> forward to the pane. Pre-existing behaviour: an
 *                           empty composer is a TUI keypad.
 *   4. non-empty draft   -> clear it. Pre-existing behaviour.
 *   5. otherwise         -> nothing, matching what fired before this existed.
 *
 * Rules 3 to 5 are unchanged by fullscreen compose; only 1 and 2 sit in front
 * of them.
 */
export function composerEscapeAction(state: ComposerEscapeState): ComposerEscapeAction {
  if (state.slashOpen) return "slash";
  if (state.expanded) return "collapse";
  if (state.canKeys && state.empty) return "pane";
  if (!state.empty) return "clear";
  return "none";
}
