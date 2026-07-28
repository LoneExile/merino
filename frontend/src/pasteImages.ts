/**
 * Detect image file paths in pane text and turn them into inline <img> slots.
 * Kitty graphics never reach the web view over pane.read — only path strings.
 *
 * Sources we care about:
 *   1) Merino phone upload:  …/merino/paste/paste-N.jpg  (user line + agent Read)
 *   2) Agent-generated:      ~/generated-images/donut.jpg  (or any home image path)
 *
 * Same basename often appears twice (user path + agent "Read …"). Promote the
 * **first** occurrence of each key to an image; later mentions stay text.
 */

export type TermPiece =
  | { kind: "text"; text: string }
  | { kind: "img"; name: string; path: string; src: string };

// Merino staged paste store (and legacy herdr-tunnel name).
const MERINO_PASTE =
  /((?:~|\/)[^\s"'`]*\/(?:merino|herdr-tunnel)\/paste\/(paste-\d+\.(?:png|jpe?g|gif|webp)))/gi;

// Any other home-relative or absolute path ending in a common image ext.
// Excludes the merino paste dir (handled above) via post-filter.
const HOME_IMAGE =
  /((?:~|\/)[^\s"'`()\[\]{}<>|,;]+\.(?:png|jpe?g|gif|webp))/gi;

/** Canonical key so ~/… and /Users/…/… of the same file collapse. */
export function pasteImageKey(pathOrName: string): string {
  const base = pathOrName.split(/[/\\]/).pop() || pathOrName;
  return base.toLowerCase();
}

function isMerinoPastePath(p: string): boolean {
  return /\/(?:merino|herdr-tunnel)\/paste\//i.test(p);
}

/** URL the <img> should load (authenticated same-origin). */
export function imageSrcForPath(path: string, name: string): string {
  if (isMerinoPastePath(path) || /^paste-\d+\./i.test(name)) {
    return `/api/paste/${encodeURIComponent(name)}`;
  }
  // Expand-ish: server resolves ~ ; pass path as query.
  return `/api/local-image?path=${encodeURIComponent(path)}`;
}


type Hit = { index: number; full: string; name: string };

function collectHits(text: string): Hit[] {
  const hits: Hit[] = [];
  const push = (re: RegExp, nameFrom: (m: RegExpExecArray) => string) => {
    re.lastIndex = 0;
    let m: RegExpExecArray | null;
    while ((m = re.exec(text)) !== null) {
      const full = m[1]!;
      // Skip merino paste paths in the generic home matcher (double-hit).
      if (re === HOME_IMAGE && isMerinoPastePath(full)) continue;
      hits.push({ index: m.index, full, name: nameFrom(m) });
    }
  };
  push(MERINO_PASTE, (m) => m[2]!);
  push(HOME_IMAGE, (m) => {
    const full = m[1]!;
    return full.split(/[/\\]/).pop() || full;
  });
  hits.sort((a, b) => a.index - b.index || b.full.length - a.full.length);
  // Drop overlapping hits (keep earlier / longer).
  const out: Hit[] = [];
  let end = 0;
  for (const h of hits) {
    if (h.index < end) continue;
    out.push(h);
    end = h.index + h.full.length;
  }
  return out;
}

export function splitPasteImages(text: string): TermPiece[] {
  if (!text) return [];
  const hits = collectHits(text);
  if (hits.length === 0) return [{ kind: "text", text }];

  const out: TermPiece[] = [];
  let last = 0;
  const seen = new Set<string>();

  for (const h of hits) {
    if (h.index > last) {
      out.push({ kind: "text", text: text.slice(last, h.index) });
    }
    const key = pasteImageKey(h.name);
    if (!seen.has(key)) {
      seen.add(key);
      out.push({
        kind: "img",
        name: h.name,
        path: h.full,
        src: imageSrcForPath(h.full, h.name),
      });
    } else {
      out.push({ kind: "text", text: h.full });
    }
    last = h.index + h.full.length;
  }
  if (last < text.length) {
    out.push({ kind: "text", text: text.slice(last) });
  }
  return out.length ? out : [{ kind: "text", text }];
}
