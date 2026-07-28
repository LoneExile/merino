import assert from "node:assert/strict";
import { pasteImageKey, splitPasteImages } from "../pasteImages.ts";

const abs =
  "/Users/lex/Library/Caches/merino/paste/paste-1785215834288577000.jpg";
const home = "~/Library/Caches/merino/paste/paste-1785215834288577000.jpg";

function imgCount(text: string): number {
  return splitPasteImages(text).filter((p) => p.kind === "img").length;
}

// Single path → one image.
assert.equal(imgCount(`${abs}\nHow many donuts?\n`), 1);

// User path + agent "Read ~path" → one image (dedupe).
{
  const text = `${abs}\nHow many donuts?\n\nRead ${home}\n`;
  assert.equal(imgCount(text), 1, JSON.stringify(splitPasteImages(text)));
}

// History scrolled: only agent Read line left → still one image.
assert.equal(imgCount(`Read ${abs}\n`), 1);

// Mid-line home path only → image (first occurrence).
assert.equal(imgCount(`• Read ${home}\n`), 1);

// Two different pastes → two images.
{
  const p2 = "/Users/lex/Library/Caches/merino/paste/paste-999.jpg";
  assert.equal(imgCount(`${abs}\n${p2}\n`), 2);
}

// Same basename twice → one image.
assert.equal(imgCount(`${abs}\n${home}\n`), 1);

assert.equal(pasteImageKey(abs), pasteImageKey(home));

console.log("pasteImages.check.ts: ok");
