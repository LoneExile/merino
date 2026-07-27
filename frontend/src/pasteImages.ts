/**
 * Detect staged herdr-tunnel paste paths in pane text and turn them into
 * inline image slots. Kitty graphics never reach the web view over pane.read —
 * only the path string does — so matching our paste store is the reliable way
 * to show what the user just attached.
 */

export type TermPiece =
  | { kind: "text"; text: string }
  | { kind: "img"; name: string; path: string };

// Match ".../herdr-tunnel/paste/paste-N.ext" with optional ~/ or absolute prefix.
const PASTE_RE =
  /((?:~|\/)[^\s"'`]*\/herdr-tunnel\/paste\/(paste-\d+\.(?:png|jpe?g|gif|webp)))/g;

export function splitPasteImages(text: string): TermPiece[] {
  if (!text) return [];
  const out: TermPiece[] = [];
  let last = 0;
  PASTE_RE.lastIndex = 0;
  let m: RegExpExecArray | null;
  while ((m = PASTE_RE.exec(text)) !== null) {
    const full = m[1]!;
    const name = m[2]!;
    if (m.index > last) {
      out.push({ kind: "text", text: text.slice(last, m.index) });
    }
    out.push({ kind: "img", name, path: full });
    last = m.index + full.length;
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
