/**
 * Parses ANSI/SGR escape sequences out of a herdr pane's raw terminal text
 * into styled segments for the web dashboard's terminal view.
 *
 * Deliberately framework-agnostic: this module has no React import and
 * returns plain data (text + a CSS-style-properties object), never HTML
 * strings. Terminal output comes from whatever program is running in the
 * pane — treat it as hostile input. Building an HTML string from it (or
 * routing it through `dangerouslySetInnerHTML`) would make arbitrary pane
 * output a markup-injection vector; returning structured segments for the
 * caller to render as real React elements means there is no string for a
 * crafted escape sequence to ever inject into.
 *
 * Scope is SGR (`ESC[...m`, Select Graphic Rendition) only. A pane screen
 * dump also contains cursor moves, erase-in-line, and OSC title sequences —
 * herdr snapshots the CURRENT screen rather than a replayable session log,
 * so those never need to be interpreted, only consumed so they don't leak
 * into the visible text as stray control bytes.
 */

/** A CSSProperties-compatible style bag — deliberately not `React.CSSProperties`
 *  itself, so this file stays dependency-free and independently testable. */
export interface AnsiInlineStyle {
  color?: string;
  backgroundColor?: string;
  fontWeight?: number;
  fontStyle?: "italic";
  textDecoration?: "underline";
  opacity?: number;
}

export interface AnsiSegment {
  text: string;
  style: AnsiInlineStyle;
}

/** Mutable run state the SGR codes accumulate into between flushes. */
interface RunState {
  bold: boolean;
  dim: boolean;
  italic: boolean;
  underline: boolean;
  inverse: boolean;
  /** CSS colour value ("var(--ansi-red)" or "rgb(r, g, b)"), or undefined
   *  for "whatever the terminal's default is". */
  fg: string | undefined;
  bg: string | undefined;
}

function defaultState(): RunState {
  return { bold: false, dim: false, italic: false, underline: false, inverse: false, fg: undefined, bg: undefined };
}

function resetState(s: RunState): void {
  s.bold = false;
  s.dim = false;
  s.italic = false;
  s.underline = false;
  s.inverse = false;
  s.fg = undefined;
  s.bg = undefined;
}

// The 16-colour palette, as CSS custom-property references (frontend/src/tokens.css).
// fg and bg draw from the identical 16-colour set — ANSI has no separate
// "background red" hue, just the same red applied to the other channel.
const BASE_NAMES = ["black", "red", "green", "yellow", "blue", "magenta", "cyan", "white"] as const;

function colorVar(name: string): string {
  return `var(--ansi-${name})`;
}

const BASE_COLORS = BASE_NAMES.map((n) => colorVar(n));
const BRIGHT_COLORS = BASE_NAMES.map((n) => colorVar(`bright-${n}`));
// 256-colour palette indices 0-15 reuse the exact same 16 tokens (0-7 base,
// 8-15 bright) — the same thing real terminal emulators do so a user's
// 16-colour theme also covers the low end of 256-colour mode.
const PALETTE_16 = [...BASE_COLORS, ...BRIGHT_COLORS];

// The terminal's own default text/background colour (frontend/src/tokens.css
// --color-term-ink / --color-term), used to resolve SGR 7 (inverse) when one
// side of the swap was never explicitly set.
const DEFAULT_FG = "var(--color-term-ink)";
const DEFAULT_BG = "var(--color-term)";

function clampByte(n: number): number {
  if (Number.isNaN(n)) return 0;
  return Math.max(0, Math.min(255, Math.round(n)));
}

/** The 6 levels xterm's 256-colour cube (indices 16-231) steps each channel through. */
const CUBE_LEVELS = [0, 95, 135, 175, 215, 255];

/** Resolves a 256-colour palette index (0-255) to a CSS colour value. */
function paletteColor(n: number): string {
  if (!Number.isInteger(n) || n < 0) return DEFAULT_FG;
  if (n < 16) return PALETTE_16[n];
  if (n < 232) {
    const i = n - 16;
    const r = CUBE_LEVELS[Math.floor(i / 36) % 6];
    const g = CUBE_LEVELS[Math.floor(i / 6) % 6];
    const b = CUBE_LEVELS[i % 6];
    return `rgb(${r}, ${g}, ${b})`;
  }
  if (n < 256) {
    const v = 8 + (n - 232) * 10;
    return `rgb(${v}, ${v}, ${v})`;
  }
  return DEFAULT_FG;
}

/**
 * Consumes a `38`/`48` "extended colour" sub-sequence starting at parts[idx]
 * (the mode selector: 5 = 256-colour palette, 2 = 24-bit truecolour).
 *
 * Returns the resolved CSS colour (or undefined if the mode is missing,
 * unrecognised, or short of arguments — malformed input is ignored, not
 * thrown on) and how many EXTRA params beyond the "38"/"48" itself were
 * consumed, so the caller's index can skip past them instead of
 * misinterpreting a colour component as its own SGR code.
 */
function consumeExtendedColor(parts: number[], idx: number): { color: string | undefined; consumed: number } {
  const mode = parts[idx];
  if (mode === 5) {
    const n = parts[idx + 1];
    if (n === undefined || Number.isNaN(n)) return { color: undefined, consumed: 1 };
    return { color: paletteColor(n), consumed: 2 };
  }
  if (mode === 2) {
    const r = parts[idx + 1];
    const g = parts[idx + 2];
    const b = parts[idx + 3];
    if (r === undefined || g === undefined || b === undefined || [r, g, b].some(Number.isNaN)) {
      return { color: undefined, consumed: 1 };
    }
    return { color: `rgb(${clampByte(r)}, ${clampByte(g)}, ${clampByte(b)})`, consumed: 4 };
  }
  // Unrecognised extended-colour mode (or none at all, e.g. a bare "38" at
  // the end of a sequence) — ignore, consuming nothing beyond "38"/"48".
  return { color: undefined, consumed: 0 };
}

/** Applies every SGR code in one `ESC[...m` sequence's parameter list to `state`. */
function applySGR(paramStr: string, state: RunState): void {
  // An empty parameter list means reset, exactly like an explicit "0" (ECMA-48).
  const parts =
    paramStr.length === 0 ? [0] : paramStr.split(";").map((p) => (p === "" ? 0 : Number.parseInt(p, 10)));

  let i = 0;
  while (i < parts.length) {
    const code = parts[i];
    if (Number.isNaN(code)) {
      i++;
      continue;
    }
    switch (true) {
      case code === 0:
        resetState(state);
        break;
      case code === 1:
        state.bold = true;
        break;
      case code === 2:
        state.dim = true;
        break;
      case code === 3:
        state.italic = true;
        break;
      case code === 4:
        state.underline = true;
        break;
      case code === 7:
        state.inverse = true;
        break;
      case code === 22: // normal intensity: cancels both bold and dim (ECMA-48)
        state.bold = false;
        state.dim = false;
        break;
      case code === 23:
        state.italic = false;
        break;
      case code === 24:
        state.underline = false;
        break;
      case code === 27:
        state.inverse = false;
        break;
      case code >= 30 && code <= 37:
        state.fg = BASE_COLORS[code - 30];
        break;
      case code === 38: {
        const { color, consumed } = consumeExtendedColor(parts, i + 1);
        if (color !== undefined) state.fg = color;
        i += consumed;
        break;
      }
      case code === 39:
        state.fg = undefined;
        break;
      case code >= 40 && code <= 47:
        state.bg = BASE_COLORS[code - 40];
        break;
      case code === 48: {
        const { color, consumed } = consumeExtendedColor(parts, i + 1);
        if (color !== undefined) state.bg = color;
        i += consumed;
        break;
      }
      case code === 49:
        state.bg = undefined;
        break;
      case code >= 90 && code <= 97:
        state.fg = BRIGHT_COLORS[code - 90];
        break;
      case code >= 100 && code <= 107:
        state.bg = BRIGHT_COLORS[code - 100];
        break;
      default:
        // Unrecognised SGR code (blink, strike-through, underline-colour
        // extensions, ...) — ignore rather than throw. A real screen dump
        // carries plenty of these.
        break;
    }
    i++;
  }
}

/** Resolves a run's accumulated state into a style object, applying SGR 7
 *  (inverse) by swapping the effective fg/bg rather than exposing a boolean
 *  the renderer would have to interpret. */
function styleFor(s: RunState): AnsiInlineStyle {
  let fg = s.fg;
  let bg = s.bg;
  if (s.inverse) {
    const swappedFg = bg ?? DEFAULT_BG;
    const swappedBg = fg ?? DEFAULT_FG;
    fg = swappedFg;
    bg = swappedBg;
  }
  const style: AnsiInlineStyle = {};
  if (fg !== undefined) style.color = fg;
  if (bg !== undefined) style.backgroundColor = bg;
  // 500 is the heaviest weight actually self-hosted (design.md: "Mono ·
  // JetBrains Mono 400/500") — 700 would be a browser-synthesised faux
  // bold, which this app's offline/self-hosted-only font policy avoids.
  if (s.bold) style.fontWeight = 500;
  if (s.dim) style.opacity = 0.7;
  if (s.italic) style.fontStyle = "italic";
  if (s.underline) style.textDecoration = "underline";
  return style;
}

/**
 * Parses `text` into styled segments, interpreting SGR escapes and dropping
 * every other control sequence (cursor moves, erase, OSC titles, ...)
 * without rendering or crashing on it.
 *
 * Plain text with no escapes at all comes back as a single segment with an
 * empty style — cheap, and visually identical to rendering the raw string.
 */
export function parseAnsi(text: string): AnsiSegment[] {
  const segments: AnsiSegment[] = [];
  const state = defaultState();
  let buf = "";

  const flush = () => {
    if (buf.length > 0) {
      segments.push({ text: buf, style: styleFor(state) });
      buf = "";
    }
  };

  const n = text.length;
  let i = 0;
  while (i < n) {
    if (text.charCodeAt(i) !== 0x1b) {
      buf += text[i];
      i++;
      continue;
    }

    const next = text[i + 1];

    if (next === "[") {
      // CSI: ESC '[' parameter-bytes(0x30-0x3F) intermediate-bytes(0x20-0x2F) final-byte(0x40-0x7E)
      let j = i + 2;
      while (j < n) {
        const c = text.charCodeAt(j);
        if (c < 0x30 || c > 0x3f) break;
        j++;
      }
      const paramEnd = j;
      while (j < n) {
        const c = text.charCodeAt(j);
        if (c < 0x20 || c > 0x2f) break;
        j++;
      }
      if (j >= n) {
        // Truncated/unterminated CSI (streamed mid-sequence, or garbage) —
        // drop just the ESC and resume as plain text so nothing throws and
        // no raw escape byte leaks into the rendered output.
        i++;
        continue;
      }
      const finalCode = text.charCodeAt(j);
      if (finalCode < 0x40 || finalCode > 0x7e) {
        // Not a valid final byte either — same graceful drop.
        i++;
        continue;
      }
      const finalByte = text[j];
      if (finalByte === "m") {
        flush();
        applySGR(text.slice(i + 2, paramEnd), state);
      }
      // Every other CSI final byte (cursor moves, erase-in-line, private
      // modes like "?25l", ...) is consumed and dropped: it affects layout
      // in a live terminal, not a static screen-dump snapshot.
      i = j + 1;
      continue;
    }

    if (next === "]") {
      // OSC: ESC ']' ... terminated by BEL (0x07) or ST (ESC '\'). An
      // unterminated OSC at the end of the buffer just runs off the end of
      // the loop without leaking its payload into the visible text —
      // nothing branches on whether a terminator was actually found, so
      // there is nothing to track beyond where it ends.
      let j = i + 2;
      while (j < n) {
        const c = text.charCodeAt(j);
        if (c === 0x07) {
          j++;
          break;
        }
        if (c === 0x1b && text.charCodeAt(j + 1) === 0x5c) {
          j += 2;
          break;
        }
        j++;
      }
      i = j;
      continue;
    }

    // A lone ESC not starting a recognised CSI/OSC sequence (garbage, a
    // single-character escape, or a truncated stream) — drop only the ESC
    // itself and keep scanning as plain text.
    i++;
  }
  flush();
  return segments;
}
