import assert from "node:assert/strict";
import {
  imageSrcForPath,
  pasteImageKey,
  readPrefixLength,
  splitPasteImages,
} from "../pasteImages.ts";
import { stripAnsi } from "../termSearch.ts";

const abs =
  "/Users/lex/Library/Caches/merino/paste/paste-1785215834288577000.jpg";
const home = "~/Library/Caches/merino/paste/paste-1785215834288577000.jpg";
const gen = "~/generated-images/donut.jpg";

function pieces(text: string) {
  return splitPasteImages(text);
}
function imgs(text: string) {
  return pieces(text).filter((p) => p.kind === "img");
}
function allText(text: string): string {
  return pieces(text)
    .filter((p): p is { kind: "text"; text: string } => p.kind === "text")
    .map((p) => p.text)
    .join("");
}
function plainVisible(text: string): string {
  return stripAnsi(allText(text));
}

// Bare path → image, no path text.
{
  const t = `${abs}\nWhat is this?\n`;
  assert.equal(imgs(t).length, 1);
  assert.ok(!plainVisible(t).includes("paste-1785215834288577000"));
}

// Plain Read line.
{
  const t = `Read ${home}\n`;
  assert.equal(imgs(t).length, 1);
  const v = plainVisible(t);
  assert.ok(!v.includes("Read"), JSON.stringify(pieces(t)));
  assert.ok(!v.includes("paste-1785215834288577000"), v);
}

// ANSI-colored Read (OMP) — the real bug.
{
  const t = `\x1b[35mRead\x1b[0m ${home}\n`;
  assert.equal(imgs(t).length, 1, JSON.stringify(pieces(t)));
  const v = plainVisible(t);
  assert.ok(!v.includes("Read"), `still has Read: ${JSON.stringify(pieces(t))}`);
  assert.ok(!v.includes("paste-1785215834288577000"), v);
  assert.ok(imgs(t)[0]!.plainLen > home.length, "plainLen includes Read chrome");
}

// Bullet + ANSI Read.
{
  const t = `• \x1b[38;5;13mRead\x1b[0m ${abs}\n`;
  assert.equal(imgs(t).length, 1);
  assert.ok(!plainVisible(t).includes("Read"));
}

// Path + later ANSI Read same file → one image, second gone.
{
  const t = `${abs}\nWhat is this?\n\n\x1b[35mRead\x1b[0m ${home}\nThe user asked\n`;
  assert.equal(imgs(t).length, 1, JSON.stringify(pieces(t)));
  const v = plainVisible(t);
  assert.ok(!v.includes("paste-1785215834288577000"), v);
  assert.ok(!/\bRead\b/.test(v), v);
  assert.ok(v.includes("What is this?"));
  assert.ok(v.includes("The user asked"));
}

// Agent-generated home image.
{
  const t = `\x1b[35mRead\x1b[0m ${gen}\n`;
  assert.equal(imgs(t).length, 1);
  assert.ok(imgs(t)[0]!.src.includes("/api/local-image?path="));
  assert.ok(!plainVisible(t).includes("donut.jpg"));
}

assert.equal(imgs(`${abs}\n${gen}\n`).length, 2);
assert.ok(imgs(`${abs}\n`)[0]!.src.startsWith("/api/paste/"));
assert.equal(pasteImageKey(abs), pasteImageKey(home));
assert.ok(imageSrcForPath(gen, "donut.jpg").includes("local-image"));

{
  const s = `Read ${abs}`;
  const idx = s.indexOf("/");
  assert.equal(readPrefixLength(s, idx, 0), "Read ".length);
}

console.log("pasteImages.check.ts: ok");
