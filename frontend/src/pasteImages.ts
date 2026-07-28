/**
 * Detect staged Merino paste paths in pane text and turn them into
 * inline image slots. Kitty graphics never reach the web view over pane.read —
 * only the path string does — so matching our paste store is the reliable way
 * to show what the user just attached.
 *
 * The same path often appears twice in the stream:
 *   1) user send: absolute path line Merino injects
 *   2) agent echo: "Read ~/Library/Caches/merino/paste/paste-….jpg"
 * And when the history window scrolls, (1) may fall out while (2) remains.
 *
 * Rule: promote the **first** occurrence of each paste basename to <img>,
 * wherever it sits on the line. Later mentions stay plain text (no double image).
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
    if (!seen.has(key)) {
      seen.add(key);
      out.push({ kind: "img", name, path: full });
    } else {
      // Duplicate basename (agent Read echo, etc.) → keep path as text.
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
