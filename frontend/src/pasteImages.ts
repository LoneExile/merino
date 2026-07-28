/**
 * Detect image file paths in pane text and turn them into inline <img> slots.
 * Kitty graphics never reach the web view over pane.read — only path strings.
 *
 * Matching runs on ANSI-stripped text (OMP colors "Read" with CSI). Spans map
 * back to raw indices so we can splice the original buffer correctly.
 *
 * Rules:
 *   - First basename → one <img>; swallow same-line Read chrome (plain)
 *   - Later same basename → drop path + Read chrome (no text residue)
 */

import { stripAnsi } from "./termSearch.ts";

export type TermPiece =
  | { kind: "text"; text: string }
  | {
      kind: "img";
      name: string;
      path: string;
      src: string;
      /** Visible plain length consumed (Read chrome + path) for search cursors. */
      plainLen: number;
    };

const MERINO_PASTE =
  /((?:~|\/)[^\s"'`]*\/(?:merino|herdr-tunnel)\/paste\/(paste-\d+\.(?:png|jpe?g|gif|webp)))/gi;

const HOME_IMAGE =
  /((?:~|\/)[^\s"'`()\[\]{}<>|,;]+\.(?:png|jpe?g|gif|webp))/gi;

/** Same-line tool chrome before a path on stripped text. */
const READ_LINE = /^(?:[ \t]*(?:[•●·▪][ \t]*)?)?(?:Read|read)[ \t]+$/;

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

/**
 * Build plain text + plainIndex → rawIndex map.
 * plainToRaw[i] = raw offset of plain character i; plainToRaw[plain.length] = raw.length.
 */
export function buildPlainMap(raw: string): { plain: string; plainToRaw: number[] } {
  // Walk with the same strip rules as stripAnsi, recording kept indices.
  const plainToRaw: number[] = [];
  let plain = "";
  let i = 0;
  const n = raw.length;
  const push = (rawIdx: number, ch: string) => {
    plainToRaw.push(rawIdx);
    plain += ch;
  };
  while (i < n) {
    if (raw[i] === "\u001b") {
      // OSC: ESC ] ... BEL or ST
      if (raw[i + 1] === "]") {
        let j = i + 2;
        while (j < n) {
          if (raw[j] === "\u0007") {
            j++;
            break;
          }
          if (raw[j] === "\u001b" && raw[j + 1] === "\\") {
            j += 2;
            break;
          }
          j++;
        }
        i = j;
        continue;
      }
      // DCS: ESC P ... ST
      if (raw[i + 1] === "P") {
        let j = i + 2;
        while (j < n) {
          if (raw[j] === "\u001b" && raw[j + 1] === "\\") {
            j += 2;
            break;
          }
          j++;
        }
        i = j;
        continue;
      }
      // CSI: ESC [ ... final
      if (raw[i + 1] === "[") {
        let j = i + 2;
        while (j < n) {
          const c = raw.charCodeAt(j);
          // Final byte of CSI: @ through ~
          if (c >= 0x40 && c <= 0x7e) {
            j++;
            break;
          }
          j++;
        }
        i = j;
        continue;
      }
      // Other ESC X — drop ESC + one char
      i += 2;
      continue;
    }
    push(i, raw[i]!);
    i++;
  }
  plainToRaw.push(n); // sentinel
  // Sanity: stripAnsi should match our plain builder.
  if (plain !== stripAnsi(raw) && plain.length !== stripAnsi(raw).length) {
    // Fall back to strip-only map (slower linear scan) if rules diverge.
    return buildPlainMapFallback(raw);
  }
  return { plain, plainToRaw };
}

function buildPlainMapFallback(raw: string): { plain: string; plainToRaw: number[] } {
  const plain = stripAnsi(raw);
  // Approximate: walk raw with strip, same as primary.
  const plainToRaw: number[] = [];
  let pi = 0;
  let i = 0;
  const stripped = stripAnsi;
  void stripped;
  // Character-by-character: if stripAnsi(raw.slice(0,i+1)).length increased, keep.
  let prevLen = 0;
  for (i = 0; i < raw.length; i++) {
    const len = stripAnsi(raw.slice(0, i + 1)).length;
    if (len > prevLen) {
      plainToRaw.push(i);
      prevLen = len;
      pi++;
    }
  }
  plainToRaw.push(raw.length);
  if (pi !== plain.length) {
    // Last resort: identity (wrong for CSI but better than crash).
    const id: number[] = [];
    for (let k = 0; k < plain.length; k++) id.push(k);
    id.push(raw.length);
    return { plain, plainToRaw: id };
  }
  return { plain, plainToRaw };
}

type PlainHit = {
  plainStart: number;
  plainEnd: number; // exclusive, path only
  full: string;
  name: string;
};

function collectPlainHits(plain: string): PlainHit[] {
  const hits: PlainHit[] = [];
  const push = (re: RegExp, nameFrom: (m: RegExpExecArray) => string) => {
    re.lastIndex = 0;
    let m: RegExpExecArray | null;
    while ((m = re.exec(plain)) !== null) {
      const full = m[1]!;
      if (re === HOME_IMAGE && isMerinoPastePath(full)) continue;
      hits.push({
        plainStart: m.index,
        plainEnd: m.index + full.length,
        full,
        name: nameFrom(m),
      });
    }
  };
  push(MERINO_PASTE, (m) => m[2]!);
  push(HOME_IMAGE, (m) => m[1]!.split(/[/\\]/).pop() || m[1]!);
  hits.sort((a, b) => a.plainStart - b.plainStart || b.full.length - a.full.length);
  const out: PlainHit[] = [];
  let end = 0;
  for (const h of hits) {
    if (h.plainStart < end) continue;
    out.push(h);
    end = h.plainEnd;
  }
  return out;
}

/**
 * On stripped text: length of same-line Read chrome immediately before pathIndex,
 * not looking before `fromPlain`.
 */
export function readPrefixLength(plain: string, pathIndex: number, fromPlain: number): number {
  if (pathIndex <= fromPlain) return 0;
  const lineStart = plain.lastIndexOf("\n", pathIndex - 1) + 1;
  const start = Math.max(fromPlain, lineStart);
  const before = plain.slice(start, pathIndex);
  if (READ_LINE.test(before)) return before.length;
  // Ends with Read chrome after whitespace-only head on this line segment.
  const m = /^(.*?)((?:[ \t]*(?:[•●·▪][ \t]*)?)(?:Read|read)[ \t]+)$/s.exec(before);
  if (m?.[2] && /^[\s•●·▪]*$/.test(m[1] ?? "")) return before.length;
  if (m?.[2]) return m[2].length;
  return 0;
}

function pushText(out: TermPiece[], text: string) {
  if (text) out.push({ kind: "text", text });
}

export function splitPasteImages(raw: string): TermPiece[] {
  if (!raw) return [];
  const { plain, plainToRaw } = buildPlainMap(raw);
  const hits = collectPlainHits(plain);
  if (hits.length === 0) return [{ kind: "text", text: raw }];

  const out: TermPiece[] = [];
  let lastRaw = 0;
  let lastPlain = 0;
  const seen = new Set<string>();

  for (const h of hits) {
    const prefixLen = readPrefixLength(plain, h.plainStart, lastPlain);
    const plainSpanStart = h.plainStart - prefixLen;
    const plainSpanEnd = h.plainEnd; // exclusive
    const rawSpanStart = plainToRaw[plainSpanStart] ?? h.plainStart;
    const rawSpanEnd = plainToRaw[plainSpanEnd] ?? raw.length;

    if (rawSpanStart > lastRaw) {
      pushText(out, raw.slice(lastRaw, rawSpanStart));
    }

    const key = pasteImageKey(h.name);
    const plainLen = plainSpanEnd - plainSpanStart;
    if (!seen.has(key)) {
      seen.add(key);
      out.push({
        kind: "img",
        name: h.name,
        path: h.full,
        src: imageSrcForPath(h.full, h.name),
        plainLen,
      });
    }
    // duplicate → emit nothing (drop Read+path)

    lastRaw = rawSpanEnd;
    lastPlain = plainSpanEnd;
  }

  if (lastRaw < raw.length) {
    pushText(out, raw.slice(lastRaw));
  }

  return normalizePieces(out);
}

function normalizePieces(pieces: TermPiece[]): TermPiece[] {
  const out: TermPiece[] = [];
  for (const p of pieces) {
    if (p.kind === "text") {
      let t = p.text.replace(/\n{3,}/g, "\n\n");
      if (!t) continue;
      const prev = out[out.length - 1];
      if (prev?.kind === "img") {
        t = t.replace(/^\n+/, "\n");
      }
      if (!t) continue;
      // Drop text that is only ANSI + whitespace (leftover color codes around removed Read).
      if (!stripAnsi(t).replace(/\s/g, "")) continue;
      out.push({ kind: "text", text: t });
    } else {
      const prev = out[out.length - 1];
      if (prev?.kind === "text") {
        // Trim trailing spaces before image; keep newlines.
        prev.text = prev.text.replace(/[ \t]+$/u, "");
        if (prev.text === "") out.pop();
      }
      out.push(p);
    }
  }
  return out.length ? out : pieces;
}
