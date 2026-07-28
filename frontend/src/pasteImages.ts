/**
 * Approximate Kitty inline images in Merino's pane view.
 *
 * pane.read is plain/ANSI text only — Kitty graphics never arrive. Live dumps
 * show the real shape of a herdr terminal:
 *
 *   user path line stays as text
 *   agent runs Read → Kitty paints the image above the Read status line
 *   ~40 full-width space-padded blank rows fill the old image cell
 *   shell `$ file "…"` paths stay as text
 *
 * So we:
 *   1) collapse space-padded blank runs (the gap)
 *   2) only inject <img> at OMP Read chrome of an image path
 *   3) keep the Read line + every other path string as text
 *   4) one img per basename (second Read does not re-insert)
 */

import { stripAnsi } from "./termSearch.ts";
import { lookupImage } from "./imageCache.ts";

export type TermPiece =
  | { kind: "text"; text: string }
  | {
      kind: "img";
      name: string;
      path: string;
      src: string;
      plainLen: number;
    };

const IMAGE_PATH =
  /((?:~|\/)[^\s"'`()\[\]{}<>|,;]+\.(?:png|jpe?g|gif|webp))/gi;

export function pasteImageKey(pathOrName: string): string {
  return (pathOrName.split(/[/\\]/).pop() || pathOrName).toLowerCase();
}

function isMerinoPastePath(p: string): boolean {
  return /\/(?:merino|herdr-tunnel)\/paste\//i.test(p);
}

export function imageSrcForPath(path: string, name: string): string {
  const cached = lookupImage(name) || lookupImage(path);
  if (cached) return cached;
  if (isMerinoPastePath(path) || /^paste-\d+\./i.test(name)) {
    return `/api/paste/${encodeURIComponent(name)}`;
  }
  return `/api/local-image?path=${encodeURIComponent(path)}`;
}

/** Kitty placeholder rows: full-width spaces then newline, repeated. */
export function collapseKittyBlankLines(raw: string): string {
  let s = raw.replace(/[ \t\u00A0]+$/gm, "");
  s = s.replace(/(?:\r?\n){3,}/g, "\n\n");
  return s;
}

export function buildPlainMap(raw: string): { plain: string; plainToRaw: number[] } {
  const plainToRaw: number[] = [];
  let plain = "";
  let i = 0;
  const n = raw.length;
  while (i < n) {
    if (raw[i] === "\u001b") {
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
      if (raw[i + 1] === "[") {
        let j = i + 2;
        while (j < n) {
          const c = raw.charCodeAt(j);
          if (c >= 0x40 && c <= 0x7e) {
            j++;
            break;
          }
          j++;
        }
        i = j;
        continue;
      }
      i += raw[i + 1] != null ? 2 : 1;
      continue;
    }
    plainToRaw.push(i);
    plain += raw[i]!;
    i++;
  }
  plainToRaw.push(n);
  const stripped = stripAnsi(raw);
  if (plain !== stripped) {
    // Fallback: map by progressive stripAnsi (slower, correct).
    const map: number[] = [];
    let prev = 0;
    for (let r = 0; r < raw.length; r++) {
      const len = stripAnsi(raw.slice(0, r + 1)).length;
      if (len > prev) {
        map.push(r);
        prev = len;
      }
    }
    map.push(raw.length);
    if (map.length - 1 === stripped.length) {
      return { plain: stripped, plainToRaw: map };
    }
  }
  return { plain, plainToRaw };
}

type PathHit = { plainStart: number; plainEnd: number; full: string; name: string };

function collectPathHits(plain: string): PathHit[] {
  const hits: PathHit[] = [];
  IMAGE_PATH.lastIndex = 0;
  let m: RegExpExecArray | null;
  while ((m = IMAGE_PATH.exec(plain)) !== null) {
    const full = m[1]!;
    hits.push({
      plainStart: m.index,
      plainEnd: m.index + full.length,
      full,
      name: full.split(/[/\\]/).pop() || full,
    });
  }
  hits.sort((a, b) => a.plainStart - b.plainStart || b.full.length - a.full.length);
  const out: PathHit[] = [];
  let end = 0;
  for (const h of hits) {
    if (h.plainStart < end) continue;
    out.push(h);
    end = h.plainEnd;
  }
  return out;
}

function lineBounds(plain: string, index: number): { start: number; end: number } {
  const start = plain.lastIndexOf("\n", index - 1) + 1;
  let end = plain.indexOf("\n", index);
  if (end < 0) end = plain.length;
  return { start, end };
}

/**
 * True when this path is on an OMP Read status line — the moment Kitty paints.
 * Prefix may include Nerd Font icons (U+E000–U+F8FF) and ANSI already stripped.
 */
export function isReadImageLine(plain: string, pathStart: number, pathEnd: number): boolean {
  const { start, end } = lineBounds(plain, pathStart);
  const before = plain.slice(start, pathStart);
  const after = plain.slice(pathEnd, end);
  // Reject quoted / shell
  if (/["'`]/.test(before) || /["'`]/.test(after)) return false;
  if (/\$/.test(before)) return false;
  // "… Read path" or "… read path" with optional private-use icons
  return (
    /^(?:[\s\uE000-\uF8FF•●·▪◦○◉◎]*)(?:Read|read)[\s\u00A0]+$/u.test(before) &&
    /^[\s\uE000-\uF8FF]*$/.test(after)
  );
}

/** @deprecated kept for checks that import the old name */
export function isDisplayPathContext(plain: string, pathStart: number, pathEnd: number): boolean {
  return isReadImageLine(plain, pathStart, pathEnd);
}

export function readPrefixLength(plain: string, pathIndex: number, fromPlain: number): number {
  // We no longer swallow the Read line — image is inserted before it.
  void plain;
  void pathIndex;
  void fromPlain;
  return 0;
}

function pushText(out: TermPiece[], text: string) {
  if (text) out.push({ kind: "text", text });
}

/**
 * Split pane text into text + img pieces.
 * Image is inserted immediately BEFORE each first Read-of-image line
 * (Kitty paints above the status line). Read text itself is kept.
 */
export function splitPasteImages(rawInput: string): TermPiece[] {
  if (!rawInput) return [];
  const raw = collapseKittyBlankLines(rawInput);
  const { plain, plainToRaw } = buildPlainMap(raw);
  const hits = collectPathHits(plain);
  if (hits.length === 0) return [{ kind: "text", text: raw }];

  const out: TermPiece[] = [];
  let lastRaw = 0;
  const seen = new Set<string>();

  for (const h of hits) {
    if (!isReadImageLine(plain, h.plainStart, h.plainEnd)) continue;
    const key = pasteImageKey(h.name);
    if (seen.has(key)) continue;
    seen.add(key);

    // Insert image at the start of the Read line (Kitty places pixels above status).
    const { start: lineStart } = lineBounds(plain, h.plainStart);
    const rawAtLine = plainToRaw[lineStart] ?? 0;

    if (rawAtLine > lastRaw) {
      pushText(out, raw.slice(lastRaw, rawAtLine));
    }
    out.push({
      kind: "img",
      name: h.name,
      path: h.full,
      src: imageSrcForPath(h.full, h.name),
      plainLen: 0,
    });
    lastRaw = rawAtLine; // Read line text still emitted from lastRaw onward
  }

  if (lastRaw < raw.length) pushText(out, raw.slice(lastRaw));
  return normalizePieces(out);
}

function normalizePieces(pieces: TermPiece[]): TermPiece[] {
  const out: TermPiece[] = [];
  for (const p of pieces) {
    if (p.kind === "text") {
      let t = p.text.replace(/\n{3,}/g, "\n\n");
      if (!t) continue;
      // Drop pure-whitespace chunks (Kitty residue after collapse).
      if (!stripAnsi(t).replace(/\s/g, "")) continue;
      out.push({ kind: "text", text: t });
    } else {
      out.push(p);
    }
  }
  return out.length ? out : pieces;
}
