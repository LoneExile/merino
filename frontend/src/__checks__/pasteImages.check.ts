import assert from "node:assert/strict";
import {
  imageSrcForPath,
  pasteImageKey,
  readPrefixLength,
  splitPasteImages,
} from "../pasteImages.ts";

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

// Bare path → image, path not left as text.
{
  const t = `${abs}\nWhat is this?\n`;
  assert.equal(imgs(t).length, 1);
  assert.ok(!allText(t).includes("paste-1785215834288577000"));
}

// Read-only line → image, no "Read" leftover.
{
  const t = `Read ${home}\n`;
  assert.equal(imgs(t).length, 1);
  const tx = allText(t);
  assert.ok(!tx.includes("Read"), JSON.stringify(pieces(t)));
  assert.ok(!tx.includes("paste-1785215834288577000"), tx);
}

// Bullet Read.
{
  const t = `• Read ${abs}\n`;
  assert.equal(imgs(t).length, 1);
  assert.ok(!allText(t).includes("Read"));
}

// Path + later Read same file → one image, second path gone.
{
  const t = `${abs}\nWhat is this?\n\nRead ${home}\nThe user asked\n`;
  assert.equal(imgs(t).length, 1, JSON.stringify(pieces(t)));
  const tx = allText(t);
  assert.ok(!tx.includes("paste-1785215834288577000"), tx);
  assert.ok(!tx.includes("Read "), tx);
  assert.ok(tx.includes("What is this?"));
  assert.ok(tx.includes("The user asked"));
}

// Agent-generated home image.
{
  const t = `Read ${gen}\n`;
  assert.equal(imgs(t).length, 1);
  assert.ok(imgs(t)[0]!.src.includes("/api/local-image?path="));
  assert.ok(!allText(t).includes("donut.jpg"));
}

// Two different files → two images.
assert.equal(imgs(`${abs}\n${gen}\n`).length, 2);

// merino paste URL
assert.ok(imgs(`${abs}\n`)[0]!.src.startsWith("/api/paste/"));

assert.equal(pasteImageKey(abs), pasteImageKey(home));
assert.ok(imageSrcForPath(gen, "donut.jpg").includes("local-image"));

// readPrefixLength unit
assert.equal(readPrefixLength(`Read ${abs}`, abs.length - abs.length, 0) >= 0, true);
{
  const s = `Read ${abs}`;
  const idx = s.indexOf(abs);
  assert.equal(readPrefixLength(s, idx, 0), "Read ".length);
}

console.log("pasteImages.check.ts: ok");
