/**
 * Detect staged Merino paste paths in pane text and turn them into
 * inline image slots. Kitty graphics never reach the web view over pane.read —
 * only the path string does — so matching our paste store is the reliable way
 * to show what the user just attached.
 *
 * The same path often appears twice in the stream:
 *   1) user send: a line that is only the absolute path (Merino inject)
 *   2) agent echo: "Read ~/Library/Caches/merino/paste/paste-….jpg"
 * Promoting every match to <img> doubles the picture with a blank gap.
 * Rules:
 *   - only line-leading paths become images (after start/newline + spaces)
 *   - each paste basename is shown as an image at most once
 */

export type TermPiece =
  | { kind: "text"; text: string }
  | { kind: "img"; name: string; path: string };

// Path to a staged paste file. Captures optional ~ home prefix.
const PASTE_PATH =
  /((?:~|\/)[^\s"'`]*\/(?:merino|herdr-tunnel)\/paste\/(paste-\d+\.(?:png|jpe?g|gif|webp)))/g;

/** Canonical key so ~/… and /Users/…/… of the same file collapse. */
export function pasteImageKey(pathOrName: string): string {
  const base = pathOrName.split(/[/\\]/).pop() || pathOrName;
  return base.toLowerCase();
}

/** True when `index` sits at the start of a line (only whitespace before it). */
export function isLineLeading(text: string, index: number): boolean {
  if (index <= 0) return true;
  let i = index - 1;
  while (i >= 0) {
    const c = text[i]!;
    if (c === "\n" || c === "\r") return true;
    if (c !== " " && c !== "\t") return false;
    i--;
  }
  return true;
}

export function splitPasteImages(text: string): TermPiece[] {
  if (!text) return [];
  const out: TermPiece[] = [];
  let last = 0;
  PASTE_PATH.lastIndex = 0;
  const seen = new Set<string>();
  let m: RegExpExecArray | null;
  while ((m = PASTE_PATH.exec(text)) !== null) {
    const full = m[1]!;
    const name = m[2]!;
    const at = m.index;
    if (at > last) {
      out.push({ kind: "text", text: text.slice(last, at) });
    }
    const key = pasteImageKey(name);
    const promote = isLineLeading(text, at) && !seen.has(key);
    if (promote) {
      seen.add(key);
      out.push({ kind: "img", name, path: full });
    } else {
      // Mid-line (e.g. "Read …path") or duplicate basename → keep as text.
      out.push({ kind: "text", text: full });
    }
    last = at + full.length;
  }
  if (last < text.length) {
    out.push({ kind: "text", text: text.slice(last) });
  }
  return out.length ? out : [{ kind: "text", text }];
}

/** Authenticated URL for a staged paste file. */
export function pasteImageURL(name: string): string {
  return `/api/paste/${encodeURIComponent(name)}`;
}
