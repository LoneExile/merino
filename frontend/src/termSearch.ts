/**
 * Search helpers for the loaded pane buffer.
 *
 * Scope is the text currently in the client (recent window, typically
 * 400–2000 lines) — herdr has no full-history search API. The UI labels
 * this as searching loaded output.
 */

/** Strip CSI/OSC/DCS so match offsets line up with visible characters. */
export function stripAnsi(input: string): string {
  // CSI ... cmd, OSC ... BEL/ST, single-char ESC sequences.
  return input
    .replace(/\u001b\][^\u0007\u001b]*(?:\u0007|\u001b\\)?/g, "")
    .replace(/\u001bP[^\u001b]*(?:\u001b\\)?/g, "")
    .replace(/\u001b\[[0-9;?]*[ -/]*[@-~]/g, "")
    .replace(/\u001b./g, "");
}

export interface TextMatch {
  /** Inclusive start index into the plain (ANSI-stripped) string. */
  start: number;
  /** Exclusive end index. */
  end: number;
}

export function findMatches(plain: string, query: string): TextMatch[] {
  const q = query.trim();
  if (!q || !plain) return [];
  const hay = plain.toLowerCase();
  const needle = q.toLowerCase();
  const out: TextMatch[] = [];
  let from = 0;
  while (from < hay.length) {
    const i = hay.indexOf(needle, from);
    if (i < 0) break;
    out.push({ start: i, end: i + needle.length });
    from = i + Math.max(1, needle.length);
    if (out.length >= 500) break; // hard cap for huge buffers
  }
  return out;
}

/**
 * Map a plain-text offset to a character offset in the original ANSI string
 * by walking both in lockstep (skips escape sequences in the source).
 */
export function plainOffsetToAnsi(source: string, plainOffset: number): number {
  let plain = 0;
  let i = 0;
  while (i < source.length && plain < plainOffset) {
    if (source.charCodeAt(i) === 0x1b) {
      // Skip escape sequence.
      i++;
      if (i >= source.length) break;
      const n = source[i];
      if (n === "[") {
        i++;
        while (i < source.length) {
          const c = source.charCodeAt(i);
          i++;
          if (c >= 0x40 && c <= 0x7e) break; // final byte
        }
      } else if (n === "]") {
        i++;
        while (i < source.length) {
          const c = source[i];
          if (c === "\u0007") {
            i++;
            break;
          }
          if (c === "\u001b" && source[i + 1] === "\\") {
            i += 2;
            break;
          }
          i++;
        }
      } else {
        i++; // short ESC-X
      }
      continue;
    }
    plain++;
    i++;
  }
  return i;
}
