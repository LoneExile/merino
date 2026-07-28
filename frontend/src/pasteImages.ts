/**
 * Detect image file paths in pane text and turn them into inline <img> slots.
 * Kitty graphics never reach the web view over pane.read — only path strings.
 *
 * Sources:
 *   1) Merino phone upload:  …/merino/paste/paste-N.jpg
 *   2) Agent-generated:      ~/generated-images/donut.jpg
 *
 * Same file often appears twice (bare path line + agent "Read …path").
 * Rules:
 *   - First basename → one <img>
 *   - Absorb same-line "Read " / "• Read " prefix into the image span
 *   - Later same basename → drop path + same prefix entirely (no pink path text)
 */

export type TermPiece =
  | { kind: "text"; text: string }
  | { kind: "img"; name: string; path: string; src: string };

const MERINO_PASTE =
  /((?:~|\/)[^\s"'`]*\/(?:merino|herdr-tunnel)\/paste\/(paste-\d+\.(?:png|jpe?g|gif|webp)))/gi;

const HOME_IMAGE =
  /((?:~|\/)[^\s"'`()\[\]{}<>|,;]+\.(?:png|jpe?g|gif|webp))/gi;

/** Same-line tool chrome immediately before a path: "Read ", "• Read ", etc. */
const READ_PREFIX = /^(.*?)((?:[ \t]*(?:[•●·▪][ \t]*)?)(?:Read|read)[ \t]+)$/s;

export function pasteImageKey(pathOrName: string): string {
  const base = pathOrName.split(/[/\\]/).pop() || pathOrName;
  return base.toLowerCase();
}

function isMerinoPastePath(p: string): boolean {
  return /\/(?:merino|herdr-tunnel)\/paste\//i.test(p);
}

export function imageSrcForPath(path: string, name: string): string {
  if (isMerinoPastePath(path) || /^paste-\d+\./i.test(name)) {
    return `/api/paste/${encodeURIComponent(name)}`;
  }
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
  const out: Hit[] = [];
  let end = 0;
  for (const h of hits) {
    if (h.index < end) continue;
    out.push(h);
    end = h.index + h.full.length;
  }
  return out;
}

/**
 * If the path sits on a line that is only optional bullet + "Read " + path,
 * return how many chars before the path to swallow (the Read chrome).
 * `from` is the start of unconsumed text (do not look before it).
 */
export function readPrefixLength(text: string, pathIndex: number, from: number): number {
  if (pathIndex <= from) return 0;
  const lineStart = text.lastIndexOf("\n", pathIndex - 1) + 1;
  const start = Math.max(from, lineStart);
  const before = text.slice(start, pathIndex);
  // Entire same-line prefix must be Read chrome (no other words).
  if (/^(?:[ \t]*(?:[•●·▪][ \t]*)?)?(?:Read|read)[ \t]+$/.test(before)) {
    return before.length;
  }
  // Or prefix ends with Read chrome after other content — only swallow the chrome.
  const m = READ_PREFIX.exec(before);
  if (m && m[2]) {
    // Only swallow when the "other content" is empty or whitespace/bullet-only.
    const head = m[1] ?? "";
    if (/^[\s•●·▪]*$/.test(head)) return before.length;
    return m[2].length;
  }
  return 0;
}

function pushText(out: TermPiece[], text: string) {
  if (text) out.push({ kind: "text", text });
}

export function splitPasteImages(text: string): TermPiece[] {
  if (!text) return [];
  const hits = collectHits(text);
  if (hits.length === 0) return [{ kind: "text", text }];

  const out: TermPiece[] = [];
  let last = 0;
  const seen = new Set<string>();

  for (const h of hits) {
    const prefixLen = readPrefixLength(text, h.index, last);
    const spanStart = h.index - prefixLen;

    // Text before the (optional Read chrome +) path.
    if (spanStart > last) {
      pushText(out, text.slice(last, spanStart));
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
    }
    // else: duplicate — drop path + Read chrome entirely (no text piece).

    last = h.index + h.full.length;

    // Drop a single trailing newline that only existed to separate a removed
    // "Read path" line, when the next char is newline and we emitted an image
    // or dropped a dup that sat alone on its line.
    // Keep normal paragraph breaks: only collapse if prefix was consumed and
    // the line is now empty behind us — handled by not emitting the path.
  }

  if (last < text.length) {
    pushText(out, text.slice(last));
  }

  // Collapse runs of blank lines left by dropped "Read path\n" lines (max 2 \n).
  return normalizePieces(out);
}

function normalizePieces(pieces: TermPiece[]): TermPiece[] {
  const out: TermPiece[] = [];
  for (const p of pieces) {
    if (p.kind === "text") {
      // Trim excessive blank lines next to images.
      let t = p.text.replace(/\n{3,}/g, "\n\n");
      if (!t) continue;
      const prev = out[out.length - 1];
      if (prev?.kind === "img") {
        // No leading blank line right after an image.
        t = t.replace(/^\n+/, "\n");
      }
      if (!t) continue;
      out.push({ kind: "text", text: t });
    } else {
      // No trailing whitespace-only text before image needed cleanup on next.
      const prev = out[out.length - 1];
      if (prev?.kind === "text") {
        prev.text = prev.text.replace(/[ \t]+$/u, "");
        if (prev.text === "") out.pop();
        else if (/^\n+$/.test(prev.text)) {
          // keep a single newline before image if any
          prev.text = "\n";
        }
      }
      out.push(p);
    }
  }
  return out.length ? out : pieces;
}
