/**
 * Standalone check for frontend/src/ansi.ts.
 *
 * This repo's frontend has no test runner (see package.json — vite + tsc
 * only, no vitest/jest), so this exercises the parser directly via Node's
 * TypeScript type-stripping instead of a real test framework:
 *
 *   node --experimental-strip-types frontend/src/__checks__/ansi.check.ts
 *
 * ansi.ts has no React/DOM dependency, so it runs unmodified here — the
 * same module the app imports, not a reimplementation of it.
 */
import { parseAnsi, type AnsiSegment } from "../ansi.ts";

let pass = 0;
let fail = 0;

function check(name: string, cond: boolean, detail?: string): void {
  if (cond) {
    pass++;
    console.log(`PASS ${name}`);
  } else {
    fail++;
    console.log(`FAIL ${name}${detail ? ` — ${detail}` : ""}`);
  }
}

function joinedText(segs: AnsiSegment[]): string {
  return segs.map((s) => s.text).join("");
}

function hasEscape(segs: AnsiSegment[]): boolean {
  return segs.some((s) => s.text.includes("\x1b"));
}

// ---------------------------------------------------------------- plain text
{
  const input = "hello, world\nno escapes here at all";
  const segs = parseAnsi(input);
  check("plain text: passthrough unchanged", joinedText(segs) === input, `got ${JSON.stringify(joinedText(segs))}`);
  check("plain text: single segment", segs.length === 1, `got ${segs.length} segments`);
  check(
    "plain text: empty style",
    segs.length === 1 && Object.keys(segs[0].style).length === 0,
    `got ${JSON.stringify(segs[0]?.style)}`,
  );
}

// ------------------------------------------------------------- 16-colour fg
{
  const segs = parseAnsi("\x1b[31mred text\x1b[0m");
  check("16-colour fg: text preserved", joinedText(segs) === "red text");
  check(
    "16-colour fg: maps to token",
    segs[0]?.style.color === "var(--ansi-red)",
    `got ${JSON.stringify(segs[0]?.style)}`,
  );
}

// --------------------------------------------------------- 16-colour bright
{
  const segs = parseAnsi("\x1b[91mbright red\x1b[0m\x1b[100mbright black bg\x1b[0m");
  check(
    "16-colour bright fg: maps to bright token",
    segs[0]?.style.color === "var(--ansi-bright-red)",
    `got ${JSON.stringify(segs[0]?.style)}`,
  );
  check(
    "16-colour bright bg: maps to bright token",
    segs[1]?.style.backgroundColor === "var(--ansi-bright-black)",
    `got ${JSON.stringify(segs[1]?.style)}`,
  );
}

// ------------------------------------------------------------------ 256-col
{
  // Index 1 is inside the 0-15 range: must resolve to the same token as SGR 31.
  const low = parseAnsi("\x1b[38;5;1mlow\x1b[0m");
  check(
    "256-colour: index <16 reuses base token",
    low[0]?.style.color === "var(--ansi-red)",
    `got ${JSON.stringify(low[0]?.style)}`,
  );

  // Index 208 is in the 6x6x6 cube (16-231): a well-known "orange".
  const cube = parseAnsi("\x1b[38;5;208morange\x1b[0m");
  check(
    "256-colour: cube index resolves to rgb()",
    cube[0]?.style.color === "rgb(255, 135, 0)",
    `got ${JSON.stringify(cube[0]?.style)}`,
  );

  // Index 244 is in the grayscale ramp (232-255).
  const gray = parseAnsi("\x1b[48;5;244mgray bg\x1b[0m");
  check(
    "256-colour: grayscale index resolves to rgb()",
    gray[0]?.style.backgroundColor === "rgb(128, 128, 128)",
    `got ${JSON.stringify(gray[0]?.style)}`,
  );
}

// --------------------------------------------------------------- truecolour
{
  const segs = parseAnsi("\x1b[38;2;10;20;30mfg\x1b[0m\x1b[48;2;200;100;50mbg\x1b[0m");
  check(
    "truecolour: 24-bit fg exact rgb",
    segs[0]?.style.color === "rgb(10, 20, 30)",
    `got ${JSON.stringify(segs[0]?.style)}`,
  );
  check(
    "truecolour: 24-bit bg exact rgb",
    segs[1]?.style.backgroundColor === "rgb(200, 100, 50)",
    `got ${JSON.stringify(segs[1]?.style)}`,
  );
  // CSI parameters are unsigned on the wire (no minus sign in the grammar),
  // so "out of range" only ever means "too high" in practice — a buggy
  // emitter overflowing its own colour math, say.
  const clamped = parseAnsi("\x1b[38;2;300;999;256mover\x1b[0m");
  check(
    "truecolour: components above 255 clamp to a byte instead of crashing",
    clamped[0]?.style.color === "rgb(255, 255, 255)",
    `got ${JSON.stringify(clamped[0]?.style)}`,
  );
}

// ------------------------------------------------------------ bold + colour
{
  const segs = parseAnsi("\x1b[1;31mBOLD RED\x1b[0m plain");
  check("bold+colour: text preserved", joinedText(segs) === "BOLD RED plain");
  check(
    "bold+colour: both style properties present on the same segment",
    segs[0]?.style.color === "var(--ansi-red)" && segs[0]?.style.fontWeight === 500,
    `got ${JSON.stringify(segs[0]?.style)}`,
  );
  check(
    "bold+colour: trailing plain text has neither",
    segs[1]?.style.color === undefined && segs[1]?.style.fontWeight === undefined,
    `got ${JSON.stringify(segs[1]?.style)}`,
  );
}

// ---------------------------------------------------------------- dim/italic/underline
{
  const segs = parseAnsi("\x1b[2mdim\x1b[22m\x1b[3mitalic\x1b[23m\x1b[4munderline\x1b[24mplain");
  check("dim: opacity applied", segs[0]?.text === "dim" && segs[0]?.style.opacity === 0.7);
  check("italic: fontStyle applied", segs[1]?.text === "italic" && segs[1]?.style.fontStyle === "italic");
  check(
    "underline: textDecoration applied",
    segs[2]?.text === "underline" && segs[2]?.style.textDecoration === "underline",
  );
  check(
    "22/23/24: each attribute individually cleared, not a full reset",
    segs[3]?.text === "plain" && Object.keys(segs[3]?.style ?? {}).length === 0,
    `got ${JSON.stringify(segs[3])}`,
  );
}

// -------------------------------------------------------------------- inverse
{
  // Inverse with nothing else set: fg becomes the terminal's default bg, bg
  // becomes the terminal's default fg (a classic "selection block").
  const plain = parseAnsi("\x1b[7minverse\x1b[0m");
  check(
    "inverse: swaps in the terminal's own default colours when nothing else is set",
    plain[0]?.style.color === "var(--color-term)" && plain[0]?.style.backgroundColor === "var(--color-term-ink)",
    `got ${JSON.stringify(plain[0]?.style)}`,
  );

  // Inverse with an explicit fg only: bg becomes that colour, fg becomes the
  // terminal's default bg.
  const withFg = parseAnsi("\x1b[31;7mred inverse\x1b[0m");
  check(
    "inverse: an explicit fg becomes the background",
    withFg[0]?.style.backgroundColor === "var(--ansi-red)" && withFg[0]?.style.color === "var(--color-term)",
    `got ${JSON.stringify(withFg[0]?.style)}`,
  );
}

// ---------------------------------------------------------------- 39/49 reset
{
  const segs = parseAnsi("\x1b[31;44mstyled\x1b[39mfg-reset-only\x1b[49mboth-reset");
  check(
    "39: resets only the foreground",
    segs[1]?.style.color === undefined && segs[1]?.style.backgroundColor === "var(--ansi-blue)",
    `got ${JSON.stringify(segs[1]?.style)}`,
  );
  check(
    "49: resets only the background (fg already unset)",
    segs[2]?.style.color === undefined && segs[2]?.style.backgroundColor === undefined,
    `got ${JSON.stringify(segs[2]?.style)}`,
  );
}

// ---------------------------------------------------------------- reset mid-line
{
  const segs = parseAnsi("\x1b[1;31mred bold\x1b[0mplain again");
  check("reset mid-line: two segments", segs.length === 2, `got ${segs.length}`);
  check(
    "reset mid-line: first segment keeps its style",
    segs[0]?.text === "red bold" && segs[0]?.style.color === "var(--ansi-red)",
  );
  check(
    "reset mid-line: second segment is fully reset",
    segs[1]?.text === "plain again" && Object.keys(segs[1]?.style ?? {}).length === 0,
    `got ${JSON.stringify(segs[1])}`,
  );
  check("reset mid-line: no escape leaks into text", !hasEscape(segs));
}

// -------------------------------------------------------- unterminated/garbage
{
  const cases = [
    "before\x1b[38;5;", // truncated extended-colour sequence, no final byte
    "before\x1b[1;3", // truncated SGR, no final byte
    "before\x1b", // lone ESC at the very end
    "before\x1b[", // CSI opened, nothing after
    "before\x1b[?25", // private-mode CSI, no final byte
    "before\x1bZgarbage", // ESC followed by an unrecognised single char
  ];
  for (const input of cases) {
    let threw = false;
    let segs: AnsiSegment[] = [];
    try {
      segs = parseAnsi(input);
    } catch {
      threw = true;
    }
    check(`garbage input does not throw: ${JSON.stringify(input)}`, !threw);
    check(`garbage input leaks no escape byte: ${JSON.stringify(input)}`, !threw && !hasEscape(segs));
    check(
      `garbage input keeps the leading plain text: ${JSON.stringify(input)}`,
      !threw && joinedText(segs).startsWith("before"),
      `got ${JSON.stringify(joinedText(segs))}`,
    );
  }
}

// --------------------------------------------------------------- OSC title
{
  const withBel = "before\x1b]0;window title\x07after";
  const withST = "before\x1b]2;other title\x1b\\after";
  const unterminated = "before\x1b]0;never closes";

  for (const [name, input, wantTail] of [
    ["BEL-terminated", withBel, "after"],
    ["ST-terminated", withST, "after"],
    ["unterminated", unterminated, ""],
  ] as const) {
    let threw = false;
    let segs: AnsiSegment[] = [];
    try {
      segs = parseAnsi(input);
    } catch {
      threw = true;
    }
    check(`OSC title (${name}): does not throw`, !threw);
    const text = joinedText(segs);
    check(`OSC title (${name}): title payload dropped, no escape leaks`, !threw && !hasEscape(segs));
    check(
      `OSC title (${name}): surrounding plain text survives`,
      !threw && text === `before${wantTail}`,
      `got ${JSON.stringify(text)}`,
    );
  }
}

// ------------------------------------------------------- non-SGR CSI dropped
{
  // Cursor move, erase-in-line, and a private-mode sequence (hide cursor) —
  // none of these are 'm', so they must be consumed and dropped, never
  // rendered, never crashing.
  const segs = parseAnsi("before\x1b[2K\x1b[10;5H\x1b[?25lafter");
  check("non-SGR CSI: dropped without affecting surrounding text", joinedText(segs) === "beforeafter");
  check("non-SGR CSI: no escape leaks into text", !hasEscape(segs));
}

console.log(`\n${pass} passed, ${fail} failed`);
if (fail > 0) process.exit(1);
